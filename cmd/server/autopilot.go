package main

import (
	"crypto-trader-go/internal/autopilot"
	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/signals"
)

// deskMarket gives the trading desk (internal/autopilot) the prices and fact-panel values its journal
// is stamped with and followed on.
type deskMarket struct{ eng *market.Engine }

func (m deskMarket) Known(id string) bool { _, ok := m.eng.InstrumentMeta(id); return ok }

func (m deskMarket) Quote(id string) (float64, int64, bool) {
	q, ok := m.eng.TradeQuote(id)
	return q.Last, q.Ts, ok && q.Last > 0
}

func (m deskMarket) Values(id string) (map[string]float64, bool) { return m.eng.FieldValues(id) }

func (m deskMarket) Pool() []autopilot.PoolCoin {
	var out []autopilot.PoolCoin
	for _, r := range m.eng.Snapshot().Rows {
		if r.Active {
			out = append(out, autopilot.PoolCoin{InstID: r.InstID, Rank: r.Rank, Category: r.Category, Values: r.Values})
		}
	}
	return out
}

func (m deskMarket) Bars1m(id string, from, to int64) []signals.Bar {
	return toSignalBars(m.eng.Bars1m(id, from, to))
}
