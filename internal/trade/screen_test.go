package trade

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

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
	rp.setNow(t0 + 30*hourMs) // a gap in the stored history after a ranking: keep the last one for a day
	if kept, err := rp.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2}); err != nil || len(kept) != 2 || kept[0].InstID != "B-USDT-SWAP" {
		t.Fatalf("want the last ranking across a gap, got %+v %v", kept, err)
	}
	rp.setNow(t0 + 40*hourMs) // more than a day into the gap: an empty universe, the run goes on
	if got, err := rp.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2}); err != nil || len(got) != 0 {
		t.Fatalf("want an empty universe after a day-long gap, got %+v %v", got, err)
	}
	if got, _ := rp.Screener(ScreenerQuery{Scope: ScopeMarket}); len(got) != 0 { // top 0 from ctx.screener is capped, not unbounded
		t.Fatalf("%+v", got)
	}
	fresh := newReplayer(st, func(id string) (Spec, bool) { s, ok := specs[id]; return s, ok }, t0, t0+24*hourMs, hourMs)
	fresh.setNow(t0 + 5*hourMs) // not 12 hours of history yet and no earlier ranking
	if _, err := fresh.Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2}); err == nil || !strings.Contains(err.Error(), "1H K 线") {
		t.Fatalf("want a missing-history error, got %v", err)
	}
	if _, err := newReplayer(st, nil, t0, t0+hourMs, hourMs).Screener(ScreenerQuery{Scope: ScopeMarket, Top: 2}); err == nil {
		t.Fatal("market scope needs specs")
	}
}

func TestMarketQuery(t *testing.T) {
	if _, err := (ScreenerQuery{Scope: ScopeMarket, Top: 10, Rule: "r"}).MarketQuery(); err == nil {
		t.Fatal("a rule must be rejected")
	}
	if _, err := (ScreenerQuery{Scope: ScopeMarket, Top: 10, MinScore: 70}).MarketQuery(); err == nil {
		t.Fatal("a minimum score must be rejected, not ignored")
	}
	for _, top := range []int{0, -1, 1000} {
		if q, err := (ScreenerQuery{Scope: ScopeMarket, Top: top}).MarketQuery(); err != nil || q.Top != MaxMarketTop {
			t.Fatalf("top %d → %d %v", top, q.Top, err)
		}
	}
}

func TestScreenRowJSONKeepsUnscoredNull(t *testing.T) {
	b, err := json.Marshal(ScreenRow{InstID: "A", Rank: 1, Last: 2, Long: math.NaN(), Short: 30})
	if err != nil || string(b) != `{"instId":"A","rank":1,"last":2,"long":null,"short":30,"values":null}` {
		t.Fatalf("%s %v", b, err)
	}
}

type flakyMD struct {
	*fakeMD
	fails int
}

func (m *flakyMD) Screener(q ScreenerQuery) ([]ScreenRow, error) {
	if m.fails > 0 {
		m.fails--
		return nil, errors.New("snapshot not ready")
	}
	return m.fakeMD.Screener(q)
}

func TestUniverseRetriesAfterFailedStart(t *testing.T) {
	md := &flakyMD{fakeMD: newFakeMD(t0), fails: 1}
	md.screen = []ScreenRow{{InstID: "X-USDT-SWAP", Rank: 1}}
	b := &book{}
	b.setMarket(md)
	ri := &instRT{inst: Instance{ID: 1, Universe: Universe{Screener: &ScreenerQuery{Scope: ScopeMarket, Top: 5, Refresh: "4H"}}},
		b: b, wake: make(chan struct{}, 1)}
	s := &Service{opts: Options{SessionTimeout: time.Second}}
	if err := s.refreshUniverse(ri, t0, true); err == nil || !ri.universeRetry {
		t.Fatalf("the start refresh should fail and ask for a retry: %v", err)
	}
	// one minute later, not on a 4H boundary: retried anyway
	if err := s.refreshUniverse(ri, t0+minuteMs, false); err != nil || ri.universeRetry || len(ri.universe) != 1 {
		t.Fatalf("retry: %v %v %v", err, ri.universeRetry, ri.universe)
	}
}
