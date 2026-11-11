package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// CTG_LIVE=1 go test ./internal/market -run TestLiveMicro -v : real OKX files (ENA, one day).
func TestLiveMicro(t *testing.T) {
	if os.Getenv("CTG_LIVE") == "" {
		t.Skip("CTG_LIVE not set")
	}
	if u, err := url.Parse(os.Getenv("CTG_PROXY")); err == nil && u.Host != "" {
		okx.ConfigureNetwork(u, false)
	}
	rest := okx.NewREST()
	ctx := context.Background()
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, tz8).UnixMilli()
	load := func(id string, ct float64) TapeData {
		fs, err := rest.TradeFiles(ctx, id, day-1, day+1)
		if err != nil || len(fs) == 0 {
			t.Fatalf("%s files %v %v", id, fs, err)
		}
		d := TapeData{InstID: id, Market: "spot", CtVal: ct}
		if ct != 1 {
			d.Market = "swap"
		}
		rest.ReadTradeFile(ctx, fs[0].URL, func(x okx.Trade) { d.Trades = append(d.Trades, TapeTrade{x.Ts, x.Px, x.Sz, x.Buy}) })
		return d
	}
	sw, sp := load("ENA-USDT-SWAP", 10), load("ENA-USDT", 1)
	q := TapeQuery{From: day, To: day + 24*hourMs, Sections: map[string]bool{"summary": true, "sizes": true, "clips": true, "sweeps": true, "large": true, "bars": true},
		ClipMin: 5, ClipGapMin: 30, Top: 5}
	t0 := time.Now()
	a, b := TapeStatsFor(sw, q), TapeStatsFor(sp, q)
	c := crossStats(a, b)
	fmt.Printf("analysis %d+%d trades in %s\n", len(sw.Trades), len(sp.Trades), time.Since(t0))
	var books [2]*BookShape
	for i, id := range []string{"ENA-USDT-SWAP", "ENA-USDT"} {
		bk, err := rest.Books(ctx, id, 5000)
		if err != nil {
			t.Fatal(err)
		}
		ct := 10.0
		if i == 1 {
			ct = 1
		}
		books[i] = bookShape(bk, ct, BookQuery{BandPct: 3, Bands: []float64{0.5, 1, 2}, WallMult: 6, Top: 3})
		books[i].Market = [2]string{"swap", "spot"}[i]
	}
	v := assess(a, b, c, nil, nil, books[0], books[1])
	roundSummary(a.Summary)
	roundSummary(b.Summary)
	a.Bars, b.Bars = nil, nil
	for _, x := range []any{a, b, c, v, books[0].Walls} {
		j, _ := json.MarshalIndent(x, "", " ")
		fmt.Printf("%d bytes\n%s\n", len(j), j)
	}
}

// CTG_LIVE=1 go test ./internal/market -run TestLiveTapeEngine -v : live WS recording + backfill + tools for ENA.
func TestLiveTapeEngine(t *testing.T) {
	if os.Getenv("CTG_LIVE") == "" {
		t.Skip("CTG_LIVE not set")
	}
	if u, err := url.Parse(os.Getenv("CTG_PROXY")); err == nil && u.Host != "" {
		okx.ConfigureNetwork(u, false)
	}
	dir := t.TempDir()
	st, err := store.Open(dir + "/ctg.db")
	if err != nil {
		t.Fatal(err)
	}
	tp, err := store.OpenTape(dir + "/tape.db")
	if err != nil {
		t.Fatal(err)
	}
	e := New(Config{Threshold: 5e6, TopN: 10}, okx.NewREST(), st)
	e.SetTape(tp, 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.refreshInstruments(ctx); err != nil {
		t.Fatal(err)
	}
	e.refreshTickers(ctx)
	e.refreshSpot(ctx)
	go e.pub.Run(ctx)
	e.startTape(ctx)
	e.RequestTape("ENA-USDT-SWAP")
	e.updateSubscriptions()
	wait := 150 * time.Second
	if s := os.Getenv("CTG_LIVE_WAIT"); s != "" {
		wait, _ = time.ParseDuration(s)
	}
	time.Sleep(wait)
	now := time.Now().UnixMilli()
	q := TapeQuery{From: now - 3*hourMs, To: now, Sections: map[string]bool{"summary": true, "clips": true, "cross": true}, ClipMin: 5, ClipGapMin: 30, Top: 3}
	t0 := time.Now()
	ta, err := e.AnalyzeTrades(ctx, "ENA-USDT-SWAP", "ENA-USDT", q, true)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("analyze (with backfill) %s\nswap coverage %+v\nspot coverage %+v\n", time.Since(t0), ta.Swap.Coverage, ta.Spot.Coverage)
	j, _ := json.Marshal(ta.Swap.Summary)
	fmt.Printf("swap summary %s\n", j)
	j, _ = json.Marshal(ta.Cross)
	fmt.Printf("cross %s\n", j)
	for _, id := range []string{"ENA-USDT-SWAP", "ENA-USDT"} {
		w, err := e.WallHistory(id, now-hourMs, now+1, 5*minuteMs, 0, 30, 5, true)
		if err != nil {
			t.Fatal(err)
		}
		j, _ = json.Marshal(w)
		fmt.Printf("walls %s: %s\n", id, j)
	}
	b, err := e.BookShapeFor(ctx, "ENA-USDT-SWAP", BookQuery{BandPct: 3, Bands: []float64{0.5, 1, 2}, WallMult: 6, Top: 3})
	if err != nil {
		t.Fatal(err)
	}
	j, _ = json.Marshal(b.Walls)
	fmt.Printf("book walls (tracked=%v) %s\n", b.Tracked, j)
	ar, err := e.Assess(ctx, "ENA", now-3*hourMs, now)
	if err != nil {
		t.Fatal(err)
	}
	j, _ = json.Marshal(ar)
	fmt.Printf("assess %d bytes: %s\n", len(j), j[:min(len(j), 1500)])
	dx, err := e.DexActivity(ctx, "ENA", 5000, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	j, _ = json.Marshal(dx)
	fmt.Printf("dex %s\n", j[:min(len(j), 1200)])
}
