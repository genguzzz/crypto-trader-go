package market

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/store"
)

// ---------- liquidations: recorded orders (OKX public REST) and an estimated liquidation map ----------
//
// Recorded: OKX /public/liquidation-orders returns every filled liquidation of a perpetual for
// about the last 24 hours (the WS channel is throttled to one order per contract per second, so we
// poll REST instead). The order-flow set (active pool ∪ watchlist ∪ viewed) is polled every minute;
// an instrument opened on the detail page is synced on request. Stored raw, aggregated per bar on
// query, kept liqKeep.
//
// Map (estimate, not exchange data): positions opened in each step (OI increase, both sides) are
// spread over leverage tiers and placed at their approximate liquidation prices; OI decreases
// shrink every open level proportionally; a level is removed once a later candle trades through
// it. Steps are 5m (5m candles + per-minute OI, including the forming bar and the latest OI
// sample) wherever that data is unbroken, hourly before. Amounts are kept per tier so the chart
// can color them by leverage. What remains is where the model expects liquidations to cluster if
// price moves there. Same kind of model as CoinGlass's liquidation map; its leverage mix, inputs
// (it aggregates exchanges and weighs in volume) and scale differ, so levels agree in shape and
// position, not in amount.

const (
	liqEvery      = time.Minute
	liqKeep       = 30 * 24 * time.Hour
	liqPages      = 30                     // × 100 orders; OKX keeps ~24h (≈2–3k orders for BTC on a normal day)
	liqFresh      = 8 * time.Second        // an open chart polls every 10 s: each poll picks up new orders
	liqWait       = 500 * time.Millisecond // longest a query waits for a sync before serving stored data
	liqMMR        = 0.005                  // maintenance margin rate assumed by the map
	liqMapMaxDays = 30
	liqMapTTL     = 5 * time.Second // map recomputed at most this often per instrument and window
)

// Leverage tiers of the map model (weights renormalised over the tiers the contract allows).
var liqTiers = []struct{ Lever, W float64 }{{5, 0.10}, {10, 0.25}, {25, 0.30}, {50, 0.20}, {100, 0.15}}

var liqSync = struct {
	sync.Mutex
	busy    map[string]chan struct{}
	at      map[string]time.Time
	covered map[string]int64 // earliest time the fetch history reaches (instrument with no stored orders)
	lever   map[string]float64
	leverAt time.Time
}{busy: map[string]chan struct{}{}, at: map[string]time.Time{}, covered: map[string]int64{}}

// syncLiq fetches liquidations newer than the newest stored one. Concurrent calls share one fetch.
func (e *Engine) syncLiq(ctx context.Context, id string) error {
	liqSync.Lock()
	if ch, ok := liqSync.busy[id]; ok {
		liqSync.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return nil
	}
	if time.Since(liqSync.at[id]) < liqFresh {
		liqSync.Unlock()
		return nil
	}
	ch := make(chan struct{})
	liqSync.busy[id] = ch
	liqSync.Unlock()
	var err error
	defer func() {
		liqSync.Lock()
		delete(liqSync.busy, id)
		if err == nil {
			liqSync.at[id] = time.Now()
		}
		liqSync.Unlock()
		close(ch)
	}()

	in := e.instFor(id)
	if in == nil || in.CtVal <= 0 {
		return fmt.Errorf("unknown instrument")
	}
	newest, _ := e.st.LiqSpan(id)
	fam := strings.TrimSuffix(id, "-SWAP")
	var rows []store.Liq
	var after, oldest int64
	exhausted := false
	for p := 0; p < liqPages; p++ {
		xs, ferr := e.rest.LiquidationOrders(ctx, fam, after, 100)
		if ferr != nil {
			err = ferr
			break
		}
		if len(xs) == 0 {
			exhausted = true
			break
		}
		pageOld := xs[0].Ts
		for _, x := range xs {
			pageOld = min(pageOld, x.Ts)
			if x.Ts >= newest && x.Px > 0 && x.Sz > 0 {
				rows = append(rows, store.Liq{Ts: x.Ts, Long: x.PosSide == "long", Px: x.Px, Sz: x.Sz, USD: x.Px * x.Sz * in.CtVal})
			}
		}
		oldest = pageOld
		if newest > 0 && pageOld < newest {
			break
		}
		// `after` is exclusive: step back to include the rest of the oldest millisecond (duplicates
		// are ignored), unless a whole page shares it
		next := pageOld + 1
		if next == after {
			next = pageOld
		}
		after = next
	}
	if len(rows) > 0 {
		if perr := e.st.PutLiqs(id, rows); perr != nil {
			return perr
		}
	}
	if newest == 0 && err == nil { // first fetch: history starts where OKX's does
		cov := oldest
		if exhausted || cov == 0 {
			cov = time.Now().Add(-24 * time.Hour).UnixMilli()
			if oldest > 0 {
				cov = min(cov, oldest)
			}
		}
		liqSync.Lock()
		liqSync.covered[id] = cov
		liqSync.Unlock()
	}
	return err
}

// liqLoop polls the order-flow set every minute and prunes old orders hourly.
func (e *Engine) liqLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(90 * time.Second): // let bootstrap use the REST budget first
	}
	t := time.NewTicker(liqEvery)
	defer t.Stop()
	var pruned time.Time
	for {
		now := time.Now()
		e.mu.RLock()
		set := e.flowIDs(now.UnixMilli())
		e.mu.RUnlock()
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		fails := 0
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := e.syncLiq(c, id); err != nil {
				fails++
			}
			cancel()
		}
		if fails > 0 {
			log.Printf("liquidations: %d/%d instruments failed", fails, len(ids))
		}
		if time.Since(pruned) > time.Hour {
			if n, err := e.st.PruneLiqs(time.Now().Add(-liqKeep).UnixMilli()); err == nil && n > 0 {
				log.Printf("liquidations: pruned %d", n)
			}
			pruned = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---------- queries ----------

type LiqBar struct {
	T     int64   `json:"t"`
	Long  float64 `json:"long"`  // USDT of long positions liquidated
	Short float64 `json:"short"` // USDT of short positions liquidated
	NL    int     `json:"nl"`
	NS    int     `json:"ns"`
}

type LiqEvent struct {
	T    int64   `json:"t"`
	Long bool    `json:"long"`
	Px   float64 `json:"px"`
	USD  float64 `json:"usd"`
}

type LiqResp struct {
	Bars   []LiqBar   `json:"bars"`
	Since  int64      `json:"since"`  // earliest covered time: bars before it are unknown, not zero
	Recent []LiqEvent `json:"recent"` // newest first
	Err    string     `json:"err,omitempty"`
	// Syncing: a fetch from OKX is still running, so Bars may lack the newest (or, on first open, all) orders.
	Syncing bool `json:"syncing,omitempty"`
}

// Liquidations returns liquidation totals per bar (UTC-aligned like candles) plus recent orders.
func (e *Engine) Liquidations(ctx context.Context, id, bar string, limit, recent int) (LiqResp, error) {
	step, ok := barMs[bar]
	if !ok {
		return LiqResp{}, fmt.Errorf("bar must be one of 1m,5m,15m,1H,4H")
	}
	if e.instFor(id) == nil {
		return LiqResp{}, fmt.Errorf("unknown instrument")
	}
	var resp LiqResp
	// Sync in the background and wait only briefly: a cold instrument pages through up to liqPages
	// REST calls (seconds), which used to hold up the whole chart. Stored orders are served at once;
	// the chart's periodic refresh picks up the rest.
	done := make(chan error, 1)
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- e.syncLiq(c, id)
	}()
	select {
	case err := <-done:
		if err != nil {
			resp.Err = err.Error() // serve what is stored
		}
	case <-time.After(liqWait):
		resp.Syncing = true
	case <-ctx.Done():
	}
	limit = max(10, min(limit, 3000))
	now := time.Now().UnixMilli()
	from := now/step*step - int64(limit-1)*step
	xs, err := e.st.Liqs(id, from, now)
	if err != nil {
		return LiqResp{}, err
	}
	_, oldest := e.st.LiqSpan(id)
	liqSync.Lock()
	cov := liqSync.covered[id]
	liqSync.Unlock()
	resp.Since = oldest
	if cov > 0 && (oldest == 0 || cov < oldest) {
		resp.Since = cov
	}
	buckets := map[int64]*LiqBar{}
	for _, x := range xs {
		b := x.Ts / step * step
		lb := buckets[b]
		if lb == nil {
			lb = &LiqBar{T: b}
			buckets[b] = lb
		}
		if x.Long {
			lb.Long += x.USD
			lb.NL++
		} else {
			lb.Short += x.USD
			lb.NS++
		}
	}
	resp.Bars = make([]LiqBar, 0, len(buckets))
	for _, b := range buckets {
		resp.Bars = append(resp.Bars, *b)
	}
	sort.Slice(resp.Bars, func(i, j int) bool { return resp.Bars[i].T < resp.Bars[j].T })
	if recent > 0 {
		resp.Recent = []LiqEvent{}
		for i := len(xs) - 1; i >= 0 && len(resp.Recent) < recent; i-- {
			resp.Recent = append(resp.Recent, LiqEvent{T: xs[i].Ts, Long: xs[i].Long, Px: xs[i].Px, USD: xs[i].USD})
		}
	}
	return resp, nil
}

type LiqBin struct {
	P       float64   `json:"p"`       // bin low price
	Long    float64   `json:"long"`    // USDT of long positions estimated to liquidate in this bin
	Short   float64   `json:"short"`   // same, short positions
	LongBy  []float64 `json:"longBy"`  // Long split by leverage tier (same order as LiqMapResp.Levers)
	ShortBy []float64 `json:"shortBy"` // Short split by leverage tier
}

type LiqMapResp struct {
	Px      float64   `json:"px"`   // last price
	Step    float64   `json:"step"` // bin size
	From    int64     `json:"from"`
	Days    int       `json:"days"`
	Lever   float64   `json:"lever"`   // contract max leverage (tiers above it are dropped)
	Levers  []float64 `json:"levers"`  // tiers used
	Weights []float64 `json:"weights"` // assumed share of new positions per tier (sums to 1)
	// Fine: start of the part modelled on 5m candles and per-minute OI (0 = hourly only). Before
	// it the model steps hourly, placing a whole hour's new positions at one price.
	Fine int64    `json:"fine"`
	At   int64    `json:"at"`   // computed at (ms)
	Bins []LiqBin `json:"bins"` // ascending by price, non-empty only
}

var liqMapCache = struct {
	sync.Mutex
	m map[string]liqMapHit
}{m: map[string]liqMapHit{}}

type liqMapHit struct {
	at   time.Time
	resp LiqMapResp
}

// LiqMap estimates where open positions would be liquidated (see the header comment). Results
// are cached for liqMapTTL so every open chart can poll it.
func (e *Engine) LiqMap(ctx context.Context, id string, days int) (LiqMapResp, error) {
	days = max(1, min(days, liqMapMaxDays))
	key := fmt.Sprintf("%s|%d", id, days)
	liqMapCache.Lock()
	hit, ok := liqMapCache.m[key]
	liqMapCache.Unlock()
	if ok && time.Since(hit.at) < liqMapTTL {
		return hit.resp, nil
	}
	resp, err := e.liqMap(ctx, id, days)
	if err != nil {
		return resp, err
	}
	liqMapCache.Lock()
	for k, v := range liqMapCache.m {
		if time.Since(v.at) > time.Minute {
			delete(liqMapCache.m, k)
		}
	}
	liqMapCache.m[key] = liqMapHit{time.Now(), resp}
	liqMapCache.Unlock()
	return resp, nil
}

func (e *Engine) liqMap(ctx context.Context, id string, days int) (LiqMapResp, error) {
	hours := days * 24
	cs, err := e.Candles(id, "1H", hours+1)
	if err != nil {
		return LiqMapResp{}, err
	}
	if len(cs) < 2 {
		return LiqMapResp{}, fmt.Errorf("not enough hourly candles")
	}
	now := time.Now().UnixMilli()
	from := cs[0].T
	ois, err := e.st.OI(id, from-2*hourMs, now)
	if err != nil {
		return LiqMapResp{}, err
	}
	// the in-memory latest OI sample is newer than the stored per-minute one
	e.mu.RLock()
	if in := e.insts[id]; in != nil && in.oiNow.OiCcy > 0 && (len(ois) == 0 || in.oiNow.Ts > ois[len(ois)-1].Ts) {
		ois = append(ois, in.oiNow)
	}
	e.mu.RUnlock()
	fine, _ := e.Candles(id, "5m", min(3000, hours*12+1))
	steps, fineFrom := liqSteps(cs, fine, ois)
	lever := e.maxLever(ctx, id)
	tiers := liqTiersFor(lever)
	last := cs[len(cs)-1].C
	if n := len(fine); n > 0 && fine[n-1].T >= cs[len(cs)-1].T {
		last = fine[n-1].C
	}
	step := niceStep(last * 0.001)
	longs, shorts := estimateLiqLevels(steps, tiers, step)
	resp := LiqMapResp{Px: last, Step: step, From: from, Days: days, Lever: lever, Fine: fineFrom, At: now}
	for _, t := range tiers {
		resp.Levers = append(resp.Levers, t.Lever)
		resp.Weights = append(resp.Weights, t.W)
	}
	lim := int64(math.Ceil(0.5 / 0.001)) // ±50% of price
	merged := map[int64]*LiqBin{}
	add := func(k int64, vs []float64, long bool) {
		if math.Abs(float64(k)-last/step) > float64(lim) {
			return
		}
		var sum float64
		for _, v := range vs {
			sum += v
		}
		if sum < 1 {
			return
		}
		b := merged[k]
		if b == nil {
			b = &LiqBin{P: float64(k) * step, LongBy: make([]float64, len(tiers)), ShortBy: make([]float64, len(tiers))}
			merged[k] = b
		}
		for i, v := range vs {
			if long {
				b.LongBy[i] += v
				b.Long += v
			} else {
				b.ShortBy[i] += v
				b.Short += v
			}
		}
	}
	for k, v := range longs {
		add(k, v, true)
	}
	for k, v := range shorts {
		add(k, v, false)
	}
	resp.Bins = make([]LiqBin, 0, len(merged))
	for _, b := range merged {
		resp.Bins = append(resp.Bins, *b)
	}
	sort.Slice(resp.Bins, func(i, j int) bool { return resp.Bins[i].P < resp.Bins[j].P })
	return resp, nil
}

// liqStep is one candle of the map model with the OI (coins) at its last sample.
type liqStep struct {
	T       int64
	H, L, C float64
	OI      float64
	HasOI   bool
}

// liqSteps joins hourly and 5m candles (both ascending) with OI samples into the model's input:
// 5m steps over the recent stretch where 5m candles and per-minute OI are unbroken (fineFrom, on
// an hour boundary; 0 = none), hourly steps before it. The first step is the OI baseline.
func liqSteps(hourly, fine []Candle, ois []store.OISample) (steps []liqStep, fineFrom int64) {
	const fStep = 5 * minuteMs
	oiH, oiF := map[int64]float64{}, map[int64]float64{} // bucket open → OI at its last sample
	for _, o := range ois {
		if o.OiCcy > 0 {
			oiH[o.Ts/hourMs*hourMs] = o.OiCcy
			oiF[o.Ts/fStep*fStep] = o.OiCcy
		}
	}
	// walk back from the newest 5m candle while candles are contiguous and each has an OI sample
	start := len(fine)
	for i := len(fine) - 1; i >= 0; i-- {
		if _, ok := oiF[fine[i].T]; !ok || (i < len(fine)-1 && fine[i+1].T-fine[i].T != fStep) {
			break
		}
		start = i
	}
	if start < len(fine) {
		fineFrom = (fine[start].T + hourMs - 1) / hourMs * hourMs
		if fineFrom <= hourly[0].T { // fine data spans the whole window: keep one hourly baseline
			fineFrom = hourly[0].T + hourMs
		}
		if fineFrom > fine[len(fine)-1].T {
			fineFrom = 0
		}
	}
	for _, c := range hourly {
		if fineFrom > 0 && c.T >= fineFrom {
			break
		}
		oi, ok := oiH[c.T]
		steps = append(steps, liqStep{T: c.T, H: c.H, L: c.L, C: c.C, OI: oi, HasOI: ok})
	}
	if fineFrom > 0 {
		for _, c := range fine {
			if c.T >= fineFrom {
				oi, ok := oiF[c.T]
				steps = append(steps, liqStep{T: c.T, H: c.H, L: c.L, C: c.C, OI: oi, HasOI: ok})
			}
		}
	}
	return steps, fineFrom
}

// maxLever is the contract's max leverage from the instrument list (refreshed every 6h; 50 if unknown).
func (e *Engine) maxLever(ctx context.Context, id string) float64 {
	liqSync.Lock()
	stale := liqSync.lever == nil || time.Since(liqSync.leverAt) > 6*time.Hour
	liqSync.Unlock()
	if stale {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		m, err := e.rest.MaxLeverage(c)
		cancel()
		liqSync.Lock()
		if err == nil && len(m) > 0 {
			liqSync.lever, liqSync.leverAt = m, time.Now()
		} else {
			liqSync.leverAt = time.Now().Add(-6*time.Hour + time.Minute) // retry in a minute
		}
		liqSync.Unlock()
	}
	liqSync.Lock()
	defer liqSync.Unlock()
	if v := liqSync.lever[id]; v > 0 {
		return v
	}
	return 50
}

type liqTier struct{ Lever, W float64 } // W normalised to sum 1

// liqTiersFor keeps the model's tiers the contract allows (≤ its max leverage), weights renormalised.
func liqTiersFor(lever float64) []liqTier {
	var out []liqTier
	var sum float64
	for _, t := range liqTiers {
		if t.Lever <= lever {
			out = append(out, liqTier{t.Lever, t.W})
			sum += t.W
		}
	}
	if len(out) == 0 {
		return []liqTier{{lever, 1}}
	}
	for i := range out {
		out[i].W /= sum
	}
	return out
}

// estimateLiqLevels runs the map model over its steps (ascending; the first is only the OI
// baseline): USDT still open per price bin (bin = floor(px/step)) and leverage tier, for long and
// short liquidations.
func estimateLiqLevels(steps []liqStep, tiers []liqTier, step float64) (longs, shorts map[int64][]float64) {
	longs, shorts = map[int64][]float64{}, map[int64][]float64{}
	if len(steps) == 0 {
		return
	}
	bin := func(p float64) int64 { return int64(math.Floor(p / step)) }
	put := func(m map[int64][]float64, k int64, i int, v float64) {
		if m[k] == nil {
			m[k] = make([]float64, len(tiers))
		}
		m[k][i] += v
	}
	scale := func(m map[int64][]float64, f float64) {
		for _, vs := range m {
			for i := range vs {
				vs[i] *= f
			}
		}
	}
	prevOI, havePrev := steps[0].OI, steps[0].HasOI && steps[0].OI > 0
	for _, c := range steps[1:] {
		// 1) levels this candle traded through are gone (liquidated, or the guess was wrong)
		lo, hi := bin(c.L), bin(c.H)
		for k := range longs {
			if k >= lo {
				delete(longs, k)
			}
		}
		for k := range shorts {
			if k <= hi {
				delete(shorts, k)
			}
		}
		// 2) OI change during the candle: opened positions (both sides) get levels, closed ones
		// shrink every level proportionally
		if !c.HasOI {
			havePrev = false // unknown change: skip, don't bridge the gap
			continue
		}
		if havePrev {
			d := c.OI - prevOI
			if d > 0 {
				p := (c.H + c.L + c.C) / 3
				for i, t := range tiers {
					usd := d * p * t.W
					put(longs, bin(p*(1-1/t.Lever+liqMMR)), i, usd)
					put(shorts, bin(p*(1+1/t.Lever-liqMMR)), i, usd)
				}
			} else if d < 0 && prevOI > 0 {
				f := max(0, 1+d/prevOI)
				scale(longs, f)
				scale(shorts, f)
			}
		}
		prevOI, havePrev = c.OI, true
	}
	return longs, shorts
}
