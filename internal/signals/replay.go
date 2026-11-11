package signals

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"
)

// Replays of built-in conditions over recorded 1m bars: the same evaluator as live (liveSub.step), fed
// minute by minute from a History instead of the market. They answer "how often would this have fired,
// and on what" before subscribing (test_signal, POST /api/v1/signals/test), and they are how the tests
// check the evaluator against real OKX history.
//
// Each replayed minute m (bar open) is seen at m + 60 s: a tick first (when the spec is checked on
// ticks; the last price is that bar's close — live ticks every ~2 s can fire earlier, on a wick), then
// the confirmed-minute event (when due). Fields, move risk, alerts, plans and scripts have no history
// here: specs with them are rejected (scripts have their own replay, test_signal_script).

// History is 1m bars per instrument (open time ascending) and the members a pool scan would see.
type History struct {
	Bars    map[string][]Bar
	Members []string // scope pool / watch (top = the first top); scope list uses its own instIds
}

// HistorySource is the optional part of Sources that loads history for replays (the market engine).
type HistorySource interface {
	Bars1m(instID string, from, to int64) []Bar
}

// histSource answers Sources and PoolSource from a History as of a moving clock.
type histSource struct {
	h   *History
	now int64 // ms
	idx map[string]map[int64]int
}

func newHistSource(h *History) *histSource {
	x := &histSource{h: h, idx: map[string]map[int64]int{}}
	for id, bs := range h.Bars {
		m := make(map[int64]int, len(bs))
		for i, b := range bs {
			m[b.T] = i
		}
		x.idx[id] = m
	}
	return x
}

// upto is the index of the newest bar closed by now (-1 = none).
func (x *histSource) upto(id string) int {
	bs := x.h.Bars[id]
	return sort.Search(len(bs), func(i int) bool { return bs[i].T+60_000 > x.now }) - 1
}

func (x *histSource) Known(id string) bool { _, ok := x.h.Bars[id]; return ok }

func (x *histSource) Quote(id string) (float64, int64, bool) {
	i := x.upto(id)
	if i < 0 {
		return 0, 0, false
	}
	b := x.h.Bars[id][i]
	return b.C, b.T + 60_000, true
}

func (x *histSource) Closed(id, bar string) (Bar, bool) {
	step, ok := barMs[bar]
	if !ok {
		return Bar{}, false
	}
	end := x.now / step * step // the newest bucket closed by now ends here
	start := end - step
	bs := x.h.Bars[id]
	var out Bar
	n := 0
	for i := sort.Search(len(bs), func(i int) bool { return bs[i].T >= start }); i < len(bs) && bs[i].T < end; i++ {
		b := bs[i]
		if n == 0 {
			out = Bar{T: start, H: b.H, L: b.L}
		}
		out.H, out.L, out.C = math.Max(out.H, b.H), math.Min(out.L, b.L), b.C
		n++
	}
	return out, n > 0
}

func (x *histSource) Values(string) (map[string]float64, bool) { return nil, false }
func (x *histSource) MoveRisk(string) (MoveRisk, bool)         { return MoveRisk{}, false }
func (x *histSource) Plan(int64) (Plan, bool)                  { return Plan{}, false }
func (x *histSource) AlertRule(int64) (string, bool)           { return "", false }
func (x *histSource) Keep([]string)                            {}

func (x *histSource) Members(_ string, top int) []string {
	if top > 0 && top < len(x.h.Members) {
		return x.h.Members[:top]
	}
	return x.h.Members
}

func (x *histSource) Recent1m(id string, n int) []Bar {
	i := x.upto(id)
	if i < 0 {
		return nil
	}
	return x.h.Bars[id][max(0, i-n+1) : i+1]
}

// ReplayHit is one replayed firing.
type ReplayHit struct {
	T       int64  `json:"t"` // when it fired (ms)
	Hits    []Hit  `json:"hits"`
	Matched string `json:"matched,omitempty"`
}

// SpecReplay is the outcome of a replay.
type SpecReplay struct {
	From      int64          `json:"from"`
	To        int64          `json:"to"`
	Minutes   int            `json:"minutes"`
	Members   int            `json:"members,omitempty"`
	FireCount int            `json:"fireCount"`
	PerDay    float64        `json:"perDay"`
	ByInst    map[string]int `json:"byInst,omitempty"` // pool: fires per instrument
	Unknown   int            `json:"unknownMinutes"`   // minutes the condition had no value (single-instrument types)
	Fires     []ReplayHit    `json:"fires"`            // the newest 50
	Note      string         `json:"note"`
}

// replayable rejects leaves the history cannot answer.
func replayable(s Spec) error {
	for _, l := range s.leaves() {
		switch l.Type {
		case "field", "moverisk", "alert", "plan":
			return fmt.Errorf("%s conditions have no history to replay (only price, zone, time and pool scans on move / range)", l.Type)
		case "script":
			return errors.New("replay a script with test_signal_script")
		case "pool":
			if l.Metric == "field" || slices.ContainsFunc(l.Legs, func(x Spec) bool { return x.Metric == "field" }) {
				return errors.New("pool scans on a field have no history to replay (only move and range)")
			}
		}
	}
	return nil
}

// ReplaySpec runs spec over the history's minutes [from, to) (bar open times) with a cooldown and no
// fire limit, and reports what would have fired. The spec is validated as of from.
func ReplaySpec(spec Spec, h *History, from, to int64, cooldownMin int) (*SpecReplay, error) {
	if err := replayable(spec); err != nil {
		return nil, err
	}
	src := newHistSource(h)
	src.now = from
	if err := spec.normalize(src, time.UnixMilli(from), 0); err != nil {
		return nil, err
	}
	if err := replayable(spec); err != nil {
		return nil, err
	}
	if cooldownMin <= 0 {
		cooldownMin = DefaultCooldown
	}
	l := newLive(Subscription{Spec: spec, CooldownMin: cooldownMin, InstIDs: spec.InstIDs(), state: unknown, Desc: spec.Label()}, build(spec))
	if spec.Type == "pool" {
		l.pool = newPoolState()
	}
	out := &SpecReplay{From: from, To: to, Fires: []ReplayHit{}, Note: "按 1 分钟收盘价回放：实盘每 ~2 秒检查一次，插针可能更早或多触发；监控池成员按当前名单回放（有幸存者偏差）。"}
	if spec.Type == "pool" {
		out.ByInst = map[string]int{}
		out.Members = len(spec.members(src))
	}
	first := true
	for m := from; m < to; m += 60_000 {
		now := time.UnixMilli(m + 60_000)
		src.now = now.UnixMilli()
		out.Minutes++
		evs := []event{}
		if first {
			evs = append(evs, event{kind: "init"})
			first = false
		}
		if spec.interval() == "tick" {
			evs = append(evs, event{kind: "tick"})
		}
		if spec.due(m) {
			evs = append(evs, event{kind: "minute", minute: m})
		}
		known := false
		for _, ev := range evs {
			fire, hits, _ := l.step(src, ev, now)
			if l.pool == nil && l.sub.state != unknown {
				known = true
			}
			if !fire {
				continue
			}
			l.fired(hits, now)
			out.FireCount++
			r := ReplayHit{T: now.UnixMilli(), Hits: hits}
			if l.pool == nil {
				r.Matched = joinObserved(l.root)
			} else {
				for _, x := range hits {
					out.ByInst[x.InstID]++
				}
			}
			out.Fires = append(out.Fires, r)
		}
		if l.pool == nil && !known {
			out.Unknown++
		}
	}
	if n := len(out.Fires); n > 50 {
		out.Fires = out.Fires[n-50:]
	}
	if days := float64(to-from) / 86_400_000; days > 0 {
		out.PerDay = math.Round(float64(out.FireCount)/days*100) / 100
	}
	return out, nil
}

func joinObserved(n *node) string {
	obs := n.observed()
	s := ""
	for i, o := range obs {
		if i > 0 {
			s += "；"
		}
		s += o
	}
	return s
}

// MaxReplayHours bounds a replay of live data (1m history of the pool tier).
const MaxReplayHours = 72

// TestSpec replays a built-in condition over the last hours of recorded 1m bars.
func (s *Service) TestSpec(ctx context.Context, spec Spec, hours float64, cooldownMin int) (*SpecReplay, error) {
	hs, ok := s.src.(HistorySource)
	if !ok {
		return nil, errors.New("no history to replay on this server")
	}
	if hours <= 0 {
		hours = 24
	}
	if hours > MaxReplayHours {
		return nil, fmt.Errorf("hours must be at most %d", MaxReplayHours)
	}
	if err := replayable(spec); err != nil {
		return nil, err
	}
	now := s.now()
	to := now.UnixMilli()/60_000*60_000 - 60_000 // the newest bar may still be forming
	from := to - int64(hours*60)*60_000
	h := &History{Bars: map[string][]Bar{}}
	ids := spec.InstIDs()
	if spec.Type == "pool" && spec.Scope != "list" {
		h.Members = spec.members(s.src)
		ids = append(ids, h.Members...)
	}
	// the window and the bars before it that the first checks look back on
	look := int64(0)
	for _, l := range spec.leaves() {
		look = max(look, int64(l.WindowMin+2)*60_000, barMs[l.Bar])
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, have := h.Bars[id]; !have {
			h.Bars[id] = hs.Bars1m(id, from-look, to)
		}
	}
	return ReplaySpec(spec, h, from, to, cooldownMin)
}
