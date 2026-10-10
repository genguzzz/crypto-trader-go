package mdlive

import (
	"testing"

	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/trade"
)

func TestMarketRows(t *testing.T) {
	rows := []market.Row{
		{InstID: "A", Category: "1", Eligible: true, Status: "live", Turnover12h: 10e6},
		{InstID: "B", Category: "1", Eligible: true, Status: "live", Turnover12h: 30e6, Active: true, Rank: 1},
		{InstID: "C", Category: "1", Eligible: true, Status: "warming", Provisional: true, Turnover12h: 20e6},
		{InstID: "STOCK", Category: "3", Eligible: true, Status: "live", Turnover12h: 99e6},
		{InstID: "ADHOC", Category: "1", Status: "live", Turnover12h: 99e6},                // watched, not recorded
		{InstID: "STALE", Category: "1", Eligible: true, Status: "stale", Turnover12h: 0},  // gap
		{InstID: "NEW", Category: "1", Eligible: true, Status: "new", Turnover12h: 0},      // listed < 12h
		{InstID: "WARM", Category: "1", Eligible: true, Status: "warming", Turnover12h: 0}, // no estimate yet
		{InstID: "D", Category: "1", Eligible: true, Status: "live", Turnover12h: 1e6, Values: map[string]float64{"x": 1}},
	}
	got := MarketRows(rows)
	want := []string{"B", "C", "A", "D"}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, id := range want {
		if got[i].InstID != id || got[i].Rank != i+1 {
			t.Fatalf("row %d: %+v", i, got[i])
		}
	}
	sel := trade.SelectScreen(got, trade.ScreenerQuery{Scope: trade.ScopeMarket, Top: 2})
	if len(sel) != 2 || sel[0].InstID != "B" || sel[1].InstID != "C" || sel[0].Long != 0 {
		t.Fatalf("%+v", sel)
	}
	f := trade.SelectScreen(got, trade.ScreenerQuery{Scope: trade.ScopeMarket, Top: 10, Filter: map[string][2]float64{"x": {0, 2}}})
	if len(f) != 1 || f[0].InstID != "D" || f[0].Rank != 4 {
		t.Fatalf("%+v", f)
	}
}
