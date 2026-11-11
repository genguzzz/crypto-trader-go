package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"crypto-trader-go/internal/aigate"
	"crypto-trader-go/internal/signals"
)

const configKey = "autopilot.config"

// Config is the desk's configuration (settings key autopilot.config; owner-editable over REST).
type Config struct {
	Enabled  bool           `json:"enabled"`
	Schedule ScheduleConfig `json:"schedule"`
	Signals  SignalConfig   `json:"signals"`
	// Strategies are the built-in scans with the owner's switches and parameters; Custom are scans the
	// owner wrote himself (any pool spec, e.g. a combo of his own fields).
	Strategies []StrategyConfig `json:"strategies"`
	Custom     []CustomStrategy `json:"custom"`
	// Model / Effort of the desk's runs ("" = the configured ones).
	Model  string `json:"model"`
	Effort string `json:"effort"`
	// SessionRuns: after this many runs the agent's session starts fresh (the journal and memory carry
	// what matters); 0 = never. Bounds the context a never-ending chat pays for on every run.
	SessionRuns int `json:"sessionRuns"`
	// MaxProposals a single run may make.
	MaxProposals int `json:"maxProposals"`
	// Focus is the owner's standing instruction, carried in every brief (≤ 600 characters).
	Focus string `json:"focus"`
}

// ScheduleConfig: a run LeadMin minutes before every EveryHours-th hour of the UTC+8 clock. OKX
// settles funding at 00:00 / 08:00 / 16:00 UTC+8 (4 h contracts also at 04 / 12 / 20), so with 2 h
// the desk looks at the market shortly before each settlement and each 4 h bar close.
type ScheduleConfig struct {
	Enabled    bool `json:"enabled"`
	EveryHours int  `json:"everyHours"` // 1 | 2 | 4 | 8
	LeadMin    int  `json:"leadMin"`    // 0–30
}

// SignalConfig paces signal runs.
type SignalConfig struct {
	Enabled    bool `json:"enabled"`
	MinGapMin  int  `json:"minGapMin"`  // at least this long between a run and the next signal run
	MaxRunsDay int  `json:"maxRunsDay"` // signal runs per 24 h
	BatchSec   int  `json:"batchSec"`   // a fire waits this long so its neighbours travel with it
	StaleMin   int  `json:"staleMin"`   // a fire that waited longer is dropped
}

// StrategyConfig is the owner's setting of one built-in strategy.
type StrategyConfig struct {
	Key     string             `json:"key"`
	Enabled bool               `json:"enabled"`
	Params  map[string]float64 `json:"params"`
}

// CustomStrategy is a pool scan the owner defined.
type CustomStrategy struct {
	Key         string          `json:"key"` // custom:<name>
	Label       string          `json:"label"`
	Enabled     bool            `json:"enabled"`
	Spec        json.RawMessage `json:"spec"` // a {"type":"pool", …} signal spec
	CooldownMin int             `json:"cooldownMin"`
}

// ParamDef describes one tunable of a built-in strategy.
type ParamDef struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Unit  string  `json:"unit"`
	Def   float64 `json:"def"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Step  float64 `json:"step"`
}

// StrategyDef is a built-in strategy: one or more pool scans built from its parameters.
type StrategyDef struct {
	Key    string     `json:"key"`
	Label  string     `json:"label"`
	Desc   string     `json:"desc"`
	Hint   string     `json:"hint"` // what the agent should check when it fires
	Params []ParamDef `json:"params"`
	On     bool       `json:"on"` // enabled by default
	build  func(p map[string]float64) []scanDef
}

// scanDef is one subscription of a strategy.
type scanDef struct {
	suffix   string // distinguishes the scans of one strategy ("" for a single scan)
	spec     signals.Spec
	cooldown int
}

func fp(v float64) *float64 { return &v }

var (
	pTop      = ParamDef{Key: "top", Label: "监控池前 N", Unit: "名", Def: 60, Min: 10, Max: 200, Step: 10}
	pCooldown = ParamDef{Key: "cooldownMin", Label: "同一币冷却", Unit: "分钟", Def: 120, Min: 30, Max: 1440, Step: 30}
)

// Strategies are the built-in scans. Field units: returns and OI change are log changes (×100 ≈ %),
// vol_surprise_4h is the log of the multiple of the 7-day median, funding_hourly is the rate per hour.
var Strategies = []StrategyDef{
	{Key: "surge", Label: "急涨急跌", On: true,
		Desc: "池内任一币在短窗口内涨或跌超过阈值",
		Hint: "先分清是延续还是一次性插针：看成交量、OI 是否跟随，大盘是否同向",
		Params: []ParamDef{pTop, {Key: "windowMin", Label: "窗口", Unit: "分钟", Def: 15, Min: 3, Max: 120, Step: 1},
			{Key: "pct", Label: "涨跌幅", Unit: "%", Def: 3, Min: 1, Max: 20, Step: 0.5}, pCooldown},
		build: func(p map[string]float64) []scanDef {
			return []scanDef{{spec: signals.Spec{Type: "pool", Scope: "pool", Top: int(p["top"]), Metric: "move", WindowMin: int(p["windowMin"]),
				Dir: "both", Pct: p["pct"]}, cooldown: int(p["cooldownMin"])}}
		}},
	{Key: "momo_up", Label: "放量上攻", On: true,
		Desc: "1 小时上涨 + 成交量放大 + 持仓增加：新资金推动的上涨",
		Hint: "顺势候选：确认不是已经超伸（move risk、资金费），找回踩入场位而不是追高",
		Params: []ParamDef{pTop, {Key: "pct", Label: "1 小时涨幅", Unit: "%", Def: 4, Min: 1, Max: 20, Step: 0.5},
			{Key: "volX", Label: "量能倍数（对 7 日中位）", Unit: "倍", Def: 2, Min: 1.2, Max: 10, Step: 0.1},
			{Key: "oiPct", Label: "1 小时持仓增加", Unit: "%", Def: 2, Min: 0, Max: 20, Step: 0.5}, pCooldown},
		build: func(p map[string]float64) []scanDef {
			return []scanDef{{spec: signals.Spec{Type: "pool", Scope: "pool", Top: int(p["top"]), Metric: "combo", Legs: []signals.Spec{
				{Metric: "move", WindowMin: 60, Dir: "up", Pct: p["pct"]},
				{Metric: "field", Field: "vol_surprise_4h", Op: ">=", Value: fp(round4(math.Log(p["volX"])))},
				{Metric: "field", Field: "oi_chg_1h", Op: ">=", Value: fp(round4(math.Log1p(p["oiPct"] / 100)))},
			}}, cooldown: int(p["cooldownMin"])}}
		}},
	{Key: "flush_down", Label: "放量下杀去杠杆", On: true,
		Desc: "1 小时下跌 + 成交量放大 + 持仓减少：多头被清洗",
		Hint: "看强平是否集中释放、是否到支撑：可能是反弹候选，也可能是破位延续，两边都要论证",
		Params: []ParamDef{pTop, {Key: "pct", Label: "1 小时跌幅", Unit: "%", Def: 4, Min: 1, Max: 20, Step: 0.5},
			{Key: "volX", Label: "量能倍数（对 7 日中位）", Unit: "倍", Def: 2, Min: 1.2, Max: 10, Step: 0.1},
			{Key: "oiPct", Label: "1 小时持仓减少", Unit: "%", Def: 2, Min: 0, Max: 20, Step: 0.5}, pCooldown},
		build: func(p map[string]float64) []scanDef {
			return []scanDef{{spec: signals.Spec{Type: "pool", Scope: "pool", Top: int(p["top"]), Metric: "combo", Legs: []signals.Spec{
				{Metric: "move", WindowMin: 60, Dir: "down", Pct: p["pct"]},
				{Metric: "field", Field: "vol_surprise_4h", Op: ">=", Value: fp(round4(math.Log(p["volX"])))},
				{Metric: "field", Field: "oi_chg_1h", Op: "<=", Value: fp(round4(math.Log1p(-p["oiPct"] / 100)))},
			}}, cooldown: int(p["cooldownMin"])}}
		}},
	{Key: "oi_build", Label: "持仓激增", On: true,
		Desc: "1 小时持仓大幅增加且价格有波动：杠杆在堆积",
		Hint: "看是谁在加仓（主动买卖、资金费方向）、清算地图上最近的密集区在哪一侧",
		Params: []ParamDef{pTop, {Key: "oiPct", Label: "1 小时持仓增加", Unit: "%", Def: 6, Min: 2, Max: 50, Step: 1},
			{Key: "rangePct", Label: "1 小时振幅", Unit: "%", Def: 2, Min: 0.5, Max: 20, Step: 0.5}, pCooldown},
		build: func(p map[string]float64) []scanDef {
			return []scanDef{{spec: signals.Spec{Type: "pool", Scope: "pool", Top: int(p["top"]), Metric: "combo", Legs: []signals.Spec{
				{Metric: "field", Field: "oi_chg_1h", Op: ">=", Value: fp(round4(math.Log1p(p["oiPct"] / 100)))},
				{Metric: "range", WindowMin: 60, Pct: p["rangePct"]},
			}}, cooldown: int(p["cooldownMin"])}}
		}},
	{Key: "funding_extreme", Label: "资金费极端", On: true,
		Desc: "每小时资金费率的绝对值超过阈值：一侧过度拥挤",
		Hint: "拥挤一侧付费：结算前后容易出现挤压或回吐；结合价格位置判断是顺势持有成本还是反向机会",
		Params: []ParamDef{{Key: "top", Label: "监控池前 N", Unit: "名", Def: 100, Min: 10, Max: 200, Step: 10},
			{Key: "ratePct", Label: "每小时费率", Unit: "%", Def: 0.03, Min: 0.005, Max: 0.5, Step: 0.005},
			{Key: "cooldownMin", Label: "同一币冷却", Unit: "分钟", Def: 480, Min: 60, Max: 1440, Step: 60}},
		build: func(p map[string]float64) []scanDef {
			r := round6(p["ratePct"] / 100)
			return []scanDef{
				{suffix: "pos", spec: signals.Spec{Type: "pool", Scope: "pool", Top: int(p["top"]), Metric: "field", Field: "funding_hourly", Op: ">=", Value: fp(r)}, cooldown: int(p["cooldownMin"])},
				{suffix: "neg", spec: signals.Spec{Type: "pool", Scope: "pool", Top: int(p["top"]), Metric: "field", Field: "funding_hourly", Op: "<=", Value: fp(-r)}, cooldown: int(p["cooldownMin"])},
			}
		}},
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

func strategyDef(key string) (StrategyDef, bool) {
	i := slices.IndexFunc(Strategies, func(d StrategyDef) bool { return d.Key == key })
	if i < 0 {
		return StrategyDef{}, false
	}
	return Strategies[i], true
}

// DefaultConfig is the desk as it ships: off, 2 h schedule 10 minutes before the hour, the built-in
// scans at their defaults.
func DefaultConfig() Config {
	c := Config{Schedule: ScheduleConfig{Enabled: true, EveryHours: 2, LeadMin: 10},
		Signals:     SignalConfig{Enabled: true, MinGapMin: 20, MaxRunsDay: 10, BatchSec: 90, StaleMin: 120},
		SessionRuns: 12, MaxProposals: 2}
	c, _ = c.normalize()
	return c
}

func (c Config) clone() Config {
	out := c
	out.Strategies = make([]StrategyConfig, len(c.Strategies))
	for i, s := range c.Strategies {
		out.Strategies[i] = StrategyConfig{Key: s.Key, Enabled: s.Enabled, Params: map[string]float64{}}
		for k, v := range s.Params {
			out.Strategies[i].Params[k] = v
		}
	}
	out.Custom = append([]CustomStrategy(nil), c.Custom...)
	return out
}

// strategy returns the label of a strategy key (a scan's key may carry a ":suffix").
func (c Config) strategy(key string) (struct{ Label, Hint string }, bool) {
	base := key
	if !strings.HasPrefix(key, "custom:") {
		base, _, _ = strings.Cut(key, ":")
	}
	if d, ok := strategyDef(base); ok {
		return struct{ Label, Hint string }{d.Label, d.Hint}, true
	}
	for _, x := range c.Custom {
		if x.Key == key {
			return struct{ Label, Hint string }{x.Label, ""}, true
		}
	}
	return struct{ Label, Hint string }{}, false
}

var everyHours = []int{1, 2, 4, 8}

// normalize validates a configuration and fills in every built-in strategy (missing ones at their
// defaults, parameters clamped to their ranges).
func (c Config) normalize() (Config, error) {
	if !slices.Contains(everyHours, c.Schedule.EveryHours) {
		if c.Schedule.EveryHours != 0 {
			return c, errors.New("schedule.everyHours must be 1, 2, 4 or 8")
		}
		c.Schedule.EveryHours = 2
	}
	if c.Schedule.LeadMin < 0 || c.Schedule.LeadMin > 30 {
		return c, errors.New("schedule.leadMin must be between 0 and 30")
	}
	def := func(v *int, d, lo, hi int, name string) error {
		if *v == 0 {
			*v = d
		}
		if *v < lo || *v > hi {
			return fmt.Errorf("%s must be between %d and %d", name, lo, hi)
		}
		return nil
	}
	if err := errors.Join(def(&c.Signals.MinGapMin, 20, 5, 240, "signals.minGapMin"), def(&c.Signals.BatchSec, 90, 10, 600, "signals.batchSec"),
		def(&c.Signals.StaleMin, 120, 15, 720, "signals.staleMin"), def(&c.MaxProposals, 2, 1, 5, "maxProposals")); err != nil {
		return c, err
	}
	if c.Signals.MaxRunsDay < 0 || c.Signals.MaxRunsDay > 48 {
		return c, errors.New("signals.maxRunsDay must be between 0 and 48")
	}
	if c.SessionRuns < 0 || c.SessionRuns > 200 {
		return c, errors.New("sessionRuns must be between 0 and 200")
	}
	if c.Model != "" && !slices.Contains(aigate.ChatModels, c.Model) {
		return c, errors.New("unknown model")
	}
	if c.Effort != "" && !slices.Contains(aigate.ChatEfforts, c.Effort) {
		return c, errors.New("unknown effort")
	}
	c.Focus = strings.TrimSpace(c.Focus)
	if utf8.RuneCountInString(c.Focus) > 600 {
		return c, errors.New("focus is too long (≤ 600 characters)")
	}
	out := make([]StrategyConfig, 0, len(Strategies))
	for _, d := range Strategies {
		sc := StrategyConfig{Key: d.Key, Enabled: d.On, Params: map[string]float64{}}
		if i := slices.IndexFunc(c.Strategies, func(x StrategyConfig) bool { return x.Key == d.Key }); i >= 0 {
			sc.Enabled = c.Strategies[i].Enabled
			for k, v := range c.Strategies[i].Params {
				sc.Params[k] = v
			}
		}
		for _, p := range d.Params {
			v, ok := sc.Params[p.Key]
			if !ok || math.IsNaN(v) {
				v = p.Def
			}
			if v < p.Min || v > p.Max {
				return c, fmt.Errorf("%s.%s must be between %g and %g", d.Key, p.Key, p.Min, p.Max)
			}
			sc.Params[p.Key] = v
		}
		for k := range sc.Params {
			if !slices.ContainsFunc(d.Params, func(p ParamDef) bool { return p.Key == k }) {
				delete(sc.Params, k)
			}
		}
		out = append(out, sc)
	}
	for _, x := range c.Strategies {
		if _, ok := strategyDef(x.Key); !ok {
			return c, fmt.Errorf("unknown strategy %q", x.Key)
		}
	}
	c.Strategies = out
	if len(c.Custom) > 8 {
		return c, errors.New("at most 8 custom strategies")
	}
	seen := map[string]bool{}
	for i := range c.Custom {
		x := &c.Custom[i]
		x.Label = strings.TrimSpace(x.Label)
		if x.Label == "" || utf8.RuneCountInString(x.Label) > 20 {
			return c, errors.New("a custom strategy needs a label of 1–20 characters")
		}
		if !strings.HasPrefix(x.Key, "custom:") || len(x.Key) > 40 || seen[x.Key] {
			return c, fmt.Errorf("custom strategy key %q must be unique and start with custom:", x.Key)
		}
		seen[x.Key] = true
		spec, err := signals.ParseSpec(string(x.Spec))
		if err != nil {
			return c, fmt.Errorf("%s: %w", x.Label, err)
		}
		if spec.Type != "pool" {
			return c, fmt.Errorf("%s: a desk strategy is a pool scan ({\"type\":\"pool\", …})", x.Label)
		}
		if x.CooldownMin == 0 {
			x.CooldownMin = 120
		}
		if x.CooldownMin < 30 || x.CooldownMin > 1440 {
			return c, fmt.Errorf("%s: cooldownMin must be between 30 and 1440", x.Label)
		}
	}
	if c.Custom == nil {
		c.Custom = []CustomStrategy{}
	}
	return c, nil
}

// desired are the scans the configuration asks for, by subscription key.
func (c Config) desired() map[string]scanDef {
	out := map[string]scanDef{}
	if !c.Enabled || !c.Signals.Enabled {
		return out
	}
	for _, sc := range c.Strategies {
		d, ok := strategyDef(sc.Key)
		if !ok || !sc.Enabled {
			continue
		}
		for _, sd := range d.build(sc.Params) {
			key := d.Key
			if sd.suffix != "" {
				key += ":" + sd.suffix
			}
			out[key] = sd
		}
	}
	for _, x := range c.Custom {
		if !x.Enabled {
			continue
		}
		if spec, err := signals.ParseSpec(string(x.Spec)); err == nil {
			out[x.Key] = scanDef{spec: spec, cooldown: x.CooldownMin}
		}
	}
	return out
}

func (d scanDef) hash() string {
	raw, _ := json.Marshal(d.spec)
	sum := sha256.Sum256(fmt.Appendf(raw, "|%d", d.cooldown))
	return hex.EncodeToString(sum[:8])
}

// reconcile makes the desk's signal subscriptions match the configuration: scans that are no longer
// wanted (or whose parameters changed) are cancelled, missing ones subscribed. A scan that cannot
// start (no pool data yet, an invalid custom spec) is reported per strategy and retried each minute.
func (s *Service) reconcile(ctx context.Context) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	cfg := s.Config()
	want := cfg.desired()
	have, err := s.scans.DeskSubs()
	if err != nil {
		s.logf("autopilot: scans: %v", err)
		return
	}
	hashes := map[string]string{}
	if rows, err := s.db.Query(`SELECT key, hash FROM desk_strategy_subs`); err == nil {
		for rows.Next() {
			var k, h string
			rows.Scan(&k, &h)
			hashes[k] = h
		}
		rows.Close()
	}
	errs := map[string]string{}
	for key, sub := range have {
		d, ok := want[key]
		if ok && hashes[key] == d.hash() {
			continue
		}
		reason := "交易台停用了这个策略"
		if ok {
			reason = "策略参数已修改"
		}
		if _, err := s.scans.Cancel(sub.ID, "desk", reason); err != nil && !errors.Is(err, signals.ErrEnded) {
			s.logf("autopilot: cancel scan %s: %v", key, err)
			continue
		}
		s.db.Exec(`DELETE FROM desk_strategy_subs WHERE key=?`, key)
		delete(have, key)
	}
	if len(want) > 0 {
		chat, err := s.ensureChat()
		if err != nil {
			s.logf("autopilot: chat: %v", err)
			return
		}
		for key, d := range want {
			if _, ok := have[key]; ok {
				continue
			}
			sub, err := s.scans.SubscribeContext(ctx, signals.SubscribeInput{ChatID: chat, Spec: d.spec, Intent: "交易台策略扫描：触发后进入交易台收件箱",
				CooldownMin: d.cooldown, Desk: key})
			if err != nil {
				errs[key] = err.Error()
				continue
			}
			s.db.Exec(`INSERT OR REPLACE INTO desk_strategy_subs(key, sub_id, hash) VALUES(?,?,?)`, key, sub.ID, d.hash())
		}
	}
	s.mu.Lock()
	s.stratErr = errs
	s.mu.Unlock()
}

func (s *Service) strategiesBroken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stratErr) > 0
}
