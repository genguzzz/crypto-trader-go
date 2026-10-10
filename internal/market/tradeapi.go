package market

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// Read API for the paper-trading layer (internal/trade/mdlive): contract metadata, quotes, a
// shallow order book, confirmed bars of any strategy timeframe, settled funding and keep-alive
// streaming for instruments the desk holds or trades.

// TradeTF maps strategy timeframes to their length. Buckets are aligned to UTC epoch multiples,
// so 1D bars run 00:00–24:00 UTC (OKX's own "1D" is aligned to UTC+8; its 4H matches either way).
var TradeTF = map[string]int64{"1m": minuteMs, "5m": 5 * minuteMs, "15m": 15 * minuteMs, "1H": hourMs, "4H": 4 * hourMs, "1D": 24 * hourMs}

// InstrumentMeta returns the catalog entry (live USDT-margined linear swap) for an instrument.
func (e *Engine) InstrumentMeta(id string) (okx.Instrument, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	i, ok := e.catalog[id]
	return i, ok
}

// TradeQuote is the freshest known top of book: streamed ticker / live candle when available,
// else the 30 s REST ticker.
type TradeQuote struct {
	Last, Bid, Ask float64
	Ts             int64
}

func (e *Engine) TradeQuote(id string) (TradeQuote, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var q TradeQuote
	if in := e.insts[id]; in != nil {
		q = TradeQuote{Last: in.price(), Bid: in.bid, Ask: in.ask, Ts: max(in.tickTs, in.liveRecv)}
	}
	if t, ok := e.tickers[id]; ok && t.Ts > q.Ts && t.Last > 0 {
		q = TradeQuote{Last: t.Last, Bid: t.Bid, Ask: t.Ask, Ts: t.Ts}
	}
	return q, q.Last > 0
}

var tradeBooks = struct {
	sync.Mutex
	m map[string]tradeBook
}{m: map[string]tradeBook{}}

type tradeBook struct {
	at time.Time
	b  okx.Book
}

// TradeBook returns a shallow order book (≤ 400 levels, /market/books), cached for 1 s.
func (e *Engine) TradeBook(ctx context.Context, id string, depth int) (okx.Book, error) {
	depth = max(1, min(depth, 400))
	tradeBooks.Lock()
	if c, ok := tradeBooks.m[id]; ok && time.Since(c.at) < time.Second {
		tradeBooks.Unlock()
		return c.b, nil
	}
	for k, c := range tradeBooks.m {
		if time.Since(c.at) > time.Minute {
			delete(tradeBooks.m, k)
		}
	}
	tradeBooks.Unlock()
	b, err := e.rest.Books(ctx, id, depth)
	if err != nil {
		return okx.Book{}, err
	}
	tradeBooks.Lock()
	tradeBooks.m[id] = tradeBook{time.Now(), b}
	tradeBooks.Unlock()
	return b, nil
}

// BarsTF returns the last n confirmed bars of timeframe tf, oldest first, aggregated from recorded
// 1m bars (tf < 1H) or 1H bars (tf ≥ 1H; recent hours not yet stored as 1H are built from 1m).
// A bucket is confirmed once the pool clock has confirmed its last minute. Missing minutes inside
// a bucket are skipped (the bar is built from what was recorded).
func (e *Engine) BarsTF(id, tf string, n int) ([]store.Bar, error) {
	step, ok := TradeTF[tf]
	if !ok {
		return nil, fmt.Errorf("timeframe must be one of 1m,5m,15m,1H,4H,1D")
	}
	n = max(1, min(n, 1000))
	in := e.instFor(id)
	if in == nil {
		return nil, fmt.Errorf("unknown instrument %s", id)
	}
	e.mu.RLock()
	last := e.lastMin
	e.mu.RUnlock()
	if last == 0 {
		last = time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs
	}
	end := (last + minuteMs) / step * step // exclusive end of the newest confirmed bucket
	from := end - int64(n)*step
	base := map[int64]store.Bar{}
	if step < hourMs {
		if db, err := e.st.Bars("candles_1m", id, from, end-1); err == nil {
			for _, b := range db {
				base[b.Ts] = b
			}
		}
		e.mu.RLock()
		in.bars.each(from, func(b store.Bar) {
			if b.Ts < end {
				base[b.Ts] = b
			}
		})
		e.mu.RUnlock()
	} else {
		if db, err := e.st.Bars("candles_1h", id, from, end-1); err == nil {
			for _, b := range db {
				base[b.Ts] = b
			}
		}
		e.mu.RLock()
		for ts, b := range in.hourly {
			if ts >= from && ts < end {
				base[ts] = b
			}
		}
		for h := max(from, end-26*hourMs); h < end; h += hourMs {
			if _, have := base[h]; have {
				continue
			}
			if b, ok := partialHour(in, h); ok {
				base[h] = b
			}
		}
		e.mu.RUnlock()
	}
	keys := make([]int64, 0, len(base))
	for ts := range base {
		keys = append(keys, ts)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var out []store.Bar
	for _, ts := range keys {
		b := base[ts]
		bucket := ts / step * step
		k := len(out)
		if k == 0 || out[k-1].Ts != bucket {
			out = append(out, store.Bar{Ts: bucket, O: b.O, H: b.H, L: b.L, C: b.C, Vol: b.Vol, VolCcy: b.VolCcy, Quote: b.Quote})
			continue
		}
		c := &out[k-1]
		c.H, c.L, c.C = math.Max(c.H, b.H), math.Min(c.L, b.L), b.C
		c.Vol += b.Vol
		c.VolCcy += b.VolCcy
		c.Quote += b.Quote
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// partialHour aggregates whatever 1m bars of one hour are in memory. Caller holds e.mu (read).
func partialHour(in *Inst, open int64) (store.Bar, bool) {
	var b store.Bar
	n := 0
	for k := int64(0); k < 60; k++ {
		x, ok := in.bars.get(open + k*minuteMs)
		if !ok {
			continue
		}
		if n == 0 {
			b = store.Bar{Ts: open, O: x.O, H: x.H, L: x.L}
		}
		b.H, b.L, b.C = math.Max(b.H, x.H), math.Min(b.L, x.L), x.C
		b.Vol += x.Vol
		b.VolCcy += x.VolCcy
		b.Quote += x.Quote
		n++
	}
	return b, n > 0
}

// SettledFunding returns the settled rate of the funding settlement at time `at` (ms).
func (e *Engine) SettledFunding(id string, at int64) (float64, bool) {
	e.mu.RLock()
	if in := e.insts[id]; in != nil {
		xs := in.fund.settled
		i := sort.Search(len(xs), func(i int) bool { return xs[i].FundingTime >= at })
		if i < len(xs) && xs[i].FundingTime == at {
			r := xs[i].Rate
			e.mu.RUnlock()
			return r, true
		}
	}
	e.mu.RUnlock()
	fs, err := e.st.Funding(id, at, at)
	if err != nil {
		return 0, false
	}
	for _, f := range fs {
		if f.Settled && f.FundingTime == at {
			return f.Rate, true
		}
	}
	return 0, false
}

// Keep keeps an instrument streaming (candles, OI, funding and tickers) while the trading layer
// needs it, independent of the user's watchlist and views.
func (e *Engine) Keep(id string, on bool) {
	if on {
		e.KeepMany([]string{id}, nil)
	} else {
		e.KeepMany(nil, []string{id})
	}
}

// KeepMany is Keep for many instruments with one subscription update (a large screener universe
// would otherwise send one WS subscribe request per instrument).
func (e *Engine) KeepMany(on, off []string) {
	var known []string
	for _, id := range on {
		if e.instFor(id) != nil {
			known = append(known, id)
		}
	}
	e.mu.Lock()
	if e.keep == nil {
		e.keep = map[string]bool{}
	}
	changed := false
	for _, id := range known {
		if !e.keep[id] {
			e.keep[id], changed = true, true
		}
	}
	for _, id := range off {
		if e.keep[id] {
			delete(e.keep, id)
			changed = true
		}
	}
	e.mu.Unlock()
	if changed {
		e.updateSubscriptions()
	}
}
