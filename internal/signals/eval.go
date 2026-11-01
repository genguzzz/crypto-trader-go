package signals

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Sources is everything conditions read; the server wires the market engine, the AI plans and the
// alert rules (cmd/server). All reads are cheap lookups; none may block.
type Sources interface {
	Known(instID string) bool
	// Quote is the last trade price and its time (ms).
	Quote(instID string) (px float64, ts int64, ok bool)
	// Closed is the last confirmed bar of size bar (1m, 5m, 15m, 1H, 4H).
	Closed(instID, bar string) (Bar, bool)
	// Values are the instrument's fact-panel fields as of the last confirmed minute.
	Values(instID string) (map[string]float64, bool)
	// MoveRisk is the continuation-risk reading on the last confirmed 1H bars.
	MoveRisk(instID string) (MoveRisk, bool)
	Plan(id int64) (Plan, bool)
	AlertRule(id int64) (name string, ok bool)
	// Keep keeps these instruments streaming live (the active subscriptions' instruments).
	Keep(instIDs []string)
}

// Bar is one confirmed candle.
type Bar struct {
	T       int64
	H, L, C float64
}

// MoveRisk is one reading of both sides.
type MoveRisk struct {
	T           int64
	Top, Bottom MoveSide
}

// MoveSide is one side's state and probabilities (0–1).
type MoveSide struct {
	Extended     bool
	PTail, PBig3 float64
}

// Plan is an AI trade plan as conditions see it (internal/aigate plans).
type Plan struct {
	ID         int64
	ChatID     int64
	InstID     string
	Side       string
	EntryLo    float64
	EntryHi    float64
	Stop       float64
	TP1, TP2   float64
	Invalidate string
	Status     string
	EnteredAt  int64
}

// Open reports whether the plan is still followed.
func (p Plan) Open() bool { return p.Status == "pending" || p.Status == "active" || p.Status == "tp1" }

// AlertHit is a fired alert rule (alerts.SetOnFired).
type AlertHit struct {
	RuleID  int64
	Rule    string
	InstID  string
	Message string
}

// tri is a condition's value: no data yet is unknown, which never makes an edge.
type tri int8

const (
	unknown tri = -1
	no      tri = 0
	yes     tri = 1
)

func triOf(b bool) tri {
	if b {
		return yes
	}
	return no
}

// event is what an evaluation reacts to. Each leaf re-reads its source only for the events that can
// change it and keeps its last value otherwise.
type event struct {
	kind   string // init | tick | minute | alert | settle (right after an alert: alert leaves back to false)
	minute int64  // minute: open time of the confirmed 1m bar just closed
	alert  *AlertHit
}

const quoteMaxAge = 2 * time.Minute

// node is a spec with its evaluation state.
type node struct {
	spec Spec
	kids []*node
	val  tri
	note string // what was observed, e.g. "现价 139.7"
	barT int64  // price on a bar close / moverisk: the bar the value is from
}

func build(s Spec) *node {
	n := &node{spec: s, val: unknown}
	for _, c := range s.Of {
		n.kids = append(n.kids, build(c))
	}
	return n
}

// eval updates the tree for one event and returns the root's value.
func (n *node) eval(src Sources, ev event, now time.Time) tri {
	s := n.spec
	switch s.Type {
	case "all", "any":
		all := s.Type == "all"
		out := triOf(all)
		for _, k := range n.kids {
			v := k.eval(src, ev, now)
			switch {
			case all && v == no, !all && v == yes:
				out = v
			case v == unknown && out != triOf(!all):
				out = unknown
			}
		}
		n.val = out
		return out
	case "price":
		if s.Bar == "" {
			if ev.kind != "alert" {
				n.livePrice(src, now, func(px float64) bool { return (s.Op == "above") == (px >= s.Level) })
			}
			return n.val
		}
		step := barMs[s.Bar]
		if ev.kind == "init" || (ev.kind == "minute" && ((ev.minute+60_000)%step == 0 || ev.minute+60_000-n.barT >= 2*step)) {
			if b, ok := src.Closed(s.InstID, s.Bar); ok {
				n.barT = b.T
				n.val = triOf((s.Op == "above") == (b.C >= s.Level))
				n.note = fmt.Sprintf("%s 收盘 %s（%s）", s.Bar, fmtPx(b.C), time.UnixMilli(b.T+step).In(tz8).Format("01-02 15:04"))
			}
		}
		return n.val
	case "zone":
		if ev.kind == "alert" {
			return n.val
		}
		n.livePrice(src, now, func(px float64) bool { return px >= s.Lo && px <= s.Hi })
		if ev.kind == "minute" && n.val == no { // a wick into the zone between two price checks
			if b, ok := src.Closed(s.InstID, "1m"); ok && b.T == ev.minute && b.L <= s.Hi && b.H >= s.Lo {
				n.val = yes
				n.note = fmt.Sprintf("1m K 线 %s–%s 触及区间（现价 %s）", fmtPx(b.L), fmtPx(b.H), fmtPx(b.C))
			}
		}
		return n.val
	case "field":
		if ev.kind == "init" || ev.kind == "minute" {
			n.val = unknown
			if vs, ok := src.Values(s.InstID); ok {
				if v, ok := vs[s.Field]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
					n.val = triOf(compare(v, s.Op, *s.Value))
					n.note = fmt.Sprintf("%s 现值 %s", coin(s.InstID), fieldText(s.Field, v))
				}
			}
		}
		return n.val
	case "moverisk":
		if ev.kind == "init" || (ev.kind == "minute" && ((ev.minute+60_000)%3_600_000 == 0 || ev.minute+60_000-n.barT >= 2*3_600_000)) {
			if r, ok := src.MoveRisk(s.InstID); ok {
				n.barT = r.T
				side := r.Top
				if s.Side == "bottom" {
					side = r.Bottom
				}
				switch s.Metric {
				case "extended":
					n.val = triOf(side.Extended == (*s.Value == 1))
					n.note = map[bool]string{true: "超伸中", false: "未超伸"}[side.Extended]
				case "pTail":
					n.val = triOf(compare(side.PTail, s.Op, *s.Value))
					n.note = fmt.Sprintf("P_tail %.0f%%", side.PTail*100)
				case "pBig3":
					n.val = triOf(compare(side.PBig3, s.Op, *s.Value))
					n.note = fmt.Sprintf("P_big3 %.0f%%", side.PBig3*100)
				}
			}
		}
		return n.val
	case "alert":
		hit := ev.kind == "alert" && ev.alert != nil && ev.alert.RuleID == s.RuleID && (s.InstID == "" || s.InstID == ev.alert.InstID)
		n.val = triOf(hit)
		if hit {
			n.note = strings.TrimSpace(ev.alert.Rule + "：" + ev.alert.Message)
		}
		return n.val
	case "time":
		n.val = triOf(now.UnixMilli() >= s.AtMs)
		if n.val == yes {
			n.note = "已到 " + time.UnixMilli(s.AtMs).In(tz8).Format("01-02 15:04")
		}
		return n.val
	case "plan":
		if ev.kind == "init" || ev.kind == "minute" {
			if p, ok := src.Plan(s.PlanID); ok {
				n.val = triOf(planReached(p, s.State))
				n.note = fmt.Sprintf("计划 #%d 状态 %s", p.ID, p.Status)
			}
		}
		return n.val
	}
	return unknown
}

func (n *node) livePrice(src Sources, now time.Time, test func(px float64) bool) {
	px, ts, ok := src.Quote(n.spec.InstID)
	if !ok || !(px > 0) || now.Sub(time.UnixMilli(ts)) > quoteMaxAge {
		n.val = unknown
		return
	}
	n.val = triOf(test(px))
	n.note = "现价 " + fmtPx(px)
}

func compare(v float64, op string, x float64) bool {
	switch op {
	case ">":
		return v > x
	case ">=":
		return v >= x
	case "<":
		return v < x
	case "<=":
		return v <= x
	}
	return false
}

// planReached: active = the entry zone was touched (also when it moved on since).
func planReached(p Plan, state string) bool {
	switch state {
	case "active":
		return p.EnteredAt > 0
	case "tp1":
		return p.Status == "tp1" || p.Status == "done"
	case "settled":
		return !p.Open()
	}
	return p.Status == state
}

// observed lists what the true leaves saw ("AAVE 进入 139.5–140.2：现价 139.7").
func (n *node) observed() []string {
	if len(n.kids) > 0 {
		var out []string
		for _, k := range n.kids {
			out = append(out, k.observed()...)
		}
		return out
	}
	if n.val != yes {
		return nil
	}
	return []string{n.spec.Describe() + "：" + n.note}
}

// unknowns lists the leaves without data (why a subscription cannot start).
func (n *node) unknowns() []string {
	if len(n.kids) > 0 {
		var out []string
		for _, k := range n.kids {
			out = append(out, k.unknowns()...)
		}
		return out
	}
	if n.val == unknown && n.spec.Type != "alert" {
		return []string{n.spec.Describe()}
	}
	return nil
}

// Progress is how far an active subscription is from firing, for the owner's list.
type Progress struct {
	InstID string  `json:"instId,omitempty"`
	Px     float64 `json:"px,omitempty"`
	Target float64 `json:"target,omitempty"`
	Pct    float64 `json:"pct"`  // |distance| to the target in % of the price (sorts by closeness); -1 = not a price
	Text   string  `json:"text"` // 现价 139.2 · 距 140.2 还差 0.7%
}

// progress describes the first leaf that has a distance (price, zone), else the first leaf's state.
func progress(s Spec, src Sources, now time.Time) *Progress {
	ls := s.leaves()
	for _, l := range ls {
		if l.Type != "price" && l.Type != "zone" {
			continue
		}
		px, ts, ok := src.Quote(l.InstID)
		if !ok || !(px > 0) || now.Sub(time.UnixMilli(ts)) > quoteMaxAge {
			return &Progress{InstID: l.InstID, Pct: -1, Text: coin(l.InstID) + " 暂无实时价格"}
		}
		p := &Progress{InstID: l.InstID, Px: px}
		head := "现价 " + fmtPx(px)
		switch {
		case l.Type == "zone" && px >= l.Lo && px <= l.Hi:
			p.Text = head + " · 已在区间内"
			return p
		case l.Type == "zone" && px > l.Hi:
			p.Target = l.Hi
		case l.Type == "zone":
			p.Target = l.Lo
		default:
			p.Target = l.Level
		}
		p.Pct = math.Abs(p.Target-px) / px * 100
		p.Text = fmt.Sprintf("%s · 距 %s 还差 %.2f%%", head, fmtPx(p.Target), p.Pct)
		if l.Type == "price" && l.Bar != "" {
			p.Text += "（看 " + l.Bar + " 收盘）"
		}
		return p
	}
	for _, l := range ls {
		switch l.Type {
		case "time":
			left := time.UnixMilli(l.AtMs).Sub(now)
			return &Progress{Pct: -1, Text: "还有 " + durText(left)}
		case "field":
			if vs, ok := src.Values(l.InstID); ok {
				if v, ok := vs[l.Field]; ok {
					return &Progress{InstID: l.InstID, Pct: -1, Text: fmt.Sprintf("现值 %s（需 %s %s）", fieldText(l.Field, v), opText[l.Op], fieldText(l.Field, *l.Value))}
				}
			}
		case "plan":
			if p, ok := src.Plan(l.PlanID); ok {
				return &Progress{InstID: p.InstID, Pct: -1, Text: "计划当前状态 " + p.Status}
			}
		}
	}
	return nil
}

func durText(d time.Duration) string {
	if d <= 0 {
		return "0 分钟"
	}
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m >= 24*60:
		return fmt.Sprintf("%d 天 %d 小时", m/1440, m%1440/60)
	case m >= 60:
		return fmt.Sprintf("%d 小时 %d 分", m/60, m%60)
	}
	return fmt.Sprintf("%d 分钟", m)
}
