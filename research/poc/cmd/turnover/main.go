// turnover measures the rolling-12h USDT turnover universe from OKX public candles.
//
// For every eligible USDT linear swap it fetches the last 720 confirmed 1m candles
// (paginated, rate-limited), plus 144×5m and 13×1H candles, and reports:
//   - warm-up wall time and request count at the chosen rate budget
//   - how many instruments pass the turnover threshold (per instCategory)
//   - minute gaps (missing 1m bars) in the 720 window
//   - agreement of 5m / 1H / 24h-ticker approximations with the exact 1m window
//   - sanity check volCcyQuote ≈ volCcy × close
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"crypto-trader-go/research/poc/okx"
)

type row struct {
	InstID       string  `json:"instId"`
	Category     string  `json:"category"`
	Exact1m      float64 `json:"exact1mUSDT"`
	Bars1m       int     `json:"bars1m"`
	Gaps1m       int     `json:"gapMinutes"`
	ZeroVolBars  int     `json:"zeroVolBars"`
	Approx5m     float64 `json:"approx5mUSDT"`
	Approx1H     float64 `json:"approx1hAlignedUSDT"`
	Ticker24Half float64 `json:"ticker24hHalfUSDT"`
	QuoteErrMax  float64 `json:"quoteVsBaseTimesCloseMaxRelErr"`
	Err          string  `json:"err,omitempty"`
}

func main() {
	rate := flag.Int("rate", 20, "candle requests per 2s (OKX limit 40)")
	threshold := flag.Float64("threshold", 10_000_000, "USDT threshold")
	topN := flag.Int("top", 200, "top N")
	out := flag.String("out", "", "write JSON report here")
	workers := flag.Int("workers", 8, "concurrent workers")
	flag.Parse()

	ctx := context.Background()
	c := okx.NewClient()
	var ins []okx.Instrument
	must(c.Get(ctx, nil, "/public/instruments", url.Values{"instType": {"SWAP"}}, &ins))
	var tick []okx.Ticker
	must(c.Get(ctx, nil, "/market/tickers", url.Values{"instType": {"SWAP"}}, &tick))
	tk := map[string]okx.Ticker{}
	for _, t := range tick {
		tk[t.InstID] = t
	}
	el := okx.Eligible(ins, nil)
	fmt.Printf("eligible USDT linear non-premarket swaps: %d\n", len(el))

	// Shared as-of minute: last fully closed minute at start.
	asOf := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute) // open time of last closed bar
	windowStart := asOf.Add(-719 * time.Minute)

	lim := okx.NewLimiter(*rate, 2*time.Second)
	start := time.Now()
	rows := make([]row, len(el))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				rows[i] = measure(ctx, c, lim, el[i], tk[el[i].InstID], asOf, windowStart)
			}
		}()
	}
	for i := range el {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)

	// Reports
	fmt.Printf("asOf(bar open) %s  warm-up %.1fs  requests %d  errors %v\n", asOf.Format(time.RFC3339), elapsed.Seconds(), c.Requests, c.Errors)
	sel := func(get func(row) float64) map[string]bool {
		var rs []row
		for _, r := range rows {
			if r.Err == "" && get(r) > *threshold {
				rs = append(rs, r)
			}
		}
		sort.Slice(rs, func(i, j int) bool {
			if get(rs[i]) != get(rs[j]) {
				return get(rs[i]) > get(rs[j])
			}
			return rs[i].InstID < rs[j].InstID
		})
		m := map[string]bool{}
		for i, r := range rs {
			if i >= *topN {
				break
			}
			m[r.InstID] = true
		}
		return m
	}
	exact := sel(func(r row) float64 { return r.Exact1m })
	byCat := map[string]int{}
	for _, r := range rows {
		if exact[r.InstID] {
			byCat[r.Category]++
		}
	}
	fmt.Printf("exact 1m universe (> %.0f, top %d): %d  by instCategory %v\n", *threshold, *topN, len(exact), byCat)
	for name, get := range map[string]func(row) float64{
		"5m(144 bars)":       func(r row) float64 { return r.Approx5m },
		"1H aligned (12)":    func(r row) float64 { return r.Approx1H },
		"ticker 24h/2 (est)": func(r row) float64 { return r.Ticker24Half },
	} {
		s := sel(get)
		fp, fn := diff(s, exact), diff(exact, s)
		fmt.Printf("  %-18s size %3d  extra %v  missing %v\n", name, len(s), fp, fn)
	}
	gapInst, zeroInst, errs := 0, 0, 0
	maxQErr := 0.0
	for _, r := range rows {
		if r.Err != "" {
			errs++
			continue
		}
		if r.Gaps1m > 0 {
			gapInst++
		}
		if r.ZeroVolBars > 0 {
			zeroInst++
		}
		maxQErr = math.Max(maxQErr, r.QuoteErrMax)
	}
	fmt.Printf("instruments with missing 1m bars in window: %d, with zero-volume bars: %d, fetch errors: %d\n", gapInst, zeroInst, errs)
	fmt.Printf("max |volCcyQuote - volCcy*close|/volCcyQuote over bars: %.3f (expected small but nonzero: VWAP≠close)\n", maxQErr)
	// Near-threshold sensitivity
	near := 0
	for _, r := range rows {
		if r.Exact1m > *threshold*0.9 && r.Exact1m < *threshold*1.1 {
			near++
		}
	}
	fmt.Printf("instruments within ±10%% of threshold (membership churn candidates): %d\n", near)

	if *out != "" {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Exact1m > rows[j].Exact1m })
		b, _ := json.MarshalIndent(map[string]any{
			"asOfBarOpen": asOf, "warmupSeconds": elapsed.Seconds(), "requests": c.Requests,
			"ratePer2s": *rate, "threshold": *threshold, "universeSize": len(exact), "byCategory": byCat, "rows": rows,
		}, "", " ")
		must(os.WriteFile(*out, b, 0o644))
	}
}

func measure(ctx context.Context, c *okx.Client, lim *okx.Limiter, in okx.Instrument, t okx.Ticker, asOf, from time.Time) row {
	r := row{InstID: in.InstID, Category: in.InstCategory}
	r.Ticker24Half = okx.F(t.VolCcy24) * okx.F(t.Last) / 2
	// 1m: paginate backwards with `after` (returns records older than ts).
	after := asOf.Add(time.Minute).UnixMilli()
	seen := map[int64]okx.Candle{}
	for page := 0; page < 4; page++ {
		var raw [][]string
		q := url.Values{"instId": {in.InstID}, "bar": {"1m"}, "limit": {"300"}, "after": {strconv.FormatInt(after, 10)}}
		if err := c.Get(ctx, lim, "/market/candles", q, &raw); err != nil {
			r.Err = err.Error()
			return r
		}
		cs := okx.ParseCandles(raw)
		if len(cs) == 0 {
			break
		}
		for _, k := range cs {
			if k.Ts >= from.UnixMilli() && k.Ts <= asOf.UnixMilli() && k.Confirmed {
				seen[k.Ts] = k
			}
		}
		after = cs[len(cs)-1].Ts
		if after <= from.UnixMilli() {
			break
		}
	}
	for m := from; !m.After(asOf); m = m.Add(time.Minute) {
		k, ok := seen[m.UnixMilli()]
		if !ok {
			r.Gaps1m++
			continue
		}
		r.Bars1m++
		r.Exact1m += k.VolCcyQuote
		if k.VolCcyQuote == 0 {
			r.ZeroVolBars++
		} else {
			r.QuoteErrMax = math.Max(r.QuoteErrMax, math.Abs(k.VolCcyQuote-k.VolCcy*k.C)/k.VolCcyQuote)
		}
	}
	// 5m: 144 bars ending at the last closed 5m bar.
	var raw5 [][]string
	if err := c.Get(ctx, lim, "/market/candles", url.Values{"instId": {in.InstID}, "bar": {"5m"}, "limit": {"145"}}, &raw5); err == nil {
		n := 0
		for _, k := range okx.ParseCandles(raw5) {
			if k.Confirmed && n < 144 {
				r.Approx5m += k.VolCcyQuote
				n++
			}
		}
	}
	// 1H: last 12 closed hourly bars.
	var raw1h [][]string
	if err := c.Get(ctx, lim, "/market/candles", url.Values{"instId": {in.InstID}, "bar": {"1H"}, "limit": {"13"}}, &raw1h); err == nil {
		n := 0
		for _, k := range okx.ParseCandles(raw1h) {
			if k.Confirmed && n < 12 {
				r.Approx1H += k.VolCcyQuote
				n++
			}
		}
	}
	return r
}

func diff(a, b map[string]bool) []string {
	var d []string
	for k := range a {
		if !b[k] {
			d = append(d, k)
		}
	}
	sort.Strings(d)
	return d
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
