// factorstudy is an EXPLORATORY falsification check of the blueprint's entry-core-v0 idea
// ("price trend + coin-OI expansion + volume surprise → directional evidence").
//
// Data (OKX public, ~60 days, 1H): candles for all eligible USDT swaps, coin-denominated OI
// history (rubik, 1H, ~60d depth) and funding history for instruments that were ever in the
// point-in-time universe (rolling 12 closed 1H bars' quote turnover > threshold).
//
// At every hour t (features use bars closed ≤ t) it computes per-instrument factors and the
// forward return over H hours, then reports the cross-sectional Spearman rank IC per factor,
// sampled every H hours (non-overlapping), split into the first 2/3 and last 1/3 of the period,
// plus a gross/net top-minus-bottom quintile spread.
//
// Known biases (reported, not fixed): only currently-listed instruments (delisted coins are
// missing → survivorship), close-to-close fills, one regime / ~60 days.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"crypto-trader-go/research/poc/okx"
)

type series struct {
	ts       []int64
	close    []float64
	quote    []float64
	oiCcy    map[int64]float64
	funding  []fundPt
	category string
}

type fundPt struct {
	ts   int64
	rate float64
}

var (
	cacheDir   string
	stamp      string // cache key: data end hour, so reruns reuse one snapshot
	days       = flag.Int("days", 51, "history days (1H OI depth is ~60d)")
	horizon    = flag.Int("h", 4, "forward horizon in hours")
	threshold  = flag.Float64("threshold", 10_000_000, "point-in-time 12h USDT turnover threshold")
	costBps    = flag.Float64("cost", 12, "round-trip cost in bps for net spread (2×taker 5bps + slippage)")
	outPath    = flag.String("out", "", "json report")
	endFlag    = flag.String("end", "", "reuse a cached snapshot: last bar open hour UTC, e.g. 20260926T15")
	cryptoOnly = flag.Bool("crypto", true, "only instCategory=1 (crypto) in the study universe")
)

func main() {
	flag.StringVar(&cacheDir, "cache", "results/cache", "raw response cache dir")
	flag.Parse()
	os.MkdirAll(cacheDir, 0o755)
	ctx := context.Background()
	c := okx.NewClient()
	var ins []okx.Instrument
	must(c.Get(ctx, nil, "/public/instruments", url.Values{"instType": {"SWAP"}}, &ins))
	el := okx.Eligible(ins, nil)

	end := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour) // last closed 1H bar open
	if *endFlag != "" {
		t, err := time.Parse("20060102T15", *endFlag)
		must(err)
		end = t
	}
	stamp = end.Format("20060102T15")
	start := end.Add(-time.Duration(*days) * 24 * time.Hour)
	fmt.Printf("period %s → %s, %d instruments\n", start.Format(time.RFC3339), end.Format(time.RFC3339), len(el))

	// 1) 1H candles for everyone (market/candles 40/2s; use 30/2s)
	candLim := okx.NewLimiter(30, 2*time.Second)
	data := map[string]*series{}
	var mu sync.Mutex
	parallel(el, 6, func(in okx.Instrument) {
		s := fetchCandles(ctx, c, candLim, in.InstID, start, end)
		s.category = in.InstCategory
		mu.Lock()
		data[in.InstID] = s
		mu.Unlock()
	})
	fmt.Printf("candles fetched, requests so far %d, errors %v\n", c.Requests, c.Errors)

	// 2) point-in-time universe size per hour (all categories) + which crypto ever qualified
	hours := []int64{}
	for t := start; !t.After(end); t = t.Add(time.Hour) {
		hours = append(hours, t.UnixMilli())
	}
	idx := map[string]map[int64]int{}
	for id, s := range data {
		m := map[int64]int{}
		for i, ts := range s.ts {
			m[ts] = i
		}
		idx[id] = m
	}
	turn12 := func(id string, ts int64) (float64, bool) {
		s := data[id]
		i, ok := idx[id][ts]
		if !ok || i < 11 {
			return 0, false
		}
		sum := 0.0
		for k := i - 11; k <= i; k++ {
			if s.ts[k] != ts-int64(i-k)*3_600_000 {
				return 0, false
			}
			sum += s.quote[k]
		}
		return sum, true
	}
	ever := map[string]bool{}
	var sizes []int
	sizeByWeekday := map[string][]int{}
	for _, ts := range hours {
		n := 0
		for id := range data {
			if v, ok := turn12(id, ts); ok && v > *threshold {
				n++
				if !*cryptoOnly || data[id].category == "1" {
					ever[id] = true
				}
			}
		}
		sizes = append(sizes, n)
		wd := time.UnixMilli(ts).UTC().Weekday().String()[:3]
		sizeByWeekday[wd] = append(sizeByWeekday[wd], n)
	}
	sortedSizes := append([]int(nil), sizes...)
	sort.Ints(sortedSizes)
	fmt.Printf("point-in-time universe size (>%.0f, all categories) over %d hours: min %d p50 %d p90 %d max %d\n",
		*threshold, len(sizes), sortedSizes[0], sortedSizes[len(sizes)/2], sortedSizes[len(sizes)*9/10], sortedSizes[len(sizes)-1])
	wdSummary := map[string]int{}
	for wd, xs := range sizeByWeekday {
		sort.Ints(xs)
		wdSummary[wd] = xs[len(xs)/2]
	}
	fmt.Printf("median universe size by weekday (UTC): %v\n", wdSummary)
	fmt.Printf("crypto instruments ever in universe: %d\n", len(ever))

	// 3) OI + funding history for those (rubik 5/2s, funding history 10/2s)
	oiLim := okx.NewLimiter(4, 2*time.Second)
	fLim := okx.NewLimiter(8, 2*time.Second)
	var everList []okx.Instrument
	for _, in := range el {
		if ever[in.InstID] {
			everList = append(everList, in)
		}
	}
	parallel(everList, 4, func(in okx.Instrument) {
		oi := fetchOI(ctx, c, oiLim, in.InstID, start)
		f := fetchFunding(ctx, c, fLim, in.InstID, start)
		mu.Lock()
		data[in.InstID].oiCcy = oi
		data[in.InstID].funding = f
		mu.Unlock()
	})
	fmt.Printf("OI/funding fetched, total requests %d, errors %v\n", c.Requests, c.Errors)

	// 4) factors & IC
	H := *horizon
	type obs struct {
		f   map[string]float64
		fwd float64
	}
	factorNames := []string{"T_trend4h", "T_trend24h", "O_oiChg4h", "V_volSurprise4h", "C_oi", "C_vol", "F_funding", "core_v0", "R_btcResid4h"}
	var btc *series = data["BTC-USDT-SWAP"]
	icSeries := map[string][]float64{}
	icPeriod := map[string][2][]float64{}
	spreadGross, spreadNet := [2][]float64{}, [2][]float64{}
	nCross := []int{}
	split := hours[len(hours)*2/3]
	for hi := 48; hi+H < len(hours); hi += H { // non-overlapping samples
		ts := hours[hi]
		var cross []obs
		for id := range ever {
			if v, ok := turn12(id, ts); !ok || v <= *threshold {
				continue // point-in-time universe
			}
			s := data[id]
			i := idx[id][ts]
			j, ok := idx[id][ts+int64(H)*3_600_000]
			if !ok || i < 48+168 {
				continue
			}
			f, ok := features(s, i, btc, idx["BTC-USDT-SWAP"])
			if !ok {
				continue
			}
			cross = append(cross, obs{f: f, fwd: math.Log(s.close[j] / s.close[i])})
		}
		if len(cross) < 10 {
			continue
		}
		nCross = append(nCross, len(cross))
		p := 0
		if ts >= split {
			p = 1
		}
		fwd := make([]float64, len(cross))
		for k, o := range cross {
			fwd[k] = o.fwd
		}
		for _, name := range factorNames {
			x := make([]float64, len(cross))
			for k, o := range cross {
				x[k] = o.f[name]
			}
			ic := spearman(x, fwd)
			if math.IsNaN(ic) {
				continue
			}
			icSeries[name] = append(icSeries[name], ic)
			pp := icPeriod[name]
			pp[p] = append(pp[p], ic)
			icPeriod[name] = pp
		}
		// quintile spread on core_v0 (long top, short bottom)
		sort.Slice(cross, func(a, b int) bool { return cross[a].f["core_v0"] > cross[b].f["core_v0"] })
		q := len(cross) / 5
		top, bot := 0.0, 0.0
		for k := 0; k < q; k++ {
			top += cross[k].fwd
			bot += cross[len(cross)-1-k].fwd
		}
		g := (top - bot) / float64(q) / 2 // per-leg average, long-short half each
		spreadGross[p] = append(spreadGross[p], g)
		spreadNet[p] = append(spreadNet[p], g-*costBps/10000)
	}
	sort.Ints(nCross)
	fmt.Printf("\nrank IC vs forward %dh log return (non-overlapping, %d cross-sections, median %d names)\n", H, len(nCross), nCross[len(nCross)/2])
	fmt.Printf("%-16s %8s %7s %7s | %8s %8s\n", "factor", "meanIC", "t", "hit%", "IC first", "IC last")
	report := map[string]any{}
	for _, name := range factorNames {
		m, t, hit := meanT(icSeries[name])
		a, _, _ := meanT(icPeriod[name][0])
		b, _, _ := meanT(icPeriod[name][1])
		fmt.Printf("%-16s %8.4f %7.2f %6.1f%% | %8.4f %8.4f\n", name, m, t, hit*100, a, b)
		report[name] = map[string]float64{"meanIC": m, "t": t, "hitRate": hit, "icFirst2of3": a, "icLast1of3": b}
	}
	for p, lbl := range []string{"first 2/3", "last 1/3"} {
		g, gt, _ := meanT(spreadGross[p])
		n, _, _ := meanT(spreadNet[p])
		fmt.Printf("core_v0 top-vs-bottom quintile per %dh (%s): gross %.2fbps (t %.2f)  net of %.0fbps %.2fbps\n", H, lbl, g*1e4, gt, *costBps, n*1e4)
		report["spread_"+strconv.Itoa(p)] = map[string]float64{"grossBps": g * 1e4, "t": gt, "netBps": n * 1e4}
	}
	if *outPath != "" {
		b, _ := json.MarshalIndent(map[string]any{"start": start, "end": end, "horizonH": H, "threshold": *threshold,
			"universeSize":            map[string]int{"min": sortedSizes[0], "p50": sortedSizes[len(sizes)/2], "p90": sortedSizes[len(sizes)*9/10], "max": sortedSizes[len(sizes)-1]},
			"universeByWeekdayMedian": wdSummary, "everInUniverse": len(ever), "ic": report, "crossSections": len(nCross)}, "", " ")
		must(os.WriteFile(*outPath, b, 0o644))
	}
}

// features uses only data at bar index i and earlier (bar i closed at ts+1h ≤ decision time).
func features(s *series, i int, btc *series, btcIdx map[int64]int) (map[string]float64, bool) {
	ret := func(k int) float64 { return math.Log(s.close[i] / s.close[i-k]) }
	// realized hourly vol over past 48h
	var r []float64
	for k := i - 47; k <= i; k++ {
		r = append(r, math.Log(s.close[k]/s.close[k-1]))
	}
	sd := stdev(r)
	if sd == 0 {
		return nil, false
	}
	T4 := ret(4) / (sd * 2)
	T24 := ret(24) / (sd * math.Sqrt(24))
	// OI change (coin-denominated) over 4h
	oiNow, ok1 := s.oiCcy[s.ts[i]]
	oiPrev, ok2 := s.oiCcy[s.ts[i-4]]
	if !ok1 || !ok2 || oiNow <= 0 || oiPrev <= 0 {
		return nil, false
	}
	O := math.Log(oiNow / oiPrev)
	// volume surprise: last 4h quote vs median 4h quote over the past 7 days
	sum4 := func(e int) float64 {
		x := 0.0
		for k := e - 3; k <= e; k++ {
			x += s.quote[k]
		}
		return x
	}
	var base []float64
	for e := i - 4; e >= i-168; e -= 4 {
		base = append(base, sum4(e))
	}
	V := math.Log1p(sum4(i)) - math.Log1p(median(base))
	// latest settled funding known at decision time
	F := math.NaN()
	for _, p := range s.funding {
		if p.ts <= s.ts[i]+3_600_000 {
			F = p.rate
		}
	}
	if math.IsNaN(F) {
		F = 0
	}
	// BTC-residual 4h return (beta=1 proxy)
	R := math.NaN()
	if bi, ok := btcIdx[s.ts[i]]; ok && bi >= 4 {
		R = ret(4) - math.Log(btc.close[bi]/btc.close[bi-4])
	}
	if math.IsNaN(R) {
		R = 0
	}
	sq := func(x float64) float64 { return math.Tanh(x / 2) } // squash to (-1,1)
	Tn, On, Vn := sq(T4), sq(O/0.05), sq(V)
	Coi := sign(Tn) * math.Max(0, On)
	Cvol := Tn * math.Max(0, Vn)
	core := 0.45*Tn + 0.30*Coi + 0.25*Cvol // blueprint entry-core-v0 coreEvidence (d=+1), penalties omitted
	return map[string]float64{"T_trend4h": T4, "T_trend24h": T24, "O_oiChg4h": O, "V_volSurprise4h": V, "C_oi": Coi, "C_vol": Cvol,
		"F_funding": F, "core_v0": core, "R_btcResid4h": R}, true
}

// ---- fetchers with on-disk cache ----

func cached(name string, fetch func() any, out any) {
	p := filepath.Join(cacheDir, name+".json")
	if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, out) == nil {
		return
	}
	v := fetch()
	b, _ := json.Marshal(v)
	os.WriteFile(p, b, 0o644)
	json.Unmarshal(b, out)
}

func fetchCandles(ctx context.Context, c *okx.Client, lim *okx.Limiter, id string, start, end time.Time) *series {
	var raw [][]string
	cached("c1h_"+id+"_"+stamp, func() any {
		var all [][]string
		after := end.Add(time.Hour).UnixMilli()
		for page := 0; page < 6; page++ {
			var r [][]string
			q := url.Values{"instId": {id}, "bar": {"1H"}, "limit": {"300"}, "after": {strconv.FormatInt(after, 10)}}
			if c.Get(ctx, lim, "/market/candles", q, &r) != nil || len(r) == 0 {
				break
			}
			all = append(all, r...)
			after, _ = strconv.ParseInt(r[len(r)-1][0], 10, 64)
			if after <= start.UnixMilli()-8*24*3_600_000 { // extra 8 days warm-up for 7d baselines
				break
			}
		}
		return all
	}, &raw)
	cs := okx.ParseCandles(raw)
	sort.Slice(cs, func(a, b int) bool { return cs[a].Ts < cs[b].Ts })
	s := &series{oiCcy: map[int64]float64{}}
	for _, k := range cs {
		if !k.Confirmed || (len(s.ts) > 0 && s.ts[len(s.ts)-1] == k.Ts) {
			continue
		}
		s.ts = append(s.ts, k.Ts)
		s.close = append(s.close, k.C)
		s.quote = append(s.quote, k.VolCcyQuote)
	}
	return s
}

func fetchOI(ctx context.Context, c *okx.Client, lim *okx.Limiter, id string, start time.Time) map[int64]float64 {
	var raw [][]string
	cached("oi1h_"+id+"_"+stamp, func() any {
		var all [][]string
		end := ""
		for page := 0; page < 16; page++ {
			var r [][]string
			q := url.Values{"instId": {id}, "period": {"1H"}, "limit": {"100"}}
			if end != "" {
				q.Set("end", end)
			}
			if c.Get(ctx, lim, "/rubik/stat/contracts/open-interest-history", q, &r) != nil || len(r) == 0 {
				break
			}
			all = append(all, r...)
			end = r[len(r)-1][0]
		}
		return all
	}, &raw)
	m := map[int64]float64{}
	for _, r := range raw { // [ts, oi(contracts), oiCcy, oiUsd]
		ts, _ := strconv.ParseInt(r[0], 10, 64)
		// Conservative alignment: snapshot stamped ts is only used at decisions after the bar opening at ts
		// has closed (ts+1h), so no look-ahead even if the stamp marks the start of its hour.
		m[ts] = okx.F(r[2])
	}
	return m
}

func fetchFunding(ctx context.Context, c *okx.Client, lim *okx.Limiter, id string, start time.Time) []fundPt {
	var raw []struct {
		FundingTime  string `json:"fundingTime"`
		RealizedRate string `json:"realizedRate"`
		FundingRate  string `json:"fundingRate"`
	}
	cached("fund_"+id+"_"+stamp, func() any {
		var all []map[string]string
		after := ""
		for page := 0; page < 6; page++ {
			var r []map[string]string
			q := url.Values{"instId": {id}, "limit": {"100"}}
			if after != "" {
				q.Set("after", after)
			}
			if c.Get(ctx, lim, "/public/funding-rate-history", q, &r) != nil || len(r) == 0 {
				break
			}
			all = append(all, r...)
			after = r[len(r)-1]["fundingTime"]
			if ts, _ := strconv.ParseInt(after, 10, 64); ts < start.UnixMilli() {
				break
			}
		}
		return all
	}, &raw)
	var out []fundPt
	for _, r := range raw {
		ts, _ := strconv.ParseInt(r.FundingTime, 10, 64)
		out = append(out, fundPt{ts, okx.F(r.RealizedRate)})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ts < out[b].ts })
	return out
}

// ---- stats ----

func parallel(xs []okx.Instrument, n int, f func(okx.Instrument)) {
	ch := make(chan okx.Instrument)
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for x := range ch {
				f(x)
			}
		}()
	}
	for _, x := range xs {
		ch <- x
	}
	close(ch)
	wg.Wait()
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
	mx, my := mean(rx), mean(ry)
	var sxy, sxx, syy float64
	for i := range rx {
		sxy += (rx[i] - mx) * (ry[i] - my)
		sxx += (rx[i] - mx) * (rx[i] - mx)
		syy += (ry[i] - my) * (ry[i] - my)
	}
	return sxy / math.Sqrt(sxx*syy)
}

func meanT(x []float64) (m, t, hit float64) {
	if len(x) < 2 {
		return math.NaN(), math.NaN(), math.NaN()
	}
	m = mean(x)
	t = m / (stdev(x) / math.Sqrt(float64(len(x))))
	for _, v := range x {
		if v > 0 {
			hit++
		}
	}
	return m, t, hit / float64(len(x))
}

func mean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}
func stdev(x []float64) float64 {
	m := mean(x)
	s := 0.0
	for _, v := range x {
		s += (v - m) * (v - m)
	}
	return math.Sqrt(s / float64(len(x)-1))
}
func median(x []float64) float64 {
	y := append([]float64(nil), x...)
	sort.Float64s(y)
	return y[len(y)/2]
}
func sign(x float64) float64 {
	if x > 0 {
		return 1
	}
	if x < 0 {
		return -1
	}
	return 0
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
