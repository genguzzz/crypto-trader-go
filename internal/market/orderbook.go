package market

import (
	"context"
	"fmt"
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

// OrderBook fetches up to 400 levels per side via REST, cached ~800ms per instrument so many
// open views don't multiply requests. Detail views poll it once per second.
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
	if b, ok := bookCache.m[id]; ok && time.Now().UnixMilli()-b.ReceivedAt < 800 {
		bookCache.Unlock()
		return b, nil
	}
	bookCache.Unlock()
	raw, err := e.rest.Books(ctx, id, 400)
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
	}
	bookCache.Lock()
	bookCache.m[id] = ob
	bookCache.Unlock()
	return ob, nil
}
