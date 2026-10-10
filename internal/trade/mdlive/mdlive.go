// Package mdlive adapts market.Engine to trade.MarketData for live paper trading.
package mdlive

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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
	_ trade.KeepBatcher = (*MD)(nil)
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
	return trade.Spec{InstID: id, CtVal: i.CtVal, TickSz: i.TickSz, LotSz: lot, MinSz: minSz, MaxLever: i.MaxLever, Category: i.Category}, true
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

// Screener ranks the active pool of the last confirmed snapshot by a rule's score, or (scope
// market) every recorded crypto swap by 12h USDT turnover.
func (m *MD) Screener(q trade.ScreenerQuery) ([]trade.ScreenRow, error) {
	snap := m.e.Snapshot()
	if len(snap.Rows) == 0 {
		return nil, fmt.Errorf("市场快照尚未就绪")
	}
	if q.Market() {
		if q.Rule != "" {
			return nil, fmt.Errorf("全市场筛选不能指定评分规则")
		}
		return trade.SelectScreen(MarketRows(snap.Rows), q.CappedTop()), nil
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
			if strings.HasPrefix(sc.Gate, "BLOCKED") {
				continue // the rule's cost gate (e.g. spread) rules the instrument out
			}
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

// MarketRows ranks the recorded (eligible) crypto swaps of a snapshot by 12h USDT turnover,
// descending (rank 1 = largest). Instruments without a full 12h window (new, stale; a missing
// minute is a gap, not zero) are left out; a warming instrument counts with its provisional value.
func MarketRows(rows []market.Row) []trade.ScreenRow {
	var xs []market.Row
	for _, r := range rows {
		if r.Eligible && r.Category == trade.CategoryCrypto && r.Turnover12h > 0 && (r.Status == "live" || r.Provisional) {
			xs = append(xs, r)
		}
	}
	sort.SliceStable(xs, func(i, j int) bool {
		if xs[i].Turnover12h != xs[j].Turnover12h {
			return xs[i].Turnover12h > xs[j].Turnover12h
		}
		return xs[i].InstID < xs[j].InstID
	})
	out := make([]trade.ScreenRow, len(xs))
	for i, r := range xs {
		out[i] = trade.ScreenRow{InstID: r.InstID, Rank: i + 1, Last: r.Last, Long: math.NaN(), Short: math.NaN(), Values: r.Values}
	}
	return out
}

// Score returns a rule's scores of the last confirmed snapshot (active pool and watchlist rows);
// an unscored side is NaN.
func (m *MD) Score(rule, id string) (float64, float64, bool) {
	r, ok := m.row(id)
	if !ok {
		return 0, 0, false
	}
	sc, ok := r.Scores[rule]
	if !ok || (sc.Long.Score == nil && sc.Short.Score == nil) {
		return 0, 0, false
	}
	l, s := math.NaN(), math.NaN()
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

func (m *MD) KeepMany(on, off []string) { m.e.KeepMany(on, off) }
