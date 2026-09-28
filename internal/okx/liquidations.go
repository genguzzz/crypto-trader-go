package okx

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// /public/liquidation-orders: 40 req/2s per IP; we use half.
var liqLimiter = NewLimiter(20, 2*time.Second)

// Liquidation is one filled liquidation order of a USDT perpetual (public data, no account).
type Liquidation struct {
	Ts      int64   // ms
	PosSide string  // "long" = a long position was liquidated (forced sell), "short" = forced buy
	Px      float64 // bankruptcy price
	Sz      float64 // contracts
}

// LiquidationOrders returns filled liquidations of one SWAP instrument family (e.g. BTC-USDT),
// newest first, strictly older than `after` (ms, 0 = latest). OKX keeps about the last 24 hours
// and, unlike the WS channel (at most one per contract per second), returns every order.
func (c *REST) LiquidationOrders(ctx context.Context, instFamily string, after int64, limit int) ([]Liquidation, error) {
	q := url.Values{"instType": {"SWAP"}, "instFamily": {instFamily}, "state": {"filled"}, "limit": {strconv.Itoa(limit)}}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	var raw []struct {
		InstID  string `json:"instId"`
		Details []struct {
			PosSide string `json:"posSide"`
			Side    string `json:"side"`
			BkPx    string `json:"bkPx"`
			Sz      string `json:"sz"`
			Ts      string `json:"ts"`
		} `json:"details"`
	}
	if err := c.get(ctx, liqLimiter, "/public/liquidation-orders", q, &raw); err != nil {
		return nil, err
	}
	var out []Liquidation
	for _, r := range raw {
		for _, d := range r.Details {
			ps := d.PosSide
			if ps != "long" && ps != "short" { // net mode: the forced order's side tells which position it closed
				ps = "long"
				if d.Side == "buy" {
					ps = "short"
				}
			}
			out = append(out, Liquidation{Ts: i64(d.Ts), PosSide: ps, Px: f(d.BkPx), Sz: f(d.Sz)})
		}
	}
	return out, nil
}

// MaxLeverage returns the maximum leverage of every SWAP instrument (instId → lever).
func (c *REST) MaxLeverage(ctx context.Context) (map[string]float64, error) {
	var raw []map[string]any
	if err := c.get(ctx, c.misc, "/public/instruments", url.Values{"instType": {"SWAP"}}, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(raw))
	for _, m := range raw {
		id, _ := m["instId"].(string)
		lv, _ := m["lever"].(string)
		if v := f(lv); id != "" && v > 0 {
			out[id] = v
		}
	}
	return out, nil
}
