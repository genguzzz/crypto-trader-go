package market

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// ---------- order-book walls: live books of the deep set, wall lifecycles, per-minute book shape ----------
//
// The OKX "books" channel (400 levels, incremental, own connection) is kept per tracked instrument
// (swap and spot). Every second each book is scanned for walls — levels within ±wallBandPct of mid
// at least wallMult × the median level size and at least a floor scaled to the instrument's 24h
// turnover. A wall's life is followed by price level: first / peak / end size, peak order count,
// distance to mid, and how much was traded against that exact price (taker sells into a bid wall,
// taker buys into an ask wall). When it shrinks below half the threshold it closes as
//   filled  — trades at its price account for ≥60% of the shrink (real interest, absorbed or consumed)
//   pulled  — trades account for ≤25% (cancelled; pulled while price approached = spoof-like)
//   mixed   — in between
//   lost    — the book stream broke while it was alive
// A wall appearing within wallChainMs of a pulled one on the same side with similar size continues
// that one's chain (a wall that keeps moving with price). Per-minute averages of depth within
// ±0.5/1/2% and wall totals are stored as book_1m.

const (
	wallBandPct  = 3.0
	wallMult     = 8.0
	wallMinUsd   = 2000.0
	wallTurnFrac = 3e-4 // floor: this share of 24h USDT turnover
	wallMinLife  = 5_000
	wallChainMs  = 10_000
)

type bookSide map[float64][2]float64 // px → sz, orders

type liveBook struct {
	id         string
	ctVal      float64
	bids, asks bookSide
	seq        int64
	ok         bool  // synced (snapshot applied, no gap since)
	at         int64 // last update ms
	walls      map[wallKey]*store.Wall
	acc        bookAcc
}

type wallKey struct {
	bid bool
	px  float64
}

// bookAcc accumulates one minute of per-second samples.
type bookAcc struct {
	minute int64
	n      int
	sum    store.BookMin
}

type wallTracker struct {
	e      *Engine
	ws     *okx.WS
	mu     sync.Mutex
	books  map[string]*liveBook
	recent []store.Wall // closed in the last wallChainMs (chain lookup)
	seq    int64
	wallCh chan store.Wall
	minCh  chan store.BookMin
}

func newWallTracker(e *Engine) *wallTracker {
	w := &wallTracker{e: e, books: map[string]*liveBook{}, wallCh: make(chan store.Wall, 5000), minCh: make(chan store.BookMin, 5000)}
	w.ws = okx.NewWS("books", okx.PublicWSURL)
	w.ws.OnMessage = w.onMessage
	w.ws.OnConnect = func(time.Time) { w.resetAll() }
	return w
}

func (w *wallTracker) run(ctx context.Context) {
	go w.ws.Run(ctx)
	go w.writer(ctx)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.scanAll(now.UnixMilli())
		}
	}
}

// setTargets subscribes the books of ids (swap and spot; value = contract value, 1 for spot).
func (w *wallTracker) setTargets(ids map[string]float64) {
	var args []okx.Arg
	now := time.Now().UnixMilli()
	w.mu.Lock()
	for id, ctVal := range ids {
		args = append(args, okx.Arg{Channel: "books", InstID: id})
		if w.books[id] == nil {
			w.books[id] = &liveBook{id: id, ctVal: ctVal, walls: map[wallKey]*store.Wall{}}
		}
	}
	for id, b := range w.books {
		if _, ok := ids[id]; !ok {
			w.closeAll(b, now, "lost")
			delete(w.books, id)
		}
	}
	w.mu.Unlock()
	w.ws.SetSubscriptions(args)
}

func (w *wallTracker) resetAll() {
	now := time.Now().UnixMilli()
	w.mu.Lock()
	for _, b := range w.books {
		b.ok = false
		w.closeAll(b, now, "lost")
	}
	w.mu.Unlock()
}

type wsBook struct {
	Asks      [][]string `json:"asks"`
	Bids      [][]string `json:"bids"`
	Ts        string     `json:"ts"`
	SeqID     int64      `json:"seqId"`
	PrevSeqID int64      `json:"prevSeqId"`
}

func (w *wallTracker) onMessage(m okx.Message) {
	var xs []wsBook
	if json.Unmarshal(m.Data, &xs) != nil || len(xs) == 0 {
		return
	}
	x := xs[0]
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.books[m.Arg.InstID]
	if b == nil {
		return
	}
	if m.Action == "snapshot" {
		b.bids, b.asks = bookSide{}, bookSide{}
		b.ok = true
	} else if !b.ok || (x.PrevSeqID != b.seq && x.PrevSeqID != -1) {
		if b.ok { // sequence gap: resync
			b.ok = false
			w.closeAll(b, time.Now().UnixMilli(), "lost")
			go w.ws.Resubscribe(okx.Arg{Channel: "books", InstID: b.id})
		}
		return
	}
	apply := func(s bookSide, lv [][]string) {
		for _, l := range lv {
			if len(l) < 2 {
				continue
			}
			px, sz := atof(l[0]), atof(l[1])
			if sz <= 0 {
				delete(s, px)
				continue
			}
			s[px] = [2]float64{sz, atof(l[len(l)-1])}
		}
	}
	apply(b.bids, x.Bids)
	apply(b.asks, x.Asks)
	b.seq = x.SeqID
	b.at = atoi(x.Ts)
}

// onTrades credits trades at a wall's exact price to that wall.
func (w *wallTracker) onTrades(id string, xs []TapeTrade) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.books[id]
	if b == nil || len(b.walls) == 0 {
		return
	}
	for _, t := range xs {
		if wl := b.walls[wallKey{bid: !t.Buy, px: t.Px}]; wl != nil { // a taker sell hits bids
			wl.FilledUsd += t.Px * t.Sz * b.ctVal
		}
	}
}

type lvl struct{ px, usd, n float64 }

func (b *liveBook) sorted() (bids, asks []lvl) {
	for px, v := range b.bids {
		bids = append(bids, lvl{px, px * v[0] * b.ctVal, v[1]})
	}
	for px, v := range b.asks {
		asks = append(asks, lvl{px, px * v[0] * b.ctVal, v[1]})
	}
	sort.Slice(bids, func(i, j int) bool { return bids[i].px > bids[j].px })
	sort.Slice(asks, func(i, j int) bool { return asks[i].px < asks[j].px })
	return
}

func (w *wallTracker) scanAll(now int64) {
	floors := map[string]float64{}
	w.mu.Lock()
	ids := make([]string, 0, len(w.books))
	for id := range w.books {
		ids = append(ids, id)
	}
	w.mu.Unlock()
	for _, id := range ids {
		floors[id] = w.e.turnover24h(id) * wallTurnFrac
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, b := range w.books {
		if b.ok && now-b.at < 30_000 {
			w.scan(b, now, floors[id])
		}
	}
}

// wallThreshold is the size a level needs to count as a wall: wallMult × the median level within
// the band, at least wallMinUsd and the turnover floor.
func wallThreshold(bids, asks []lvl, mid, floor float64) float64 {
	var xs []float64
	for _, side := range [][]lvl{bids, asks} {
		for _, l := range side {
			if math.Abs(l.px/mid-1)*100 <= wallBandPct {
				xs = append(xs, l.usd)
			}
		}
	}
	if len(xs) == 0 {
		return math.Inf(1)
	}
	sort.Float64s(xs)
	return max(wallMult*xs[len(xs)/2], wallMinUsd, floor)
}

func (w *wallTracker) scan(b *liveBook, now int64, floor float64) {
	bids, asks := b.sorted()
	if len(bids) == 0 || len(asks) == 0 {
		return
	}
	mid := (bids[0].px + asks[0].px) / 2
	thr := wallThreshold(bids, asks, mid, floor)
	seen := map[wallKey]lvl{}
	for _, side := range []struct {
		bid bool
		xs  []lvl
	}{{true, bids}, {false, asks}} {
		for _, l := range side.xs {
			if math.Abs(l.px/mid-1)*100 > wallBandPct {
				break
			}
			k := wallKey{side.bid, l.px}
			if l.usd >= thr || (b.walls[k] != nil && l.usd >= thr/2) {
				seen[k] = l
			}
		}
	}
	dist := func(px float64) float64 { return math.Abs(px/mid-1) * 1e4 }
	for k, wl := range b.walls {
		l, ok := seen[k]
		if !ok {
			cur := 0.0
			if v, ok := b.bids[k.px]; ok && k.bid {
				cur = k.px * v[0] * b.ctVal
			} else if v, ok := b.asks[k.px]; ok && !k.bid {
				cur = k.px * v[0] * b.ctVal
			}
			wl.EndUsd, wl.EndDistBps = cur, dist(k.px)
			w.close(b, k, now, "")
			continue
		}
		wl.EndUsd, wl.EndDistBps = l.usd, dist(k.px)
		wl.MinDistBps = min(wl.MinDistBps, wl.EndDistBps)
		if l.usd > wl.PeakUsd {
			wl.PeakUsd = l.usd
		}
		wl.PeakOrders = max(wl.PeakOrders, int(l.n))
		wl.T1 = now
	}
	for k, l := range seen {
		if b.walls[k] != nil {
			continue
		}
		w.seq++
		id := now*1000 + w.seq%1000
		wl := &store.Wall{ID: id, InstID: b.id, Bid: k.bid, Px: k.px, T0: now, T1: now, FirstUsd: l.usd, PeakUsd: l.usd,
			EndUsd: l.usd, PeakOrders: int(l.n), Dist0Bps: dist(k.px), MinDistBps: dist(k.px), EndDistBps: dist(k.px), Chain: id}
		for i := len(w.recent) - 1; i >= 0; i-- { // continues a wall pulled a moment ago?
			r := w.recent[i]
			if r.InstID == b.id && r.Bid == k.bid && r.Px != k.px && now-r.T1 <= wallChainMs && r.Outcome == "pulled" &&
				l.usd >= r.PeakUsd*0.7 && l.usd <= r.PeakUsd*1.3 {
				wl.Chain = r.Chain
				break
			}
		}
		b.walls[k] = wl
	}
	w.sample(b, now, bids, asks, mid)
}

// close ends a wall and queues it (outcome "" = decide from fills).
func (w *wallTracker) close(b *liveBook, k wallKey, now int64, outcome string) {
	wl := b.walls[k]
	delete(b.walls, k)
	wl.T1 = now
	if outcome == "" {
		outcome = wallOutcome(wl.PeakUsd-wl.EndUsd, wl.FilledUsd)
	}
	wl.Outcome = outcome
	if wl.T1-wl.T0 < wallMinLife && wl.FilledUsd == 0 {
		return // flicker
	}
	w.recent = append(w.recent, *wl)
	for len(w.recent) > 0 && now-w.recent[0].T1 > wallChainMs {
		w.recent = w.recent[1:]
	}
	select {
	case w.wallCh <- *wl:
	default:
	}
}

func wallOutcome(shrink, filled float64) string {
	switch {
	case shrink <= 0 || filled >= 0.6*shrink:
		return "filled"
	case filled <= 0.25*shrink:
		return "pulled"
	}
	return "mixed"
}

func (w *wallTracker) closeAll(b *liveBook, now int64, outcome string) {
	for k := range b.walls {
		w.close(b, k, now, outcome)
	}
}

// sample adds this second's book shape to the minute accumulator (flushing the previous minute).
func (w *wallTracker) sample(b *liveBook, now int64, bids, asks []lvl, mid float64) {
	m := now / minuteMs * minuteMs
	if b.acc.minute != m {
		if b.acc.n > 0 {
			n := float64(b.acc.n)
			s := b.acc.sum
			out := store.BookMin{InstID: b.id, Ts: b.acc.minute, Mid: s.Mid, SpreadBps: s.SpreadBps / n,
				Bid05: s.Bid05 / n, Ask05: s.Ask05 / n, Bid1: s.Bid1 / n, Ask1: s.Ask1 / n, Bid2: s.Bid2 / n, Ask2: s.Ask2 / n,
				WallBid: s.WallBid / n, WallAsk: s.WallAsk / n, MaxBid: s.MaxBid, MaxAsk: s.MaxAsk, Cover: s.Cover / n}
			select {
			case w.minCh <- out:
			default:
			}
		}
		b.acc = bookAcc{minute: m}
	}
	s := &b.acc.sum
	b.acc.n++
	s.Mid = mid
	s.SpreadBps += (asks[0].px - bids[0].px) / mid * 1e4
	depth := func(xs []lvl, pct float64) float64 {
		t := 0.0
		for _, l := range xs {
			if math.Abs(l.px/mid-1)*100 > pct {
				break
			}
			t += l.usd
		}
		return t
	}
	s.Bid05 += depth(bids, 0.5)
	s.Ask05 += depth(asks, 0.5)
	s.Bid1 += depth(bids, 1)
	s.Ask1 += depth(asks, 1)
	s.Bid2 += depth(bids, 2)
	s.Ask2 += depth(asks, 2)
	s.Cover += min(mid/bids[len(bids)-1].px-1, asks[len(asks)-1].px/mid-1) * 100
	for k, wl := range b.walls {
		if k.bid {
			s.WallBid += wl.EndUsd
			s.MaxBid = max(s.MaxBid, wl.EndUsd)
		} else {
			s.WallAsk += wl.EndUsd
			s.MaxAsk = max(s.MaxAsk, wl.EndUsd)
		}
	}
}

func (w *wallTracker) writer(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var walls []store.Wall
	var mins []store.BookMin
	flush := func() {
		if err := w.e.tape.st.PutWalls(walls); err != nil {
			log.Printf("walls: write: %v", err)
		}
		if err := w.e.tape.st.PutBookMins(mins); err != nil {
			log.Printf("book_1m: write: %v", err)
		}
		walls, mins = walls[:0], mins[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case x := <-w.wallCh:
			walls = append(walls, x)
		case x := <-w.minCh:
			mins = append(mins, x)
		case <-t.C:
			flush()
		}
	}
}

// ActiveWalls returns the walls standing now on a tracked book (nil if not tracked) and whether
// the book is tracked and in sync.
func (w *wallTracker) ActiveWalls(id string) ([]store.Wall, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.books[id]
	if b == nil || !b.ok {
		return nil, false
	}
	out := make([]store.Wall, 0, len(b.walls))
	for _, wl := range b.walls {
		x := *wl
		x.Outcome = "active"
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EndUsd > out[j].EndUsd })
	return out, true
}

// turnover24h is the 24h USDT turnover of a swap or spot id (0 if unknown).
func (e *Engine) turnover24h(id string) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if t, ok := e.tickers[id]; ok {
		return t.VolCcy24h * t.Last
	}
	if t, ok := e.spotTickers[id]; ok {
		return t.VolCcy24h
	}
	return 0
}
