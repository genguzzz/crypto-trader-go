package signals

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"crypto-trader-go/internal/features"
)

// Pool signals: one subscription scans every instrument of a scope, each with its own edge.
//
//	{"type":"pool", "scope":"pool"|"watch"|"list", "top":50, "instIds":[…], "exclude":[…],
//	 "metric":"move",  "windowMin":15, "dir":"up"|"down"|"both", "pct":3}   change over the window ≥ pct %
//	 "metric":"range", "windowMin":5, "pct":2}                              high–low swing in the window ≥ pct %
//	 "metric":"field", "field":"funding_hourly", "op":">", "value":0.0005}  a fact-panel field crosses a value
//
// move: last price (or the newest confirmed close) against the close windowMin minutes earlier, so
// windowMin 1 is the minute-to-minute move. range: (highest high / lowest low − 1) over the window's 1m
// bars. Members are re-read on every check (the active pool changes); a member checked for the first
// time — at subscribe, after joining the scope, or after a restart — only records its state, so coins
// already beyond the threshold never fire until they first fall back. A member fires on its own
// false→true edge, at most once per cooldownMin; members that fire on the same check are delivered
// together (one fire, a hit per coin, largest first). Checked every confirmed minute (every "1m",
// default) or on each price tick (every "tick"); a pool scan is always the whole subscription.

// PoolSource is the part of Sources a pool scan needs (the market engine provides it).
type PoolSource interface {
	// Members are the instruments of a scope now: "pool" = the active pool (top > 0: ranks ≤ top), "watch".
	Members(scope string, top int) []string
	// Recent1m is up to the last n confirmed 1m bars, oldest first (gaps absent).
	Recent1m(instID string, n int) []Bar
}

const (
	poolMaxWindow   = 1440
	poolMaxList     = 50
	PoolMaxFires    = 50 // a scan keeps going: more fires than a single-instrument watch
	poolDefaultFire = 10
	poolMaxHits     = 10 // hits written into one delivery (the rest are counted)
	poolStaleMs     = 3 * 60_000
)

var poolMetrics = map[string]bool{"move": true, "range": true, "field": true}

func (s *Spec) normalizePool(src Sources) error {
	if _, ok := src.(PoolSource); !ok {
		return errors.New("pool signals need the market engine's pool data")
	}
	if s.InstID != "" || s.Level != 0 || s.Bar != "" || s.Lo != 0 || s.Hi != 0 || s.Side != "" || s.Code != "" {
		return errors.New("pool takes scope, top, instIds, exclude, metric and the metric's settings (windowMin, dir, pct | field, op, value)")
	}
	switch s.Scope {
	case "", "pool":
		s.Scope = "pool"
		if s.Top < 0 || s.Top > 500 {
			return errors.New("pool: top must be between 1 and 500 (0 = the whole active pool)")
		}
		if len(s.List) > 0 {
			return errors.New("pool: instIds only with scope list")
		}
	case "watch":
		if s.Top != 0 || len(s.List) > 0 {
			return errors.New("pool: scope watch takes neither top nor instIds")
		}
	case "list":
		if s.Top != 0 {
			return errors.New("pool: top only with scope pool")
		}
		if len(s.List) < 1 || len(s.List) > poolMaxList {
			return fmt.Errorf("pool: scope list needs 1–%d instIds", poolMaxList)
		}
		var ids []string
		for _, id := range s.List {
			id = NormalizeInst(id)
			if !src.Known(id) {
				return fmt.Errorf("unknown instrument %s (OKX USDT perpetual ids like AAVE-USDT-SWAP)", id)
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		s.List = ids
	default:
		return errors.New("pool: scope must be pool, watch or list")
	}
	for i, id := range s.Exclude {
		s.Exclude[i] = NormalizeInst(id)
	}
	switch s.Every {
	case "", "1m", "tick":
	default:
		return errors.New("pool: every must be 1m (default) or tick — scans of altcoins are checked at least every minute")
	}
	if !poolMetrics[s.Metric] {
		return errors.New("pool: metric must be move (change over a window), range (high–low swing in a window) or field (a fact-panel field)")
	}
	window := func() error {
		if s.WindowMin < 1 || s.WindowMin > poolMaxWindow {
			return fmt.Errorf("pool: windowMin must be between 1 and %d minutes", poolMaxWindow)
		}
		if !(s.Pct > 0) || s.Pct > 100 {
			return errors.New("pool: pct is the threshold in percent, in (0, 100], e.g. 3 for 3%")
		}
		if s.Field != "" || s.Op != "" || s.Value != nil {
			return fmt.Errorf("pool: field / op / value belong to metric field, not %s", s.Metric)
		}
		return nil
	}
	switch s.Metric {
	case "move":
		if err := window(); err != nil {
			return err
		}
		switch s.Dir {
		case "":
			s.Dir = "both"
		case "up", "down", "both":
		default:
			return errors.New("pool: dir must be up, down or both")
		}
	case "range":
		if err := window(); err != nil {
			return err
		}
		if s.Dir != "" {
			return errors.New("pool: range has no dir (a swing either way); use metric move for a direction")
		}
	case "field":
		if s.WindowMin != 0 || s.Pct != 0 || s.Dir != "" {
			return errors.New("pool: metric field takes field, op and value (no windowMin / pct / dir)")
		}
		f, ok := features.Lookup(s.Field)
		if !ok || f.Key == "turnover12h" {
			return fmt.Errorf("pool: unknown field %q (see list_fields; turnover12h is not a signal field)", s.Field)
		}
		if err := cmpOp(s); err != nil {
			return err
		}
		if s.Value == nil || math.IsNaN(*s.Value) || math.IsInf(*s.Value, 0) {
			return errors.New("pool: value is required (in the field's own unit)")
		}
	}
	return nil
}

// members are the instruments the scan checks now.
func (s Spec) members(src Sources) []string {
	var ids []string
	if s.Scope == "list" {
		ids = s.List
	} else if ps, ok := src.(PoolSource); ok {
		ids = ps.Members(s.Scope, s.Top)
	}
	if len(s.Exclude) == 0 {
		return ids
	}
	out := ids[:0:0]
	for _, id := range ids {
		if !slices.Contains(s.Exclude, id) {
			out = append(out, id)
		}
	}
	return out
}

func windowText(m int) string {
	switch {
	case m%1440 == 0:
		return fmt.Sprintf("%d 天", m/1440)
	case m%60 == 0:
		return fmt.Sprintf("%d 小时", m/60)
	}
	return fmt.Sprintf("%d 分钟", m)
}

func (s Spec) scopeText() string {
	var t string
	switch s.Scope {
	case "watch":
		t = "自选"
	case "list":
		names := coins(s.List)
		if len(names) > 3 {
			t = strings.Join(names[:3], "、") + fmt.Sprintf(" 等 %d 个币中", len(names))
		} else {
			t = strings.Join(names, "、") + " 中"
		}
	default:
		t = "监控池"
		if s.Top > 0 {
			t = fmt.Sprintf("监控池前 %d", s.Top)
		}
	}
	if len(s.Exclude) > 0 {
		t += "（除 " + strings.Join(coins(s.Exclude), "、") + "）"
	}
	return t
}

var dirText = map[string]string{"up": "涨", "down": "跌", "both": "涨或跌"}

func (s Spec) describePool() string {
	switch s.Metric {
	case "move":
		return fmt.Sprintf("%s任一币 %s内%s ≥ %g%%", s.scopeText(), windowText(s.WindowMin), dirText[s.Dir], s.Pct)
	case "range":
		return fmt.Sprintf("%s任一币 %s内振幅 ≥ %g%%", s.scopeText(), windowText(s.WindowMin), s.Pct)
	case "field":
		v := 0.0
		if s.Value != nil {
			v = *s.Value
		}
		f, _ := features.Lookup(s.Field)
		return fmt.Sprintf("%s任一币 %s %s %s", s.scopeText(), f.Label, opText[s.Op], fieldText(s.Field, v))
	}
	return "全池扫描"
}

// reading is one member's measurement at one check.
type reading struct {
	val   float64 // move / range: percent; field: the field's value
	px    float64
	known bool
	hit   bool
	text  string
}

// read measures one member. On a minute event the reading is as of that confirmed minute; on a tick
// as of the last price; on init as of the member's newest bar (priming only).
func (s Spec) read(src Sources, inst string, ev event, now time.Time) reading {
	var r reading
	if s.Metric == "field" {
		vs, ok := src.Values(inst)
		if !ok {
			return r
		}
		v, ok := vs[s.Field]
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return r
		}
		r.val, r.known = v, true
		r.hit = compare(v, s.Op, *s.Value)
		r.text = fmt.Sprintf("%s %s", coin(inst), fieldText(s.Field, v))
		if px, ts, ok := src.Quote(inst); ok && now.Sub(time.UnixMilli(ts)) <= quoteMaxAge {
			r.px = px
		}
		return r
	}
	ps, ok := src.(PoolSource)
	if !ok {
		return r
	}
	w := int64(s.WindowMin)
	bars := ps.Recent1m(inst, s.WindowMin+3)
	if len(bars) == 0 {
		return r
	}
	last := bars[len(bars)-1]
	var px float64
	var refT int64 // the bar whose close starts the window
	switch ev.kind {
	case "minute":
		if last.T != ev.minute {
			return r // this member has no bar for the minute just confirmed
		}
		px, refT = last.C, ev.minute-w*60_000
	case "tick":
		q, ts, ok := src.Quote(inst)
		if !ok || !(q > 0) || now.Sub(time.UnixMilli(ts)) > quoteMaxAge || now.UnixMilli()-(last.T+60_000) > poolStaleMs {
			return r
		}
		px, refT = q, (now.UnixMilli()-w*60_000)/60_000*60_000-60_000
	case "init":
		if now.UnixMilli()-(last.T+60_000) > poolStaleMs {
			return r
		}
		px, refT = last.C, last.T-w*60_000
	default:
		return r
	}
	r.px = px
	switch s.Metric {
	case "move":
		i := slices.IndexFunc(bars, func(b Bar) bool { return b.T == refT })
		if i < 0 || !(bars[i].C > 0) {
			return r
		}
		r.val = (px/bars[i].C - 1) * 100
		r.known = true
		switch s.Dir {
		case "up":
			r.hit = r.val >= s.Pct
		case "down":
			r.hit = r.val <= -s.Pct
		default:
			r.hit = math.Abs(r.val) >= s.Pct
		}
		r.text = fmt.Sprintf("%s %s内 %+.2f%%（%s → %s）", coin(inst), windowText(s.WindowMin), r.val, fmtPx(bars[i].C), fmtPx(px))
	case "range":
		hi, lo := 0.0, math.Inf(1)
		var hiT, loT int64
		n := 0
		for _, b := range bars {
			if b.T <= refT {
				continue
			}
			n++
			if b.H > hi {
				hi, hiT = b.H, b.T
			}
			if b.L < lo {
				lo, loT = b.L, b.T
			}
		}
		if ev.kind == "tick" {
			if px > hi {
				hi, hiT = px, now.UnixMilli()
			}
			if px < lo {
				lo, loT = px, now.UnixMilli()
			}
		}
		if n < max(1, s.WindowMin*4/5) || !(lo > 0) {
			return r
		}
		r.val = (hi/lo - 1) * 100
		r.known, r.hit = true, r.val >= s.Pct
		way := "先跌后涨"
		if hiT < loT {
			way = "先涨后跌"
		}
		r.text = fmt.Sprintf("%s %s振幅 %.2f%%（%s–%s，%s，现价 %s）", coin(inst), windowText(s.WindowMin), r.val, fmtPx(lo), fmtPx(hi), way, fmtPx(px))
	}
	return r
}

// poolState is a pool subscription's per-member memory (persisted as signal_subs.pool_state).
type poolState struct {
	State map[string]int8  `json:"s"` // last known value per member: 0 / 1
	Fired map[string]int64 `json:"f"` // last fire per member (ms)
}

func newPoolState() *poolState {
	return &poolState{State: map[string]int8{}, Fired: map[string]int64{}}
}

func loadPoolState(raw string) *poolState {
	p := newPoolState()
	if raw != "" {
		json.Unmarshal([]byte(raw), p)
		if p.State == nil {
			p.State = map[string]int8{}
		}
		if p.Fired == nil {
			p.Fired = map[string]int64{}
		}
	}
	return p
}

// scan checks every member and returns the ones that turned true and are off cooldown (largest
// move first), whether the remembered state changed, and how many members had a value.
func (p *poolState) scan(s Spec, src Sources, ev event, now time.Time, cooldown time.Duration) (hits []Hit, changed bool, known int) {
	if ev.kind != "init" && ev.kind != "minute" && ev.kind != "tick" {
		return nil, false, 0
	}
	members := s.members(src)
	in := make(map[string]bool, len(members))
	for _, id := range members {
		in[id] = true
	}
	for id := range p.State { // left the scope: forget it (it primes again if it comes back)
		if !in[id] {
			delete(p.State, id)
			changed = true
		}
	}
	for id, t := range p.Fired {
		if !in[id] && now.Sub(time.UnixMilli(t)) > cooldown {
			delete(p.Fired, id)
			changed = true
		}
	}
	for _, id := range members {
		r := s.read(src, id, ev, now)
		if !r.known {
			continue
		}
		known++
		v := int8(0)
		if r.hit {
			v = 1
		}
		prev, had := p.State[id]
		if !had || prev != v {
			p.State[id] = v
			changed = true
		}
		if ev.kind == "init" || !had || prev == 1 || v == 0 {
			continue
		}
		if t, ok := p.Fired[id]; ok && now.Sub(time.UnixMilli(t)) < cooldown {
			continue
		}
		hits = append(hits, Hit{InstID: id, Px: r.px, Value: round4(r.val), Text: r.text})
	}
	sort.SliceStable(hits, func(i, j int) bool { return math.Abs(hits[i].Value) > math.Abs(hits[j].Value) })
	return hits, changed, known
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

// poolProgress shows the member closest to its threshold now (as of its newest bar).
func poolProgress(s Spec, src Sources, now time.Time) *Progress {
	members := s.members(src)
	if len(members) == 0 {
		return &Progress{Pct: -1, Text: "范围内暂无币"}
	}
	var best reading
	bestInst, bestScore, known := "", math.Inf(-1), 0
	for _, id := range members {
		r := s.read(src, id, event{kind: "init"}, now)
		if !r.known {
			continue
		}
		known++
		score := math.Abs(r.val)
		switch {
		case s.Metric == "move" && s.Dir == "up":
			score = r.val
		case s.Metric == "move" && s.Dir == "down":
			score = -r.val
		case s.Metric == "field":
			score = r.val
			if s.Op == "<" || s.Op == "<=" {
				score = -r.val
			}
		}
		if score > bestScore {
			best, bestInst, bestScore = r, id, score
		}
	}
	if bestInst == "" {
		return &Progress{Pct: -1, Text: fmt.Sprintf("监控 %d 个币 · 暂无数据", len(members))}
	}
	return &Progress{InstID: bestInst, Px: best.px, Pct: -1, Text: fmt.Sprintf("监控 %d 个币（%d 个有数据）· 最接近：%s", len(members), known, best.text)}
}
