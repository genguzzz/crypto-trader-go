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
// Map (estimate, not exchange data): positions opened each hour (OI increase, both sides) are spread
// over leverage tiers and placed at their approximate liquidation prices; OI decreases shrink every
// open level proportionally; a level is removed once a later candle trades through it. What remains
// is where the model expects liquidations to cluster if price moves there.

const (
	liqEvery      = time.Minute
	liqKeep       = 30 * 24 * time.Hour
	liqPages      = 30 // × 100 orders; OKX keeps ~24h (≈2–3k orders for BTC on a normal day)
	liqFresh      = 20 * time.Second
	liqMMR        = 0.005 // maintenance margin rate assumed by the map
	liqMapMaxDays = 30
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
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := e.syncLiq(c, id); err != nil {
		resp.Err = err.Error() // serve what is stored
	}
	cancel()
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
	P     float64 `json:"p"`     // bin low price
	Long  float64 `json:"long"`  // USDT of long positions estimated to liquidate in this bin
	Short float64 `json:"short"` // same, short positions
}

type LiqMapResp struct {
	Px     float64   `json:"px"`   // last price
	Step   float64   `json:"step"` // bin size
	From   int64     `json:"from"`
	Days   int       `json:"days"`
	Lever  float64   `json:"lever"`  // contract max leverage (tiers above it are dropped)
	Levers []float64 `json:"levers"` // tiers used
	Bins   []LiqBin  `json:"bins"`   // ascending by price, non-empty only
}

// LiqMap estimates where open positions would be liquidated (see the header comment).
func (e *Engine) LiqMap(ctx context.Context, id string, days int) (LiqMapResp, error) {
	days = max(1, min(days, liqMapMaxDays))
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
	oiAt := map[int64]float64{} // hour open → OI (coins) at its last sample
	for _, o := range ois {
		if o.OiCcy > 0 {
			oiAt[o.Ts/hourMs*hourMs] = o.OiCcy
		}
	}
	lever := e.maxLever(ctx, id)
	tiers := liqTiersFor(lever)
	last := cs[len(cs)-1].C
	step := niceStep(last * 0.001)
	longs, shorts := estimateLiqLevels(cs, oiAt, tiers, step)
	resp := LiqMapResp{Px: last, Step: step, From: from, Days: days, Lever: lever}
	for _, t := range tiers {
		resp.Levers = append(resp.Levers, t.Lever)
	}
	lim := int64(math.Ceil(0.5 / 0.001)) // ±50% of price
	merged := map[int64]*LiqBin{}
	add := func(k int64, v float64, long bool) {
		if v < 1 || math.Abs(float64(k)-last/step) > float64(lim) {
			return
		}
		b := merged[k]
		if b == nil {
			b = &LiqBin{P: float64(k) * step}
			merged[k] = b
		}
		if long {
			b.Long += v
		} else {
			b.Short += v
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

// estimateLiqLevels runs the map model over hourly candles (ascending) and OI in coins by hour open:
// USDT still open per price bin (bin = floor(px/step)) for long and short liquidations.
func estimateLiqLevels(cs []Candle, oiAt map[int64]float64, tiers []liqTier, step float64) (longs, shorts map[int64]float64) {
	longs, shorts = map[int64]float64{}, map[int64]float64{}
	bin := func(p float64) int64 { return int64(math.Floor(p / step)) }
	prevOI := oiAt[cs[0].T/hourMs*hourMs-hourMs]
	havePrev := prevOI > 0
	for _, c := range cs {
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
		oi, ok := oiAt[c.T/hourMs*hourMs]
		if !ok {
			havePrev = false // unknown change: skip, don't bridge the gap
			continue
		}
		if havePrev {
			d := oi - prevOI
			if d > 0 {
				p := (c.H + c.L + c.C) / 3
				for _, t := range tiers {
					usd := d * p * t.W
					longs[bin(p*(1-1/t.Lever+liqMMR))] += usd
					shorts[bin(p*(1+1/t.Lever-liqMMR))] += usd
				}
			} else if d < 0 && prevOI > 0 {
				f := max(0, 1+d/prevOI)
				for k := range longs {
					longs[k] *= f
				}
				for k := range shorts {
					shorts[k] *= f
				}
			}
		}
		prevOI, havePrev = oi, true
	}
	return longs, shorts
}
