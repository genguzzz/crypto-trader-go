package market

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

func TestTapeCodecRoundTrip(t *testing.T) {
	m := int64(1_793_376_000_000)
	in := []TapeTrade{
		{m + 84, 0.18975, 1, true}, {m + 414, 0.18976, 2.5, false}, {m + 414, 0.18974, 1455, false},
		{m + 59_999, 0.00001234, 123456789.12, true}, {m + 59_999, 104523.7, 0.01, false},
	}
	b := encodeMinute(m, in)
	out, err := decodeMinute(m, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d trades", len(out))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("trade %d: got %+v want %+v", i, out[i], in[i])
		}
	}
	if x, _ := decodeMinute(m, nil, nil); len(x) != 0 {
		t.Error("empty minute must decode to nothing")
	}
}

func TestTapeBufClose(t *testing.T) {
	m := int64(1_000 * minuteMs)
	b := &tapeBuf{minute: m, since: m + 2000} // subscribed mid-minute: first minute incomplete
	b.pending = []TapeTrade{{m + 5000, 1, 1, true}, {m + minuteMs + 10, 2, 1, false}, {m + 3*minuteMs + 1, 3, 1, true}}
	rows := b.close("X", m+3*minuteMs)
	if len(rows) != 2 || rows[0].Ts != m+minuteMs || rows[0].N != 1 || rows[1].Ts != m+2*minuteMs || rows[1].N != 0 || rows[1].Data != nil {
		t.Fatalf("rows %+v", rows)
	}
	if len(b.pending) != 1 || b.minute != m+3*minuteMs {
		t.Fatalf("left %+v at %d", b.pending, b.minute)
	}
}

// synthetic market: background noise both ways plus a sell ladder of 11 identical clips.
func dumpTape() TapeData {
	var xs []TapeTrade
	t0 := int64(1_793_376_000_000)
	px := 0.19
	for i := 0; i < 3000; i++ { // noise: 2-25 contracts each way
		ts := t0 + int64(i)*20_000
		xs = append(xs, TapeTrade{ts, px, float64(2 + i%23), i%2 == 0})
	}
	for k := 0; k < 11; k++ { // the ladder: 6237 contracts every 2 min, price stepping down
		ts := t0 + 10*hourMs + int64(k)*120_000 + 7
		p := 0.1905 - float64(k)*0.0003
		xs = append(xs, TapeTrade{ts, p, 3000, false}, TapeTrade{ts, p - 0.00001, 3237, false}) // one order, two levels
	}
	sortTrades(xs)
	return TapeData{InstID: "ENA-USDT-SWAP", Market: "swap", CtVal: 10, Trades: xs}
}

func sortTrades(xs []TapeTrade) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j].Ts < xs[j-1].Ts; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func TestClipLadderAndAssess(t *testing.T) {
	d := dumpTape()
	q := TapeQuery{From: d.Trades[0].Ts, To: d.Trades[len(d.Trades)-1].Ts + 1, Sections: map[string]bool{"summary": true, "sizes": true, "clips": true, "sweeps": true, "large": true},
		ClipMin: 5, ClipGapMin: 30, Top: 5}
	s := TapeStatsFor(d, q)
	if len(s.Clips) == 0 {
		t.Fatal("no clip run found")
	}
	c := s.Clips[0]
	if c.Side != "sell" || c.Qty != 6237 || c.Count != 11 || c.DriftBps > -100 || c.WithTrendPct < 90 || c.Pattern != "clockwork" {
		t.Fatalf("clip %+v", c)
	}
	if s.Sweeps != nil && s.Sweeps.SellN != 0 {
		t.Fatalf("two-level orders are not sweeps: %+v", s.Sweeps)
	}
	if s.Summary.LargeSellUsd <= 0 || s.Summary.LargeDeltaPct >= 0 {
		t.Fatalf("large sells not seen: %+v", s.Summary)
	}
	a := assess(s, nil, nil, nil, nil, nil, nil)
	if a.Dump.Level < 1 || !strings.Contains(strings.Join(a.Dump.Evidence, "|"), "同尺寸卖单") {
		t.Fatalf("dump verdict %+v", a.Dump)
	}
	if b, _ := json.Marshal(s); len(b) > 12000 {
		t.Fatalf("payload %d bytes", len(b))
	}
}

func TestWashPairs(t *testing.T) {
	var os []TapeOrder
	for i := 0; i < 20; i++ {
		ts := int64(i) * 60_000
		os = append(os, TapeOrder{Ts: ts, Buy: true, Px: 1, Usd: 1234.5, Qty: 1234.5}, TapeOrder{Ts: ts + 800, Buy: false, Px: 1.0001, Usd: 1234.5, Qty: 1234.5})
		os = append(os, TapeOrder{Ts: ts + 30_000, Buy: true, Px: 1, Usd: 1000, Qty: 1000}, TapeOrder{Ts: ts + 30_500, Buy: false, Px: 1, Usd: 1000, Qty: 1000}) // round: ignored
	}
	w := washPairs(os, 200)
	if w.Pairs != 20 || math.Abs(w.Usd-20*2469) > 1 {
		t.Fatalf("wash %+v", w)
	}
}

func TestCrossLeader(t *testing.T) {
	// spot moves first, swap copies 10 s later
	var sp, sw []TapeOrder
	px := 100.0
	t0 := int64(0)
	path := []float64{}
	for i := 0; i < 2000; i++ {
		px *= 1 + 0.001*math.Sin(float64(i)*1.7)*math.Cos(float64(i)*0.31)
		path = append(path, px)
	}
	for i, p := range path {
		ts := t0 + int64(i)*5000
		sp = append(sp, TapeOrder{Ts: ts + 100, Buy: true, Px: p, Px0: p, Px1: p, Usd: 100})
		if i >= 2 {
			q := path[i-2]
			sw = append(sw, TapeOrder{Ts: ts + 200, Buy: false, Px: q, Px0: q, Px1: q, Usd: 1000})
		}
	}
	c := crossStats(&TapeStats{orders: sw}, &TapeStats{orders: sp})
	if c == nil || c.Leader != "spot" || c.LeadLagSec != 10 {
		t.Fatalf("cross %+v", c)
	}
	if math.Abs(c.SpotSharePct-float64(100*2000)/float64(100*2000+1000*1998)*100) > 0.1 {
		t.Fatalf("share %v", c.SpotSharePct)
	}
}

func TestBookShapeWalls(t *testing.T) {
	b := okx.Book{}
	for i := 0; i < 200; i++ {
		b.Bids = append(b.Bids, okx.BookLevel{Px: 100 - 0.01*float64(i+1), Sz: 10, N: 3})
		b.Asks = append(b.Asks, okx.BookLevel{Px: 100 + 0.01*float64(i+1), Sz: 10, N: 3})
	}
	b.Bids[50] = okx.BookLevel{Px: b.Bids[50].Px, Sz: 500, N: 1} // one big player 0.51% below
	s := bookShape(b, 1, BookQuery{BandPct: 3, Bands: []float64{0.5, 1}, WallMult: 6, Top: 3})
	if len(s.Walls) != 1 || s.Walls[0].Side != "bid" || s.Walls[0].Orders != 1 || s.Walls[0].XMedian < 40 {
		t.Fatalf("walls %+v", s.Walls)
	}
	if s.Bands[1].Imb <= 0 || math.Abs(s.Bands[0].BidUsd/s.Bands[0].AskUsd-1) > 0.05 {
		t.Fatalf("bands %+v", s.Bands)
	}
}

func TestWallTrackerFilledAndPulled(t *testing.T) {
	w := &wallTracker{books: map[string]*liveBook{}, wallCh: make(chan store.Wall, 10), minCh: make(chan store.BookMin, 10)}
	b := &liveBook{id: "X-USDT", ctVal: 1, bids: bookSide{}, asks: bookSide{}, ok: true, walls: map[wallKey]*store.Wall{}}
	w.books[b.id] = b
	for i := 1; i <= 50; i++ {
		b.bids[100-0.1*float64(i)] = [2]float64{10, 2}
		b.asks[100+0.1*float64(i)] = [2]float64{10, 2}
	}
	bidWall, askWall := 100-0.1*5, 100+0.1*8
	b.bids[bidWall] = [2]float64{400, 1}
	b.asks[askWall] = [2]float64{400, 1}
	now := int64(1_000_000)
	w.scan(b, now, 0)
	if len(b.walls) != 2 {
		t.Fatalf("walls %+v", b.walls)
	}
	// the bid wall gets eaten by taker sells; the ask wall is cancelled as price comes closer
	w.onTrades(b.id, []TapeTrade{{now + 1000, bidWall, 380, false}})
	b.bids[bidWall] = [2]float64{20, 1}
	delete(b.asks, askWall)
	for i := 1; i <= 7; i++ { // asks below it are lifted and bids follow: price moves up toward it
		delete(b.asks, 100+0.1*float64(i))
		b.bids[100+0.1*float64(i-1)] = [2]float64{10, 2}
	}
	w.scan(b, now+60_000, 0)
	got := map[bool]store.Wall{}
	for len(w.wallCh) > 0 {
		x := <-w.wallCh
		got[x.Bid] = x
	}
	if got[true].Outcome != "filled" || got[false].Outcome != "pulled" {
		t.Fatalf("outcomes bid %+v ask %+v", got[true], got[false])
	}
	if !pulledNear(got[false]) {
		t.Fatalf("ask wall should count as pulled near price: %+v", got[false])
	}
}

func TestSizeBucketsAuto(t *testing.T) {
	var os []TapeOrder
	var sizes []float64
	vol := 0.0
	for i := 1; i <= 1000; i++ {
		u := math.Pow(10, 2+float64(i)/250) // 100 … 1M, heavy tail like real order sizes
		vol += u
		os = append(os, TapeOrder{Usd: u, Buy: i%3 == 0})
		sizes = append(sizes, u)
	}
	bs := sizeBuckets(os, nil, sizes, vol)
	if len(bs) < 4 || bs[0].Label != "<p50" {
		t.Fatalf("buckets %+v", bs)
	}
	n := 0
	for _, b := range bs {
		n += b.BuyN + b.SellN
	}
	if n != 1000 {
		t.Fatalf("lost orders: %d", n)
	}
}
