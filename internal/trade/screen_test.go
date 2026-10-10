package trade

import (
	"strings"
	"testing"

	"crypto-trader-go/internal/store"
)

func TestValidUniverseScope(t *testing.T) {
	s := &Service{}
	cases := []struct {
		q   ScreenerQuery
		err string
	}{
		{ScreenerQuery{Top: 50}, ""},
		{ScreenerQuery{Scope: "pool", Top: 51}, "1–50"},
		{ScreenerQuery{Scope: "market", Top: 300}, ""},
		{ScreenerQuery{Scope: "Market", Top: 120, Filter: map[string][2]float64{"ret_1h": {-1, 1}}}, ""},
		{ScreenerQuery{Scope: "market", Top: 301}, "1–300"},
		{ScreenerQuery{Scope: "market", Top: 0}, "1–300"},
		{ScreenerQuery{Scope: "market", Top: 10, Rule: "r"}, "评分规则"},
		{ScreenerQuery{Scope: "market", Top: 10, MinScore: 60}, "最低分"},
		{ScreenerQuery{Scope: "all", Top: 10}, "scope"},
	}
	for _, c := range cases {
		q := c.q
		u, err := s.validUniverse(Universe{Screener: &q})
		if c.err == "" {
			if err != nil {
				t.Fatalf("%+v: %v", c.q, err)
			}
			if want := map[bool]string{true: ScopeMarket, false: ""}[strings.EqualFold(c.q.Scope, "market")]; u.Screener.Scope != want {
				t.Fatalf("%+v: scope stored as %q", c.q, u.Screener.Scope)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Fatalf("%+v: want %q, got %v", c.q, c.err, err)
		}
	}
}

func TestSelectScreenByRank(t *testing.T) {
	rows := []ScreenRow{{InstID: "C", Rank: 3}, {InstID: "A", Rank: 1}, {InstID: "B", Rank: 2}}
	got := SelectScreen(rows, ScreenerQuery{Scope: ScopeMarket, Top: 2})
	if len(got) != 2 || got[0].InstID != "A" || got[1].InstID != "B" {
		t.Fatalf("%+v", got)
	}
}

func TestReplayerMarketScreener(t *testing.T) {
	st := openStore(t)
	specs := map[string]Spec{
		"A-USDT-SWAP":    {InstID: "A-USDT-SWAP", Category: CategoryCrypto},
		"B-USDT-SWAP":    {InstID: "B-USDT-SWAP", Category: CategoryCrypto},
		"C-USDT-SWAP":    {InstID: "C-USDT-SWAP", Category: CategoryCrypto},
		"GAP-USDT-SWAP":  {InstID: "GAP-USDT-SWAP", Category: CategoryCrypto},
		"TSLA-USDT-SWAP": {InstID: "TSLA-USDT-SWAP", Category: "3"},
	}
	now := t0 + 12*hourMs + 30*minuteMs // the 12 complete hours are [t0, t0+12h)
	var bars []store.InstBar
	add := func(id string, from, to int64, quote float64) {
		for ts := from; ts < to; ts += hourMs {
			bars = append(bars, store.InstBar{InstID: id, Bar: store.Bar{Ts: ts, O: 1, H: 1, L: 1, C: 1, Quote: quote}})
		}
	}
	add("A-USDT-SWAP", t0-hourMs, t0+13*hourMs, 100) // the bar at t0+12h is not closed at now
	add("B-USDT-SWAP", t0, t0+12*hourMs, 300)
	add("C-USDT-SWAP", t0, t0+12*hourMs, 200)
	add("GAP-USDT-SWAP", t0+hourMs, t0+12*hourMs, 1e6) // a missing hour: left out, not zero
	add("TSLA-USDT-SWAP", t0, t0+12*hourMs, 1e6)       // not crypto
	add("UNKNOWN-USDT-SWAP", t0, t0+12*hourMs, 1e6)    // no spec: cannot be traded
	if err := st.PutBars("candles_1h", bars); err != nil {
		t.Fatal(err)
	}
	rp := newReplayer(st, func(id string) (Spec, bool) { s, ok := specs[id]; return s, ok }, t0, t0+24*hourMs, hourMs)
	rp.setNow(now)
	got, err := rp.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].InstID != "B-USDT-SWAP" || got[0].Rank != 1 || got[1].InstID != "C-USDT-SWAP" || got[1].Rank != 2 {
		t.Fatalf("%+v", got)
	}
	all, _ := rp.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 300})
	if len(all) != 3 || all[2].InstID != "A-USDT-SWAP" || all[2].Rank != 3 || all[2].Last != 1 {
		t.Fatalf("%+v", all)
	}
	if _, err := rp.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2, Rule: "r"}); err == nil {
		t.Fatal("a rule must be rejected in market scope")
	}
	rp.setNow(t0 + 5*hourMs) // not 12 hours of history yet
	if _, err := rp.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2}); err == nil || !strings.Contains(err.Error(), "1H K 线") {
		t.Fatalf("want a missing-history error, got %v", err)
	}
	if _, err := newReplayer(st, nil, t0, t0+hourMs, hourMs).Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2}); err == nil {
		t.Fatal("market scope needs specs")
	}
}
