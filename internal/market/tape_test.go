package market

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// The codec is exact for any decimal prices and sizes OKX sends (random round trips).
func TestTapeCodecRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for round := 0; round < 200; round++ {
		m := int64(1_793_000_000_000) + int64(round)*minuteMs
		dec := r.Intn(9)
		var xs []TapeTrade
		ts := m
		for i := 0; i < 1+r.Intn(300); i++ {
			ts += int64(r.Intn(400))
			px, _ := strconv.ParseFloat(strconv.FormatFloat(r.Float64()*math.Pow10(r.Intn(6)), 'f', dec, 64), 64)
			sz, _ := strconv.ParseFloat(strconv.FormatFloat(r.Float64()*1000, 'f', r.Intn(4), 64), 64)
			if px <= 0 || sz <= 0 {
				continue
			}
			xs = append(xs, TapeTrade{Ts: min(ts, m+minuteMs-1), Px: px, Sz: sz, Buy: r.Intn(2) == 0})
		}
		got, err := decodeMinute(m, encodeMinute(m, xs), nil)
		if err != nil || len(got) != len(xs) {
			t.Fatalf("round %d: %v (%d of %d)", round, err, len(got), len(xs))
		}
		for i := range xs {
			if got[i] != xs[i] {
				t.Fatalf("round %d trade %d: %+v != %+v", round, i, got[i], xs[i])
			}
		}
	}
}

// fakeOKXTrades serves a daily trade file for one UTC+8 day and history-trades after it: one trade per
// minute (three at the same ms every 7th minute: one taker order over three prices), none every 10th.
type fakeOKXTrades struct {
	srv       *httptest.Server
	fileStart int64
	histFrom  int64
	pages     atomic.Int64
	zip       []byte
}

func tradesAt(m int64) []okx.Trade {
	k := m / minuteMs
	if k%10 == 0 {
		return nil
	}
	buy := k%2 == 0
	if k%7 == 0 {
		return []okx.Trade{{ID: k * 10, Px: 100, Sz: 1, Buy: buy, Ts: m + 30_000}, {ID: k*10 + 1, Px: 100.1, Sz: 2, Buy: buy, Ts: m + 30_000},
			{ID: k*10 + 2, Px: 100.2, Sz: 3, Buy: buy, Ts: m + 30_000}}
	}
	return []okx.Trade{{ID: k * 10, Px: 100, Sz: 2.5, Buy: buy, Ts: m + 30_000}}
}

func newFakeOKXTrades(t *testing.T, id string, now int64) *fakeOKXTrades {
	nowMin := now / minuteMs * minuteMs
	f := &fakeOKXTrades{}
	f.fileStart = (nowMin+8*hourMs)/utc8DayMs*utc8DayMs - 8*hourMs - utc8DayMs // yesterday (UTC+8), published
	f.histFrom = f.fileStart + utc8DayMs
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create(id + "-trades.csv")
	fmt.Fprintln(w, "instrument_name,trade_id,side,price,size,created_time,source")
	for m := f.fileStart; m < f.histFrom; m += minuteMs {
		for _, x := range tradesAt(m) {
			fmt.Fprintf(w, "%s,%d,%s,%g,%g,%d,0\n", id, x.ID, map[bool]string{true: "buy", false: "sell"}[x.Buy], x.Px, x.Sz, x.Ts)
		}
	}
	zw.Close()
	f.zip = zb.Bytes()
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/file.zip":
			w.Write(f.zip)
		case "/public/market-data-history":
			if q.Get("instType") != "SPOT" || q.Get("instIdList") != id {
				t.Errorf("trade files asked for %v", q)
			}
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "data": []any{map[string]any{"details": []any{map[string]any{
				"groupDetails": []any{map[string]string{"dateTs": fmt.Sprint(f.fileStart), "filename": id + "-trades-d.zip", "sizeMB": "1", "url": f.srv.URL + "/file.zip"}}}}}}})
		case "/market/history-trades":
			f.pages.Add(1)
			after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
			var data []map[string]string
			for m := now / minuteMs * minuteMs; m >= f.histFrom && len(data) < 100; m -= minuteMs {
				xs := tradesAt(m)
				for i := len(xs) - 1; i >= 0; i-- { // newest first
					x := xs[i]
					if (q.Get("type") == "1" && x.ID >= after) || (q.Get("type") == "2" && x.Ts >= after) || x.Ts >= now {
						continue
					}
					if len(data) < 100 {
						data = append(data, map[string]string{"tradeId": fmt.Sprint(x.ID), "px": fmt.Sprint(x.Px), "sz": fmt.Sprint(x.Sz),
							"side": map[bool]string{true: "buy", false: "sell"}[x.Buy], "ts": fmt.Sprint(x.Ts)})
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newTapeEngine(t *testing.T, base string) *Engine {
	st := openStore(t)
	tp, err := store.OpenTape(filepath.Join(t.TempDir(), "tape.db"))
	if err != nil {
		t.Fatal(err)
	}
	rest := okx.NewREST()
	rest.BaseURL = base
	e := New(Config{Threshold: 1, TopN: 10}, rest, st)
	e.SetTape(tp, 100)
	return e
}

// A query over a window nobody recorded is backfilled from the daily file plus history-trades:
// every closed minute covered, the trades exactly what OKX served (taker orders grouped), and a
// second query does not download again.
func TestLoadTapeBackfill(t *testing.T) {
	id := "X-USDT"
	now := time.Now().UnixMilli()
	f := newFakeOKXTrades(t, id, now)
	e := newTapeEngine(t, f.srv.URL)
	from := f.fileStart + 6*hourMs
	d, err := e.LoadTape(context.Background(), id, from, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Market != "spot" || d.CtVal != 1 {
		t.Fatalf("market %s ctVal %v", d.Market, d.CtVal)
	}
	c := d.Coverage
	if c.Pct < 99 || c.Covered < c.Minutes-3 || c.Backfill == "" {
		t.Fatalf("coverage %+v", c)
	}
	for _, g := range c.Gaps { // only the newest minutes (not closed yet, no live recording) may be missing
		if g[0] < now-4*minuteMs {
			t.Fatalf("gap %v in the middle (coverage %+v)", g, c)
		}
	}
	// the trades: exactly the generator's in the covered minutes
	want := 0
	for m := from / minuteMs * minuteMs; m < now/minuteMs*minuteMs; m += minuteMs { // the forming minute is not part of coverage
		covered := true
		for _, g := range c.Gaps {
			if m >= g[0] && m < g[1] {
				covered = false
			}
		}
		if covered {
			want += len(tradesAt(m))
		}
	}
	if len(d.Trades) != want {
		t.Fatalf("%d trades, want %d", len(d.Trades), want)
	}
	os := groupOrders(d.Trades, 1)
	sweeps := 0
	for _, o := range os {
		if o.Levels == 3 {
			sweeps++
			if o.Qty != 6 || math.Abs(o.Px-(100*1+100.1*2+100.2*3)/6) > 1e-9 {
				t.Fatalf("3-level order %+v", o)
			}
		}
	}
	if sweeps == 0 {
		t.Fatal("three fills at one ms were not grouped into one taker order")
	}
	pages := f.pages.Load()
	if _, err := e.LoadTape(context.Background(), id, from, now-5*minuteMs, true); err != nil {
		t.Fatal(err)
	}
	if f.pages.Load() != pages {
		t.Fatalf("second query downloaded again (%d → %d pages)", pages, f.pages.Load())
	}
}

// With the page cap reached, coverage says how much is missing and the minute holding the oldest
// fetched trade (possibly partial) is not stored.
func TestLoadTapeBackfillPageCap(t *testing.T) {
	old := tapePages
	tapePages = 2
	defer func() { tapePages = old }()
	id := "X-USDT"
	now := time.Now().UnixMilli()
	f := newFakeOKXTrades(t, id, now)
	e := newTapeEngine(t, f.srv.URL)
	from := now - 6*hourMs
	if from < f.histFrom {
		t.Skip("too close to UTC+8 midnight for this window")
	}
	d, err := e.LoadTape(context.Background(), id, from, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Coverage.Pct > 80 || d.Coverage.Covered == 0 || len(d.Coverage.Gaps) == 0 {
		t.Fatalf("coverage %+v", d.Coverage)
	}
	if want := "still missing"; !bytes.Contains([]byte(d.Coverage.Backfill), []byte(want)) {
		t.Fatalf("backfill note %q", d.Coverage.Backfill)
	}
	// every stored minute is complete: its trade count is the generator's
	rows, _ := e.tape.st.TapeRows(id, from, now)
	for _, r := range rows {
		if r.N != len(tradesAt(r.Ts)) {
			t.Fatalf("minute %d stored %d trades, has %d", r.Ts, r.N, len(tradesAt(r.Ts)))
		}
	}
}

// Live recording: trades go to their minute; a minute is stored only when subscribed for all of it;
// a reconnect makes the current minute incomplete.
func TestTapeLiveRecording(t *testing.T) {
	e := newTapeEngine(t, "http://127.0.0.1:1")
	id := "Y-USDT"
	e.syncTape(map[string]bool{id: true})
	b := e.tape.bufs[id]
	m0 := b.minute
	b.since = m0 + 30_000 // subscribed mid-minute (pinned: the wall clock may be anywhere in the minute)
	msg := func(ts int64, px, sz string, side string) okx.Message {
		raw, _ := json.Marshal([]map[string]string{{"instId": id, "tradeId": "1", "px": px, "sz": sz, "side": side, "ts": fmt.Sprint(ts)}})
		return okx.Message{Arg: okx.Arg{Channel: "trades", InstID: id}, Data: raw}
	}
	e.onTapeTrades(msg(m0+minuteMs+5, "1.5", "10", "buy"))
	e.onTapeTrades(msg(m0+2*minuteMs+5, "1.6", "3", "sell"))
	e.onTapeTrades(msg(m0-5, "1.4", "1", "buy")) // before the buffer: dropped
	e.tape.mu.Lock()
	rows := b.close(id, m0+3*minuteMs)
	e.tape.mu.Unlock()
	if len(rows) != 2 || rows[0].Ts != m0+minuteMs || rows[0].N != 1 || rows[1].N != 1 {
		t.Fatalf("rows %+v (first minute must be dropped: subscribed mid-minute)", rows)
	}
	// a reconnect: the minute it happens in is incomplete and dropped, the next one is stored
	e.tapeReconnect()
	e.tape.mu.Lock()
	since := b.since
	b.minute = since / minuteMs * minuteMs
	b.pending = []TapeTrade{{Ts: b.minute + 10, Px: 1, Sz: 1, Buy: true}}
	rows = b.close(id, b.minute+2*minuteMs)
	e.tape.mu.Unlock()
	if since <= time.Now().UnixMilli() || len(rows) != 1 || rows[0].Ts != since/minuteMs*minuteMs+minuteMs || rows[0].N != 0 {
		t.Fatalf("after a reconnect: since %d rows %+v", since, rows)
	}
}

// Retention: coins no longer recorded go first; RecentTape restores the grace members after a restart.
func TestTapeStoreRetention(t *testing.T) {
	tp, err := store.OpenTape(filepath.Join(t.TempDir(), "tape.db"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []store.TapeRow
	for _, id := range []string{"A-USDT-SWAP", "A-USDT", "B-USDT-SWAP"} {
		for m := int64(0); m < 10; m++ {
			rows = append(rows, store.TapeRow{InstID: id, Ts: m * minuteMs, N: 1, Data: []byte{1}})
		}
	}
	if err := tp.PutTape(rows); err != nil {
		t.Fatal(err)
	}
	tp.PutTape([]store.TapeRow{{InstID: "A-USDT", Ts: 0, N: 9}}) // existing minutes are never overwritten
	if r, _ := tp.TapeRows("A-USDT", 0, minuteMs); len(r) != 1 || r[0].N != 1 {
		t.Fatalf("overwrite: %+v", r)
	}
	n, err := tp.PruneExcept([]string{"A-USDT-SWAP", "A-USDT"}, 5*minuteMs)
	if err != nil || n != 5 {
		t.Fatalf("prune except: %d %v", n, err)
	}
	if r, _ := tp.TapeMinutes("B-USDT-SWAP", 0, 10*minuteMs); len(r) != 5 {
		t.Fatalf("B kept %d minutes", len(r))
	}
	if r, _ := tp.TapeMinutes("A-USDT", 0, 10*minuteMs); len(r) != 10 {
		t.Fatalf("A lost minutes: %d", len(r))
	}
	recent, _ := tp.RecentTape(8 * minuteMs)
	if len(recent) != 3 || recent["B-USDT-SWAP"] != 9*minuteMs {
		t.Fatalf("recent %v", recent)
	}
	if n, _ := tp.Prune(8*minuteMs, 0); n != 8+8+3 {
		t.Fatalf("prune: %d", n)
	}
}

// The deep set: pool ranks ≤ TapeTopN plus their spot pairs; a coin that drops out stays for the grace.
func TestTapeTargetsGrace(t *testing.T) {
	e := newTapeEngine(t, "http://127.0.0.1:1")
	var rows []Row
	for i := 1; i <= TapeTopN+5; i++ {
		id := fmt.Sprintf("C%02d-USDT-SWAP", i)
		in := newInst(id)
		in.SpotID = fmt.Sprintf("C%02d-USDT", i)
		e.insts[id] = in
		rows = append(rows, Row{InstID: id, Active: true, Rank: i})
	}
	e.snap.Store(&Snapshot{Rows: rows})
	now := int64(10 * hourMs)
	e.mu.RLock()
	tr, bk := e.tapeTargets(now)
	e.mu.RUnlock()
	if len(tr) != 2*TapeTopN || !tr["C01-USDT"] || tr[fmt.Sprintf("C%02d-USDT-SWAP", TapeTopN+1)] || len(bk) != 2*BookTopN {
		t.Fatalf("trades %d books %d", len(tr), len(bk))
	}
	// C01 drops out of the pool: still taped within the grace, gone after it
	rows[0].Active, rows[0].Rank = false, 0
	e.snap.Store(&Snapshot{Rows: rows})
	e.mu.RLock()
	tr, _ = e.tapeTargets(now + hourMs)
	e.mu.RUnlock()
	if !tr["C01-USDT-SWAP"] || !tr["C01-USDT"] {
		t.Fatal("dropped coin lost within the grace")
	}
	e.mu.RLock()
	tr, _ = e.tapeTargets(now + tapeGrace.Milliseconds() + hourMs)
	e.mu.RUnlock()
	if tr["C01-USDT-SWAP"] {
		t.Fatal("dropped coin kept after the grace")
	}
	// an agent request adds a coin outside the top for tapeReqFor
	e.RequestTape(fmt.Sprintf("C%02d-USDT-SWAP", TapeTopN+3))
	e.mu.RLock()
	tr, bk = e.tapeTargets(time.Now().UnixMilli())
	e.mu.RUnlock()
	if !tr[fmt.Sprintf("C%02d-USDT", TapeTopN+3)] || !bk[fmt.Sprintf("C%02d-USDT-SWAP", TapeTopN+3)] {
		t.Fatal("requested coin not taped / tracked")
	}
}

// A book stream reset ends every standing wall as lost (its fate is unknown).
func TestWallsLostOnReset(t *testing.T) {
	w := &wallTracker{books: map[string]*liveBook{}, wallCh: make(chan store.Wall, 10), minCh: make(chan store.BookMin, 10)}
	b := &liveBook{id: "X", ctVal: 1, bids: bookSide{}, asks: bookSide{}, ok: true, walls: map[wallKey]*store.Wall{}}
	b.walls[wallKey{true, 99}] = &store.Wall{InstID: "X", Bid: true, Px: 99, T0: 0, PeakUsd: 1e5}
	w.books["X"] = b
	w.resetAll()
	x := <-w.wallCh
	if x.Outcome != "lost" || b.ok || len(b.walls) != 0 {
		t.Fatalf("%+v ok=%v", x, b.ok)
	}
}
