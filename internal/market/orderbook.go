package market

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// OrderBook is an OKX book snapshot converted to USDT notional (px × contracts × ctVal).
// Levels: [price, usdt, contracts]. Bids descending, asks ascending.
type OrderBook struct {
	InstID     string       `json:"instId"`
	Ts         int64        `json:"ts"`         // exchange timestamp
	ReceivedAt int64        `json:"receivedAt"` // when this server fetched it
	CtVal      float64      `json:"ctVal"`
	Mid        float64      `json:"mid"`
	BestBid    float64      `json:"bestBid"`
	BestAsk    float64      `json:"bestAsk"`
	SpreadBps  float64      `json:"spreadBps"`
	Bids       [][3]float64 `json:"bids"`
	Asks       [][3]float64 `json:"asks"`
	Levels     int          `json:"levels"`   // raw levels per side fetched (OKX max 5000)
	CoverBid   float64      `json:"coverBid"` // % below mid the fetched bids reach
	CoverAsk   float64      `json:"coverAsk"` // % above mid the fetched asks reach
}

const (
	bookDepth = 5000 // /market/books-full: the deepest book OKX publishes
	bookExact = 400  // levels per side sent as-is; deeper ones are merged into price buckets
)

// compressSide keeps the first bookExact levels and merges the rest into buckets of ≥5 bps of mid
// (≤300 buckets), each at its farthest price, so cumulative depth by price is unchanged.
func compressSide(xs [][3]float64, mid float64) [][3]float64 {
	if len(xs) <= bookExact || mid <= 0 {
		return xs
	}
	out := append([][3]float64(nil), xs[:bookExact]...)
	start, far := xs[bookExact][0], xs[len(xs)-1][0]
	w := math.Max(mid*0.0005, math.Abs(far-start)/300)
	var cur [3]float64
	curB := int64(-1)
	for _, l := range xs[bookExact:] {
		b := int64(math.Abs(l[0]-start) / w)
		if b != curB && curB >= 0 {
			out = append(out, cur)
			cur = [3]float64{}
		}
		curB = b
		cur[0], cur[1], cur[2] = l[0], cur[1]+l[1], cur[2]+l[2]
	}
	return append(out, cur)
}

var bookCache = struct {
	sync.Mutex
	m map[string]OrderBook
}{m: map[string]OrderBook{}}

// sweepCaches drops expired order-book and spot-candle cache entries (they are only reused for
// ~1s / 20s; without this every instrument ever viewed would stay in memory).
func sweepCaches() {
	now := time.Now()
	bookCache.Lock()
	for id, b := range bookCache.m {
		if now.UnixMilli()-b.ReceivedAt > 10_000 {
			delete(bookCache.m, id)
		}
	}
	bookCache.Unlock()
	spotCache.Lock()
	for k, c := range spotCache.m {
		if now.Sub(c.at) > time.Minute {
			delete(spotCache.m, k)
		}
	}
	spotCache.Unlock()
}

// OrderBook fetches up to 5000 levels per side (books-full), cached ~900ms per instrument so many
// open views don't multiply requests. Detail views poll it once per second. 400 levels only span
// ~±0.1% on BTC, far short of the ±0.5–5% depth views; 5000 reach ~±1% on BTC, tens of % on small coins.
func (e *Engine) OrderBook(ctx context.Context, id string) (OrderBook, error) {
	in := e.instFor(id)
	ctVal := 0.0
	if in != nil {
		e.mu.RLock()
		ctVal = in.CtVal
		e.mu.RUnlock()
	}
	if in == nil || ctVal <= 0 {
		return OrderBook{}, fmt.Errorf("unknown instrument or contract value")
	}
	bookCache.Lock()
	if b, ok := bookCache.m[id]; ok && time.Now().UnixMilli()-b.ReceivedAt < 900 {
		bookCache.Unlock()
		return b, nil
	}
	bookCache.Unlock()
	raw, err := e.rest.Books(ctx, id, bookDepth)
	if err != nil {
		return OrderBook{}, err
	}
	ob := OrderBook{InstID: id, Ts: raw.Ts, ReceivedAt: time.Now().UnixMilli(), CtVal: ctVal}
	for _, l := range raw.Bids {
		ob.Bids = append(ob.Bids, [3]float64{l.Px, l.Px * l.Sz * ctVal, l.Sz})
	}
	for _, l := range raw.Asks {
		ob.Asks = append(ob.Asks, [3]float64{l.Px, l.Px * l.Sz * ctVal, l.Sz})
	}
	if len(ob.Bids) > 0 && len(ob.Asks) > 0 {
		ob.BestBid, ob.BestAsk = ob.Bids[0][0], ob.Asks[0][0]
		ob.Mid = (ob.BestBid + ob.BestAsk) / 2
		ob.SpreadBps = (ob.BestAsk - ob.BestBid) / ob.Mid * 1e4
		ob.Levels = max(len(ob.Bids), len(ob.Asks))
		ob.CoverBid = (ob.Mid - ob.Bids[len(ob.Bids)-1][0]) / ob.Mid * 100
		ob.CoverAsk = (ob.Asks[len(ob.Asks)-1][0] - ob.Mid) / ob.Mid * 100
		ob.Bids, ob.Asks = compressSide(ob.Bids, ob.Mid), compressSide(ob.Asks, ob.Mid)
	}
	bookCache.Lock()
	bookCache.m[id] = ob
	bookCache.Unlock()
	return ob, nil
}
