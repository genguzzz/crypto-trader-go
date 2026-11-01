package signals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Script conditions: {"type":"script","code":"<Python>"} is a ctg.Condition class (strategies/ctg/condition.py)
// that the strategy container runs on every close of its timeframe. Validation compiles it, checks what
// it declares (≤ 10 instruments, 1m–4H, lookback 10–500) and replays it over recent history (up to 7
// days, ≤ 1000 bars) to see how often it would have fired, how fast it is and whether it holds now.
// Live, each confirmed minute that closes a script's bar sends one batch with every due script to the
// worker; results come back as a "script" event. N consecutive errors end the subscription and push a
// note. docs/trading-design.md §8, "Script signals".

// Script limits (docs/operations.md).
const (
	MaxScriptBytes       = 8000
	MaxScripts           = 10 // active script subscriptions
	DefaultScriptMs      = 200
	DefaultScriptPerDay  = 6.0
	DefaultScriptErrors  = 3
	replayDays           = 7
	replayMaxBars        = 1000 // lookback + replayed closes per instrument (market.BarsTF reads ≤ 1000)
	replayTimeoutSeconds = 60
	maxReplayFires       = 50
	scriptInfraNotice    = 30 * time.Minute
)

// ScriptTemplate is the starting point of a new script (the app's editor, GET /api/v1/signals/script-template).
const ScriptTemplate = `from ctg import Condition, Fire, ta


class MySignal(Condition):
    """AAVE 15m RSI 从 30 以下收回"""  # 一行说明，显示在信号卡片上

    instruments = ["AAVE-USDT-SWAP"]  # 最多 10 个合约
    timeframe = "15m"                 # 每根 15m K 线收盘时运行：1m 5m 15m 1H 4H
    lookback = 100                    # 每个合约给多少根历史 K 线（10–500）

    def check(self, ctx, bars):
        b = bars["AAVE-USDT-SWAP"]     # b.ts b.o b.h b.l b.c b.vol b.volq：numpy 数组，最新一根在最后
        rsi = ta.rsi(b.c, 14)
        hit = rsi[-2] < 30 <= rsi[-1]  # 上一根在 30 以下，这一根收回
        return Fire(hit, values={"rsi": rsi[-1]}, note=f"RSI {rsi[-1]:.1f}")
`

// ErrNoScripts: no strategy runner to run scripts on.
var ErrNoScripts = errors.New("script signals need the strategy runner (ctg-strategy), which is not connected — use built-in condition types")

// ScriptBackend runs scripts: the strategy host relays a request to the script worker and returns its
// reply (strategyhost.Host.Conditions); Bars is the confirmed history (market.Engine.BarsTF).
type ScriptBackend interface {
	Conditions(ctx context.Context, req any) (json.RawMessage, error)
	Bars(instID, tf string, n int) ([]Candle, error)
}

// Candle is one confirmed bar of any timeframe (open time ms, quote volume in USDT).
type Candle struct {
	Ts                  int64
	O, H, L, C, Vol, VQ float64
}

// ScriptMeta is what a script declares; validation fills it in and it is stored with the spec.
type ScriptMeta struct {
	Class     string   `json:"class"`
	Desc      string   `json:"desc"`
	InstIDs   []string `json:"instIds"`
	Timeframe string   `json:"timeframe"`
	Lookback  int      `json:"lookback"`
	Hash      string   `json:"hash"`
}

// ScriptEval is one check of a script at a bar close.
type ScriptEval struct {
	At      int64              `json:"at"`   // close of the bar it checked (ms)
	Fire    *bool              `json:"fire"` // null = unknown
	Values  map[string]float64 `json:"values,omitempty"`
	Note    string             `json:"note,omitempty"`
	Ms      float64            `json:"ms"`
	Error   string             `json:"error,omitempty"`
	Missing []string           `json:"missing,omitempty"` // instruments without the bar of this close
}

func (r ScriptEval) tri() tri {
	if r.Fire == nil {
		return unknown
	}
	return triOf(*r.Fire)
}

// text is what was observed: the script's note and values, or why there is no value.
func (r ScriptEval) text() string {
	switch {
	case r.Error != "":
		return "出错：" + r.Error
	case len(r.Missing) > 0:
		return "缺少 K 线：" + strings.Join(coins(r.Missing), "、")
	}
	parts := []string{}
	if r.Note != "" {
		parts = append(parts, r.Note)
	}
	if v := valuesText(r.Values); v != "" {
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		switch r.tri() {
		case yes:
			return "成立"
		case no:
			return "未成立"
		}
		return "无结论"
	}
	return strings.Join(parts, " · ")
}

func valuesText(vs map[string]float64) string {
	keys := make([]string, 0, len(vs))
	for k := range vs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + strconv.FormatFloat(vs[k], 'g', 5, 64)
	}
	return strings.Join(out, " ")
}

func coins(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = coin(id)
	}
	return out
}

// Replay is a script run over recent history: how often it would have fired and how it behaves.
type Replay struct {
	At           int64        `json:"at"`   // when it ran (ms)
	From         int64        `json:"from"` // first and newest bar close checked
	To           int64        `json:"to"`
	Timeframe    string       `json:"timeframe"`
	Bars         int          `json:"bars"` // closes checked
	Days         float64      `json:"days"`
	States       string       `json:"states"`    // one per close: 1 holds, 0 not, - unknown
	Fires        []ReplayFire `json:"fires"`     // false→true changes, newest last (the last 50)
	FireCount    int          `json:"fireCount"` // all of them
	PerDay       float64      `json:"perDay"`
	Unknown      int          `json:"unknown"`
	Errors       int          `json:"errors"`
	FirstError   string       `json:"firstError,omitempty"`
	FirstErrorAt int64        `json:"firstErrorAt,omitempty"`
	AvgMs        float64      `json:"avgMs"`
	MaxMs        float64      `json:"maxMs"`
	NoFactors    bool         `json:"noFactors,omitempty"` // it reads ctx.factors, which replays do not have
	Now          *ScriptEval  `json:"now,omitempty"`       // the newest close: whether it holds right now
	OK           bool         `json:"ok"`
	// Problem: error | slow | often | unknown | holds | nodata (empty when OK); Reason says it for the agent.
	Problem string `json:"problem,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// ReplayFire is one change from false to true in a replay.
type ReplayFire struct {
	T      int64              `json:"t"`
	Values map[string]float64 `json:"values,omitempty"`
	Note   string             `json:"note,omitempty"`
}

// Diagnostic is a compile or declaration problem at a line of the script.
type Diagnostic struct {
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// ScriptTest is the result of validating a script (test endpoint, test_signal_script, and the first
// step of subscribing to one).
type ScriptTest struct {
	OK          bool         `json:"ok"`
	Problem     string       `json:"problem,omitempty"` // compile, or a Replay problem
	Reason      string       `json:"reason,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Meta        *ScriptMeta  `json:"meta,omitempty"`
	Replay      *Replay      `json:"replay,omitempty"`
	Limits      ScriptLimits `json:"limits"`
}

// ScriptLimits are the caps a script is held to.
type ScriptLimits struct {
	MaxBytes      int     `json:"maxBytes"`
	BudgetMs      int     `json:"budgetMs"` // CPU per check
	MaxAvgMs      float64 `json:"maxAvgMs"`
	MaxFiresDay   float64 `json:"maxFiresPerDay"`
	MaxErrors     int     `json:"maxErrors"` // consecutive live errors that end the subscription
	ReplayDays    int     `json:"replayDays"`
	MaxActive     int     `json:"maxActive"`
	ActiveScripts int     `json:"activeScripts"`
}

// ScriptState is a script subscription's record: the replay it was accepted with and how its live checks go.
type ScriptState struct {
	Replay      *Replay     `json:"replay,omitempty"`
	Last        *ScriptEval `json:"last,omitempty"` // newest live check
	Checks      int         `json:"checks"`
	AvgMs       float64     `json:"avgMs"`
	Errors      int         `json:"errors"` // consecutive
	LastError   string      `json:"lastError,omitempty"`
	LastErrorAt int64       `json:"lastErrorAt,omitempty"`
}

// ScriptInfo is a subscription's script for the list and detail views.
type ScriptInfo struct {
	ScriptMeta
	Code string `json:"code"`
	ScriptState
}

// SetScripts wires the script runner. Without it script specs are rejected.
func (s *Service) SetScripts(b ScriptBackend) { s.mu.Lock(); s.scripts = b; s.mu.Unlock() }

func (s *Service) backend() ScriptBackend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scripts
}

func (s *Service) limits() ScriptLimits {
	l := ScriptLimits{MaxBytes: MaxScriptBytes, BudgetMs: s.opts.ScriptMs, MaxAvgMs: float64(s.opts.ScriptMs) / 4,
		MaxFiresDay: s.opts.ScriptPerDay, MaxErrors: s.opts.ScriptErrors, ReplayDays: replayDays, MaxActive: MaxScripts}
	s.mu.Lock()
	for _, x := range s.live {
		if x.script != nil {
			l.ActiveScripts++
		}
	}
	s.mu.Unlock()
	return l
}

// script returns the spec's script leaf.
func (s Spec) script() (Spec, bool) {
	for _, l := range s.leaves() {
		if l.Type == "script" {
			return l, true
		}
	}
	return Spec{}, false
}

func (s Spec) scripts() int {
	n := 0
	for _, l := range s.leaves() {
		if l.Type == "script" {
			n++
		}
	}
	return n
}

// fillCode puts code into the spec's script leaves that have none (the tools take the code as its own argument).
func (s *Spec) fillCode(code string) {
	if s.Type == "script" && s.Code == "" {
		s.Code = code
	}
	for i := range s.Of {
		s.Of[i].fillCode(code)
	}
}

// setScript stores the validated meta on the script leaf.
func (s *Spec) setScript(m *ScriptMeta) {
	if s.Type == "script" {
		s.Script = m
	}
	for i := range s.Of {
		s.Of[i].setScript(m)
	}
}

// scriptDue reports whether the confirmed minute closes the script's bar.
func (s Spec) scriptDue(minute int64) bool {
	if s.Script == nil {
		return false
	}
	step, ok := barMs[s.Script.Timeframe]
	return ok && (minute+60_000)%step == 0
}

// call sends one request to the script worker and decodes the reply; reply errors marked infra (the
// worker could not run at all) come back as errors, others are the caller's to read.
func (s *Service) call(ctx context.Context, timeout time.Duration, req map[string]any, out any) error {
	b := s.backend()
	if b == nil {
		return ErrNoScripts
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := b.Conditions(ctx, req)
	if err != nil {
		return fmt.Errorf("script runner: %w", err)
	}
	var head struct {
		Error string `json:"error"`
		Infra bool   `json:"infra"`
	}
	if json.Unmarshal(raw, &head) == nil && head.Infra {
		return errors.New("script runner: " + head.Error)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("script runner: bad reply: %v", err)
	}
	return nil
}

// TestScript compiles a script and replays it over recent history. An error means the check could not
// run (no runner, no data source); a script that fails a check comes back with OK false and the reason.
func (s *Service) TestScript(ctx context.Context, code string) (ScriptTest, error) {
	out := ScriptTest{Diagnostics: []Diagnostic{}, Limits: s.limits()}
	code = strings.TrimSpace(code)
	switch {
	case code == "":
		return out, errors.New("script: code is required (a ctg Condition class)")
	case len(code) > MaxScriptBytes:
		out.Problem, out.Reason = "compile", fmt.Sprintf("the script is %d bytes; the cap is %d", len(code), MaxScriptBytes)
		return out, nil
	}
	var chk struct {
		OK          bool         `json:"ok"`
		Diagnostics []Diagnostic `json:"diagnostics"`
		Meta        *ScriptMeta  `json:"meta"`
		Error       string       `json:"error"`
	}
	if err := s.call(ctx, 40*time.Second, map[string]any{"op": "check", "code": code}, &chk); err != nil {
		return out, err
	}
	if chk.Error != "" {
		return out, errors.New("script runner: " + chk.Error)
	}
	if chk.Diagnostics != nil {
		out.Diagnostics = chk.Diagnostics
	}
	if !chk.OK || chk.Meta == nil {
		out.Problem, out.Reason = "compile", "the script does not compile"
		if len(chk.Diagnostics) > 0 {
			d := chk.Diagnostics[0]
			out.Reason = fmt.Sprintf("line %d: %s", d.Line, d.Message)
		}
		return out, nil
	}
	m := chk.Meta
	for i, id := range m.InstIDs {
		m.InstIDs[i] = NormalizeInst(id)
		if !s.src.Known(m.InstIDs[i]) {
			out.Problem, out.Reason = "compile", fmt.Sprintf("unknown instrument %s in instruments (OKX USDT perpetual ids like AAVE-USDT-SWAP)", id)
			return out, nil
		}
	}
	if _, ok := barMs[m.Timeframe]; !ok {
		out.Problem, out.Reason = "compile", "timeframe must be 1m, 5m, 15m, 1H or 4H"
		return out, nil
	}
	out.Meta = m
	r, err := s.replay(ctx, code, *m)
	if err != nil {
		return out, err
	}
	out.Replay = r
	out.OK, out.Problem, out.Reason = r.OK, r.Problem, r.Reason
	return out, nil
}

// replay runs the script over the last 7 days of its timeframe (as far as 1000 bars reach) and judges it.
func (s *Service) replay(ctx context.Context, code string, m ScriptMeta) (*Replay, error) {
	b := s.backend()
	if b == nil {
		return nil, ErrNoScripts
	}
	step := barMs[m.Timeframe]
	steps := int(min(replayDays*24*3_600_000/step, int64(replayMaxBars-m.Lookback+1)))
	now := s.now()
	r := &Replay{At: now.UnixMilli(), Timeframe: m.Timeframe}
	bars := map[string][][7]float64{}
	var to int64 = math.MaxInt64
	for _, id := range m.InstIDs {
		cs, err := b.Bars(id, m.Timeframe, steps+m.Lookback-1)
		if err != nil || len(cs) == 0 {
			r.Problem, r.Reason = "nodata", fmt.Sprintf("no %s bars for %s", m.Timeframe, id)
			return r, nil
		}
		bars[id] = rows(cs)
		to = min(to, cs[len(cs)-1].Ts+step)
	}
	if now.UnixMilli()-to > 2*step+60_000 {
		r.Problem, r.Reason = "nodata", fmt.Sprintf("the newest %s bar of every instrument closed at %s — the data is stale", m.Timeframe, fmtTime(to))
		return r, nil
	}
	r.To, r.From = to, to-int64(steps-1)*step
	var got struct {
		States     string       `json:"states"`
		Edges      []ReplayFire `json:"edges"`
		EdgeCount  int          `json:"edgeCount"`
		Errors     int          `json:"errors"`
		Timeouts   int          `json:"timeouts"`
		FirstError *struct {
			T     int64  `json:"t"`
			Error string `json:"error"`
		} `json:"firstError"`
		Unknown int     `json:"unknown"`
		AvgMs   float64 `json:"avgMs"`
		MaxMs   float64 `json:"maxMs"`
		Factors bool    `json:"factors"`
		Last    *struct {
			T int64 `json:"t"`
			ScriptEval
		} `json:"last"`
		Error string `json:"error"`
	}
	err := s.call(ctx, (replayTimeoutSeconds+20)*time.Second, map[string]any{"op": "replay", "code": code, "bars": bars,
		"start": r.From, "end": r.To, "budgetMs": s.opts.ScriptMs, "maxTotalS": replayTimeoutSeconds}, &got)
	if err != nil {
		return nil, err
	}
	r.States, r.Bars = got.States, len(got.States)
	r.Days = math.Round(float64(r.Bars)*float64(step)/86_400_000*100) / 100
	r.FireCount, r.Unknown, r.Errors = got.EdgeCount, got.Unknown, got.Errors
	r.AvgMs, r.MaxMs, r.NoFactors = got.AvgMs, got.MaxMs, got.Factors
	if n := len(got.Edges); n > maxReplayFires {
		got.Edges = got.Edges[n-maxReplayFires:]
	}
	r.Fires = got.Edges
	if r.Fires == nil {
		r.Fires = []ReplayFire{}
	}
	if r.Days > 0 {
		r.PerDay = math.Round(float64(r.FireCount)/r.Days*100) / 100
	}
	if got.FirstError != nil {
		r.FirstError, r.FirstErrorAt = got.FirstError.Error, got.FirstError.T
	}
	if got.Last != nil {
		e := got.Last.ScriptEval
		e.At = got.Last.T
		r.Now = &e
	}
	switch {
	case got.Error != "":
		r.Problem, r.Reason = "error", "the replay stopped: "+got.Error
	case r.Errors > 0 && got.Timeouts > 0:
		r.Problem, r.Reason = "slow", fmt.Sprintf("checks ran out of the %d ms CPU budget on %d of %d closes (first at %s); make it cheaper (fewer bars or instruments, vectorised numpy)",
			s.opts.ScriptMs, got.Timeouts, r.Bars, fmtTime(r.FirstErrorAt))
	case r.Errors > 0:
		r.Problem, r.Reason = "error", fmt.Sprintf("the script raised on %d of %d closes, first at %s: %s", r.Errors, r.Bars, fmtTime(r.FirstErrorAt), r.FirstError)
	case r.AvgMs > float64(s.opts.ScriptMs)/4:
		r.Problem, r.Reason = "slow", fmt.Sprintf("a check takes %.1f ms on average; the cap is %.0f ms", r.AvgMs, float64(s.opts.ScriptMs)/4)
	case r.Unknown == r.Bars:
		r.Problem, r.Reason = "unknown", "the script returned no value (None, or bars missing) on every close"
	case r.PerDay > s.opts.ScriptPerDay:
		r.Problem, r.Reason = "often", fmt.Sprintf("it would have fired %d times in %.1f days (%.1f a day; the cap is %g): make the condition stricter or the timeframe slower",
			r.FireCount, r.Days, r.PerDay, s.opts.ScriptPerDay)
	case r.Now == nil || r.Now.Fire == nil:
		r.Problem, r.Reason = "unknown", "the script has no value on the newest close ("+fmtTime(r.To)+")"
		if r.Now != nil && len(r.Now.Missing) > 0 {
			r.Reason += ": bars missing for " + strings.Join(r.Now.Missing, ", ")
		}
	case *r.Now.Fire:
		r.Problem, r.Reason = "holds", fmt.Sprintf("the condition already holds on the newest close (%s: %s); a subscription fires only when it turns true. "+
			"Act on it now, or tighten the condition", fmtTime(r.To), r.Now.text())
	default:
		r.OK = true
	}
	return r, nil
}

func rows(cs []Candle) [][7]float64 {
	out := make([][7]float64, 0, len(cs))
	for _, c := range cs {
		r := [7]float64{float64(c.Ts), c.O, c.H, c.L, c.C, c.Vol, c.VQ}
		ok := true
		for _, v := range r {
			ok = ok && !math.IsNaN(v) && !math.IsInf(v, 0)
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func fmtTime(ms int64) string { return time.UnixMilli(ms).In(tz8).Format("01-02 15:04") }

// prepareScript validates the spec's script (at most one) and fills in its meta; the replay comes back
// for the subscription record.
func (s *Service) prepareScript(ctx context.Context, spec *Spec) (*Replay, error) {
	switch spec.scripts() {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, errors.New("at most one script per subscription")
	}
	if spec.Every == "tick" {
		return nil, errors.New("every tick is not allowed with a script: a script runs when its timeframe's bar closes (every = 1m…1H sets a slower check)")
	}
	l := s.limits()
	if l.ActiveScripts >= l.MaxActive {
		return nil, fmt.Errorf("%d script signals are active; the cap is %d — cancel one first (list_signals)", l.ActiveScripts, l.MaxActive)
	}
	leaf, _ := spec.script()
	t, err := s.TestScript(ctx, leaf.Code)
	if err != nil {
		return nil, err
	}
	if !t.OK {
		return nil, fmt.Errorf("script rejected (%s): %s", t.Problem, t.Reason)
	}
	spec.setScript(t.Meta)
	return t.Replay, nil
}

// ---------- live checks ----------

type scriptJob struct {
	id    int64
	leaf  Spec
	close int64 // the bar close it checks
}

type scriptBatch struct {
	jobs    []scriptJob
	results map[int64]ScriptEval
	infra   string // the batch could not run: not the scripts' fault
}

// scriptsDue lists the script subscriptions due at the confirmed minute; all = every one (catch-up after
// a start), at its newest close.
func (s *Service) scriptsDue(minute int64, all bool) []scriptJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []scriptJob
	for id, l := range s.live {
		leaf, ok := l.sub.Spec.script()
		if !ok || leaf.Script == nil {
			continue
		}
		if !all && (!l.sub.Spec.due(minute) || !leaf.scriptDue(minute)) {
			continue
		}
		step := barMs[leaf.Script.Timeframe]
		close := (minute + 60_000) / step * step
		if all && l.script != nil && l.script.Last != nil && l.script.Last.At >= close {
			continue // checked that close (or a newer one) before the restart: an older bar must not make an edge
		}
		out = append(out, scriptJob{id: id, leaf: leaf, close: close})
	}
	slices.SortFunc(out, func(a, b scriptJob) int { return int(a.id - b.id) })
	return out
}

// startScripts runs a batch in the background; its results come back to the event loop. A batch still
// running when the next is due makes the next one skip (its scripts stay unknown until their next close).
func (s *Service) startScripts(ctx context.Context, jobs []scriptJob) {
	if len(jobs) == 0 {
		return
	}
	if !s.scriptBusy.CompareAndSwap(false, true) {
		s.logf("signals: %d script checks skipped: the previous batch is still running", len(jobs))
		return
	}
	go func() {
		defer s.scriptBusy.Store(false)
		b := s.runScripts(ctx, jobs)
		select {
		case s.scriptRes <- b:
		case <-ctx.Done():
		}
	}()
}

// runScripts sends one eval batch with each script's bars and fact-panel values.
func (s *Service) runScripts(ctx context.Context, jobs []scriptJob) scriptBatch {
	out := scriptBatch{jobs: jobs, results: map[int64]ScriptEval{}}
	b := s.backend()
	if b == nil {
		out.infra = ErrNoScripts.Error()
		return out
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		m := j.leaf.Script
		bars, facts := map[string][][7]float64{}, map[string]map[string]float64{}
		step := barMs[m.Timeframe]
		for _, id := range m.InstIDs {
			if cs, err := b.Bars(id, m.Timeframe, m.Lookback+1); err == nil {
				for len(cs) > 0 && cs[len(cs)-1].Ts+step > j.close { // a newer bar than the one this check is for
					cs = cs[:len(cs)-1]
				}
				if len(cs) > m.Lookback {
					cs = cs[len(cs)-m.Lookback:]
				}
				bars[id] = rows(cs) // missing or stale bars: the worker reports them and the check is unknown
			}
			if vs, ok := s.src.Values(id); ok {
				f := map[string]float64{}
				for k, v := range vs {
					if !math.IsNaN(v) && !math.IsInf(v, 0) {
						f[k] = v
					}
				}
				facts[id] = f
			}
		}
		items = append(items, map[string]any{"key": j.id, "code": j.leaf.Code, "now": j.close, "bars": bars, "factors": facts})
	}
	budget := time.Duration(s.opts.ScriptMs) * time.Millisecond
	var got struct {
		Items []struct {
			Key   int64 `json:"key"`
			Crash bool  `json:"crash"`
			ScriptEval
		} `json:"items"`
		Error string `json:"error"`
	}
	if err := s.call(ctx, time.Duration(len(jobs))*(5*budget+2*time.Second)+15*time.Second,
		map[string]any{"op": "eval", "budgetMs": s.opts.ScriptMs, "items": items}, &got); err != nil {
		out.infra = err.Error()
		return out
	}
	if got.Error != "" && len(got.Items) == 0 {
		out.infra = got.Error
		return out
	}
	closes := map[int64]int64{}
	for _, j := range jobs {
		closes[j.id] = j.close
	}
	for _, it := range got.Items {
		e := it.ScriptEval
		e.At = closes[it.Key]
		out.results[it.Key] = e
	}
	return out
}

// applyScripts records a batch's results and evaluates each subscription on them.
func (s *Service) applyScripts(b scriptBatch) {
	s.evalMu.Lock()
	defer s.evalMu.Unlock()
	now := s.now()
	if b.infra == "" {
		s.infraSince, s.infraNoted = time.Time{}, false
	} else {
		s.logf("signals: script batch failed: %s", b.infra)
		if s.infraSince.IsZero() {
			s.infraSince = now
		}
		if !s.infraNoted && now.Sub(s.infraSince) >= scriptInfraNotice {
			s.infraNoted = true
			s.pushText(fmt.Sprintf("信号脚本已连续 %s无法运行：%s\n%d 个脚本信号暂时无法判断，恢复后自动继续。", durText(now.Sub(s.infraSince)), clip(b.infra, 200), len(b.jobs)), "")
		}
	}
	for _, j := range b.jobs {
		s.mu.Lock()
		l := s.live[j.id]
		s.mu.Unlock()
		if l == nil || l.script == nil {
			continue // ended meanwhile
		}
		st := l.script
		r, ok := b.results[j.id]
		switch {
		case !ok:
			reason := b.infra
			if reason == "" {
				reason = "no result"
			}
			st.LastError, st.LastErrorAt = "脚本未运行："+clip(reason, 300), now.UnixMilli()
			s.saveScript(l)
			continue
		case r.Error != "":
			st.Errors++
			st.LastError, st.LastErrorAt, st.Last = r.Error, now.UnixMilli(), &r
		default:
			st.Errors = 0
			st.Last = &r
			if len(r.Missing) == 0 {
				st.AvgMs = (st.AvgMs*float64(st.Checks) + r.Ms) / float64(st.Checks+1)
				st.Checks++
			}
		}
		s.saveScript(l)
		if st.Errors >= s.opts.ScriptErrors {
			s.endScript(l, r.Error)
			continue
		}
		s.check(l, event{kind: "script", script: &r}, now)
	}
}

func (s *Service) saveScript(l *liveSub) {
	raw, _ := json.Marshal(l.script)
	if _, err := s.db.Exec(`UPDATE signal_subs SET script=? WHERE id=?`, string(raw), l.sub.ID); err != nil {
		s.logf("signals: save script state #%d: %v", l.sub.ID, err)
	}
}

// endScript ends a subscription whose script keeps failing and tells the owner (no silent failures).
func (s *Service) endScript(l *liveSub, lastErr string) {
	reason := fmt.Sprintf("脚本连续 %d 次出错，已停止：%s", l.script.Errors, clip(lastErr, 200))
	sub, err := s.Cancel(l.sub.ID, "system", reason)
	if err != nil {
		return
	}
	s.logf("signals: #%d ended: %s", sub.ID, reason)
	link := ""
	if s.opts.PublicURL != "" && sub.ChatID > 0 {
		link = s.opts.PublicURL + "/go/chat/" + strconv.FormatInt(sub.ChatID, 10)
	}
	s.pushText(fmt.Sprintf("信号 #%d 已停止：%s\n%s\n订阅者：%s", sub.ID, sub.Desc, reason, sub.Subscriber), link)
}

func (s *Service) pushText(text, link string) {
	s.mu.Lock()
	n := s.notify
	s.mu.Unlock()
	if n != nil {
		n(text, link)
	}
}

// scriptInfo is the subscription's script for the views.
func scriptInfo(spec Spec, raw string) *ScriptInfo {
	leaf, ok := spec.script()
	if !ok || leaf.Script == nil {
		return nil
	}
	x := &ScriptInfo{ScriptMeta: *leaf.Script, Code: leaf.Code}
	if raw != "" {
		json.Unmarshal([]byte(raw), &x.ScriptState)
	}
	return x
}

// scriptProgress is the active script subscription's state line (its newest check).
func scriptProgress(x *ScriptInfo) *Progress {
	if x.Last == nil {
		if x.LastError != "" {
			return &Progress{Pct: -1, Text: x.LastError}
		}
		return &Progress{Pct: -1, Text: "等待 " + x.Timeframe + " 收盘检查"}
	}
	state := map[tri]string{yes: "成立", no: "未成立", unknown: "无结论"}[x.Last.tri()]
	text := fmt.Sprintf("%s 收盘 %s：%s", x.Timeframe, fmtTime(x.Last.At), state)
	if t := x.Last.text(); t != state && t != "无结论" {
		text += " · " + t
	}
	if x.Errors > 0 {
		text += fmt.Sprintf("（连续出错 %d 次）", x.Errors)
	}
	return &Progress{Pct: -1, Text: text}
}

// ---------- the agent's tool ----------

// TestScriptTool is test_signal_script: compile + replay, nothing is stored.
func (s *Service) TestScriptTool(ctx context.Context, _ int64, a map[string]string) (any, error) {
	t, err := s.TestScript(ctx, a["code"])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"ok": t.OK, "limits": t.Limits}
	if !t.OK {
		out["problem"], out["reason"] = t.Problem, t.Reason
	}
	if len(t.Diagnostics) > 0 {
		out["diagnostics"] = t.Diagnostics
	}
	if m := t.Meta; m != nil {
		out["declared"] = map[string]any{"desc": m.Desc, "instIds": m.InstIDs, "timeframe": m.Timeframe, "lookback": m.Lookback}
	}
	if r := t.Replay; r != nil && r.Bars > 0 {
		rep := map[string]any{"closes": r.Bars, "days": r.Days, "from": fmtTime(r.From), "to": fmtTime(r.To), "fires": r.FireCount,
			"perDay": r.PerDay, "unknownCloses": r.Unknown, "errors": r.Errors, "avgMs": r.AvgMs, "maxMs": r.MaxMs,
			"holdsShare": holdsShare(r.States)}
		if r.FirstError != "" {
			rep["firstError"] = fmtTime(r.FirstErrorAt) + " " + r.FirstError
		}
		if r.NoFactors {
			rep["note"] = "the script reads ctx.factors, which replays do not have (NaN): fires there are undercounted"
		}
		sample := []map[string]any{}
		for i := max(0, len(r.Fires)-8); i < len(r.Fires); i++ {
			f := r.Fires[i]
			sample = append(sample, map[string]any{"time": fmtTime(f.T), "note": f.Note, "values": f.Values})
		}
		rep["lastFires"] = sample
		if r.Now != nil {
			rep["now"] = fmtTime(r.Now.At) + " " + r.Now.text()
		}
		out["replay"] = rep
	}
	if t.OK {
		out["next"] = "Passes. Subscribe with subscribe_signal: spec {\"type\":\"script\"} (or inside all/any) and this code as code."
	}
	return out, nil
}

func holdsShare(states string) string {
	n, k := 0, 0
	for _, c := range states {
		if c != '-' {
			n++
		}
		if c == '1' {
			k++
		}
	}
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", float64(k)/float64(n)*100)
}
