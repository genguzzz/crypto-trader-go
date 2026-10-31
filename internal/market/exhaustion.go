package market

// Exhaustion: a checklist of classic blow-off / capitulation signs as 0–100 scores, plus the validated
// move-risk probabilities (moverisk.go). The checklist DESCRIBES a move; in a 2-year OKX OOS study
// (claude-workspace/rev-study) neither it nor any other exhaustion feature predicted direction (AUC
// 0.47–0.53) and fading extended moves lost ~0.2%/trade with a 59% stop rate, so it must not be read
// as a reversal probability.
//
// Each item maps a reading onto 0…1 with a ramp and carries a weight; the score is the weighted share,
// times a range gate: a top only counts near the top of the last 30 bars (and a bottom near the floor),
// so a coin that already dumped from its high is past its top.

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// ExhaustionItem is one line of the checklist.
type ExhaustionItem struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`  // what was measured, with the reading (Chinese, for display)
	Weight float64 `json:"weight"` // points available
	Points float64 `json:"points"` // points earned (0 … weight)
}

// Exhaustion is the top and bottom score of one instrument on one bar size, with the checklists.
type Exhaustion struct {
	InstID   string           `json:"instId"`
	Bar      string           `json:"bar"`
	T        int64            `json:"t"` // open time of the latest bar
	Last     float64          `json:"last"`
	Top      int              `json:"top"`    // 0–100
	Bottom   int              `json:"bottom"` // 0–100
	AtTop    float64          `json:"atTop"`  // range gate for the top score, 0–1
	AtBottom float64          `json:"atBottom"`
	TopItems []ExhaustionItem `json:"topItems"`
	BotItems []ExhaustionItem `json:"bottomItems"`
	ATR      float64          `json:"atr"`
	EMA20    float64          `json:"ema20"`
	High     float64          `json:"high"` // extreme of the last 3 bars
	Low      float64          `json:"low"`
	// a fade plan for the side that scores higher: entry zone near the extreme, stop beyond it,
	// targets at EMA20 and the 38.2% retrace of the move
	Plan *FadePlan `json:"plan,omitempty"`
	// Squeeze: shorting is vetoed — funding ≤ −0.05% per settlement, or negative funding while price is
	// over 15% above its 1H EMA24. In 6 months of OKX alt pumps (claude pump-study) that state ran another
	// +20% within 24h 43% of the time vs 29% for all pumps; no fade-short plan is given then.
	Squeeze    bool   `json:"squeeze,omitempty"`
	SqueezeWhy string `json:"squeezeWhy,omitempty"`
	// Risk: the validated part (moverisk.go) — continuation-tail and move-size probabilities on 1H.
	// The checklist scores above describe the move; in 2 years of OOS tests they had no direction power.
	Risk *MoveRisk `json:"risk,omitempty"`
}

// FadePlan is the reversal trade the scores point to (reference only).
type FadePlan struct {
	Side    string  `json:"side"` // short (fade a top) | long (fade a bottom)
	EntryLo float64 `json:"entryLo"`
	EntryHi float64 `json:"entryHi"`
	Stop    float64 `json:"stop"`
	TP1     float64 `json:"tp1"`
	TP2     float64 `json:"tp2"`
	RR      float64 `json:"rr"` // reward/risk to TP1 from the middle of the zone
}

func ramp(x, lo, hi float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(0, math.Min(1, (x-lo)/(hi-lo)))
}

func emaSeries(xs []float64, n int) []float64 {
	k := 2 / float64(n+1)
	out := make([]float64, len(xs))
	for i, x := range xs {
		if i == 0 {
			out[i] = x
		} else {
			out[i] = out[i-1] + k*(x-out[i-1])
		}
	}
	return out
}

func rsiSeries(cl []float64, n int) []float64 {
	out := make([]float64, len(cl))
	for i := range out {
		out[i] = math.NaN()
	}
	var up, dn float64
	for i := 1; i < len(cl); i++ {
		d := cl[i] - cl[i-1]
		g, l := math.Max(d, 0), math.Max(-d, 0)
		if i <= n {
			up, dn = up+g/float64(n), dn+l/float64(n)
			if i < n {
				continue
			}
		} else {
			up, dn = (up*float64(n-1)+g)/float64(n), (dn*float64(n-1)+l)/float64(n)
		}
		if dn == 0 {
			out[i] = 100
		} else {
			out[i] = 100 - 100/(1+up/dn)
		}
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

func usdShort(v float64) string {
	if math.Abs(v) >= 1e6 {
		return fmt.Sprintf("$%.1fM", v/1e6)
	}
	return fmt.Sprintf("$%.0fK", v/1e3)
}

// Exhaustion scores the latest bars of an instrument (bar 15m, 1H or 4H).
func (e *Engine) Exhaustion(ctx context.Context, id, bar string) (Exhaustion, error) {
	if bar != "15m" && bar != "1H" && bar != "4H" {
		return Exhaustion{}, fmt.Errorf("bar must be one of 15m,1H,4H")
	}
	cs, err := e.Candles(id, bar, 150)
	if err != nil {
		return Exhaustion{}, err
	}
	if len(cs) < 40 {
		return Exhaustion{}, fmt.Errorf("not enough candles")
	}
	liq := map[int64]LiqBar{}
	if lr, err := e.Liquidations(ctx, id, bar, 60, 0); err == nil {
		for _, b := range lr.Bars {
			liq[b.T] = b
		}
	}
	flow := map[int64]FlowBar{}
	if fr, err := e.OrderFlow(ctx, id, bar, 24, false); err == nil {
		for _, b := range fr.Bars {
			flow[b.T] = b
		}
	}
	var vals map[string]float64
	var pct map[string]float64
	fundRate, hasFund := 0.0, false
	if info, ok := e.Instrument(id); ok {
		if info.Row != nil {
			vals, pct = info.Row.Values, info.Row.Pct
		}
		fundRate, hasFund = info.FundingRate, info.FundingTime > 0
	}

	n := len(cs)
	closes := make([]float64, n)
	for i, c := range cs {
		closes[i] = c.C
	}
	e20 := emaSeries(closes, 20)[n-1]
	trs := make([]float64, 0, n-1)
	for i := 1; i < n; i++ {
		p, c := cs[i-1], cs[i]
		trs = append(trs, math.Max(c.H-c.L, math.Max(math.Abs(c.H-p.C), math.Abs(c.L-p.C))))
	}
	a := emaSeries(trs, 14)[len(trs)-1]
	if a <= 0 {
		a = 1e-12
	}
	r := rsiSeries(closes, 14)
	last := cs[n-1]
	recent := cs[n-3:]
	hiI, loI := n-3, n-3
	for i := n - 3; i < n; i++ {
		if cs[i].H > cs[hiI].H {
			hiI = i
		}
		if cs[i].L < cs[loI].L {
			loI = i
		}
	}
	hiBar, loBar := cs[hiI], cs[loI]
	baseHi, baseLo := math.Inf(-1), math.Inf(1)
	for _, c := range cs[n-30 : n-6] {
		baseHi, baseLo = math.Max(baseHi, c.H), math.Min(baseLo, c.L)
	}
	vols := []float64{}
	for _, c := range cs[max(0, n-51) : n-3] {
		vols = append(vols, c.Quote)
	}
	vmed := median(vols)
	vclimax := math.NaN()
	if vmed > 0 {
		m := 0.0
		for _, c := range recent {
			m = math.Max(m, c.Quote)
		}
		vclimax = m / vmed
	}
	rng := func(c Candle) float64 { return math.Max(c.H-c.L, 1e-12) }
	upWick := (hiBar.H - math.Max(hiBar.O, hiBar.C)) / rng(hiBar)
	loWick := (math.Min(loBar.O, loBar.C) - loBar.L) / rng(loBar)
	rsiNow, rsiMin := math.Inf(-1), math.Inf(1)
	for _, x := range r[n-3:] {
		if !math.IsNaN(x) {
			rsiNow, rsiMin = math.Max(rsiNow, x), math.Min(rsiMin, x)
		}
	}
	// divergence: the recent extreme beyond the prior swing while RSI does not confirm it
	pHi, pLo := n-20, n-20
	for i := n - 20; i < n-4; i++ {
		if cs[i].H > cs[pHi].H {
			pHi = i
		}
		if cs[i].L < cs[pLo].L {
			pLo = i
		}
	}
	bearDiv := hiBar.H > cs[pHi].H && !math.IsNaN(r[hiI]) && !math.IsNaN(r[pHi]) && r[hiI] < r[pHi]
	bullDiv := loBar.L < cs[pLo].L && !math.IsNaN(r[loI]) && !math.IsNaN(r[pLo]) && r[loI] > r[pLo]

	var shorts, longs []float64
	for _, c := range cs[max(0, n-51):] {
		b := liq[c.T]
		shorts, longs = append(shorts, b.Short), append(longs, b.Long)
	}
	k := len(shorts)
	sRecent := shorts[k-3] + shorts[k-2] + shorts[k-1]
	lRecent := longs[k-3] + longs[k-2] + longs[k-1]
	sMed, lMed := median(shorts[:k-3]), median(longs[:k-3])
	delta3 := 0.0
	for _, c := range recent {
		f := flow[c.T]
		delta3 += f.Buy - f.Sell
	}
	fund8 := vals["funding_hourly"] * 8
	fpct, hasFpct := pct["funding_hourly"]

	extHi, extLo := (hiBar.H-e20)/a, (e20-loBar.L)/a
	runUp, runDn := (hiBar.H-baseLo)/a, (baseHi-loBar.L)/a
	offHi, offLo := (hiBar.H-last.C)/a, (last.C-loBar.L)/a

	liqTerm := func(recent, med float64) float64 {
		ratio := 0.0
		if med > 0 {
			ratio = recent / med
		} else if recent > 5e5 {
			ratio = 3
		}
		return ramp(ratio, 2, 8) * ramp(recent, 1e5, 1e6)
	}
	liqLabel := func(side string, recent, med float64) string {
		s := side + "爆仓 " + usdShort(recent)
		if med > 0 {
			s += fmt.Sprintf("（%.0fx 常态）", recent/med)
		}
		return s
	}
	volLabel := "量能 —"
	if !math.IsNaN(vclimax) {
		volLabel = fmt.Sprintf("量能 %.1fx 中位", vclimax)
	}
	fundLabel := fmt.Sprintf("资金费 %+.4f%%/8h", fund8*100)
	if hasFpct {
		fundLabel += fmt.Sprintf(" · %.0f 分位", fpct)
	}
	fundTop, fundBot := ramp(fund8, 0.0002, 0.0008), ramp(fund8, -0.0002, -0.0008)
	if hasFpct {
		fundTop, fundBot = ramp(fpct, 70, 95), ramp(fpct, 30, 5)
	}
	flowLabel := func(sell bool) string {
		if sell {
			return "近 3 根主动净卖 " + usdShort(math.Abs(delta3))
		}
		return "近 3 根主动净买 " + usdShort(math.Abs(delta3))
	}
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	it := func(key, label string, w, x float64) ExhaustionItem {
		return ExhaustionItem{Key: key, Label: label, Weight: w, Points: math.Round(x*w*10) / 10}
	}
	divTxt := func(b bool, s string) string {
		if b {
			return s
		}
		return ""
	}
	top := []ExhaustionItem{
		it("ext", fmt.Sprintf("高点偏离 EMA20 %+.1f ATR", extHi), 15, ramp(extHi, 1.5, 3.5)),
		it("run", fmt.Sprintf("本轮涨幅 %.1f ATR", runUp), 10, ramp(runUp, 3, 8)),
		it("vol", volLabel, 15, ramp(vclimax, 1.5, 4)),
		it("wick", fmt.Sprintf("上影 %.0f%% · 已从高点回落 %.1f ATR", upWick*100, offHi), 15, math.Max(ramp(upWick, 0.25, 0.6), ramp(offHi, 0.3, 1.2))),
		it("rsi", fmt.Sprintf("RSI %.0f", rsiNow)+divTxt(bearDiv, " · 顶背离"), 10, math.Max(ramp(rsiNow, 65, 80), b2f(bearDiv))),
		it("liq", liqLabel("空头", sRecent, sMed), 15, liqTerm(sRecent, sMed)),
		it("funding", fundLabel, 10, fundTop),
		it("flow", flowLabel(delta3 < 0), 10, b2f(delta3 < 0)),
	}
	bot := []ExhaustionItem{
		it("ext", fmt.Sprintf("低点偏离 EMA20 %+.1f ATR", -extLo), 15, ramp(extLo, 1.5, 3.5)),
		it("run", fmt.Sprintf("本轮跌幅 %.1f ATR", runDn), 10, ramp(runDn, 3, 8)),
		it("vol", volLabel, 15, ramp(vclimax, 1.5, 4)),
		it("wick", fmt.Sprintf("下影 %.0f%% · 已从低点反弹 %.1f ATR", loWick*100, offLo), 15, math.Max(ramp(loWick, 0.25, 0.6), ramp(offLo, 0.3, 1.2))),
		it("rsi", fmt.Sprintf("RSI %.0f", rsiMin)+divTxt(bullDiv, " · 底背离"), 10, math.Max(ramp(rsiMin, 35, 20), b2f(bullDiv))),
		it("liq", liqLabel("多头", lRecent, lMed), 15, liqTerm(lRecent, lMed)),
		it("funding", fundLabel, 10, fundBot),
		it("flow", flowLabel(delta3 > 0), 10, b2f(delta3 > 0)),
	}
	rHi, rLo := math.Inf(-1), math.Inf(1)
	for _, c := range cs[n-30:] {
		rHi, rLo = math.Max(rHi, c.H), math.Min(rLo, c.L)
	}
	span := math.Max(rHi-rLo, 1e-12)
	atTop := ramp((hiBar.H-rLo)/span, 0.6, 0.92)
	atBottom := ramp((rHi-loBar.L)/span, 0.6, 0.92)
	score := func(xs []ExhaustionItem, gate float64) int {
		var got, all float64
		for _, x := range xs {
			got, all = got+x.Points, all+x.Weight
		}
		return int(math.Round(100 * gate * got / all))
	}
	out := Exhaustion{InstID: id, Bar: bar, T: last.T, Last: last.C, Top: score(top, atTop), Bottom: score(bot, atBottom),
		AtTop: atTop, AtBottom: atBottom, TopItems: top, BotItems: bot, ATR: a, EMA20: e20, High: hiBar.H, Low: loBar.L}

	// squeeze veto (pump-study backtest rule), on the 1H EMA24 whatever the bar size
	if hasFund {
		ext24 := math.NaN()
		if h1, err := e.Candles(id, "1H", 60); err == nil && len(h1) >= 24 {
			c1 := make([]float64, len(h1))
			for i, c := range h1 {
				c1[i] = c.C
			}
			ext24 = h1[len(h1)-1].C/emaSeries(c1, 24)[len(c1)-1] - 1
		}
		switch {
		case fundRate <= -0.0005:
			out.Squeeze, out.SqueezeWhy = true, fmt.Sprintf("资金费 %.3f%%/期，空头拥挤（轧空燃料）", fundRate*100)
		case fundRate < 0 && ext24 > 0.15:
			out.Squeeze, out.SqueezeWhy = true, fmt.Sprintf("资金费为负且高于 1H EMA24 %.0f%%", ext24*100)
		}
	}

	// fade plan for the stronger side (rev.py plan_lines); none for a short into a squeeze
	p := FadePlan{TP1: e20}
	if out.Top >= out.Bottom {
		p.Side, p.Stop = "short", hiBar.H+0.5*a
		p.EntryLo, p.EntryHi = math.Max(last.C, hiBar.H-a), hiBar.H-0.2*a
		p.TP2 = baseLo + 0.382*(hiBar.H-baseLo)
	} else {
		p.Side, p.Stop = "long", loBar.L-0.5*a
		p.EntryLo, p.EntryHi = loBar.L+0.2*a, math.Min(last.C, loBar.L+a)
		p.TP2 = baseHi - 0.382*(baseHi-loBar.L)
	}
	if p.EntryLo > p.EntryHi {
		p.EntryLo, p.EntryHi = p.EntryHi, p.EntryLo
	}
	if mid := (p.EntryLo + p.EntryHi) / 2; mid != p.Stop {
		p.RR = math.Round(math.Abs(p.TP1-mid)/math.Abs(mid-p.Stop)*10) / 10
	}
	if !(p.Side == "short" && out.Squeeze) {
		out.Plan = &p
	}
	out.Risk = e.MoveRisk(id)
	return out, nil
}
