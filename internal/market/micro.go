package market

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ---------- microstructure statistics over taped trades ----------
//
// Trades are first grouped into taker orders: consecutive trades with the same millisecond and
// side are one aggressive order that filled at one or more prices (the WS stream sends one row per
// price, the daily files one per maker fill — grouping makes both the same). Everything below
// counts taker orders, sizes in USDT.

// TapeOrder is one aggressive (taker) order.
type TapeOrder struct {
	Ts       int64
	Buy      bool
	Px       float64 // VWAP of its fills
	Px0, Px1 float64 // first / last fill price
	Usd      float64
	Qty      float64 // contracts (swap) or coins (spot)
	Fills    int     // distinct fill rows (≥3 at several prices = a sweep)
	Levels   int     // distinct prices
}

func groupOrders(xs []TapeTrade, ctVal float64) []TapeOrder {
	var out []TapeOrder
	var lastPx float64
	for i, t := range xs {
		usd := t.Px * t.Sz * ctVal
		if i > 0 {
			o := &out[len(out)-1]
			if t.Ts == o.Ts && t.Buy == o.Buy {
				o.Px = (o.Px*o.Qty + t.Px*t.Sz) / (o.Qty + t.Sz) // volume-weighted: Σ px·qty / Σ qty
				o.Usd += usd
				o.Qty += t.Sz
				o.Fills++
				if t.Px != lastPx {
					o.Levels++
				}
				o.Px1, lastPx = t.Px, t.Px
				continue
			}
		}
		out = append(out, TapeOrder{Ts: t.Ts, Buy: t.Buy, Px: t.Px, Px0: t.Px, Px1: t.Px, Usd: usd, Qty: t.Sz, Fills: 1, Levels: 1})
		lastPx = t.Px
	}
	for i := range out { // float noise from summing decimal sizes
		out[i].Qty = roundSig(out[i].Qty, 10)
	}
	return out
}

func roundSig(x float64, sig int) float64 {
	if x == 0 {
		return 0
	}
	p := math.Pow10(sig - 1 - int(math.Floor(math.Log10(math.Abs(x)))))
	return math.Round(x*p) / p
}

// TapeQuery selects what TapeStatsFor computes.
type TapeQuery struct {
	From, To    int64
	Sections    map[string]bool // summary sizes bars profile clips sweeps large
	SizeEdges   []float64       // USDT bucket edges; nil = percentile-based
	LargeUsd    float64         // "large order" threshold; 0 = auto (≈ p99)
	BarMs       int64           // 0 = auto (≤ 48 bars)
	ProfileBins int
	ClipMin     int     // repeats for a clip run
	ClipMinUsd  float64 // 0 = auto (≈ p90, ≥ 200)
	ClipTolPct  float64 // sizes within ±this % count as the same clip (0 = exact)
	ClipGapMin  float64 // a run breaks after this many minutes without a repeat
	Top         int
	SweepBps    float64 // a sweep: ≥ 3 price levels and ≥ this first→last move (default 5)
	Side        int     // lists: 1 buys, -1 sells, 0 both
	MinUsd      float64 // lists: ignore orders below this
}

func (q TapeQuery) on(s string) bool { return q.Sections[s] }

// TapeStats is the analysis of one market (swap or spot).
type TapeStats struct {
	InstID   string        `json:"instId"`
	Market   string        `json:"market"`
	Coverage TapeCoverage  `json:"coverage"`
	Summary  *TapeSummary  `json:"summary,omitempty"`
	Sizes    []SizeBucket  `json:"sizes,omitempty"`
	Bars     []TapeBar     `json:"bars,omitempty"`
	Profile  *PriceProfile `json:"profile,omitempty"`
	Clips    []ClipRun     `json:"clips,omitempty"`
	Sweeps   *SweepStats   `json:"sweeps,omitempty"`
	Large    []LargeOrder  `json:"large,omitempty"`
	Note     string        `json:"note,omitempty"`

	orders  []TapeOrder
	q       TapeQuery
	clipAll []ClipRun // every run (signals use them; Clips is the top of these)
	wash    WashStats
}

// TapeSummary: totals, the order-size distribution and who drove the delta.
type TapeSummary struct {
	Orders        int       `json:"orders"`
	OrdersPerHour float64   `json:"ordersPerHour"`
	BuyUsd        float64   `json:"buyUsd"`
	SellUsd       float64   `json:"sellUsd"`
	DeltaUsd      float64   `json:"deltaUsd"`
	BuyPct        float64   `json:"buyPct"`
	Open          float64   `json:"open"`
	Close         float64   `json:"close"`
	High          float64   `json:"high"`
	Low           float64   `json:"low"`
	RetPct        float64   `json:"retPct"`
	Vwap          float64   `json:"vwap"`
	SizeP50       float64   `json:"sizeP50"` // taker order size percentiles, USDT
	SizeP90       float64   `json:"sizeP90"`
	SizeP99       float64   `json:"sizeP99"`
	SizeMax       float64   `json:"sizeMax"`
	LargeUsd      float64   `json:"largeUsd"` // threshold used for large*
	LargeBuyUsd   float64   `json:"largeBuyUsd"`
	LargeSellUsd  float64   `json:"largeSellUsd"`
	LargeDeltaPct float64   `json:"largeDeltaPct"`    // (large buy − large sell) / total volume, %
	RestDeltaPct  float64   `json:"restDeltaPct"`     // the same for orders below the threshold
	Top1PctShare  float64   `json:"top1PctShare"`     // % of volume from the largest 1% of orders
	LambdaBps     float64   `json:"lambdaBpsPer100k"` // price impact: 1m return (bps) per 100k USDT net taker flow (OLS)
	LambdaR2      float64   `json:"lambdaR2"`
	Wash          WashStats `json:"wash"`
}

// WashStats: opposite-side taker orders of the identical (non-round) size within 2 s at about the
// same price — what one party trading with itself, or a bot ping-ponging, leaves behind.
type WashStats struct {
	Pairs    int     `json:"pairs"`
	Usd      float64 `json:"usd"`
	PctOfVol float64 `json:"pctOfVol"`
	MinUsd   float64 `json:"minUsd"` // orders below this were not considered
}

type SizeBucket struct {
	Lo      float64 `json:"lo"`
	Hi      float64 `json:"hi"` // 0 = no upper bound
	Label   string  `json:"label,omitempty"`
	BuyUsd  float64 `json:"buyUsd"`
	SellUsd float64 `json:"sellUsd"`
	NetUsd  float64 `json:"netUsd"`
	BuyN    int     `json:"buyN"`
	SellN   int     `json:"sellN"`
	VolPct  float64 `json:"volPct"`
}

type TapeBar struct {
	T   int64   `json:"t"`
	O   float64 `json:"o"`
	H   float64 `json:"h"`
	L   float64 `json:"l"`
	C   float64 `json:"c"`
	Buy float64 `json:"buy"`
	Sel float64 `json:"sell"`
	Cvd float64 `json:"cvd"`
	LB  float64 `json:"largeBuy"`
	LS  float64 `json:"largeSell"`
	N   int     `json:"n"`
}

type PriceProfile struct {
	Step float64        `json:"step"`
	Poc  float64        `json:"poc"` // bin with the most volume (low edge)
	Bins []ProfileLevel `json:"bins"`
}

type ProfileLevel struct {
	Px  float64 `json:"px"`
	Buy float64 `json:"buy"`
	Sel float64 `json:"sell"`
	LB  float64 `json:"largeBuy"`
	LS  float64 `json:"largeSell"`
}

// ClipRun is a run of taker orders of one identical size on one side: an algorithm slicing a big
// order (TWAP / iceberg-style market orders), or a bot.
type ClipRun struct {
	Side         string  `json:"side"`
	Qty          float64 `json:"qty"`
	UsdEach      float64 `json:"usdEach"`
	Count        int     `json:"count"`
	TotalUsd     float64 `json:"totalUsd"`
	T0           int64   `json:"t0"`
	T1           int64   `json:"t1"`
	MedGapSec    float64 `json:"medGapSec"`
	GapCV        float64 `json:"gapCV"` // stdev / mean of gaps: < 0.5 = clockwork
	Px0          float64 `json:"px0"`
	Px1          float64 `json:"px1"`
	DriftBps     float64 `json:"driftBps"`
	WithTrendPct float64 `json:"withTrendPct"` // % of steps where the next clip printed at a worse price for the clipper (pushing)
	SideSharePct float64 `json:"sideSharePct"` // its share of all same-side taker volume during the run
	OppSame      int     `json:"oppSame"`      // opposite-side orders of the same size during the run
	Round        bool    `json:"round"`        // a round size many people type (1000, 500): weaker evidence
	Pattern      string  `json:"pattern"`      // burst | clockwork | ladder | repeat
}

type SweepStats struct {
	BuyN    int          `json:"buyN"`
	SellN   int          `json:"sellN"`
	BuyUsd  float64      `json:"buyUsd"`
	SellUsd float64      `json:"sellUsd"`
	Top     []LargeOrder `json:"top"`
}

type LargeOrder struct {
	T          int64   `json:"t"`
	Side       string  `json:"side"`
	Px         float64 `json:"px"`
	Usd        float64 `json:"usd"`
	Levels     int     `json:"levels"`
	ImpactBps  float64 `json:"impactBps"`            // first → last fill price
	After1mBps float64 `json:"after1mBps,omitempty"` // price 1 / 5 minutes later vs its VWAP, + = moved its way
	After5mBps float64 `json:"after5mBps,omitempty"`
}

func sideName(buy bool) string {
	if buy {
		return "buy"
	}
	return "sell"
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := q * float64(len(sorted)-1)
	lo := int(math.Floor(i))
	hi := min(lo+1, len(sorted)-1)
	return sorted[lo] + (sorted[hi]-sorted[lo])*(i-float64(lo))
}

// nice rounds to 1/2/2.5/5 × 10^k (nearest below-or-equal).
func nice(x float64) float64 {
	if x <= 0 {
		return 0
	}
	p := math.Pow10(int(math.Floor(math.Log10(x))))
	for _, m := range []float64{5, 2.5, 2, 1} {
		if x >= m*p {
			return m * p
		}
	}
	return p
}

// isRound: a size people type by hand (≤ 2 significant digits, ≥ 10).
func isRound(q float64) bool {
	if q < 10 {
		return false
	}
	s := strings.TrimRight(strconv.FormatFloat(q, 'f', -1, 64), "0")
	s = strings.TrimSuffix(s, ".")
	return len(strings.TrimLeft(s, "0")) <= 2 && !strings.Contains(s, ".")
}

// TapeStatsFor analyses one market's trades.
func TapeStatsFor(d TapeData, q TapeQuery) *TapeStats {
	s := &TapeStats{InstID: d.InstID, Market: d.Market, Coverage: d.Coverage, q: q}
	s.orders = groupOrders(d.Trades, d.CtVal)
	os := s.orders
	if len(os) == 0 {
		s.Note = "no trades in the window"
		return s
	}
	sizes := make([]float64, len(os))
	for i, o := range os {
		sizes[i] = o.Usd
	}
	sort.Float64s(sizes)
	p50, p90, p99 := quantile(sizes, 0.5), quantile(sizes, 0.9), quantile(sizes, 0.99)
	if q.LargeUsd <= 0 {
		q.LargeUsd = max(nice(p99), 1000)
	}
	if q.ClipMinUsd <= 0 {
		q.ClipMinUsd = max(nice(p90), 200)
	}
	if q.SweepBps <= 0 {
		q.SweepBps = 5
	}
	s.q = q
	sum := &TapeSummary{Orders: len(os), SizeP50: p50, SizeP90: p90, SizeP99: p99, SizeMax: sizes[len(sizes)-1], LargeUsd: q.LargeUsd,
		Open: os[0].Px0, Close: os[len(os)-1].Px1, High: os[0].Px, Low: os[0].Px}
	var pv float64
	for _, o := range os {
		if o.Buy {
			sum.BuyUsd += o.Usd
			if o.Usd >= q.LargeUsd {
				sum.LargeBuyUsd += o.Usd
			}
		} else {
			sum.SellUsd += o.Usd
			if o.Usd >= q.LargeUsd {
				sum.LargeSellUsd += o.Usd
			}
		}
		sum.High = max(sum.High, o.Px0, o.Px1)
		sum.Low = min(sum.Low, o.Px0, o.Px1)
		pv += o.Px * o.Usd
	}
	vol := sum.BuyUsd + sum.SellUsd
	sum.DeltaUsd = sum.BuyUsd - sum.SellUsd
	sum.BuyPct = sum.BuyUsd / vol * 100
	sum.Vwap = pv / vol
	sum.RetPct = (sum.Close/sum.Open - 1) * 100
	sum.LargeDeltaPct = (sum.LargeBuyUsd - sum.LargeSellUsd) / vol * 100
	sum.RestDeltaPct = (sum.DeltaUsd - (sum.LargeBuyUsd - sum.LargeSellUsd)) / vol * 100
	k := max(1, len(sizes)/100)
	top := 0.0
	for _, x := range sizes[len(sizes)-k:] {
		top += x
	}
	sum.Top1PctShare = top / vol * 100
	if hrs := float64(os[len(os)-1].Ts-os[0].Ts) / float64(hourMs); hrs > 0 {
		sum.OrdersPerHour = float64(len(os)) / hrs
	}
	sum.LambdaBps, sum.LambdaR2 = impactLambda(os)
	s.wash = washPairs(os, max(200, quantile(sizes, 0.75)))
	s.wash.PctOfVol = s.wash.Usd / vol * 100
	sum.Wash = s.wash
	if q.on("summary") {
		s.Summary = sum
	}
	if q.on("sizes") {
		s.Sizes = sizeBuckets(os, q.SizeEdges, sizes, vol)
	}
	if q.on("bars") {
		s.Bars = tapeBars(os, q)
	}
	if q.on("profile") {
		s.Profile = priceProfile(os, sum.Low, sum.High, q)
	}
	s.clipAll = clipRuns(os, q)
	if q.on("clips") {
		for _, r := range s.clipAll {
			if (q.Side == 0 || (q.Side > 0) == (r.Side == "buy")) && r.UsdEach >= q.MinUsd {
				s.Clips = append(s.Clips, r)
			}
			if len(s.Clips) >= q.Top {
				break
			}
		}
	}
	if q.on("sweeps") {
		s.Sweeps = sweeps(os, q)
	}
	if q.on("large") {
		s.Large = largeOrders(os, q)
	}
	return s
}

func (q TapeQuery) keep(o TapeOrder) bool {
	return o.Usd >= q.MinUsd && (q.Side == 0 || (q.Side > 0) == o.Buy)
}

// impactLambda: OLS of per-minute log return (bps) on per-minute net taker USDT (per 100k).
func impactLambda(os []TapeOrder) (slope, r2 float64) {
	type mrow struct{ net, last, first float64 }
	byMin := map[int64]*mrow{}
	var keys []int64
	for _, o := range os {
		m := o.Ts / minuteMs
		r := byMin[m]
		if r == nil {
			r = &mrow{first: o.Px0}
			byMin[m] = r
			keys = append(keys, m)
		}
		if o.Buy {
			r.net += o.Usd
		} else {
			r.net -= o.Usd
		}
		r.last = o.Px1
	}
	var xs, ys []float64
	prev := 0.0
	for _, m := range keys {
		r := byMin[m]
		ref := prev
		if ref == 0 {
			ref = r.first
		}
		xs = append(xs, r.net/1e5)
		ys = append(ys, math.Log(r.last/ref)*1e4)
		prev = r.last
	}
	return ols(xs, ys)
}

func ols(xs, ys []float64) (slope, r2 float64) {
	n := float64(len(xs))
	if n < 10 {
		return 0, 0
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx, my = mx/n, my/n
	var sxy, sxx, syy float64
	for i := range xs {
		sxy += (xs[i] - mx) * (ys[i] - my)
		sxx += (xs[i] - mx) * (xs[i] - mx)
		syy += (ys[i] - my) * (ys[i] - my)
	}
	if sxx == 0 || syy == 0 {
		return 0, 0
	}
	slope = sxy / sxx
	r2 = sxy * sxy / (sxx * syy)
	return round2(slope), round2(r2)
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func washPairs(os []TapeOrder, minUsd float64) WashStats {
	w := WashStats{MinUsd: math.Round(minUsd)}
	type ref struct {
		i  int
		ts int64
	}
	last := map[[2]float64]ref{} // (qty, buy?1:0) → latest unmatched order
	used := map[int]bool{}
	for i, o := range os {
		if o.Usd < minUsd || isRound(o.Qty) {
			continue
		}
		b := 0.0
		if o.Buy {
			b = 1
		}
		if r, ok := last[[2]float64{o.Qty, 1 - b}]; ok && !used[r.i] && o.Ts-r.ts <= 2000 &&
			math.Abs(os[r.i].Px/o.Px-1) <= 5e-4 {
			used[r.i], used[i] = true, true
			w.Pairs++
			w.Usd += o.Usd + os[r.i].Usd
			delete(last, [2]float64{o.Qty, 1 - b})
			continue
		}
		last[[2]float64{o.Qty, b}] = ref{i, o.Ts}
	}
	w.Usd = math.Round(w.Usd)
	return w
}

func sizeBuckets(os []TapeOrder, edges, sorted []float64, vol float64) []SizeBucket {
	labels := map[float64]string{}
	if len(edges) == 0 {
		for _, p := range []struct {
			q float64
			l string
		}{{0.5, "p50"}, {0.9, "p90"}, {0.99, "p99"}, {0.999, "p99.9"}} {
			x := nice(quantile(sorted, p.q))
			if x > 0 && (len(edges) == 0 || x > edges[len(edges)-1]) {
				edges = append(edges, x)
				labels[x] = p.l
			}
		}
	}
	out := make([]SizeBucket, len(edges)+1)
	lo := 0.0
	for i := range out {
		out[i].Lo = lo
		if i < len(edges) {
			out[i].Hi = edges[i]
			lo = edges[i]
			if l := labels[edges[i]]; l != "" {
				out[i].Label = "<" + l
			}
		} else if len(labels) > 0 {
			out[i].Label = "≥p99.9"
			if l := labels[edges[len(edges)-1]]; l != "" {
				out[i].Label = "≥" + l
			}
		}
	}
	for _, o := range os {
		i := sort.Search(len(edges), func(i int) bool { return edges[i] > o.Usd })
		if o.Buy {
			out[i].BuyUsd += o.Usd
			out[i].BuyN++
		} else {
			out[i].SellUsd += o.Usd
			out[i].SellN++
		}
	}
	for i := range out {
		out[i].VolPct = round2((out[i].BuyUsd + out[i].SellUsd) / vol * 100)
		out[i].BuyUsd, out[i].SellUsd = math.Round(out[i].BuyUsd), math.Round(out[i].SellUsd)
		out[i].NetUsd = out[i].BuyUsd - out[i].SellUsd
	}
	return out
}

func autoBar(span int64) int64 {
	for _, b := range []int64{minuteMs, 5 * minuteMs, 15 * minuteMs, hourMs, 4 * hourMs} {
		if span/b <= 48 {
			return b
		}
	}
	return 4 * hourMs
}

func tapeBars(os []TapeOrder, q TapeQuery) []TapeBar {
	bar := q.BarMs
	if bar <= 0 {
		bar = autoBar(q.To - q.From)
	}
	var out []TapeBar
	cvd := 0.0
	for _, o := range os {
		t := floorTo(o.Ts, bar)
		if len(out) == 0 || out[len(out)-1].T != t {
			out = append(out, TapeBar{T: t, O: o.Px0, H: o.Px, L: o.Px})
		}
		b := &out[len(out)-1]
		b.H, b.L, b.C = max(b.H, o.Px0, o.Px1), min(b.L, o.Px0, o.Px1), o.Px1
		b.N++
		if o.Buy {
			b.Buy += o.Usd
			cvd += o.Usd
			if o.Usd >= q.LargeUsd {
				b.LB += o.Usd
			}
		} else {
			b.Sel += o.Usd
			cvd -= o.Usd
			if o.Usd >= q.LargeUsd {
				b.LS += o.Usd
			}
		}
		b.Cvd = cvd
	}
	for i := range out {
		b := &out[i]
		b.Buy, b.Sel, b.Cvd, b.LB, b.LS = math.Round(b.Buy), math.Round(b.Sel), math.Round(b.Cvd), math.Round(b.LB), math.Round(b.LS)
		b.O, b.H, b.L, b.C = roundSig(b.O, 6), roundSig(b.H, 6), roundSig(b.L, 6), roundSig(b.C, 6)
	}
	return out
}

// floorTo floors to a bar boundary in UTC+8 for bars of a day or less (4H bars start 00/04/… Beijing).
func floorTo(ts, bar int64) int64 {
	const off = 8 * hourMs
	return (ts+off)/bar*bar - off
}

func priceProfile(os []TapeOrder, lo, hi float64, q TapeQuery) *PriceProfile {
	n := q.ProfileBins
	if n <= 0 {
		n = 24
	}
	if hi <= lo {
		hi = lo * 1.0001
	}
	step := (hi - lo) / float64(n)
	p := &PriceProfile{Step: roundSig(step, 4), Bins: make([]ProfileLevel, n)}
	for i := range p.Bins {
		p.Bins[i].Px = lo + step*float64(i)
	}
	for _, o := range os {
		i := min(n-1, max(0, int((o.Px-lo)/step)))
		b := &p.Bins[i]
		if o.Buy {
			b.Buy += o.Usd
			if o.Usd >= q.LargeUsd {
				b.LB += o.Usd
			}
		} else {
			b.Sel += o.Usd
			if o.Usd >= q.LargeUsd {
				b.LS += o.Usd
			}
		}
	}
	best := -1.0
	for i := range p.Bins {
		b := &p.Bins[i]
		b.Px = roundSig(b.Px, 6)
		if v := b.Buy + b.Sel; v > best {
			best, p.Poc = v, b.Px
		}
		b.Buy, b.Sel, b.LB, b.LS = math.Round(b.Buy), math.Round(b.Sel), math.Round(b.LB), math.Round(b.LS)
	}
	return p
}

// clipRuns finds runs of same-size taker orders (≥ ClipMin, each ≥ ClipMinUsd), largest first.
func clipRuns(os []TapeOrder, q TapeQuery) []ClipRun {
	minN := max(q.ClipMin, 3)
	gap := int64(max(q.ClipGapMin, 1) * float64(minuteMs))
	key := func(o TapeOrder) float64 {
		if q.ClipTolPct <= 0 {
			return o.Qty
		}
		return math.Round(math.Log(o.Qty) / math.Log1p(q.ClipTolPct/100))
	}
	type k2 struct {
		buy bool
		k   float64
	}
	groups := map[k2][]int{}
	for i, o := range os {
		if o.Usd >= q.ClipMinUsd {
			groups[k2{o.Buy, key(o)}] = append(groups[k2{o.Buy, key(o)}], i)
		}
	}
	// same-side volume prefix sums (share of side volume during a run)
	cumB, cumS := make([]float64, len(os)+1), make([]float64, len(os)+1)
	for i, o := range os {
		cumB[i+1], cumS[i+1] = cumB[i], cumS[i]
		if o.Buy {
			cumB[i+1] += o.Usd
		} else {
			cumS[i+1] += o.Usd
		}
	}
	var out []ClipRun
	emit := func(g k2, idx []int) {
		if len(idx) < minN {
			return
		}
		first, last := os[idx[0]], os[idx[len(idx)-1]]
		r := ClipRun{Side: sideName(g.buy), Qty: first.Qty, Count: len(idx), T0: first.Ts, T1: last.Ts, Px0: roundSig(first.Px, 6), Px1: roundSig(last.Px, 6), Round: isRound(first.Qty)}
		var gaps []float64
		with := 0
		for j, i := range idx {
			r.TotalUsd += os[i].Usd
			if j > 0 {
				gaps = append(gaps, float64(os[i].Ts-os[idx[j-1]].Ts)/1000)
				d := os[i].Px - os[idx[j-1]].Px
				if (g.buy && d > 0) || (!g.buy && d < 0) {
					with++
				}
			}
		}
		r.UsdEach = math.Round(r.TotalUsd / float64(len(idx)))
		r.TotalUsd = math.Round(r.TotalUsd)
		sg := append([]float64(nil), gaps...)
		sort.Float64s(sg)
		r.MedGapSec = round2(quantile(sg, 0.5))
		mean, sd := meanSd(gaps)
		if mean > 0 {
			r.GapCV = round2(sd / mean)
		}
		r.DriftBps = math.Round((r.Px1/r.Px0 - 1) * 1e4)
		r.WithTrendPct = math.Round(float64(with) / float64(len(idx)-1) * 100)
		lo, hi := idx[0], idx[len(idx)-1]+1
		side := cumS[hi] - cumS[lo]
		if g.buy {
			side = cumB[hi] - cumB[lo]
		}
		if side > 0 {
			r.SideSharePct = math.Round(r.TotalUsd / side * 100)
		}
		for i := lo; i < hi; i++ {
			if os[i].Buy != g.buy && key(os[i]) == g.k {
				r.OppSame++
			}
		}
		switch {
		case r.MedGapSec < 2:
			r.Pattern = "burst"
		case r.GapCV < 0.5:
			r.Pattern = "clockwork"
		case r.WithTrendPct >= 70:
			r.Pattern = "ladder"
		default:
			r.Pattern = "repeat"
		}
		out = append(out, r)
	}
	for g, idx := range groups {
		start := 0
		for j := 1; j <= len(idx); j++ {
			if j == len(idx) || os[idx[j]].Ts-os[idx[j-1]].Ts > gap {
				emit(g, idx[start:j])
				start = j
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalUsd > out[j].TotalUsd })
	return out
}

func meanSd(xs []float64) (mean, sd float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		sd += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(sd / float64(len(xs)))
}

func sweeps(os []TapeOrder, q TapeQuery) *SweepStats {
	s := &SweepStats{}
	var top []LargeOrder
	for i, o := range os {
		if o.Levels < 3 || math.Abs(o.Px1/o.Px0-1)*1e4 < q.SweepBps || !q.keep(o) {
			continue
		}
		if o.Buy {
			s.BuyN++
			s.BuyUsd += o.Usd
		} else {
			s.SellN++
			s.SellUsd += o.Usd
		}
		top = append(top, largeOf(os, i))
	}
	s.BuyUsd, s.SellUsd = math.Round(s.BuyUsd), math.Round(s.SellUsd)
	sort.Slice(top, func(i, j int) bool { return top[i].Usd > top[j].Usd })
	s.Top = top[:min(len(top), q.Top)]
	return s
}

func largeOrders(os []TapeOrder, q TapeQuery) []LargeOrder {
	idx := make([]int, 0, len(os))
	for i, o := range os {
		if q.keep(o) {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(a, b int) bool { return os[idx[a]].Usd > os[idx[b]].Usd })
	var out []LargeOrder
	for _, i := range idx[:min(len(idx), q.Top)] {
		out = append(out, largeOf(os, i))
	}
	return out
}

func largeOf(os []TapeOrder, i int) LargeOrder {
	o := os[i]
	l := LargeOrder{T: o.Ts, Side: sideName(o.Buy), Px: roundSig(o.Px, 6), Usd: math.Round(o.Usd), Levels: o.Levels,
		ImpactBps: math.Round(math.Abs(o.Px1/o.Px0-1) * 1e4)}
	sign := -1.0
	if o.Buy {
		sign = 1
	}
	after := func(d int64) float64 {
		j := sort.Search(len(os), func(j int) bool { return os[j].Ts > o.Ts+d })
		if j == 0 || j >= len(os) { // window ends before then
			return 0
		}
		return math.Round(sign * (os[j-1].Px1/o.Px - 1) * 1e4)
	}
	l.After1mBps, l.After5mBps = after(minuteMs), after(5*minuteMs)
	return l
}

// ---------- spot vs swap ----------

// CrossStats compares the two markets of one coin over the same window.
type CrossStats struct {
	SpotSharePct  float64 `json:"spotSharePct"` // spot taker volume / (spot + swap)
	SwapDeltaUsd  float64 `json:"swapDeltaUsd"`
	SpotDeltaUsd  float64 `json:"spotDeltaUsd"`
	Leader        string  `json:"leader"` // spot | swap | none: whose 5s returns lead the other's
	LeadLagSec    int     `json:"leadLagSec"`
	LeadCorr      float64 `json:"leadCorr"`
	ReverseCorr   float64 `json:"reverseCorr"`
	SameTimeCorr  float64 `json:"sameTimeCorr"`
	BasisBpsMean  float64 `json:"basisBpsMean"` // (spot / swap − 1), per-minute last prices
	BasisBpsFirst float64 `json:"basisBpsFirstHalf"`
	BasisBpsLast  float64 `json:"basisBpsSecondHalf"`
	SpotLambda    float64 `json:"spotLambdaBpsPer100k"`
	SwapLambda    float64 `json:"swapLambdaBpsPer100k"`
}

func crossStats(sw, sp *TapeStats) *CrossStats {
	if sw == nil || sp == nil || len(sw.orders) == 0 || len(sp.orders) == 0 {
		return nil
	}
	c := &CrossStats{Leader: "none"}
	vol := func(os []TapeOrder) (v, d float64) {
		for _, o := range os {
			v += o.Usd
			if o.Buy {
				d += o.Usd
			} else {
				d -= o.Usd
			}
		}
		return
	}
	vw, dw := vol(sw.orders)
	vs, ds := vol(sp.orders)
	c.SpotSharePct = round2(vs / (vs + vw) * 100)
	c.SwapDeltaUsd, c.SpotDeltaUsd = math.Round(dw), math.Round(ds)
	c.SpotLambda, _ = impactLambda(sp.orders)
	c.SwapLambda, _ = impactLambda(sw.orders)
	// lead-lag on 5 s last-price returns
	const step = 5000
	lo := max(sw.orders[0].Ts, sp.orders[0].Ts) / step * step
	hi := min(sw.orders[len(sw.orders)-1].Ts, sp.orders[len(sp.orders)-1].Ts)
	if hi-lo > 30*minuteMs {
		series := func(os []TapeOrder) []float64 {
			n := int((hi-lo)/step) + 1
			px := make([]float64, n)
			j := 0
			last := 0.0
			for i := range px {
				end := lo + int64(i+1)*step
				for j < len(os) && os[j].Ts < end {
					last = os[j].Px1
					j++
				}
				px[i] = last
			}
			r := make([]float64, n)
			for i := 1; i < n; i++ {
				if px[i] > 0 && px[i-1] > 0 {
					r[i] = math.Log(px[i] / px[i-1])
				}
			}
			return r
		}
		a, b := series(sp.orders), series(sw.orders)
		c.SameTimeCorr = round2(corrLag(a, b, 0))
		best, bestK := 0.0, 0
		for k := 1; k <= 6; k++ {
			if v := corrLag(a, b, k); v > best { // spot now vs swap later
				best, bestK = v, k
			}
			if v := corrLag(b, a, k); v > best {
				best, bestK = v, -k
			}
		}
		c.LeadCorr, c.LeadLagSec, c.Leader = round2(best), bestK*5, "none"
		if bestK > 0 {
			c.ReverseCorr = round2(corrLag(b, a, bestK))
		} else if bestK < 0 {
			c.ReverseCorr = round2(corrLag(a, b, -bestK))
		}
		if best >= 0.05 && best-c.ReverseCorr >= 0.03 {
			if bestK > 0 {
				c.Leader = "spot"
			} else {
				c.Leader = "swap"
			}
		}
		if c.LeadLagSec < 0 {
			c.LeadLagSec = -c.LeadLagSec
		}
	}
	// basis per minute
	last := func(os []TapeOrder) map[int64]float64 {
		m := map[int64]float64{}
		for _, o := range os {
			m[o.Ts/minuteMs] = o.Px1
		}
		return m
	}
	ms, mw := last(sp.orders), last(sw.orders)
	var keys []int64
	for k := range ms {
		if _, ok := mw[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if len(keys) > 0 {
		var all, a1, a2 float64
		h := len(keys) / 2
		for i, k := range keys {
			v := (ms[k]/mw[k] - 1) * 1e4
			all += v
			if i < h {
				a1 += v
			} else {
				a2 += v
			}
		}
		c.BasisBpsMean = round2(all / float64(len(keys)))
		if h > 0 {
			c.BasisBpsFirst = round2(a1 / float64(h))
		}
		c.BasisBpsLast = round2(a2 / float64(len(keys)-h))
	}
	return c
}

// corrLag: correlation of a[t] with b[t+k].
func corrLag(a, b []float64, k int) float64 {
	n := min(len(a), len(b)) - k
	if n < 30 {
		return 0
	}
	var ma, mb float64
	for i := 0; i < n; i++ {
		ma += a[i]
		mb += b[i+k]
	}
	ma, mb = ma/float64(n), mb/float64(n)
	var sab, saa, sbb float64
	for i := 0; i < n; i++ {
		x, y := a[i]-ma, b[i+k]-mb
		sab += x * y
		saa += x * x
		sbb += y * y
	}
	if saa == 0 || sbb == 0 {
		return 0
	}
	return sab / math.Sqrt(saa*sbb)
}

// ---------- engine entry points ----------

// TradeAnalysis is analyze_trades' answer.
type TradeAnalysis struct {
	From  int64       `json:"from"`
	To    int64       `json:"to"`
	Swap  *TapeStats  `json:"swap,omitempty"`
	Spot  *TapeStats  `json:"spot,omitempty"`
	Cross *CrossStats `json:"cross,omitempty"`
}

// AnalyzeTrades loads and analyses the swap and/or spot tape ("" = skip that market).
func (e *Engine) AnalyzeTrades(ctx context.Context, swapID, spotID string, q TapeQuery, fetch bool) (*TradeAnalysis, error) {
	out := &TradeAnalysis{From: q.From, To: q.To}
	type res struct {
		s   *TapeStats
		err error
	}
	load := func(id string) res {
		if id == "" {
			return res{}
		}
		d, err := e.LoadTape(ctx, id, q.From, q.To, fetch)
		if err != nil {
			return res{err: err}
		}
		s := TapeStatsFor(d, q)
		roundSummary(s.Summary)
		return res{s: s}
	}
	ch := make(chan res, 1)
	go func() { ch <- load(spotID) }()
	sw := load(swapID)
	sp := <-ch
	if sw.err != nil {
		return nil, sw.err
	}
	if sp.err != nil {
		return nil, sp.err
	}
	out.Swap, out.Spot = sw.s, sp.s
	if q.on("cross") {
		out.Cross = crossStats(sw.s, sp.s)
	}
	return out, nil
}

// TapeOrdersResp is get_trades' answer.
type TapeOrdersResp struct {
	InstID   string       `json:"instId"`
	Market   string       `json:"market"`
	Coverage TapeCoverage `json:"coverage"`
	Total    int          `json:"total"` // orders matching the filter (rows may be fewer: limit)
	Cols     []string     `json:"cols"`
	Rows     [][6]float64 `json:"rows"`
}

func (e *Engine) TapeOrders(ctx context.Context, id string, from, to int64, fetch bool, minUsd float64, side, limit int, newest bool) (*TapeOrdersResp, error) {
	d, err := e.LoadTape(ctx, id, from, to, fetch)
	if err != nil {
		return nil, err
	}
	r := &TapeOrdersResp{InstID: id, Market: d.Market, Coverage: d.Coverage, Cols: []string{"t", "side", "px", "usd", "qty", "levels"}}
	os := groupOrders(d.Trades, d.CtVal)
	var keep []TapeOrder
	for _, o := range os {
		if o.Usd >= minUsd && (side == 0 || (side > 0) == o.Buy) {
			keep = append(keep, o)
		}
	}
	r.Total = len(keep)
	if newest {
		for i, j := 0, len(keep)-1; i < j; i, j = i+1, j-1 {
			keep[i], keep[j] = keep[j], keep[i]
		}
	}
	for _, o := range keep[:min(len(keep), limit)] {
		s := -1.0
		if o.Buy {
			s = 1
		}
		r.Rows = append(r.Rows, [6]float64{float64(o.Ts), s, roundSig(o.Px, 8), math.Round(o.Usd*100) / 100, o.Qty, float64(o.Levels)})
	}
	return r, nil
}
