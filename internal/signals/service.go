// Package signals lets the agent (and the owner) wait for a market condition instead of being asked
// again: a subscription holds a condition (Spec), who subscribed (an AI chat, or the owner) and what the
// subscriber means to do when it fires. Conditions are evaluated server-side on events only — the
// engine's price broadcast (2 s), each confirmed minute and each alert — and fire on the edge from false
// to true. A firing appends a "⚡ 信号" message to the subscriber's chat and runs the agent there with
// its full history; a chat that is answering gets it injected into the running turn (internal/aigate
// SteerChat), and only when that fails is the wake-up queued until the turn ends. The message carries
// the state of the AI position the subscription manages (tradeId) or of the chat's positions in its
// instruments. The agent then re-checks and may call propose_trade, which still needs the owner's
// approval (internal/realtrade): nothing here trades. A subscription linked to a position is cancelled
// when the position closes (CancelForTrade). docs/trading-design.md §8.
//
//	active → fired      (maxFires reached)
//	active → expired    (expiresAt passed)
//	active → cancelled  (the subscriber, or the owner; by whom and why are kept; "system" when a script keeps failing)
//
// Script conditions (a Python ctg.Condition run by the strategy container) are in script.go.
package signals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"crypto-trader-go/internal/store"
)

const schema = `
CREATE TABLE IF NOT EXISTS signal_subs(id INTEGER PRIMARY KEY AUTOINCREMENT, created INTEGER NOT NULL, updated INTEGER NOT NULL,
  kind TEXT NOT NULL, chat_id INTEGER NOT NULL DEFAULT 0, label TEXT NOT NULL DEFAULT '', inst_ids TEXT NOT NULL DEFAULT '',
  spec TEXT NOT NULL, descr TEXT NOT NULL, intent TEXT NOT NULL DEFAULT '', plan_id INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL, max_fires INTEGER NOT NULL DEFAULT 1, cooldown_min INTEGER NOT NULL DEFAULT 60,
  fires INTEGER NOT NULL DEFAULT 0, last_fired_at INTEGER NOT NULL DEFAULT 0, state INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL, status_at INTEGER NOT NULL, ended_by TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
  script TEXT NOT NULL DEFAULT '', trade_id INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS signal_subs_status ON signal_subs(status, expires_at);
CREATE INDEX IF NOT EXISTS signal_subs_chat ON signal_subs(chat_id);
CREATE TABLE IF NOT EXISTS signal_fires(id INTEGER PRIMARY KEY AUTOINCREMENT, sub_id INTEGER NOT NULL, ts INTEGER NOT NULL,
  px REAL NOT NULL DEFAULT 0, matched TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '',
  wake TEXT NOT NULL, wake_chat INTEGER NOT NULL DEFAULT 0, wake_at INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS signal_fires_sub ON signal_fires(sub_id, id);
CREATE INDEX IF NOT EXISTS signal_fires_wake ON signal_fires(wake, ts);
`

// Subscriber kinds and statuses.
const (
	KindChat  = "chat"  // an AI chat: its agent is woken in that chat
	KindOwner = "owner" // the owner over REST: a new "信号 · …" chat is started on fire

	StatusActive    = "active"
	StatusFired     = "fired"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"

	// fire wake-up states
	WakeQueued  = "queued"  // the running turn could not take it or the agent is down: retried when a turn ends / each minute
	WakeStarted = "started" // the agent runs (wake_chat)
	WakeSkipped = "skipped" // daily wake-up cap reached: recorded and pushed, nobody woken
	WakeFailed  = "failed"  // final error (chat deleted, queued too long)
)

// Caps (docs/operations.md).
const (
	DefaultMaxActive   = 30
	DefaultWakesPerDay = 30
	MaxPerChat         = 5
	MaxExpiry          = 7 * 24 * time.Hour
	MinExpiry          = 15 * time.Minute
	DefaultExpiry      = 24 * time.Hour
	MaxFiresCap        = 5
	MinCooldown        = 5
	DefaultCooldown    = 60
	queueTimeout       = 2 * time.Hour
	maxIntent          = 1500
)

var (
	ErrNotFound = errors.New("signal subscription not found")
	ErrNotYours = errors.New("this subscription belongs to another subscriber; only the owner may cancel it (pass ownerAsked=true only when he asked)")
	ErrEnded    = errors.New("subscription is no longer active")
	// ErrBusy / ErrAgentDown are what a Waker returns to have the wake-up retried.
	ErrBusy      = errors.New("chat is answering")
	ErrAgentDown = errors.New("agent unavailable")
)

// Waker runs the agent for a fired subscription.
type Waker interface {
	// Wake delivers message to the chat as a signal message and runs the agent there (full history):
	// a new turn, or injected into the running one. ErrBusy / ErrAgentDown: retried later; any other
	// error is final.
	Wake(chat int64, message string) error
	// Start runs the agent in a new chat (owner subscriptions) and returns its id.
	Start(title, message, instID string) (int64, error)
	// ChatTitle is a chat's title; false when it does not exist.
	ChatTitle(chat int64) (string, bool)
}

// Notifier pushes a short note when a subscription fires (the Telegram trade bot); must not block.
type Notifier func(text, link string)

// Options wire the service.
type Options struct {
	MaxActive    int     // CTG_SIGNAL_MAX_ACTIVE
	WakesPerDay  int     // CTG_SIGNAL_MAX_WAKES_DAY; 0 = the default, negative = none (fires are recorded and pushed only)
	ScriptMs     int     // CTG_SIGNAL_SCRIPT_MS: CPU budget of one script check
	ScriptPerDay float64 // CTG_SIGNAL_SCRIPT_FIRES_DAY: a script that would fire more often in its replay is rejected
	ScriptErrors int     // CTG_SIGNAL_SCRIPT_ERRORS: consecutive live errors that end a script subscription
	PublicURL    string
	Now          func() time.Time
	Logf         func(format string, args ...any)
}

// OptionsFromEnv reads the caps; unset or invalid keeps the default.
func OptionsFromEnv() Options {
	o := Options{MaxActive: DefaultMaxActive, WakesPerDay: DefaultWakesPerDay}
	if v, err := strconv.Atoi(os.Getenv("CTG_SIGNAL_MAX_ACTIVE")); err == nil && v > 0 {
		o.MaxActive = v
	}
	if v, err := strconv.Atoi(os.Getenv("CTG_SIGNAL_SCRIPT_MS")); err == nil && v > 0 {
		o.ScriptMs = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("CTG_SIGNAL_SCRIPT_FIRES_DAY"), 64); err == nil && v > 0 {
		o.ScriptPerDay = v
	}
	if v, err := strconv.Atoi(os.Getenv("CTG_SIGNAL_SCRIPT_ERRORS")); err == nil && v > 0 {
		o.ScriptErrors = v
	}
	if v, err := strconv.Atoi(os.Getenv("CTG_SIGNAL_MAX_WAKES_DAY")); err == nil && v >= 0 {
		o.WakesPerDay = v
		if v == 0 {
			o.WakesPerDay = -1
		}
	}
	return o
}

// Subscription is one stored subscription (the list and detail views).
type Subscription struct {
	ID          int64       `json:"id"`
	Created     int64       `json:"created"`
	Updated     int64       `json:"updated"`
	Kind        string      `json:"kind"` // chat | owner
	ChatID      int64       `json:"chatId,omitempty"`
	Label       string      `json:"label"`      // the chat's title when subscribed
	Subscriber  string      `json:"subscriber"` // AI 对话 #12 · 标题 | 主人
	InstIDs     []string    `json:"instIds"`
	Spec        Spec        `json:"spec"`
	Desc        string      `json:"desc"`
	Intent      string      `json:"intent"`
	PlanID      int64       `json:"planId,omitempty"`
	TradeID     int64       `json:"tradeId,omitempty"` // the AI position (its open proposal) this watch manages
	ExpiresAt   int64       `json:"expiresAt"`
	MaxFires    int         `json:"maxFires"`
	CooldownMin int         `json:"cooldownMin"`
	FireCount   int         `json:"fireCount"`
	LastFiredAt int64       `json:"lastFiredAt,omitempty"`
	Status      string      `json:"status"`
	StatusAt    int64       `json:"statusAt"`
	EndedBy     string      `json:"endedBy,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	Levels      []Level     `json:"levels,omitempty"`
	Progress    *Progress   `json:"progress,omitempty"` // active only
	History     []Fire      `json:"history,omitempty"`  // detail only
	Script      *ScriptInfo `json:"script,omitempty"`   // script conditions: code, declarations, replay, live checks
	state       tri
}

// Fire is one firing and what became of its wake-up.
type Fire struct {
	ID       int64   `json:"id"`
	Ts       int64   `json:"ts"`
	Px       float64 `json:"px,omitempty"`
	Matched  string  `json:"matched"`
	Wake     string  `json:"wake"`
	WakeChat int64   `json:"wakeChat,omitempty"`
	WakeAt   int64   `json:"wakeAt,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// Service owns the subscriptions.
type Service struct {
	st   *store.Store
	db   *sql.DB
	src  Sources
	opts Options
	now  func() time.Time
	logf func(string, ...any)

	mu       sync.Mutex
	live     map[int64]*liveSub
	waker    Waker
	notify   Notifier
	onChange func(kind string, id, chat int64)
	scripts  ScriptBackend
	context  func(chat int64, instIDs []string, tradeID int64) string // the positions block of a wake-up (internal/realtrade)

	ticks   chan struct{}
	minutes chan int64
	alerts  chan AlertHit
	wakes   chan struct{}
	evalMu  sync.Mutex // one evaluation at a time (loop, subscribe)

	scriptRes  chan scriptBatch
	scriptBusy atomic.Bool
	infraSince time.Time // first of consecutive script batches that could not run (evalMu)
	infraNoted bool
}

type liveSub struct {
	sub    Subscription
	root   *node
	script *ScriptState // script subscriptions only
}

func newLive(sub Subscription, root *node) *liveSub {
	l := &liveSub{sub: sub, root: root}
	if sub.Script != nil {
		st := sub.Script.ScriptState
		l.script = &st
	}
	return l
}

// New creates the tables and loads the active subscriptions.
func New(st *store.Store, src Sources, opts Options) (*Service, error) {
	if opts.MaxActive <= 0 {
		opts.MaxActive = DefaultMaxActive
	}
	switch {
	case opts.WakesPerDay == 0:
		opts.WakesPerDay = DefaultWakesPerDay
	case opts.WakesPerDay < 0:
		opts.WakesPerDay = 0
	}
	if opts.ScriptMs <= 0 {
		opts.ScriptMs = DefaultScriptMs
	}
	if opts.ScriptPerDay <= 0 {
		opts.ScriptPerDay = DefaultScriptPerDay
	}
	if opts.ScriptErrors <= 0 {
		opts.ScriptErrors = DefaultScriptErrors
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	opts.PublicURL = strings.TrimRight(opts.PublicURL, "/")
	s := &Service{st: st, db: st.DB, src: src, opts: opts, now: opts.Now, logf: opts.Logf, live: map[int64]*liveSub{},
		ticks: make(chan struct{}, 1), minutes: make(chan int64, 8), alerts: make(chan AlertHit, 64), wakes: make(chan struct{}, 1),
		scriptRes: make(chan scriptBatch, 4)}
	if _, err := s.db.Exec(schema); err != nil {
		return nil, fmt.Errorf("signals schema: %w", err)
	}
	for _, c := range []struct{ name, def string }{{"script", "TEXT NOT NULL DEFAULT ''"}, {"trade_id", "INTEGER NOT NULL DEFAULT 0"}} {
		var has int // added 2026-11-01: tables made before get the column
		s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('signal_subs') WHERE name=?`, c.name).Scan(&has)
		if has == 0 {
			if _, err := s.db.Exec(`ALTER TABLE signal_subs ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
				return nil, fmt.Errorf("signals schema: %w", err)
			}
		}
	}
	subs, err := s.query(`status='active'`, nil, 1000)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sub.Spec.capEvery() { // stored before the rule: alts are checked at least every minute
			sub.Desc = sub.Spec.Label()
			raw, _ := json.Marshal(sub.Spec)
			s.db.Exec(`UPDATE signal_subs SET spec=?, descr=? WHERE id=?`, string(raw), sub.Desc, sub.ID)
		}
		s.live[sub.ID] = newLive(sub, build(sub.Spec))
	}
	return s, nil
}

// SetWaker wires the agent (aigate). Without it fires are recorded and queued.
func (s *Service) SetWaker(w Waker) { s.mu.Lock(); s.waker = w; s.mu.Unlock() }

// SetContext sets the source of the positions block in a wake-up message (internal/realtrade
// WakeContext): the AI position a subscription manages, or the chat's in its instruments.
func (s *Service) SetContext(f func(chat int64, instIDs []string, tradeID int64) string) {
	s.mu.Lock()
	s.context = f
	s.mu.Unlock()
}

// SetNotifier sets the push on fire.
func (s *Service) SetNotifier(n Notifier) { s.mu.Lock(); s.notify = n; s.mu.Unlock() }

// SetOnChange sets the hook called after any change (kind: subscribed | fired | woken | cancelled |
// expired; forward to SSE); must not block.
func (s *Service) SetOnChange(f func(kind string, id, chat int64)) {
	s.mu.Lock()
	s.onChange = f
	s.mu.Unlock()
}

func (s *Service) changed(kind string, id, chat int64) {
	s.mu.Lock()
	f := s.onChange
	s.mu.Unlock()
	if f != nil {
		f(kind, id, chat)
	}
}

// ---------- event intake (never blocks) ----------

// Tick: prices moved (the engine's 2 s price broadcast).
func (s *Service) Tick() {
	select {
	case s.ticks <- struct{}{}:
	default:
	}
}

// Minute: a confirmed minute (open time of its 1m bar).
func (s *Service) Minute(asOf int64) {
	select {
	case s.minutes <- asOf:
	default:
	}
}

// Alert: an alert rule fired.
func (s *Service) Alert(h AlertHit) {
	select {
	case s.alerts <- h:
	default:
	}
}

// ChatDone: a chat turn ended; queued wake-ups may go now.
func (s *Service) ChatDone(int64) {
	select {
	case s.wakes <- struct{}{}:
	default:
	}
}

// Run evaluates subscriptions on events until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.evaluate(event{kind: "init"}) // catch up on what changed while the server was down
	s.keep()
	s.processQueue()
	s.startScripts(ctx, s.scriptsDue(s.now().UnixMilli()/60_000*60_000-120_000, true)) // scripts: their newest close
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.ticks:
			s.evaluate(event{kind: "tick"})
		case m := <-s.minutes:
			s.expireDue()
			s.evaluate(event{kind: "minute", minute: m})
			s.startScripts(ctx, s.scriptsDue(m, false))
			s.processQueue()
		case b := <-s.scriptRes:
			s.applyScripts(b)
		case h := <-s.alerts:
			s.evaluate(event{kind: "alert", alert: &h})
		case <-s.wakes:
			s.processQueue()
		}
	}
}

// evaluate runs one event through every active subscription and fires those that turned true.
func (s *Service) evaluate(ev event) {
	s.evalMu.Lock()
	defer s.evalMu.Unlock()
	now := s.now()
	s.mu.Lock()
	ls := make([]*liveSub, 0, len(s.live))
	for _, l := range s.live {
		ls = append(ls, l)
	}
	s.mu.Unlock()
	slices.SortFunc(ls, func(a, b *liveSub) int { return int(a.sub.ID - b.sub.ID) })
	for _, l := range ls {
		if ev.kind == "tick" && (l.sub.Spec.interval() != "tick" || !l.sub.Spec.live()) {
			continue
		}
		if ev.kind == "minute" && !l.sub.Spec.due(ev.minute) {
			continue
		}
		s.check(l, ev, now)
	}
}

// check evaluates one subscription on an event and fires it on the edge from false to true. Caller holds evalMu.
func (s *Service) check(l *liveSub, ev event, now time.Time) {
	if now.UnixMilli() >= l.sub.ExpiresAt {
		return // expireDue ends it
	}
	v := l.root.eval(s.src, ev, now)
	if v == unknown {
		return
	}
	prev := l.sub.state
	s.setState(l, v)
	if v == yes && prev == no && (l.sub.LastFiredAt == 0 || now.Sub(time.UnixMilli(l.sub.LastFiredAt)) >= time.Duration(l.sub.CooldownMin)*time.Minute) {
		s.fire(l, now)
	}
	if ev.kind == "alert" && l.sub.Status == StatusActive { // an alert is a moment: its leaves are false again right after
		if v := l.root.eval(s.src, event{kind: "settle"}, now); v != unknown {
			s.setState(l, v)
		}
	}
}

func (s *Service) setState(l *liveSub, v tri) {
	if v != l.sub.state {
		l.sub.state = v
		s.db.Exec(`UPDATE signal_subs SET state=? WHERE id=?`, int(v), l.sub.ID)
	}
}

// live reports whether any leaf reads the last price (worth evaluating on each price tick).
func (s Spec) live() bool {
	for _, l := range s.leaves() {
		if (l.Type == "price" && l.Bar == "") || l.Type == "zone" || l.Type == "time" {
			return true
		}
	}
	return false
}

// fire records a firing, ends the subscription when it used up its fires, and wakes the subscriber.
func (s *Service) fire(l *liveSub, now time.Time) {
	sub := &l.sub
	matched := strings.Join(l.root.observed(), "；")
	var px float64
	if ids := sub.InstIDs; len(ids) > 0 {
		px, _, _ = s.src.Quote(ids[0])
	}
	sub.FireCount++
	sub.LastFiredAt = now.UnixMilli()
	status := StatusActive
	if sub.FireCount >= sub.MaxFires {
		status = StatusFired
	}
	msg := s.wakeMessage(*sub, matched, px, now)
	res, err := s.db.Exec(`INSERT INTO signal_fires(sub_id, ts, px, matched, message, wake) VALUES(?,?,?,?,?,?)`,
		sub.ID, now.UnixMilli(), px, matched, msg, WakeQueued)
	if err != nil {
		s.logf("signals: fire #%d: %v", sub.ID, err)
		return
	}
	fireID, _ := res.LastInsertId()
	s.db.Exec(`UPDATE signal_subs SET fires=?, last_fired_at=?, updated=?, status=?, status_at=CASE WHEN ?<>status THEN ? ELSE status_at END WHERE id=?`,
		sub.FireCount, sub.LastFiredAt, now.UnixMilli(), status, status, now.UnixMilli(), sub.ID)
	sub.Status = status
	if status != StatusActive {
		s.mu.Lock()
		delete(s.live, sub.ID)
		s.mu.Unlock()
		s.keep()
	}
	wake := WakeQueued
	if s.opts.WakesPerDay == 0 || s.wakesToday(now) > s.opts.WakesPerDay { // this fire is already counted as queued
		wake = WakeSkipped
		s.db.Exec(`UPDATE signal_fires SET wake=?, error=? WHERE id=?`, WakeSkipped,
			fmt.Sprintf("已达每日唤醒上限 %d（CTG_SIGNAL_MAX_WAKES_DAY），未唤醒 AI", s.opts.WakesPerDay), fireID)
	}
	s.logf("signals: #%d fired (%s): %s", sub.ID, wake, matched)
	s.changed("fired", sub.ID, sub.ChatID)
	if wake == WakeQueued {
		s.tryWake(fireID)
	}
	s.push(*sub, fireID, matched, px)
}

// wakesToday counts fires that woke (or wait to wake) an agent in the last 24 h.
func (s *Service) wakesToday(now time.Time) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM signal_fires WHERE ts>=? AND wake IN ('queued','started')`, now.Add(-24*time.Hour).UnixMilli()).Scan(&n)
	return n
}

// wakeMessage is what the subscriber's agent reads: the signal, what matched, its own plan, and what to do.
func (s *Service) wakeMessage(sub Subscription, matched string, px float64, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ 信号 #%d 触发：%s\n", sub.ID, sub.Desc)
	fmt.Fprintf(&b, "时间：%s（UTC+8）", now.In(tz8).Format("01-02 15:04:05"))
	if px > 0 && len(sub.InstIDs) > 0 {
		fmt.Fprintf(&b, " · %s 现价 %s", coin(sub.InstIDs[0]), fmtPx(px))
	}
	b.WriteString("\n")
	if matched != "" {
		b.WriteString("触发值：" + matched + "\n")
	}
	if sub.Intent != "" {
		b.WriteString("你订阅时的计划：" + sub.Intent + "\n")
	}
	s.mu.Lock()
	ctxf := s.context
	s.mu.Unlock()
	if ctxf != nil && (sub.TradeID > 0 || sub.Kind == KindChat) {
		if c := ctxf(sub.ChatID, sub.InstIDs, sub.TradeID); c != "" {
			b.WriteString(c + "\n")
		}
	}
	if sub.PlanID > 0 {
		if p, ok := s.src.Plan(sub.PlanID); ok {
			fmt.Fprintf(&b, "关联计划 #%d：%s %s · 入场 %s–%s · 止损 %s · 止盈 %s", p.ID, map[bool]string{true: "做多", false: "做空"}[p.Side == "long"],
				coin(p.InstID), fmtPx(p.EntryLo), fmtPx(p.EntryHi), fmtPx(p.Stop), fmtPx(p.TP1))
			if p.TP2 > 0 {
				b.WriteString(" / " + fmtPx(p.TP2))
			}
			if p.Invalidate != "" {
				b.WriteString(" · 失效：" + p.Invalidate)
			}
			b.WriteString("（当前状态 " + p.Status + "）\n")
		}
	}
	if left := sub.MaxFires - sub.FireCount; left > 0 {
		fmt.Fprintf(&b, "这是第 %d/%d 次触发；订阅仍有效（冷却 %d 分钟，%s 到期）。\n", sub.FireCount, sub.MaxFires, sub.CooldownMin,
			time.UnixMilli(sub.ExpiresAt).In(tz8).Format("01-02 15:04"))
	} else {
		fmt.Fprintf(&b, "这是第 %d/%d 次触发；订阅已结束。\n", sub.FireCount, sub.MaxFires)
	}
	b.WriteString("\n请先用最新数据（有仓位就先 get_positions）重新检查，不要沿用订阅时的数字，然后三选一：\n" +
		"1. 计划仍然成立 → 用 propose_trade 提交对应的操作（开仓 / 补仓 / 减仓 / 平仓 / 调整止盈止损，主人审核后才执行）；\n" +
		"2. 还差一步 → 用 subscribe_signal 订阅调整后的条件（管理仓位的带 tradeId），并说明为什么；\n" +
		"3. 已经不成立 → 说明原因；若这个订阅仍有效且不再需要，用 unsubscribe_signal 取消。\n" +
		"（这是自动触发的信号会话，主人此刻不一定在看。你只能用 propose_trade 提交待主人审核的提议，不能下单；" +
		"没有足够把握就不要提议，直接说明理由。最多提议一笔。）")
	return b.String()
}

// tryWake starts one queued wake-up; it reports whether it is settled (started, failed).
func (s *Service) tryWake(fireID int64) bool {
	var subID, ts int64
	var msg string
	if err := s.db.QueryRow(`SELECT sub_id, ts, message FROM signal_fires WHERE id=? AND wake='queued'`, fireID).Scan(&subID, &ts, &msg); err != nil {
		return true
	}
	sub, err := s.get(subID)
	if err != nil {
		return s.wakeDone(fireID, WakeFailed, 0, err.Error())
	}
	s.mu.Lock()
	w := s.waker
	s.mu.Unlock()
	now := s.now()
	if w == nil {
		if now.Sub(time.UnixMilli(ts)) > queueTimeout {
			return s.wakeDone(fireID, WakeFailed, 0, "AI 未接入")
		}
		return false
	}
	chat := sub.ChatID
	if sub.Kind == KindChat {
		err = w.Wake(chat, msg)
	} else {
		title := "信号 · " + sub.Desc
		if r := []rune(title); len(r) > 40 {
			title = string(r[:40]) + "…"
		}
		inst := ""
		if len(sub.InstIDs) > 0 {
			inst = sub.InstIDs[0]
		}
		chat, err = w.Start(title, msg, inst)
	}
	switch {
	case err == nil:
		return s.wakeDone(fireID, WakeStarted, chat, "")
	case errors.Is(err, ErrBusy), errors.Is(err, ErrAgentDown):
		if now.Sub(time.UnixMilli(ts)) > queueTimeout {
			return s.wakeDone(fireID, WakeFailed, 0, "排队超过 2 小时仍未能唤醒："+err.Error())
		}
		return false
	}
	if sub.Kind == KindChat && sub.Status == StatusActive && strings.Contains(err.Error(), "not found") {
		s.Cancel(sub.ID, "system", "订阅它的对话已不存在")
	}
	return s.wakeDone(fireID, WakeFailed, 0, err.Error())
}

func (s *Service) wakeDone(fireID int64, state string, chat int64, errText string) bool {
	now := s.now().UnixMilli()
	res, _ := s.db.Exec(`UPDATE signal_fires SET wake=?, wake_chat=?, wake_at=?, error=? WHERE id=? AND wake='queued'`, state, chat, now, errText, fireID)
	if n, _ := res.RowsAffected(); n == 1 {
		var sub int64
		s.db.QueryRow(`SELECT sub_id FROM signal_fires WHERE id=?`, fireID).Scan(&sub)
		s.changed("woken", sub, chat)
	}
	return true
}

// processQueue retries queued wake-ups, oldest first.
func (s *Service) processQueue() {
	rows, err := s.db.Query(`SELECT id FROM signal_fires WHERE wake='queued' ORDER BY id`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.tryWake(id)
	}
}

// push sends the short note on fire.
func (s *Service) push(sub Subscription, fireID int64, matched string, px float64) {
	s.mu.Lock()
	n := s.notify
	s.mu.Unlock()
	if n == nil {
		return
	}
	var wake, errText string
	var chat int64
	s.db.QueryRow(`SELECT wake, wake_chat, error FROM signal_fires WHERE id=?`, fireID).Scan(&wake, &chat, &errText)
	text := fmt.Sprintf("⚡ 信号 #%d 触发：%s", sub.ID, sub.Desc)
	if px > 0 && len(sub.InstIDs) > 0 {
		text += fmt.Sprintf("（%s 现价 %s）", coin(sub.InstIDs[0]), fmtPx(px))
	}
	text += "\n订阅者：" + sub.Subscriber
	switch wake {
	case WakeStarted:
		text += "\n→ AI 正在评估"
	case WakeQueued:
		text += "\n→ 对话正忙，结束后 AI 再评估"
	default:
		text += "\n→ 未唤醒 AI：" + errText
	}
	if chat == 0 {
		chat = sub.ChatID
	}
	link := ""
	if s.opts.PublicURL != "" && chat > 0 {
		link = s.opts.PublicURL + "/go/chat/" + strconv.FormatInt(chat, 10)
	}
	n(text, link)
}

// expireDue ends subscriptions past their expiry.
func (s *Service) expireDue() {
	now := s.now().UnixMilli()
	rows, err := s.db.Query(`SELECT id, chat_id FROM signal_subs WHERE status='active' AND expires_at<=?`, now)
	if err != nil {
		return
	}
	type ended struct{ id, chat int64 }
	var es []ended
	for rows.Next() {
		var e ended
		rows.Scan(&e.id, &e.chat)
		es = append(es, e)
	}
	rows.Close()
	for _, e := range es {
		res, _ := s.db.Exec(`UPDATE signal_subs SET status='expired', status_at=?, updated=?, ended_by='timeout' WHERE id=? AND status='active'`, now, now, e.id)
		if n, _ := res.RowsAffected(); n == 1 {
			s.mu.Lock()
			delete(s.live, e.id)
			s.mu.Unlock()
			s.changed("expired", e.id, e.chat)
		}
	}
	if len(es) > 0 {
		s.keep()
	}
}

// keep streams the active subscriptions' instruments.
func (s *Service) keep() {
	s.mu.Lock()
	var ids []string
	for _, l := range s.live {
		for _, id := range l.sub.InstIDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	s.mu.Unlock()
	s.src.Keep(ids)
}

// ---------- subscribe / cancel ----------

// SubscribeInput creates a subscription. ChatID > 0 = the agent in that chat; 0 = the owner.
type SubscribeInput struct {
	ChatID       int64
	Spec         Spec
	Intent       string
	ExpiresHours float64
	PlanID       int64
	TradeID      int64 // the AI position this watch manages: cancelled when it closes
	MaxFires     int
	CooldownMin  int
	Every        string // check interval, see Spec.Every
}

// Subscribe validates the condition (it must be known and false right now), checks the caps and stores it.
func (s *Service) Subscribe(in SubscribeInput) (Subscription, error) {
	return s.SubscribeContext(context.Background(), in)
}

// SubscribeContext is Subscribe; ctx bounds a script's validation (compile + replay, seconds).
func (s *Service) SubscribeContext(ctx context.Context, in SubscribeInput) (Subscription, error) {
	now := s.now()
	in.Intent = strings.TrimSpace(in.Intent)
	switch {
	case utf8.RuneCountInString(in.Intent) < 6:
		return Subscription{}, errors.New("intent is required: what you will do when it fires (the plan: entry, stop, targets, invalidation)")
	case utf8.RuneCountInString(in.Intent) > maxIntent:
		return Subscription{}, fmt.Errorf("intent is too long (≤ %d characters)", maxIntent)
	}
	spec := in.Spec
	if in.Every != "" {
		spec.Every = in.Every
	}
	replay, err := s.prepareScript(ctx, &spec)
	if err != nil {
		return Subscription{}, err
	}
	if err := spec.normalize(s.src, now, 0); err != nil {
		return Subscription{}, err
	}
	spec.capEvery() // alts: at least every minute
	if n := len(spec.leaves()); n > maxLeaves {
		return Subscription{}, fmt.Errorf("at most %d conditions per subscription (got %d)", maxLeaves, n)
	}
	exp := DefaultExpiry
	if in.ExpiresHours > 0 {
		exp = time.Duration(in.ExpiresHours * float64(time.Hour))
	}
	if exp < MinExpiry || exp > MaxExpiry {
		return Subscription{}, fmt.Errorf("expiresHours must be between %.2g and %.0f (7 days)", MinExpiry.Hours(), MaxExpiry.Hours())
	}
	maxFires := in.MaxFires
	if maxFires == 0 {
		maxFires = 1
	}
	if maxFires < 1 || maxFires > MaxFiresCap {
		return Subscription{}, fmt.Errorf("maxFires must be between 1 and %d", MaxFiresCap)
	}
	cool := in.CooldownMin
	if cool == 0 {
		cool = DefaultCooldown
	}
	if cool < MinCooldown || cool > 1440 {
		return Subscription{}, fmt.Errorf("cooldownMin must be between %d and 1440", MinCooldown)
	}
	kind, label := KindOwner, ""
	if in.ChatID > 0 {
		s.mu.Lock()
		w := s.waker
		s.mu.Unlock()
		if w == nil {
			return Subscription{}, errors.New("the agent is not wired; cannot subscribe for a chat")
		}
		t, ok := w.ChatTitle(in.ChatID)
		if !ok {
			return Subscription{}, fmt.Errorf("chat %d not found", in.ChatID)
		}
		kind, label = KindChat, t
	}
	if in.PlanID > 0 {
		if _, ok := s.src.Plan(in.PlanID); !ok {
			return Subscription{}, fmt.Errorf("unknown plan %d", in.PlanID)
		}
	}
	var active, mine int
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(chat_id=? AND kind='chat'),0) FROM signal_subs WHERE status='active'`, in.ChatID).Scan(&active, &mine)
	if active >= s.opts.MaxActive {
		return Subscription{}, fmt.Errorf("%d subscriptions are active; the cap is %d — cancel one first (list_signals)", active, s.opts.MaxActive)
	}
	if kind == KindChat && mine >= MaxPerChat {
		return Subscription{}, fmt.Errorf("this conversation already has %d active subscriptions (cap %d) — one per setup; cancel or reuse one", mine, MaxPerChat)
	}
	// the condition must be readable and false now: it fires on the change, not on what already holds
	s.evalMu.Lock()
	defer s.evalMu.Unlock()
	root := build(spec)
	var script *ScriptState
	if replay != nil {
		script = &ScriptState{Replay: replay, Last: replay.Now}
		root.setScript(replay.Now)
	}
	switch root.eval(s.src, event{kind: "init"}, now) {
	case unknown:
		return Subscription{}, fmt.Errorf("no current data for: %s — cannot watch it yet", strings.Join(root.unknowns(), "；"))
	case yes:
		return Subscription{}, fmt.Errorf("the condition already holds now (%s); a subscription fires only when it turns true. "+
			"Act on it now, or subscribe to the next level", strings.Join(root.observed(), "；"))
	}
	raw, _ := json.Marshal(spec)
	scriptRaw := ""
	if script != nil {
		b, _ := json.Marshal(script)
		scriptRaw = string(b)
	}
	desc := spec.Label()
	res, err := s.db.Exec(`INSERT INTO signal_subs(created, updated, kind, chat_id, label, inst_ids, spec, descr, intent, plan_id, expires_at,
		max_fires, cooldown_min, state, status, status_at, script, trade_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now.UnixMilli(), now.UnixMilli(), kind, in.ChatID, label, strings.Join(spec.InstIDs(), ","), string(raw), desc, in.Intent, in.PlanID,
		now.Add(exp).UnixMilli(), maxFires, cool, int(no), StatusActive, now.UnixMilli(), scriptRaw, max(in.TradeID, 0))
	if err != nil {
		return Subscription{}, err
	}
	id, _ := res.LastInsertId()
	sub, err := s.get(id)
	if err != nil {
		return sub, err
	}
	s.mu.Lock()
	s.live[id] = newLive(sub, root)
	s.mu.Unlock()
	s.keep()
	s.changed("subscribed", id, in.ChatID)
	return s.Get(id)
}

// Cancel ends an active subscription. by: "owner", "chat:<id>", "system".
func (s *Service) Cancel(id int64, by, reason string) (Subscription, error) {
	reason = strings.TrimSpace(reason)
	if r := []rune(reason); len(r) > 300 {
		reason = string(r[:300])
	}
	now := s.now().UnixMilli()
	res, err := s.db.Exec(`UPDATE signal_subs SET status='cancelled', status_at=?, updated=?, ended_by=?, reason=? WHERE id=? AND status='active'`,
		now, now, by, reason, id)
	if err != nil {
		return Subscription{}, err
	}
	sub, gerr := s.Get(id)
	if gerr != nil {
		return sub, gerr
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sub, ErrEnded
	}
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
	s.keep()
	s.changed("cancelled", id, sub.ChatID)
	return sub, nil
}

// CancelFor is the agent's unsubscribe: only its own chat's subscriptions, unless the owner asked.
func (s *Service) CancelFor(chat, id int64, reason string, ownerAsked bool) (Subscription, error) {
	sub, err := s.get(id)
	if err != nil {
		return sub, err
	}
	if !ownerAsked && (sub.Kind != KindChat || sub.ChatID != chat) {
		return sub, ErrNotYours
	}
	by := "chat:" + strconv.FormatInt(chat, 10)
	if ownerAsked && sub.ChatID != chat {
		by += "（主人要求）"
	}
	return s.Cancel(id, by, reason)
}

// CancelForTrade ends the active subscriptions that manage an AI position that closed (by system,
// with reason) and returns their ids.
func (s *Service) CancelForTrade(tradeID int64, reason string) []int64 {
	if tradeID <= 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT id FROM signal_subs WHERE status='active' AND trade_id=?`, tradeID)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []int64
	for _, id := range ids {
		if _, err := s.Cancel(id, "system", reason); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// BriefFor lists, in one line, the active subscriptions that manage an AI position (tradeID), or a
// chat's on an instrument when tradeID is 0: "#id condition（intent）".
func (s *Service) BriefFor(chat int64, instID string, tradeID int64) string {
	q := ListQuery{Status: StatusActive, Limit: 10}
	if tradeID > 0 {
		subs, err := s.query(`status='active' AND trade_id=?`, []any{tradeID}, 10)
		if err != nil || len(subs) == 0 {
			if chat <= 0 {
				return ""
			}
		} else {
			return brief(subs)
		}
	}
	if chat <= 0 {
		return ""
	}
	q.ChatID, q.InstID = chat, instID
	subs, err := s.List(q)
	if err != nil {
		return ""
	}
	return brief(subs)
}

func brief(subs []Subscription) string {
	parts := make([]string, 0, len(subs))
	for _, x := range subs {
		p := fmt.Sprintf("#%d %s", x.ID, x.Desc)
		if x.TradeID > 0 {
			p += fmt.Sprintf("（仓位 #%d）", x.TradeID)
		}
		if x.Intent != "" {
			p += "（" + clip(x.Intent, 80) + "）"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "；")
}

// ---------- reads ----------

const subCols = `id, created, updated, kind, chat_id, label, spec, descr, intent, plan_id, expires_at, max_fires, cooldown_min,
  fires, last_fired_at, state, status, status_at, ended_by, reason, script, trade_id`

func scanSub(row interface{ Scan(...any) error }) (Subscription, error) {
	var x Subscription
	var raw, script string
	var st int
	err := row.Scan(&x.ID, &x.Created, &x.Updated, &x.Kind, &x.ChatID, &x.Label, &raw, &x.Desc, &x.Intent, &x.PlanID, &x.ExpiresAt,
		&x.MaxFires, &x.CooldownMin, &x.FireCount, &x.LastFiredAt, &st, &x.Status, &x.StatusAt, &x.EndedBy, &x.Reason, &script, &x.TradeID)
	if err != nil {
		return x, err
	}
	x.state = tri(st)
	json.Unmarshal([]byte(raw), &x.Spec)
	x.Script = scriptInfo(x.Spec, script)
	x.InstIDs = x.Spec.InstIDs()
	if x.InstIDs == nil {
		x.InstIDs = []string{}
	}
	x.Levels = x.Spec.levels()
	x.Subscriber = "主人"
	if x.Kind == KindChat {
		x.Subscriber = fmt.Sprintf("AI 对话 #%d · %s", x.ChatID, x.Label)
	}
	return x, nil
}

func (s *Service) get(id int64) (Subscription, error) {
	x, err := scanSub(s.db.QueryRow(`SELECT `+subCols+` FROM signal_subs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// Get returns a subscription with its fire history and, while active, its distance to firing.
func (s *Service) Get(id int64) (Subscription, error) {
	x, err := s.get(id)
	if err != nil {
		return x, err
	}
	rows, err := s.db.Query(`SELECT id, ts, px, matched, wake, wake_chat, wake_at, error FROM signal_fires WHERE sub_id=? ORDER BY id`, id)
	if err != nil {
		return x, err
	}
	defer rows.Close()
	x.History = []Fire{}
	for rows.Next() {
		var f Fire
		if err := rows.Scan(&f.ID, &f.Ts, &f.Px, &f.Matched, &f.Wake, &f.WakeChat, &f.WakeAt, &f.Error); err != nil {
			return x, err
		}
		x.History = append(x.History, f)
	}
	if x.Status == StatusActive {
		x.Progress = s.progress(x)
	}
	return x, rows.Err()
}

// ListQuery filters List. Status: active | fired | ended (expired, cancelled) | all.
type ListQuery struct {
	Status string
	InstID string
	ChatID int64
	Kind   string
	Limit  int
}

// List returns subscriptions newest first, active ones with their progress.
func (s *Service) List(q ListQuery) ([]Subscription, error) {
	where, args := []string{"1=1"}, []any{}
	switch q.Status {
	case "", "all":
	case "ended":
		where = append(where, "status IN ('expired','cancelled')")
	case StatusActive, StatusFired, StatusExpired, StatusCancelled:
		where = append(where, "status=?")
		args = append(args, q.Status)
	default:
		return nil, fmt.Errorf("unknown status %q", q.Status)
	}
	if q.InstID != "" {
		where = append(where, "(','||inst_ids||',') LIKE ?")
		args = append(args, "%,"+NormalizeInst(q.InstID)+",%")
	}
	if q.ChatID > 0 {
		where = append(where, "chat_id=? AND kind='chat'")
		args = append(args, q.ChatID)
	}
	if q.Kind != "" {
		where = append(where, "kind=?")
		args = append(args, q.Kind)
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	return s.query(strings.Join(where, " AND "), args, q.Limit)
}

func (s *Service) query(where string, args []any, limit int) ([]Subscription, error) {
	rows, err := s.db.Query(`SELECT `+subCols+` FROM signal_subs WHERE `+where+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	now := s.now()
	for rows.Next() {
		x, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.src != nil {
		for i := range out {
			if out[i].Status == StatusActive {
				out[i].Progress = s.progressAt(out[i], now)
			}
		}
	}
	return out, nil
}

func (s *Service) progress(x Subscription) *Progress { return s.progressAt(x, s.now()) }

// progressAt is the distance to firing; a script without a price leaf shows its newest check.
func (s *Service) progressAt(x Subscription, now time.Time) *Progress {
	if p := progress(x.Spec, s.src, now); p != nil || x.Script == nil {
		return p
	}
	return scriptProgress(x.Script)
}

// Caps is the configured limits and today's use.
type Caps struct {
	MaxActive   int          `json:"maxActive"`
	PerChat     int          `json:"perChat"`
	WakesPerDay int          `json:"wakesPerDay"`
	WakesToday  int          `json:"wakesToday"`
	Active      int          `json:"active"`
	MaxDays     int          `json:"maxDays"`
	Scripts     ScriptLimits `json:"scripts"`
}

func (s *Service) Caps() Caps {
	c := Caps{MaxActive: s.opts.MaxActive, PerChat: MaxPerChat, WakesPerDay: s.opts.WakesPerDay, WakesToday: s.wakesToday(s.now()), MaxDays: int(MaxExpiry.Hours() / 24),
		Scripts: s.limits()}
	s.db.QueryRow(`SELECT COUNT(*) FROM signal_subs WHERE status='active'`).Scan(&c.Active)
	return c
}

// LinkPlan attaches a plan the chat's agent just wrote to the subscriptions it made for that coin during
// the same answer (they were created before the plan had an id).
func (s *Service) LinkPlan(chat int64, instID string, planID int64) {
	if chat <= 0 || planID <= 0 {
		return
	}
	res, err := s.db.Exec(`UPDATE signal_subs SET plan_id=?, updated=? WHERE kind='chat' AND chat_id=? AND status='active' AND plan_id=0
		AND (','||inst_ids||',') LIKE ? AND created>=?`, planID, s.now().UnixMilli(), chat, "%,"+instID+",%", s.now().Add(-15*time.Minute).UnixMilli())
	if err != nil {
		s.logf("signals: link plan %d: %v", planID, err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.evalMu.Lock() // the evaluation reads l.sub
		s.mu.Lock()
		for _, l := range s.live {
			if l.sub.Kind == KindChat && l.sub.ChatID == chat && l.sub.PlanID == 0 && slices.Contains(l.sub.InstIDs, instID) {
				l.sub.PlanID = planID
			}
		}
		s.mu.Unlock()
		s.evalMu.Unlock()
		s.changed("linked", 0, chat)
	}
}

// WokenBy returns the subscription whose firing started the chat's current turn (a wake-up within
// the last 15 minutes), so a proposal made in it is labelled as coming from that signal.
func (s *Service) WokenBy(chat int64) (int64, bool) {
	if chat <= 0 {
		return 0, false
	}
	var id int64
	err := s.db.QueryRow(`SELECT sub_id FROM signal_fires WHERE wake_chat=? AND wake='started' AND wake_at>=? ORDER BY id DESC LIMIT 1`,
		chat, s.now().Add(-15*time.Minute).UnixMilli()).Scan(&id)
	return id, err == nil
}
