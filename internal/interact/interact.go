// Package interact is the two-way human interaction layer: notifications, prompts that wait for a
// choice, and bot commands. Hub implements trade.Interactor over pluggable channels (Telegram and
// the web UI today). Notifications go through a persistent per-channel outbox; prompts are
// persisted and resolved by the first answer from any channel (or their default at the deadline).
// See docs/trading-design.md §1 and §4.
package interact

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crypto-trader-go/internal/store"
	"crypto-trader-go/internal/trade"
)

// Channel is one way of reaching the owner. Implementations must be safe for concurrent use.
// Notify, Ask and Resolve are called by the hub's per-channel sender in outbox order and may be
// retried after an error (an error implementing RetryAfter() time.Duration sets the delay; one
// implementing Permanent() bool == true drops the message).
type Channel interface {
	Name() string
	Notify(ctx context.Context, ev trade.Event) error
	// Ask sends the prompt with its options. p.ID is the full id; answers may use either it or
	// the short id the channel received through Hub.Record.
	Ask(ctx context.Context, p trade.Prompt) error
	// Resolve updates what Ask sent to show the final result.
	Resolve(ctx context.Context, promptID, choice, by string) error
	// Run receives answers and commands until ctx ends.
	Run(ctx context.Context, answers chan<- Answer)
}

// Answer is a choice received by a channel.
type Answer struct {
	PromptID string // full or short prompt id
	Choice   string // option key
	By       string // "telegram:<user id>", "web", ...
	// Reply, if set, is called once (asynchronously) with a short outcome text for the person.
	Reply func(text string)
}

// Commands is what bot commands call; the lead wires it to the trade service. Every method returns
// plain text (no markup) for the reply.
type Commands interface {
	Status() string
	Positions() string
	Pause(arg string) string
	Resume(arg string) string
}

// Options tune the hub. Zero values are production defaults.
type Options struct {
	TelegramAPI string        // default https://api.telegram.org
	PollTimeout time.Duration // getUpdates long-poll timeout, default 50 s
	Backoff     time.Duration // first retry delay, default 2 s (doubles per failure)
	MaxBackoff  time.Duration // default 5 min
	SweepEvery  time.Duration // how often orphaned prompts are expired, default 5 s
	Commands    Commands
	OnChange    func() // prompts opened or closed (forward to SSE); must not block
	Logf        func(format string, args ...any)
}

const (
	mergeThreshold   = 30 // a run of more pending events than this is sent as one summary
	scanLimit        = 1000
	promptKeep       = 90 * 24 * time.Hour
	messageKeep      = 14 * 24 * time.Hour
	outboxKeep       = 3 * 24 * time.Hour
	defaultAskWindow = 2 * time.Minute
)

var (
	ErrNotFound  = errors.New("提示不存在")
	ErrBadChoice = errors.New("无效选项")
	ErrClosed    = errors.New("提示已结束")
)

// Hub implements trade.Interactor.
type Hub struct {
	st     *store.Store
	db     *sql.DB
	client *http.Client
	opts   Options
	logf   func(string, ...any)
	web    *WebChannel

	cfgMu sync.Mutex // serialises config changes / channel restarts

	mu        sync.Mutex
	cfg       Config
	runCtx    context.Context
	active    map[string]*remote // running remote channels by name
	tg        *telegram          // running bot (also when it has no recipients)
	waiters   map[string]chan struct{}
	commands  Commands
	onChange  func()
	strangers []Stranger
	answers   chan Answer
	wg        sync.WaitGroup

	testBusy atomic.Bool
}

type remote struct {
	ch     Channel
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

var _ trade.Interactor = (*Hub)(nil)

// New creates the tables, loads the config and closes prompts whose deadline passed while the
// process was down (default choice, by "timeout"). client is used for Telegram (no proxy needed in
// production); its Timeout is ignored in favour of per-call deadlines.
func New(st *store.Store, client *http.Client, opts Options) (*Hub, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if opts.TelegramAPI == "" {
		opts.TelegramAPI = "https://api.telegram.org"
	}
	opts.TelegramAPI = strings.TrimRight(opts.TelegramAPI, "/")
	if opts.PollTimeout <= 0 {
		opts.PollTimeout = 50 * time.Second
	}
	if opts.Backoff <= 0 {
		opts.Backoff = 2 * time.Second
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 5 * time.Minute
	}
	if opts.SweepEvery <= 0 {
		opts.SweepEvery = 5 * time.Second
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	hc := *client
	hc.Timeout = 0
	h := &Hub{st: st, db: st.DB, client: &hc, opts: opts, logf: opts.Logf, active: map[string]*remote{},
		waiters: map[string]chan struct{}{}, commands: opts.Commands, onChange: opts.OnChange, answers: make(chan Answer, 16)}
	h.web = &WebChannel{h: h}
	if _, err := h.db.Exec(schema); err != nil {
		return nil, fmt.Errorf("interact schema: %w", err)
	}
	if raw, ok := st.Setting(configKey); ok {
		c, err := parseConfig(raw)
		if err != nil {
			h.logf("interact: ignoring unreadable config")
		} else {
			h.cfg = c
		}
	}
	h.expireDue(time.Now())
	return h, nil
}

// SetCommands wires bot commands (may be called after New, before or after Run).
func (h *Hub) SetCommands(c Commands) {
	h.mu.Lock()
	h.commands = c
	h.mu.Unlock()
}

// SetOnChange sets the prompt change hook (see Options.OnChange).
func (h *Hub) SetOnChange(f func()) {
	h.mu.Lock()
	h.onChange = f
	h.mu.Unlock()
}

func (h *Hub) running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runCtx != nil && h.runCtx.Err() == nil
}

// Web returns the web channel.
func (h *Hub) Web() *WebChannel { return h.web }

func (h *Hub) cmds() Commands {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commands
}

func (h *Hub) config() Config {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg
}

func (h *Hub) changed() {
	h.mu.Lock()
	f := h.onChange
	h.mu.Unlock()
	if f != nil {
		f()
	}
}

// targets are the remote channels messages are queued for (by config, so messages queued before
// Run or during a bot restart are kept).
func targets(c Config) []string {
	var out []string
	if c.Telegram.delivers() {
		out = append(out, "telegram")
	}
	return out
}

// Run starts the channels, the answer loop and the expiry sweep, and blocks until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	h.cfgMu.Lock()
	h.mu.Lock()
	h.runCtx = ctx
	h.mu.Unlock()
	h.startChannels()
	h.cfgMu.Unlock()

	go h.web.Run(ctx, h.answers)
	sweep := time.NewTicker(h.opts.SweepEvery)
	defer sweep.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	h.prune(time.Now())
	for {
		select {
		case <-ctx.Done():
			h.cfgMu.Lock()
			h.stopChannels()
			h.cfgMu.Unlock()
			h.wg.Wait()
			return
		case a := <-h.answers:
			h.handleAnswer(a)
		case <-sweep.C:
			h.expireDue(time.Now())
		case <-prune.C:
			h.prune(time.Now())
		}
	}
}

func (h *Hub) handleAnswer(a Answer) {
	rec, err := h.Answer(a.PromptID, a.Choice, a.By)
	if a.Reply == nil {
		return
	}
	var text string
	switch {
	case err == nil:
		text = "已记录：" + rec.label(rec.Choice)
	case errors.Is(err, ErrClosed):
		text = "已结束：" + rec.label(rec.Choice)
		if rec.By == byCancelled {
			text = "已失效"
		}
	case errors.Is(err, ErrNotFound):
		text = "提示不存在或已清理"
	case errors.Is(err, ErrBadChoice):
		text = "无效选项"
	default:
		text = "处理失败，请稍后再试"
	}
	h.wg.Add(1)
	go func() { defer h.wg.Done(); a.Reply(text) }()
}

// startChannels starts the channels the config asks for. Caller holds cfgMu.
func (h *Hub) startChannels() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runCtx == nil || h.runCtx.Err() != nil || len(h.active) > 0 {
		return
	}
	tc := h.cfg.Telegram
	if !tc.polls() {
		return
	}
	t := newTelegram(h, tc)
	h.tg = t
	ctx, cancel := context.WithCancel(h.runCtx)
	r := &remote{ch: t, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	h.active[t.Name()] = r
	deliver := tc.delivers()
	go func() {
		defer close(r.done)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); t.Run(ctx, h.answers) }()
		if deliver {
			h.sender(ctx, r)
		}
		wg.Wait()
	}()
}

// stopChannels cancels every running remote channel and waits for it. Caller holds cfgMu.
func (h *Hub) stopChannels() {
	h.mu.Lock()
	act := h.active
	h.active = map[string]*remote{}
	h.tg = nil
	h.mu.Unlock()
	for _, r := range act {
		r.cancel()
	}
	for _, r := range act {
		<-r.done
	}
}

func (h *Hub) wakeAll(names []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range names {
		if r := h.active[n]; r != nil {
			select {
			case r.wake <- struct{}{}:
			default:
			}
		}
	}
}

// ---------- trade.Interactor ----------

// Notify queues an event for the remote channels and returns at once.
func (h *Hub) Notify(ev trade.Event) {
	if ev.Ts == 0 {
		ev.Ts = time.Now().UnixMilli()
	}
	cfg := h.config()
	if !cfg.wants(ev.Kind) {
		return
	}
	names := targets(cfg)
	if len(names) == 0 {
		return
	}
	b, _ := json.Marshal(ev)
	if err := h.enqueue(names, obEvent, string(b)); err != nil {
		h.logf("interact: queue %s event: %v", ev.Kind, err)
		return
	}
	h.wakeAll(names)
}

// Ask persists the prompt, sends it to every channel and blocks until the first answer, the
// deadline (→ default, by "timeout") or ctx (→ prompt withdrawn, ctx error returned). Asking again
// with the id of an existing prompt waits for (or returns) that prompt's result.
func (h *Hub) Ask(ctx context.Context, p trade.Prompt) (string, string, error) {
	if len(p.Options) == 0 {
		return "", "", errors.New("prompt has no options")
	}
	for _, o := range p.Options {
		if o.Key == "" || len(o.Key) > 24 || strings.ContainsAny(o.Key, ": ") {
			return "", "", fmt.Errorf("invalid option key %q", o.Key)
		}
	}
	now := time.Now()
	if p.Deadline.IsZero() {
		p.Deadline = now.Add(defaultAskWindow)
	}
	short := newShort()
	if p.ID == "" {
		p.ID = "pr-" + short
	}
	if p.Event.Ts == 0 {
		p.Event.Ts = now.UnixMilli()
	}
	if p.Event.Kind == "" {
		p.Event.Kind = "prompt"
	}
	rec := Record{ID: p.ID, Short: short, Event: p.Event, Options: p.Options, Deadline: p.Deadline.UnixMilli(),
		Default: p.Default, State: StateOpen, CreatedAt: now.UnixMilli()}
	if !rec.has(rec.Default) {
		rec.Default = p.Options[len(p.Options)-1].Key
		h.logf("interact: prompt %s default %q is not an option; using %q", p.ID, p.Default, rec.Default)
	}

	h.mu.Lock()
	done, waiting := h.waiters[p.ID]
	if !waiting {
		done = make(chan struct{})
		h.waiters[p.ID] = done
	}
	h.mu.Unlock()

	inserted, err := h.insertPrompt(rec) // no-op when the id exists (re-ask after a restart, or a concurrent Ask)
	if err != nil {
		return "", "", fmt.Errorf("persist prompt: %w", err)
	}
	if inserted {
		names := targets(h.config())
		if err := h.enqueue(names, obAsk, rec.ID); err != nil {
			h.logf("interact: queue prompt %s: %v", rec.ID, err)
		}
		h.wakeAll(names)
		h.web.Ask(ctx, p)
	} else {
		existing, ok := h.record(p.ID)
		if !ok {
			return "", "", ErrNotFound
		}
		if existing.State != StateOpen {
			h.release(p.ID)
			return finalResult(existing)
		}
		rec = existing
	}

	timer := time.NewTimer(time.Until(time.UnixMilli(rec.Deadline)))
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		h.close(rec, StateExpired, rec.Default, byTimeout)
	case <-ctx.Done():
		h.close(rec, StateExpired, "", byCancelled)
	}
	final, ok := h.record(rec.ID)
	if !ok {
		return "", "", ErrNotFound
	}
	if final.By == byCancelled && ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	return finalResult(final)
}

func finalResult(r Record) (string, string, error) {
	if r.By == byCancelled {
		return "", r.By, ErrClosed
	}
	return r.Choice, r.By, nil
}

func (h *Hub) release(id string) {
	h.mu.Lock()
	if ch, ok := h.waiters[id]; ok {
		close(ch)
		delete(h.waiters, id)
	}
	h.mu.Unlock()
}

// close resolves an open prompt (first caller wins), wakes waiters and updates every channel.
func (h *Hub) close(rec Record, state, choice, by string) bool {
	won, err := h.closePrompt(rec.ID, state, choice, by, time.Now().UnixMilli())
	if err != nil {
		h.logf("interact: close prompt %s: %v", rec.ID, err)
		return false
	}
	if !won {
		return false
	}
	h.release(rec.ID)
	names := targets(h.config())
	if err := h.enqueue(names, obResolve, rec.ID); err != nil {
		h.logf("interact: queue resolve %s: %v", rec.ID, err)
	}
	h.wakeAll(names)
	h.web.Resolve(context.Background(), rec.ID, choice, by)
	return true
}

// Answer resolves a prompt from any channel. The first valid answer wins; later ones get
// ErrClosed together with the final record.
func (h *Hub) Answer(id, choice, by string) (Record, error) {
	rec, ok := h.record(id)
	if !ok {
		return Record{}, ErrNotFound
	}
	if rec.State != StateOpen {
		return rec, ErrClosed
	}
	if !rec.has(choice) {
		return rec, ErrBadChoice
	}
	if time.Now().UnixMilli() >= rec.Deadline {
		h.close(rec, StateExpired, rec.Default, byTimeout)
		rec, _ = h.record(rec.ID)
		return rec, ErrClosed
	}
	if !h.close(rec, StateAnswered, choice, by) {
		rec, _ = h.record(rec.ID)
		return rec, ErrClosed
	}
	rec, _ = h.record(rec.ID)
	return rec, nil
}

// expireDue closes open prompts past their deadline (orphans after a restart, or nobody waiting).
func (h *Hub) expireDue(now time.Time) {
	recs, err := h.dueOpen(now.UnixMilli())
	if err != nil {
		h.logf("interact: expire prompts: %v", err)
		return
	}
	for _, r := range recs {
		h.close(r, StateExpired, r.Default, byTimeout)
	}
}

// ---------- outbox sender ----------

type retryAfterer interface{ RetryAfter() time.Duration }
type permanenter interface{ Permanent() bool }

func (h *Hub) sender(ctx context.Context, r *remote) {
	fails := 0
	for ctx.Err() == nil {
		rows, err := h.pending(r.ch.Name(), scanLimit)
		if err != nil {
			h.logf("interact: %s outbox: %v", r.ch.Name(), err)
			if !sleep(ctx, h.opts.Backoff) {
				return
			}
			continue
		}
		if len(rows) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-r.wake:
			}
			continue
		}
		ids, err := h.deliver(ctx, r.ch, rows)
		if err != nil && ctx.Err() != nil {
			return
		}
		var pe permanenter
		if err != nil && errors.As(err, &pe) && pe.Permanent() {
			h.logf("interact: %s: dropping undeliverable message: %v", r.ch.Name(), err)
			err = nil
		}
		if err == nil {
			fails = 0
			if derr := h.dequeue(ids); derr != nil {
				h.logf("interact: %s outbox: %v", r.ch.Name(), derr)
				sleep(ctx, h.opts.Backoff)
			}
			continue
		}
		fails++
		d := h.opts.Backoff << min(fails-1, 20)
		if d > h.opts.MaxBackoff || d <= 0 {
			d = h.opts.MaxBackoff
		}
		var ra retryAfterer
		if errors.As(err, &ra) && ra.RetryAfter() > 0 {
			d = ra.RetryAfter()
		}
		h.logf("interact: %s: send failed (%d), retry in %s: %v", r.ch.Name(), fails, d.Round(time.Millisecond), err)
		if !sleep(ctx, d) {
			return
		}
	}
}

// deliver sends the head of the queue: one message, or a merged summary when a run of more than
// mergeThreshold events is waiting. It returns the outbox ids that are done once err is nil.
func (h *Hub) deliver(ctx context.Context, ch Channel, rows []obRow) ([]int64, error) {
	head := rows[0]
	switch head.Kind {
	case obEvent:
		n := 0
		for n < len(rows) && rows[n].Kind == obEvent {
			n++
		}
		if n > mergeThreshold {
			evs := make([]trade.Event, 0, n)
			ids := make([]int64, 0, n)
			for _, r := range rows[:n] {
				var ev trade.Event
				if json.Unmarshal([]byte(r.Payload), &ev) == nil {
					evs = append(evs, ev)
				}
				ids = append(ids, r.ID)
			}
			sort.SliceStable(evs, func(i, j int) bool { return evs[i].Ts < evs[j].Ts })
			return ids, ch.Notify(ctx, summaryEvent(evs))
		}
		var ev trade.Event
		if err := json.Unmarshal([]byte(head.Payload), &ev); err != nil {
			return []int64{head.ID}, nil
		}
		return []int64{head.ID}, ch.Notify(ctx, ev)
	case obAsk:
		rec, ok := h.record(head.Payload)
		if !ok || rec.State != StateOpen {
			return []int64{head.ID}, nil // closed before it could be sent: nothing to ask
		}
		return []int64{head.ID}, ch.Ask(ctx, rec.prompt())
	case obResolve:
		rec, ok := h.record(head.Payload)
		if !ok {
			return []int64{head.ID}, nil
		}
		return []int64{head.ID}, ch.Resolve(ctx, rec.ID, rec.Choice, rec.By)
	}
	return []int64{head.ID}, nil
}

// summaryEvent carries a pre-rendered backlog summary through Channel.Notify.
func summaryEvent(evs []trade.Event) trade.Event {
	lines := renderSummary(evs)
	return trade.Event{Kind: kindSummary, Title: lines[0], Lines: lines[1:], Ts: time.Now().UnixMilli()}
}

const kindSummary = "summary"

// render returns the message lines for an event (summary events are already rendered).
func render(ev trade.Event) []string {
	if ev.Kind == kindSummary {
		return append([]string{ev.Title}, ev.Lines...)
	}
	return renderEvent(ev)
}

func (r Record) prompt() trade.Prompt {
	return trade.Prompt{ID: r.ID, Event: r.Event, Options: r.Options, Deadline: time.UnixMilli(r.Deadline), Default: r.Default}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ---------- config ----------

// Config returns the masked config with the bot status.
func (h *Hub) Config() ConfigView {
	h.mu.Lock()
	v := h.cfg.view()
	t := h.tg
	strangers := append([]Stranger{}, h.strangers...)
	h.mu.Unlock()
	v.Status = TelegramStatus{State: "disabled"}
	if t != nil {
		v.Status = t.status()
	} else if v.Telegram.Enabled {
		v.Status.State = "stopped"
	}
	v.Status.Recipients = len(v.Telegram.AllowUserIDs)
	v.Status.Backlog = h.backlog("telegram")
	v.Status.Strangers = strangers
	return v
}

// SaveConfig validates, persists and applies a config (restarting the bot when it changed).
func (h *Hub) SaveConfig(in ConfigView) (ConfigView, error) {
	h.cfgMu.Lock()
	h.mu.Lock()
	old := h.cfg
	h.mu.Unlock()
	next, err := old.merge(in)
	if err != nil {
		h.cfgMu.Unlock()
		return ConfigView{}, err
	}
	b, _ := json.Marshal(next)
	if err := h.st.PutSetting(configKey, string(b)); err != nil {
		h.cfgMu.Unlock()
		return ConfigView{}, err
	}
	restart := !sameTelegram(old.Telegram, next.Telegram)
	if restart {
		h.stopChannels()
	}
	h.mu.Lock()
	h.cfg = next
	h.mu.Unlock()
	if !next.Telegram.delivers() {
		h.dropChannel("telegram")
	}
	if restart {
		h.startChannels()
	}
	h.cfgMu.Unlock()
	return h.Config(), nil
}

// Stranger is someone who wrote to the bot without being on the allowlist (shown in the settings
// so the owner can add their own id after sending /start).
type Stranger struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	At   int64  `json:"at"`
}

func (h *Hub) noteStranger(s Stranger) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, x := range h.strangers {
		if x.ID == s.ID {
			h.strangers = append(h.strangers[:i], h.strangers[i+1:]...)
			break
		}
	}
	h.strangers = append([]Stranger{s}, h.strangers...)
	if len(h.strangers) > 5 {
		h.strangers = h.strangers[:5]
	}
}

// Test sends a test notification synchronously to every remote channel (reporting the result) and
// opens a test prompt in the background.
func (h *Hub) Test(ctx context.Context) (map[string]string, string, error) {
	if !h.testBusy.CompareAndSwap(false, true) {
		return nil, "", errors.New("上一个测试提示尚未结束")
	}
	ev := trade.Event{Kind: "test", Title: "测试消息", Lines: []string{"交互通道连通正常", "随后会发送一条测试确认，可任选一项"}, Ts: time.Now().UnixMilli(), Ref: "test"}
	res := map[string]string{}
	h.mu.Lock()
	var chans []Channel
	for _, r := range h.active {
		chans = append(chans, r.ch)
	}
	cfg := h.cfg
	base := h.runCtx
	h.mu.Unlock()
	if base == nil || base.Err() != nil {
		h.testBusy.Store(false)
		return nil, "", errors.New("交互服务未运行")
	}
	switch {
	case !cfg.Telegram.Enabled:
		res["telegram"] = "未启用"
	case !cfg.Telegram.delivers():
		res["telegram"] = "白名单为空：先在私聊中向机器人发送 /start 获取用户 ID"
	case len(chans) == 0:
		res["telegram"] = "未运行"
	}
	for _, c := range chans {
		if _, set := res[c.Name()]; set {
			continue
		}
		if err := c.Notify(ctx, ev); err != nil {
			res[c.Name()] = err.Error()
		} else {
			res[c.Name()] = "ok"
		}
	}
	p := trade.Prompt{ID: "test-" + newShort(), Default: "ignore", Deadline: time.Now().Add(2 * time.Minute),
		Event:   trade.Event{Kind: "test", Title: "测试确认", Lines: []string{"这是一条测试确认，不会触发任何操作"}, Ref: "test"},
		Options: []trade.PromptOption{{Key: "ok", Label: "收到"}, {Key: "ignore", Label: "忽略"}}}
	go func() { // ends with the hub: base is the Run context
		defer h.testBusy.Store(false)
		actx, cancel := context.WithTimeout(base, 3*time.Minute)
		defer cancel()
		h.Ask(actx, p)
	}()
	return res, p.ID, nil
}
