package realtrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Triggers start headless agent runs that may end in a proposal. A firing renders the trigger's
// prompt template and starts a normal AI chat titled "信号 · <label>" (so it is in the owner's history
// with every step); a propose_trade call from that chat is recorded with source signal / schedule.
//
// Adding a trigger type: implement Kind (Due for time-driven kinds, Match for event-driven ones),
// register it in kinds, and add a disabled default in defaultTriggers. Event sources call
// Service.Publish (internal/alerts does, through main).

// TriggerDef is one configured trigger (settings key realtrade.triggers; owner-editable over REST).
type TriggerDef struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"` // schedule | alert
	Label       string  `json:"label"`
	Enabled     bool    `json:"enabled"`
	EveryMin    int     `json:"everyMin,omitempty"` // schedule: fires at UTC multiples of this
	RuleIDs     []int64 `json:"ruleIds,omitempty"`  // alert: only these rules (empty = any rule)
	CooldownMin int     `json:"cooldownMin"`        // at least this long between two runs
	Prompt      string  `json:"prompt"`             // template, see vars

	// state (not stored with the config)
	LastFiredAt int64  `json:"lastFiredAt,omitempty"`
	LastChatID  int64  `json:"lastChatId,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

// Signal is an event a trigger may react to (Service.Publish).
type Signal struct {
	Type   string            // e.g. alert
	InstID string            // the instrument it is about, if any
	Vars   map[string]string // template variables
}

// Kind decides when a trigger of its type fires.
type Kind interface {
	// Due reports whether a time-driven trigger fires now, given its previous period start (event kinds: false).
	Due(def TriggerDef, prev, now time.Time) bool
	// Match reports whether an event fires the trigger (time-driven kinds: false).
	Match(def TriggerDef, sig Signal) bool
}

var kinds = map[string]Kind{"schedule": scheduleKind{}, "alert": alertKind{}}

type scheduleKind struct{}

func (scheduleKind) Due(d TriggerDef, prev, now time.Time) bool {
	if d.EveryMin <= 0 {
		return false
	}
	every := int64(d.EveryMin) * 60_000
	return now.UnixMilli()/every > prev.UnixMilli()/every
}
func (scheduleKind) Match(TriggerDef, Signal) bool { return false }

type alertKind struct{}

func (alertKind) Due(TriggerDef, time.Time, time.Time) bool { return false }
func (alertKind) Match(d TriggerDef, sig Signal) bool {
	if sig.Type != "alert" {
		return false
	}
	if len(d.RuleIDs) == 0 {
		return true
	}
	id, _ := strconv.ParseInt(sig.Vars["ruleId"], 10, 64)
	return slices.Contains(d.RuleIDs, id)
}

// promptFooter is appended to every signal prompt: the run is unattended and can only propose.
const promptFooter = "\n\n（这是自动触发的信号会话，主人此刻不一定在看。你只能用 propose_trade 提交待主人审核的交易提议，不能下单；" +
	"没有足够把握就不要提议，直接说明不交易的理由。最多提议一笔。）"

var defaultTriggers = []TriggerDef{
	{ID: "scan", Kind: "schedule", Label: "定时扫描", EveryMin: 240, CooldownMin: 60,
		Prompt: "定时扫描（{{time}}）：看一下大盘（BTC、ETH）和监控池，找出此刻风险收益最好的一个永续合约交易机会" +
			"（市价入场 + 止损 + 止盈，逐仓），做完整的多空分析。"},
	{ID: "alerts", Kind: "alert", Label: "告警信号", CooldownMin: 60,
		Prompt: "告警触发（{{time}}）：规则「{{rule}}」在 {{instId}} 触发：{{message}}。分析这个合约当前是否有值得做的交易" +
			"（市价入场 + 止损 + 止盈，逐仓），给出多空两方面的理由。"},
}

const (
	triggersKey      = "realtrade.triggers"
	triggerTick      = 30 * time.Second
	minEvery         = 15
	minCooldown      = 10
	defaultRunsDaily = 12
)

// AgentRunner starts a headless agent run as a new chat and returns its id (aigate.StartChat).
type AgentRunner func(title, prompt, instID string) (int64, error)

type triggers struct {
	s       *Service
	mu      sync.Mutex
	defs    []TriggerDef
	errs    map[string]string
	runner  AgentRunner
	signals chan Signal
	started time.Time
	daily   int
}

func newTriggers(s *Service) *triggers {
	t := &triggers{s: s, errs: map[string]string{}, signals: make(chan Signal, 64), started: s.now(), daily: defaultRunsDaily}
	if v, err := strconv.Atoi(os.Getenv("CTG_TRADE_MAX_RUNS_DAY")); err == nil && v >= 0 {
		t.daily = v
	}
	t.defs = mergeDefs(defaultTriggers, nil)
	if raw, ok := s.st.Setting(triggersKey); ok {
		var saved []TriggerDef
		if json.Unmarshal([]byte(raw), &saved) == nil {
			t.defs = mergeDefs(defaultTriggers, saved)
		} else {
			s.logf("realtrade: ignoring unreadable trigger config")
		}
	}
	return t
}

// mergeDefs applies saved settings onto the built-in triggers (kind and id fixed by the code).
func mergeDefs(base, saved []TriggerDef) []TriggerDef {
	out := make([]TriggerDef, len(base))
	copy(out, base)
	for i := range out {
		out[i].Enabled = false
		for _, v := range saved {
			if v.ID != out[i].ID {
				continue
			}
			out[i].Enabled = v.Enabled
			if v.EveryMin >= minEvery {
				out[i].EveryMin = v.EveryMin
			}
			if v.CooldownMin >= minCooldown {
				out[i].CooldownMin = v.CooldownMin
			}
			out[i].RuleIDs = v.RuleIDs
			if p := strings.TrimSpace(v.Prompt); p != "" {
				out[i].Prompt = p
			}
		}
	}
	return out
}

// SetRunner wires the agent (aigate). Without it triggers record an error instead of running.
func (s *Service) SetRunner(r AgentRunner) {
	s.triggers.mu.Lock()
	s.triggers.runner = r
	s.triggers.mu.Unlock()
}

// Publish hands an event to the event-driven triggers (never blocks; dropped when the queue is full).
func (s *Service) Publish(sig Signal) {
	select {
	case s.triggers.signals <- sig:
	default:
	}
}

// Triggers lists the triggers with their last run.
func (s *Service) Triggers() []TriggerDef {
	t := s.triggers
	t.mu.Lock()
	defs := append([]TriggerDef(nil), t.defs...)
	for i := range defs {
		defs[i].LastError = t.errs[defs[i].ID]
	}
	t.mu.Unlock()
	out := make([]TriggerDef, len(defs))
	for i, d := range defs {
		s.db.QueryRow(`SELECT ts, chat_id FROM trade_trigger_runs WHERE trigger_id=? ORDER BY ts DESC LIMIT 1`, d.ID).Scan(&d.LastFiredAt, &d.LastChatID)
		out[i] = d
	}
	return out
}

// SaveTriggers updates triggers by id (enabled, everyMin, ruleIds, cooldownMin, prompt); unknown ids are rejected.
func (s *Service) SaveTriggers(in []TriggerDef) ([]TriggerDef, error) {
	t := s.triggers
	t.mu.Lock()
	cur := append([]TriggerDef(nil), t.defs...)
	t.mu.Unlock()
	for _, v := range in {
		i := slices.IndexFunc(cur, func(d TriggerDef) bool { return d.ID == v.ID })
		if i < 0 {
			return nil, fmt.Errorf("unknown trigger %q", v.ID)
		}
		if v.EveryMin != 0 && v.EveryMin < minEvery {
			return nil, fmt.Errorf("everyMin must be at least %d", minEvery)
		}
		if v.CooldownMin != 0 && v.CooldownMin < minCooldown {
			return nil, fmt.Errorf("cooldownMin must be at least %d", minCooldown)
		}
		if utf8.RuneCountInString(v.Prompt) > 4000 {
			return nil, errors.New("prompt is too long")
		}
		if v.Prompt == "" {
			v.Prompt = cur[i].Prompt
		}
		if v.EveryMin == 0 {
			v.EveryMin = cur[i].EveryMin
		}
		if v.CooldownMin == 0 {
			v.CooldownMin = cur[i].CooldownMin
		}
		cur[i].Enabled, cur[i].EveryMin, cur[i].CooldownMin, cur[i].RuleIDs, cur[i].Prompt = v.Enabled, v.EveryMin, v.CooldownMin, v.RuleIDs, v.Prompt
	}
	raw, _ := json.Marshal(cur)
	if err := s.st.PutSetting(triggersKey, string(raw)); err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.defs = mergeDefs(defaultTriggers, cur)
	t.mu.Unlock()
	return s.Triggers(), nil
}

func (t *triggers) run(ctx context.Context) {
	tick := time.NewTicker(triggerTick)
	defer tick.Stop()
	prev := t.s.now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			now = t.s.now()
			for _, d := range t.snapshot() {
				if d.Enabled && kinds[d.Kind] != nil && kinds[d.Kind].Due(d, prev, now) {
					t.fire(d, Signal{Vars: map[string]string{}})
				}
			}
			prev = now
		case sig := <-t.signals:
			for _, d := range t.snapshot() {
				if d.Enabled && kinds[d.Kind] != nil && kinds[d.Kind].Match(d, sig) {
					t.fire(d, sig)
				}
			}
		}
	}
}

func (t *triggers) snapshot() []TriggerDef {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TriggerDef(nil), t.defs...)
}

func (t *triggers) fail(id, msg string) {
	t.mu.Lock()
	t.errs[id] = msg
	t.mu.Unlock()
	t.s.logf("realtrade: trigger %s: %s", id, msg)
}

// fire starts one agent run, subject to the trigger's cooldown and the daily run cap.
func (t *triggers) fire(d TriggerDef, sig Signal) {
	now := t.s.now()
	var last int64
	t.s.db.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM trade_trigger_runs WHERE trigger_id=?`, d.ID).Scan(&last)
	if last > 0 && now.Sub(time.UnixMilli(last)) < time.Duration(d.CooldownMin)*time.Minute {
		return
	}
	var today int
	t.s.db.QueryRow(`SELECT COUNT(*) FROM trade_trigger_runs WHERE ts>=?`, now.Add(-24*time.Hour).UnixMilli()).Scan(&today)
	if today >= t.daily {
		t.fail(d.ID, fmt.Sprintf("已达每日信号会话上限 %d（CTG_TRADE_MAX_RUNS_DAY）", t.daily))
		return
	}
	t.mu.Lock()
	run := t.runner
	t.mu.Unlock()
	if run == nil {
		t.fail(d.ID, "AI 未接入")
		return
	}
	vars := map[string]string{"time": now.In(tz8).Format("2006-01-02 15:04"), "instId": sig.InstID}
	for k, v := range sig.Vars {
		vars[k] = v
	}
	pairs := make([]string, 0, 2*len(vars))
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	prompt := strings.NewReplacer(pairs...).Replace(d.Prompt) + promptFooter
	title := "信号 · " + d.Label
	if sig.InstID != "" {
		title += " · " + coin(sig.InstID)
	}
	chat, err := run(title, prompt, sig.InstID)
	if err != nil {
		t.fail(d.ID, err.Error())
		return
	}
	kind := SourceSignal
	if d.Kind == "schedule" {
		kind = SourceSchedule
	}
	if _, err := t.s.db.Exec(`INSERT OR REPLACE INTO trade_trigger_runs(chat_id, trigger_id, kind, label, ts) VALUES(?,?,?,?,?)`,
		chat, d.ID, kind, d.Label, now.UnixMilli()); err != nil {
		t.s.logf("realtrade: trigger run %d: %v", chat, err)
	}
	t.mu.Lock()
	delete(t.errs, d.ID)
	t.mu.Unlock()
}

// triggerRun returns the source kind and label of a chat a trigger started.
func (s *Service) triggerRun(chat int64) (string, string, bool) {
	if chat <= 0 {
		return "", "", false
	}
	var kind, label string
	err := s.db.QueryRow(`SELECT kind, label FROM trade_trigger_runs WHERE chat_id=?`, chat).Scan(&kind, &label)
	return kind, label, err == nil
}
