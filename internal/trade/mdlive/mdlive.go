// Package mdlive adapts market.Engine to trade.MarketData for live paper trading.
package mdlive

import (
	"context"
	"fmt"
	"math"
	"time"

	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/trade"
)

// MD is the live MarketData. Mark price is approximated by the last trade price (v1).
type MD struct{ e *market.Engine }

func New(e *market.Engine) *MD { return &MD{e: e} }

var (
	_ trade.MarketData  = (*MD)(nil)
	_ trade.DepthQuoter = (*MD)(nil)
)

func (m *MD) Now() int64 { return time.Now().UnixMilli() }

func (m *MD) Spec(id string) (trade.Spec, bool) {
	i, ok := m.e.InstrumentMeta(id)
	if !ok || i.CtVal <= 0 {
		return trade.Spec{}, false
	}
	lot := i.LotSz
	if lot <= 0 {
		lot = i.MinSz
	}
	if lot <= 0 {
		lot = 1
	}
	minSz := i.MinSz
	if minSz <= 0 {
		minSz = lot
	}
	return trade.Spec{InstID: id, CtVal: i.CtVal, TickSz: i.TickSz, LotSz: lot, MinSz: minSz, MaxLever: i.MaxLever}, true
}

func (m *MD) Quote(_ context.Context, id string) (trade.Quote, error) {
	q, ok := m.e.TradeQuote(id)
	if !ok {
		return trade.Quote{}, fmt.Errorf("no price for %s", id)
	}
	return trade.Quote{Last: q.Last, Bid: q.Bid, Ask: q.Ask, Mark: q.Last, Ts: q.Ts}, nil
}

// QuoteDepth adds a 50-level order book (contracts per level) to the quote.
func (m *MD) QuoteDepth(ctx context.Context, id string) (trade.Quote, error) {
	q, err := m.Quote(ctx, id)
	if err != nil {
		return q, err
	}
	b, err := m.e.TradeBook(ctx, id, 50)
	if err != nil {
		return q, nil // fall back to the top-of-book model
	}
	for _, l := range b.Asks {
		q.Asks = append(q.Asks, [2]float64{l.Px, l.Sz})
	}
	for _, l := range b.Bids {
		q.Bids = append(q.Bids, [2]float64{l.Px, l.Sz})
	}
	if len(q.Asks) > 0 && len(q.Bids) > 0 {
		q.Bid, q.Ask = q.Bids[0][0], q.Asks[0][0]
	}
	return q, nil
}

func (m *MD) Bars(id, tf string, n int) ([]trade.Bar, error) {
	xs, err := m.e.BarsTF(id, tf, n)
	if err != nil {
		return nil, err
	}
	out := make([]trade.Bar, len(xs))
	for i, x := range xs {
		out[i] = trade.Bar{Ts: x.Ts, O: x.O, H: x.H, L: x.L, C: x.C, Vol: x.Vol, VolQ: x.Quote}
	}
	return out, nil
}

func (m *MD) row(id string) (market.Row, bool) {
	for _, r := range m.e.Snapshot().Rows {
		if r.InstID == id {
			return r, true
		}
	}
	return market.Row{}, false
}

func finiteValues(v map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(v))
	for k, x := range v {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			out[k] = x
		}
	}
	return out
}

// Factors returns the fact-panel values of the last confirmed snapshot.
func (m *MD) Factors(id string) (map[string]float64, bool) {
	r, ok := m.row(id)
	if !ok || len(r.Values) == 0 {
		return nil, false
	}
	return finiteValues(r.Values), true
}

// Screener ranks the active pool of the last confirmed snapshot by a rule's score.
func (m *MD) Screener(q trade.ScreenerQuery) ([]trade.ScreenRow, error) {
	snap := m.e.Snapshot()
	if len(snap.Rows) == 0 {
		return nil, fmt.Errorf("市场快照尚未就绪")
	}
	if q.Rule != "" {
		if _, ok := m.e.Rule(q.Rule); !ok {
			return nil, fmt.Errorf("评分规则 %s 不存在", q.Rule)
		}
	}
	var rows []trade.ScreenRow
	for _, r := range snap.Rows {
		if !r.Active {
			continue
		}
		x := trade.ScreenRow{InstID: r.InstID, Rank: r.Rank, Last: r.Last, Long: math.NaN(), Short: math.NaN(), Values: r.Values}
		if sc, ok := r.Scores[q.Rule]; ok {
			if sc.Long.Score != nil {
				x.Long = *sc.Long.Score
			}
			if sc.Short.Score != nil {
				x.Short = *sc.Short.Score
			}
		}
		rows = append(rows, x)
	}
	return trade.SelectScreen(rows, q), nil
}

// Score returns a rule's scores of the last confirmed snapshot (active pool and watchlist rows);
// an unscored side is 0.
func (m *MD) Score(rule, id string) (float64, float64, bool) {
	r, ok := m.row(id)
	if !ok {
		return 0, 0, false
	}
	sc, ok := r.Scores[rule]
	if !ok || (sc.Long.Score == nil && sc.Short.Score == nil) {
		return 0, 0, false
	}
	var l, s float64
	if sc.Long.Score != nil {
		l = *sc.Long.Score
	}
	if sc.Short.Score != nil {
		s = *sc.Short.Score
	}
	return l, s, true
}

func (m *MD) FundingRate(id string, at int64) (float64, bool) { return m.e.SettledFunding(id, at) }

func (m *MD) Keep(id string, on bool) { m.e.Keep(id, on) }
