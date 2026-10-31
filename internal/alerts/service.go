package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/store"
)

const (
	settingKey     = "alert_channels"
	historyDays    = 30
	historyMaxRows = 5000
	maxRules       = 100
	queueSize      = 64
	lastKeep       = 8 * 24 * time.Hour // > the longest cooldown (7 days)
)

// Channels selects where a rule's alerts go (history is always kept).
type Channels struct {
	Telegram bool `json:"telegram"`
	Webhook  bool `json:"webhook"`
}

// Rule is the API form of a monitoring rule.
type Rule struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	Type        string          `json:"type"`
	Params      json.RawMessage `json:"params"`
	Scope       string          `json:"scope"` // active | watch | inst
	InstID      string          `json:"instId,omitempty"`
	CooldownMin int             `json:"cooldownMin"`
	Channels    Channels        `json:"channels"`
	CreatedAt   int64           `json:"createdAt"`
	UpdatedAt   int64           `json:"updatedAt"`
	// derived
	Summary   string `json:"summary"`
	LastFired int64  `json:"lastFired,omitempty"`
	Error     string `json:"error,omitempty"` // stored rule no longer valid (e.g. its scoring rule was deleted)
}

type compiled struct {
	Rule
	cond   Condition
	primed bool // false until the first evaluation after load/save: record state without firing
}

type job struct {
	events []store.AlertEvent
	chans  map[int64]Channels
}

type Service struct {
	st   *store.Store
	env  Env
	send sender

	mu      sync.Mutex
	rules   map[int64]*compiled
	state   map[store.AlertKey]bool  // last known condition (edge trigger)
	last    map[store.AlertKey]int64 // last fire time (cooldown)
	lastMin int64                    // last evaluated minute

	chMu sync.RWMutex
	ch   ChannelConfig

	queue chan job
}

// New loads rules, channel config and recent fire times. client is used for deliveries.
func New(st *store.Store, env Env, client *http.Client) (*Service, error) {
	s := &Service{st: st, env: env, rules: map[int64]*compiled{}, state: map[store.AlertKey]bool{}, last: map[store.AlertKey]int64{},
		queue: make(chan job, queueSize),
		send:  sender{client: client, hook: PublicOnlyClient(), telegramBase: "https://api.telegram.org", attempts: 3, timeout: 10 * time.Second, backoff: 2 * time.Second}}
	if raw, ok := st.Setting(settingKey); ok {
		if err := json.Unmarshal([]byte(raw), &s.ch); err != nil {
			log.Printf("alerts: ignoring unreadable channel config")
		}
	}
	rs, err := st.AlertRules()
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		c := compile(r, env)
		s.rules[r.ID] = c
	}
	if last, err := st.LastAlertFires(time.Now().Add(-lastKeep).UnixMilli()); err == nil {
		s.last = last
	}
	return s, nil
}

func compile(r store.AlertRule, env Env) *compiled {
	c := &compiled{Rule: Rule{ID: r.ID, Name: r.Name, Enabled: r.Enabled, Type: r.Type, Params: r.Params, Scope: r.Scope, InstID: r.InstID,
		CooldownMin: r.CooldownMin, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}}
	json.Unmarshal(r.Channels, &c.Channels)
	if t, ok := lookupType(r.Type); ok {
		cond, _, err := t.parse(r.Params, env)
		if err != nil {
			c.Error = err.Error()
		} else {
			c.cond, c.Summary = cond, cond.Summary()
		}
	} else {
		c.Error = "未知类型 " + r.Type
	}
	return c
}

// Run delivers queued alerts and prunes history until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.prune()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.prune()
		case j := <-s.queue:
			s.deliver(ctx, j)
		}
	}
}

func (s *Service) prune() {
	cut := time.Now().Add(-lastKeep).UnixMilli()
	s.mu.Lock()
	for k, ts := range s.last {
		if ts < cut {
			delete(s.last, k)
		}
	}
	s.mu.Unlock()
	if err := s.st.PruneAlertEvents(time.Now().Add(-historyDays*24*time.Hour).UnixMilli(), historyMaxRows); err != nil {
		log.Printf("alerts: prune history: %v", err)
	}
}

// ---------- rules ----------

func (s *Service) Types() []Type { return Types }

func (s *Service) Rules() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Rule, 0, len(s.rules))
	for _, c := range s.rules {
		r := c.Rule
		for k, ts := range s.last {
			if k.RuleID == r.ID && ts > r.LastFired {
				r.LastFired = ts
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var instRe = regexp.MustCompile(`^[A-Z0-9]{1,20}-USDT-SWAP$`)

// validate normalises and checks a rule, returning its condition and canonical params.
func (s *Service) validate(r *Rule) (Condition, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || utf8.RuneCountInString(r.Name) > 40 {
		return nil, fmt.Errorf("名称需为 1–40 个字")
	}
	t, ok := lookupType(r.Type)
	if !ok {
		return nil, fmt.Errorf("未知类型 %q", r.Type)
	}
	cond, norm, err := t.parse(r.Params, s.env)
	if err != nil {
		return nil, err
	}
	r.Params, _ = json.Marshal(norm)
	switch r.Scope {
	case "active", "watch":
		r.InstID = ""
	case "inst":
		r.InstID = strings.ToUpper(strings.TrimSpace(r.InstID))
		if !instRe.MatchString(r.InstID) {
			return nil, fmt.Errorf("合约需为 XXX-USDT-SWAP 形式")
		}
	default:
		return nil, fmt.Errorf("范围需为 active / watch / inst")
	}
	if r.CooldownMin < 0 || r.CooldownMin > 7*24*60 {
		return nil, fmt.Errorf("冷却时间需在 0 ~ 10080 分钟之间")
	}
	return cond, nil
}

// SaveRule creates (ID 0) or updates a rule. A saved rule starts fresh: its edge state is reset
// and its first minute only records state, so instruments already meeting the condition do not
// fire (a new score rule would otherwise alert on the whole top of the pool at once).
func (s *Service) SaveRule(r Rule) (Rule, error) {
	cond, err := s.validate(&r)
	if err != nil {
		return Rule{}, err
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == 0 {
		if len(s.rules) >= maxRules {
			return Rule{}, fmt.Errorf("最多 %d 条监控规则", maxRules)
		}
		r.CreatedAt = now
	} else if old, ok := s.rules[r.ID]; ok {
		r.CreatedAt = old.CreatedAt
	} else {
		return Rule{}, fmt.Errorf("规则不存在")
	}
	r.UpdatedAt = now
	ch, _ := json.Marshal(r.Channels)
	id, err := s.st.PutAlertRule(store.AlertRule{ID: r.ID, Name: r.Name, Enabled: r.Enabled, Type: r.Type, Params: r.Params, Scope: r.Scope,
		InstID: r.InstID, CooldownMin: r.CooldownMin, Channels: ch, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, fmt.Errorf("规则不存在")
	}
	if err != nil {
		return Rule{}, err
	}
	r.ID, r.Summary, r.Error, r.LastFired = id, cond.Summary(), "", 0
	s.rules[id] = &compiled{Rule: r, cond: cond}
	s.resetState(id)
	return r, nil
}

func (s *Service) DeleteRule(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("规则不存在")
	}
	if err := s.st.DeleteAlertRule(id); err != nil {
		return err
	}
	delete(s.rules, id)
	s.resetState(id)
	return nil
}

func (s *Service) resetState(id int64) {
	for k := range s.state {
		if k.RuleID == id {
			delete(s.state, k)
		}
	}
}

// ---------- evaluation ----------

func inScope(r *Rule, row *market.Row) bool {
	switch r.Scope {
	case "active":
		return row.Active
	case "watch":
		return row.Watch
	case "inst":
		return row.InstID == r.InstID
	}
	return false
}

// evaluate runs every enabled rule on a confirmed snapshot and returns the alerts that fire.
// Per (rule, instrument): fire when the condition turns true (known false/absent → true) and the
// cooldown since the last fire has passed. Unknown values (missing inputs) leave the state as is.
func (s *Service) evaluate(snap *market.Snapshot) []store.AlertEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap == nil || snap.AsOfMinute <= s.lastMin {
		return nil
	}
	s.lastMin = snap.AsOfMinute
	ts := snap.DecisionTs
	var out []store.AlertEvent
	ids := make([]int64, 0, len(s.rules))
	for id := range s.rules {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		c := s.rules[id]
		if !c.Enabled || c.cond == nil {
			continue
		}
		seen := map[string]bool{}
		for i := range snap.Rows {
			row := &snap.Rows[i]
			if !inScope(&c.Rule, row) {
				continue
			}
			seen[row.InstID] = true
			v, known, hit := c.cond.Check(row)
			if !known {
				continue
			}
			k := store.AlertKey{RuleID: id, InstID: row.InstID}
			was := s.state[k]
			s.state[k] = hit
			if !hit || was || !c.primed {
				continue
			}
			if last, ok := s.last[k]; ok && c.CooldownMin > 0 && ts-last < int64(c.CooldownMin)*60_000 {
				continue
			}
			s.last[k] = ts
			out = append(out, store.AlertEvent{Ts: ts, RuleID: id, RuleName: c.Name, InstID: row.InstID, Type: c.Type,
				Value: v, Message: c.cond.Message(row.InstID, v)})
		}
		for k := range s.state { // left the scope: re-entering counts as a new crossing
			if k.RuleID == id && !seen[k.InstID] {
				delete(s.state, k)
			}
		}
		c.primed = true
	}
	return out
}

// OnSnapshot is the engine hook: evaluate, record history, queue delivery.
func (s *Service) OnSnapshot(snap *market.Snapshot) {
	evs := s.evaluate(snap)
	if len(evs) == 0 {
		return
	}
	chans := map[int64]Channels{}
	s.mu.Lock()
	for _, ev := range evs {
		if c, ok := s.rules[ev.RuleID]; ok {
			chans[ev.RuleID] = c.Channels
		}
	}
	s.mu.Unlock()
	for i := range evs {
		ch := chans[evs[i].RuleID]
		evs[i].Delivery = map[string]string{}
		if ch.Telegram {
			evs[i].Delivery["telegram"] = "pending"
		}
		if ch.Webhook {
			evs[i].Delivery["webhook"] = "pending"
		}
		id, err := s.st.AddAlertEvent(evs[i])
		if err != nil {
			log.Printf("alerts: record: %v", err)
		}
		evs[i].ID = id
	}
	select {
	case s.queue <- job{events: evs, chans: chans}:
	default:
		for _, ev := range evs {
			for k := range ev.Delivery {
				ev.Delivery[k] = "失败：发送队列已满"
			}
			s.st.SetAlertDelivery(ev.ID, ev.Delivery)
		}
	}
}

// deliver sends one minute's alerts: one combined Telegram message, one webhook POST per alert.
func (s *Service) deliver(ctx context.Context, j job) {
	cfg := s.channels()
	var lines []int
	for i, ev := range j.events {
		if j.chans[ev.RuleID].Telegram {
			lines = append(lines, i)
		}
	}
	if len(lines) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "OKX Helper 监控 · %s（UTC+8）\n", time.UnixMilli(j.events[0].Ts).In(cst).Format("01-02 15:04"))
		for _, i := range lines {
			fmt.Fprintf(&b, "• [%s] %s\n", j.events[i].RuleName, j.events[i].Message)
		}
		res := status(s.send.telegram(ctx, cfg.Telegram, strings.TrimRight(b.String(), "\n")))
		for _, i := range lines {
			j.events[i].Delivery["telegram"] = res
		}
	}
	for i, ev := range j.events {
		if !j.chans[ev.RuleID].Webhook {
			continue
		}
		p := Payload{RuleID: ev.RuleID, Rule: ev.RuleName, InstID: ev.InstID, Type: ev.Type, Value: ev.Value, Message: ev.Message, Ts: ev.Ts}
		j.events[i].Delivery["webhook"] = status(s.send.webhook(ctx, cfg.Webhook, p))
	}
	for _, ev := range j.events {
		if len(ev.Delivery) > 0 && ev.ID > 0 {
			s.st.SetAlertDelivery(ev.ID, ev.Delivery)
		}
	}
}

var cst = time.FixedZone("UTC+8", 8*3600)

func status(err error) string {
	if err == nil {
		return "ok"
	}
	return "失败：" + err.Error()
}

// History returns recent alerts, newest first.
func (s *Service) History(limit int) ([]store.AlertEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.st.AlertEvents(limit)
}

// ---------- channels ----------

func (s *Service) channels() ChannelConfig {
	s.chMu.RLock()
	defer s.chMu.RUnlock()
	return s.ch
}

func (s *Service) ChannelView() ChannelView { return s.channels().View() }

func (s *Service) SaveChannels(in ChannelView) (ChannelView, error) {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	next, err := s.ch.Merge(in)
	if err != nil {
		return s.ch.View(), err
	}
	if err := s.persist(next); err != nil {
		return s.ch.View(), err
	}
	return s.ch.View(), nil
}

// ClearChannel removes one channel's configuration (telegram | webhook).
func (s *Service) ClearChannel(name string) (ChannelView, error) {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	next := s.ch
	switch name {
	case "telegram":
		next.Telegram = TelegramConfig{}
	case "webhook":
		next.Webhook = WebhookConfig{}
	default:
		return s.ch.View(), fmt.Errorf("未知渠道 %q", name)
	}
	if err := s.persist(next); err != nil {
		return s.ch.View(), err
	}
	return s.ch.View(), nil
}

func (s *Service) persist(c ChannelConfig) error { // chMu held
	b, _ := json.Marshal(c)
	if err := s.st.PutSetting(settingKey, string(b)); err != nil {
		return err
	}
	s.ch = c
	return nil
}

// Test sends a test message through one channel using the saved configuration.
// Notify sends a free-form message to the Telegram channel when one is configured (used by the AI
// chat to say an answer is ready); without a channel it does nothing.
func (s *Service) Notify(ctx context.Context, text string) error {
	cfg := s.channels()
	if !cfg.Telegram.Configured() {
		return nil
	}
	return s.send.telegram(ctx, cfg.Telegram, text)
}

func (s *Service) Test(ctx context.Context, name string) error {
	cfg := s.channels()
	now := time.Now()
	msg := "OKX Helper 监控测试消息 · " + now.In(cst).Format("01-02 15:04:05") + "（UTC+8）"
	switch name {
	case "telegram":
		return s.send.telegram(ctx, cfg.Telegram, msg)
	case "webhook":
		return s.send.webhook(ctx, cfg.Webhook, Payload{Rule: "测试", Type: "test", Message: msg, Ts: now.UnixMilli(), Test: true})
	}
	return fmt.Errorf("未知渠道 %q", name)
}
