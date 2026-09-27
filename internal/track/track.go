// Package track evaluates a rule's recorded score snapshots against realized forward returns.
// This is the only evidence the product accepts for "a score is useful" (revision §2.3).
package track

import (
	"math"
	"sort"

	"crypto-trader-go/internal/store"
)

const hourMs = int64(3_600_000)

// TakerFeeBps is the per-side taker fee assumed for net figures (OKX regular tier USDT swaps).
const TakerFeeBps = 5.0

type Stats struct {
	HorizonH    int     `json:"horizonH"`
	CrossSects  int     `json:"crossSections"`
	MedianNames int     `json:"medianNames"`
	LongIC      float64 `json:"longIC"`
	LongT       float64 `json:"longT"`
	LongHit     float64 `json:"longHit"`
	ShortIC     float64 `json:"shortIC"`
	ShortT      float64 `json:"shortT"`
	ShortHit    float64 `json:"shortHit"`
	LongTopBps  float64 `json:"longTopExcessBps"`  // top-quintile long mean return minus pool mean (gross)
	LongTopNet  float64 `json:"longTopNetBps"`     // top-quintile long absolute return minus fees+spread
	ShortTopBps float64 `json:"shortTopExcessBps"` // top-quintile short: mean(−ret) minus pool mean(−ret)
	ShortTopNet float64 `json:"shortTopNetBps"`
	FirstTs     int64   `json:"firstTs"`
	LastTs      int64   `json:"lastTs"`
	CumLongIC   []Point `json:"cumLongIC"`
	Verdict     string  `json:"verdict"`
}

type Point struct {
	Ts int64   `json:"ts"`
	V  float64 `json:"v"`
}

// Evaluate computes stats for horizon h hours. price(inst, t) returns the close at t.
// Cross-sections are sampled every h hours (non-overlapping) on hour-aligned timestamps.
func Evaluate(rows []store.ScoreRow, h int, price func(string, int64) (float64, bool)) Stats {
	st := Stats{HorizonH: h}
	byTs := map[int64][]store.ScoreRow{}
	for _, r := range rows {
		if r.Ts%hourMs != 0 || (r.Ts/hourMs)%int64(h) != 0 {
			continue
		}
		byTs[r.Ts] = append(byTs[r.Ts], r)
	}
	tss := make([]int64, 0, len(byTs))
	for ts := range byTs {
		tss = append(tss, ts)
	}
	sort.Slice(tss, func(i, j int) bool { return tss[i] < tss[j] })
	var lic, sic, ltop, lnet, stop, snet []float64
	var names []int
	cum := 0.0
	for _, ts := range tss {
		type obs struct {
			l, s      *float64
			fwd, cost float64
		}
		var xs []obs
		for _, r := range byTs[ts] {
			p1, ok := price(r.InstID, ts+int64(h)*hourMs)
			if !ok || r.Close <= 0 || p1 <= 0 {
				continue
			}
			xs = append(xs, obs{r.Long, r.Short, math.Log(p1 / r.Close), (2*TakerFeeBps + r.Spread) / 1e4})
		}
		if len(xs) < 5 {
			continue
		}
		names = append(names, len(xs))
		poolMean := 0.0
		for _, o := range xs {
			poolMean += o.fwd
		}
		poolMean /= float64(len(xs))
		side := func(get func(obs) *float64, sign float64, ics, tops, nets *[]float64) (float64, bool) {
			var sc, fw []float64
			var sel []obs
			for _, o := range xs {
				if v := get(o); v != nil {
					sc = append(sc, *v)
					fw = append(fw, sign*o.fwd)
					sel = append(sel, o)
				}
			}
			if len(sc) < 5 {
				return 0, false
			}
			ic := spearman(sc, fw)
			if math.IsNaN(ic) {
				return 0, false
			}
			*ics = append(*ics, ic)
			idx := make([]int, len(sc))
			for i := range idx {
				idx[i] = i
			}
			sort.Slice(idx, func(a, b int) bool { return sc[idx[a]] > sc[idx[b]] })
			q := max(1, len(idx)/5)
			top, cost := 0.0, 0.0
			for _, i := range idx[:q] {
				top += fw[i]
				cost += sel[i].cost
			}
			top /= float64(q)
			cost /= float64(q)
			*tops = append(*tops, (top-sign*poolMean)*1e4)
			*nets = append(*nets, (top-cost)*1e4)
			return ic, true
		}
		if ic, ok := side(func(o obs) *float64 { return o.l }, 1, &lic, &ltop, &lnet); ok {
			cum += ic
			st.CumLongIC = append(st.CumLongIC, Point{ts, cum})
		}
		side(func(o obs) *float64 { return o.s }, -1, &sic, &stop, &snet)
		if st.FirstTs == 0 {
			st.FirstTs = ts
		}
		st.LastTs = ts
	}
	st.CrossSects = len(names)
	if len(names) > 0 {
		sort.Ints(names)
		st.MedianNames = names[len(names)/2]
	}
	st.LongIC, st.LongT, st.LongHit = meanT(lic)
	st.ShortIC, st.ShortT, st.ShortHit = meanT(sic)
	st.LongTopBps, _, _ = meanT(ltop)
	st.LongTopNet, _, _ = meanT(lnet)
	st.ShortTopBps, _, _ = meanT(stop)
	st.ShortTopNet, _, _ = meanT(snet)
	switch {
	case st.CrossSects < 30:
		st.Verdict = "样本不足"
	case (st.LongT > 2 && st.LongTopNet > 0) || (st.ShortT > 2 && st.ShortTopNet > 0):
		st.Verdict = "有初步证据（仍需更长样本）"
	case st.LongT < -2 || st.ShortT < -2:
		st.Verdict = "方向相反（显著为负）"
	default:
		st.Verdict = "无显著证据"
	}
	return st
}

func meanT(x []float64) (m, t, hit float64) {
	if len(x) == 0 {
		return 0, 0, 0
	}
	for _, v := range x {
		m += v
		if v > 0 {
			hit++
		}
	}
	m /= float64(len(x))
	hit /= float64(len(x))
	if len(x) < 2 {
		return m, 0, hit
	}
	s := 0.0
	for _, v := range x {
		s += (v - m) * (v - m)
	}
	sd := math.Sqrt(s / float64(len(x)-1))
	if sd > 0 {
		t = m / (sd / math.Sqrt(float64(len(x))))
	}
	return
}

func ranks(x []float64) []float64 {
	idx := make([]int, len(x))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return x[idx[a]] < x[idx[b]] })
	r := make([]float64, len(x))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && x[idx[j+1]] == x[idx[i]] {
			j++
		}
		for k := i; k <= j; k++ {
			r[idx[k]] = float64(i+j) / 2
		}
		i = j + 1
	}
	return r
}

func spearman(x, y []float64) float64 {
	rx, ry := ranks(x), ranks(y)
	var mx, my float64
	for i := range rx {
		mx += rx[i]
		my += ry[i]
	}
	mx /= float64(len(rx))
	my /= float64(len(ry))
	var sxy, sxx, syy float64
	for i := range rx {
		sxy += (rx[i] - mx) * (ry[i] - my)
		sxx += (rx[i] - mx) * (rx[i] - mx)
		syy += (ry[i] - my) * (ry[i] - my)
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}
