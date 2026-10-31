package market

// Move risk after an extended move (rev-study, 2y OKX 1H, 19-month walk-forward OOS): two calibrated
// probabilities per side that did hold up out of sample, unlike any "will it top / bottom here" signal
// (every exhaustion feature and model scored direction AUC 0.47–0.53):
//
//	PTail — price trades ≥ 2 ATR beyond the 3-bar extreme within the next 24 1H bars: for a fade, the
//	        chance its stop is run (AUC ≈ 0.63, 19/19 months above 0.5);
//	PBig3 — the move from the close reaches ≥ 3 ATR either way within 24 bars: how big the next move
//	        is, not which way (AUC ≈ 0.62).
//
// Each is a logistic model over five features, each mapped to its percentile by linear interpolation
// on 21 cutpoints. Coefficients, cutpoints and base rates are the research's spec.json, embedded
// as-is; the reference implementation is the study's score.py (testdata vectors match it).

import (
	_ "embed"
	"encoding/json"
	"math"
	"sort"
)

//go:embed moverisk_spec.json
var moveRiskSpecJSON []byte

type moveModel struct {
	Features  []string             `json:"features"`
	Intercept float64              `json:"intercept"`
	Coef      map[string]float64   `json:"coef"`
	Cuts      map[string][]float64 `json:"cuts"`
	BaseRate  float64              `json:"base_rate"`
}

type moveSpec struct {
	MinCandles int `json:"min_candles"`
	State      struct {
		ExtMin    float64 `json:"ext_min"`
		Pos30Min  float64 `json:"pos30_min"`
		Turn24Min float64 `json:"turn24_min"`
	} `json:"state"`
	Models map[string]moveModel `json:"models"`
}

var moveRisk = func() moveSpec {
	var s moveSpec
	if err := json.Unmarshal(moveRiskSpecJSON, &s); err != nil {
		panic("moverisk spec: " + err.Error())
	}
	return s
}()

// MoveRiskSide is one side's reading: top = the move up a short would fade, bottom = the move down.
type MoveRiskSide struct {
	InState  bool               `json:"inState"` // extended: ext ≥ 2 ATR, extreme in the top/bottom 10% of 30 bars, 24h turnover ≥ 2M
	Ext      float64            `json:"ext"`     // extreme's distance from EMA20 in ATR
	Pos30    float64            `json:"pos30"`
	PTail    float64            `json:"pTail"`
	PBig3    float64            `json:"pBig3"`
	BaseTail float64            `json:"baseTail"` // the rates over all extended states, to read the probabilities against
	BaseBig3 float64            `json:"baseBig3"`
	Extreme  float64            `json:"extreme"`
	Features map[string]float64 `json:"features"`
}

// MoveRisk is both sides on the last confirmed 1H bars.
type MoveRisk struct {
	T      int64        `json:"t"`
	Close  float64      `json:"close"`
	ATR    float64      `json:"atr"`
	Top    MoveRiskSide `json:"top"`
	Bottom MoveRiskSide `json:"bottom"`
}

func sampleStd(xs []float64) float64 {
	if len(xs) < 2 {
		return math.NaN()
	}
	m := 0.0
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	v := 0.0
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	return math.Sqrt(v / float64(len(xs)-1))
}

// interpU is numpy.interp(x, cuts, 0…1 in 21 steps) − 0.5; missing x → 0.
func interpU(x float64, cuts []float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || len(cuts) < 2 {
		return 0
	}
	n := len(cuts)
	q := func(i int) float64 { return float64(i) / float64(n-1) }
	if x <= cuts[0] {
		return q(0) - 0.5
	}
	if x >= cuts[n-1] {
		return q(n-1) - 0.5
	}
	j := sort.SearchFloat64s(cuts, x) // first cut ≥ x
	if cuts[j] == x {
		return q(j) - 0.5
	}
	lo, hi := cuts[j-1], cuts[j]
	return q(j-1) + (x-lo)/(hi-lo)*(q(j)-q(j-1)) - 0.5
}

func (m moveModel) prob(f map[string]float64) float64 {
	z := m.Intercept
	for _, k := range m.Features {
		x, ok := f[k]
		if !ok {
			x = math.NaN()
		}
		z += m.Coef[k] * interpU(x, m.Cuts[k])
	}
	return 1 / (1 + math.Exp(-z))
}

// moveFeatures computes the spec's inputs for one side (+1 top, −1 bottom) on candles oldest first.
func moveFeatures(cs []Candle, side float64) (map[string]float64, float64, float64, float64, float64, float64) {
	n := len(cs)
	t := n - 1
	tr := make([]float64, n)
	cl := make([]float64, n)
	for i, c := range cs {
		cl[i] = c.C
		if i == 0 {
			tr[i] = c.H - c.L
		} else {
			p := cs[i-1].C
			tr[i] = math.Max(c.H-c.L, math.Max(math.Abs(c.H-p), math.Abs(c.L-p)))
		}
	}
	atr := math.Max(emaSeries(tr, 14)[t], 1e-12)
	e20 := emaSeries(cl, 20)[t]
	var x, base, run float64
	if side > 0 {
		x = math.Max(cs[t].H, math.Max(cs[t-1].H, cs[t-2].H))
		base = math.Inf(1)
		for i := t - 29; i <= t-6; i++ {
			base = math.Min(base, cs[i].L)
		}
		run = (x - base) / atr
	} else {
		x = math.Min(cs[t].L, math.Min(cs[t-1].L, cs[t-2].L))
		base = math.Inf(-1)
		for i := t - 29; i <= t-6; i++ {
			base = math.Max(base, cs[i].H)
		}
		run = (base - x) / atr
	}
	hi30, lo30 := math.Inf(-1), math.Inf(1)
	for i := t - 29; i <= t; i++ {
		hi30, lo30 = math.Max(hi30, cs[i].H), math.Min(lo30, cs[i].L)
	}
	span := math.Max(hi30-lo30, 1e-12)
	pos30 := (x - lo30) / span
	if side < 0 {
		pos30 = (hi30 - x) / span
	}
	ext := side * (x - e20) / atr

	lq := make([]float64, n)
	for i, c := range cs {
		lq[i] = math.Log(math.Max(c.Quote, 1))
	}
	prior := lq[t-50 : t-2] // bars t-50 … t-3 (48)
	pm := 0.0
	for _, v := range prior {
		pm += v
	}
	pm /= float64(len(prior))
	lqMax := math.Max(lq[t], math.Max(lq[t-1], lq[t-2]))
	volz := (lqMax - pm) / sampleStd(prior)
	qs := make([]float64, 0, 48)
	for _, c := range cs[t-50 : t-2] {
		qs = append(qs, c.Quote)
	}
	medq := median(qs)
	qMax := math.Max(cs[t].Quote, math.Max(cs[t-1].Quote, cs[t-2].Quote))
	vclimax := math.NaN()
	if medq > 0 {
		vclimax = qMax / medq
	}
	lr := make([]float64, n-1)
	for i := 1; i < n; i++ {
		lr[i-1] = math.Log(cl[i] / cl[i-1])
	}
	rvRatio := sampleStd(lr[len(lr)-6:]) / sampleStd(lr[len(lr)-78:len(lr)-6])
	turn24, hi24, lo24 := 0.0, math.Inf(-1), math.Inf(1)
	for i := t - 23; i <= t; i++ {
		turn24 += cs[i].Quote
		hi24, lo24 = math.Max(hi24, cs[i].H), math.Min(lo24, cs[i].L)
	}
	f := map[string]float64{
		"atr_pct":    atr / cl[t],
		"ret24_pct":  side * math.Log(cl[t]/cl[t-24]),
		"run":        run,
		"volz":       volz,
		"vclimax":    vclimax,
		"rv_ratio":   rvRatio,
		"range_tr":   (hi24 - lo24) / (atr * math.Sqrt(24)),
		"log_turn24": math.Log(math.Max(turn24, 1)),
		"off":        side * (x - cl[t]) / atr,
		"ext":        ext,
	}
	return f, ext, pos30, turn24, atr, x
}

// scoreMoveRisk scores both sides of confirmed 1H candles (oldest first, ≥ spec.MinCandles).
func scoreMoveRisk(cs []Candle) MoveRisk {
	out := MoveRisk{T: cs[len(cs)-1].T, Close: cs[len(cs)-1].C}
	for _, sd := range []struct {
		name string
		side float64
		dst  *MoveRiskSide
	}{{"top", 1, &out.Top}, {"bottom", -1, &out.Bottom}} {
		f, ext, pos30, turn24, atr, x := moveFeatures(cs, sd.side)
		st := moveRisk.State
		mt, mb := moveRisk.Models[sd.name+"_tail2"], moveRisk.Models[sd.name+"_big3"]
		*sd.dst = MoveRiskSide{
			InState: ext >= st.ExtMin && pos30 >= st.Pos30Min && turn24 >= st.Turn24Min,
			Ext:     ext, Pos30: pos30, PTail: mt.prob(f), PBig3: mb.prob(f),
			BaseTail: mt.BaseRate, BaseBig3: mb.BaseRate, Extreme: x, Features: f,
		}
		out.ATR = atr
	}
	return out
}

// MoveRisk returns the continuation-tail and move-size probabilities on the last confirmed 1H bars,
// or nil when there is not enough history.
func (e *Engine) MoveRisk(id string) *MoveRisk {
	cs, err := e.Candles(id, "1H", moveRisk.MinCandles+5)
	if err != nil {
		return nil
	}
	for len(cs) > 0 && !cs[len(cs)-1].Confirmed { // the spec scores closed bars only
		cs = cs[:len(cs)-1]
	}
	if len(cs) < moveRisk.MinCandles {
		return nil
	}
	r := scoreMoveRisk(cs[len(cs)-moveRisk.MinCandles:])
	return &r
}
