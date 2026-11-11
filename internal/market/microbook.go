package market

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// ---------- live book shape (swap and spot, 5000 levels with order counts) ----------

// BookQuery tunes BookShapeFor.
type BookQuery struct {
	BandPct   float64   // walls and the distance histogram within ±this % of mid
	Bands     []float64 // depth bands, % from mid
	BucketBps float64   // histogram bucket width; 0 = auto (≈ 20 buckets per side)
	WallMult  float64   // wall = level ≥ this × the median level in the band
	MinWall   float64   // and ≥ this USDT
	Top       int       // walls per side
}

type BookBand struct {
	Pct       float64 `json:"pct"`
	BidUsd    float64 `json:"bidUsd"`
	AskUsd    float64 `json:"askUsd"`
	Imb       float64 `json:"imb"` // (bid − ask) / (bid + ask)
	BidOrders int     `json:"bidOrders"`
	AskOrders int     `json:"askOrders"`
}

type BookBucket struct {
	FromBps float64 `json:"fromBps"` // distance from mid, bps
	BidUsd  float64 `json:"bidUsd"`
	AskUsd  float64 `json:"askUsd"`
	BidN    int     `json:"bidOrders"`
	AskN    int     `json:"askOrders"`
}

type BookWall struct {
	Side      string  `json:"side"`
	Px        float64 `json:"px"`
	DistPct   float64 `json:"distPct"`
	Usd       float64 `json:"usd"`
	Orders    int     `json:"orders"`
	AvgOrder  float64 `json:"avgOrderUsd"`
	XMedian   float64 `json:"xMedian"`
	AgeMin    float64 `json:"ageMin,omitempty"`    // how long it has stood (tracked books only)
	FilledUsd float64 `json:"filledUsd,omitempty"` // traded against it so far (tracked books only)
}

// BookShape is one market's book now.
type BookShape struct {
	InstID        string       `json:"instId"`
	Market        string       `json:"market"`
	Ts            int64        `json:"ts"`
	Mid           float64      `json:"mid"`
	SpreadBps     float64      `json:"spreadBps"`
	Levels        [2]int       `json:"levels"`   // bid, ask levels fetched
	CoverPct      [2]float64   `json:"coverPct"` // how far each side reaches
	Bands         []BookBand   `json:"bands"`
	Buckets       []BookBucket `json:"buckets,omitempty"`
	MedianLevel   float64      `json:"medianLevelUsd"`
	AvgOrder      [2]float64   `json:"avgOrderUsd"`   // bid, ask (within the band)
	Top10Share    [2]float64   `json:"top10SharePct"` // % of band depth in the 10 largest levels, bid / ask
	Walls         []BookWall   `json:"walls"`
	Turnover24h   float64      `json:"turnover24h"`
	Depth2PctTurn float64      `json:"depth2PctOfTurnover"` // (bid+ask within ±2%) / 24h turnover, %
	Tracked       bool         `json:"tracked"`             // walls carry age (live book tracked)
}

var rawBookCache = struct {
	sync.Mutex
	m map[string]rawBook
}{m: map[string]rawBook{}}

type rawBook struct {
	b  okx.Book
	at time.Time
}

func (e *Engine) rawBook(ctx context.Context, id string) (okx.Book, error) {
	rawBookCache.Lock()
	if c, ok := rawBookCache.m[id]; ok && time.Since(c.at) < time.Second {
		rawBookCache.Unlock()
		return c.b, nil
	}
	for k, c := range rawBookCache.m {
		if time.Since(c.at) > time.Minute {
			delete(rawBookCache.m, k)
		}
	}
	rawBookCache.Unlock()
	b, err := e.rest.Books(ctx, id, bookDepth)
	if err != nil {
		return b, err
	}
	rawBookCache.Lock()
	rawBookCache.m[id] = rawBook{b, time.Now()}
	rawBookCache.Unlock()
	return b, nil
}

// BookShapeFor fetches and analyses the current book of a swap or spot id.
func (e *Engine) BookShapeFor(ctx context.Context, id string, q BookQuery) (*BookShape, error) {
	ctVal, market := 1.0, "spot"
	if in := e.instFor(id); in != nil && id == in.ID {
		e.mu.RLock()
		ctVal, market = in.CtVal, "swap"
		e.mu.RUnlock()
	}
	b, err := e.rawBook(ctx, id)
	if err != nil {
		return nil, err
	}
	s := bookShape(b, ctVal, q)
	s.InstID, s.Market = id, market
	s.Turnover24h = math.Round(e.turnover24h(id))
	if s.Turnover24h > 0 && len(s.Bands) > 0 {
		for _, bd := range s.Bands {
			if bd.Pct == 2 {
				s.Depth2PctTurn = round2((bd.BidUsd + bd.AskUsd) / s.Turnover24h * 100)
			}
		}
	}
	if e.walls != nil {
		if act, ok := e.walls.ActiveWalls(id); ok {
			s.Tracked = true
			now := time.Now().UnixMilli()
			for i := range s.Walls {
				w := &s.Walls[i]
				for _, a := range act {
					if a.Px == w.Px && a.Bid == (w.Side == "bid") {
						w.AgeMin = math.Round(float64(now-a.T0)/6e4*10) / 10
						w.FilledUsd = math.Round(a.FilledUsd)
					}
				}
			}
		}
	}
	return s, nil
}

func bookShape(b okx.Book, ctVal float64, q BookQuery) *BookShape {
	s := &BookShape{Ts: b.Ts, Levels: [2]int{len(b.Bids), len(b.Asks)}}
	if len(b.Bids) == 0 || len(b.Asks) == 0 {
		return s
	}
	mid := (b.Bids[0].Px + b.Asks[0].Px) / 2
	s.Mid = mid
	s.SpreadBps = round2((b.Asks[0].Px - b.Bids[0].Px) / mid * 1e4)
	s.CoverPct = [2]float64{round2((1 - b.Bids[len(b.Bids)-1].Px/mid) * 100), round2((b.Asks[len(b.Asks)-1].Px/mid - 1) * 100)}
	usd := func(l okx.BookLevel) float64 { return l.Px * l.Sz * ctVal }
	dist := func(l okx.BookLevel) float64 { return math.Abs(l.Px/mid-1) * 100 }
	for _, pct := range q.Bands {
		bd := BookBand{Pct: pct}
		for _, l := range b.Bids {
			if dist(l) > pct {
				break
			}
			bd.BidUsd += usd(l)
			bd.BidOrders += l.N
		}
		for _, l := range b.Asks {
			if dist(l) > pct {
				break
			}
			bd.AskUsd += usd(l)
			bd.AskOrders += l.N
		}
		if t := bd.BidUsd + bd.AskUsd; t > 0 {
			bd.Imb = round2((bd.BidUsd - bd.AskUsd) / t)
		}
		bd.BidUsd, bd.AskUsd = math.Round(bd.BidUsd), math.Round(bd.AskUsd)
		s.Bands = append(s.Bands, bd)
	}
	// band levels
	var all []float64
	var sides [2][]okx.BookLevel
	for i, side := range [][]okx.BookLevel{b.Bids, b.Asks} {
		for _, l := range side {
			if dist(l) > q.BandPct {
				break
			}
			sides[i] = append(sides[i], l)
			all = append(all, usd(l))
		}
	}
	sort.Float64s(all)
	med := quantile(all, 0.5)
	s.MedianLevel = math.Round(med)
	for i, side := range sides {
		var tot float64
		var n int
		xs := make([]float64, 0, len(side))
		for _, l := range side {
			tot += usd(l)
			n += l.N
			xs = append(xs, usd(l))
		}
		if n > 0 {
			s.AvgOrder[i] = math.Round(tot / float64(n))
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(xs)))
		top := 0.0
		for _, x := range xs[:min(10, len(xs))] {
			top += x
		}
		if tot > 0 {
			s.Top10Share[i] = math.Round(top / tot * 100)
		}
	}
	// distance histogram
	bw := q.BucketBps
	if bw <= 0 {
		bw = max(1, nice(q.BandPct*100/20))
	}
	nb := int(math.Ceil(q.BandPct * 100 / bw))
	if nb > 0 && nb <= 400 {
		s.Buckets = make([]BookBucket, nb)
		for i := range s.Buckets {
			s.Buckets[i].FromBps = float64(i) * bw
		}
		for i, side := range sides {
			for _, l := range side {
				k := min(nb-1, int(dist(l)*100/bw))
				if i == 0 {
					s.Buckets[k].BidUsd += usd(l)
					s.Buckets[k].BidN += l.N
				} else {
					s.Buckets[k].AskUsd += usd(l)
					s.Buckets[k].AskN += l.N
				}
			}
		}
		for i := range s.Buckets {
			s.Buckets[i].BidUsd, s.Buckets[i].AskUsd = math.Round(s.Buckets[i].BidUsd), math.Round(s.Buckets[i].AskUsd)
		}
	}
	// walls
	thr := max(q.WallMult*med, q.MinWall)
	for i, side := range sides {
		var ws []BookWall
		for _, l := range side {
			u := usd(l)
			if u < thr {
				continue
			}
			w := BookWall{Side: [2]string{"bid", "ask"}[i], Px: l.Px, DistPct: round2((l.Px/mid - 1) * 100), Usd: math.Round(u), Orders: l.N}
			if l.N > 0 {
				w.AvgOrder = math.Round(u / float64(l.N))
			}
			if med > 0 {
				w.XMedian = math.Round(u/med*10) / 10
			}
			ws = append(ws, w)
		}
		sort.Slice(ws, func(a, b int) bool { return ws[a].Usd > ws[b].Usd })
		s.Walls = append(s.Walls, ws[:min(len(ws), q.Top)]...)
	}
	return s
}

// ---------- recorded walls and book shape over time ----------

// WallSideStats summarises one side's wall lifecycles.
type WallSideStats struct {
	N             int     `json:"n"`
	FilledN       int     `json:"filledN"`
	FilledUsd     float64 `json:"filledUsd"`
	PulledN       int     `json:"pulledN"`
	PulledUsd     float64 `json:"pulledUsd"`
	ApproachedN   int     `json:"approachedN"`       // walls price came within 30 bps of
	ApproachedPul int     `json:"approachedPulledN"` // … of which pulled without trading
	PulledNearN   int     `json:"pulledNearN"`       // pulled once price was within 30 bps and at least halfway closer
	PulledNearUsd float64 `json:"pulledNearUsd"`
	MixedN        int     `json:"mixedN"`
	LostN         int     `json:"lostN"`
	MedLifeMin    float64 `json:"medLifeMin"`
	LongLivedN    int     `json:"longLivedN"`   // stood ≥ 30 min
	MovingChains  int     `json:"movingChains"` // pulled-and-re-placed chains of ≥ 3 walls (a wall following price)
	MinLifeSec    int     `json:"minLifeSec"`   // walls shorter than this were left out (market-maker re-quotes)
}

type WallReport struct {
	InstID string         `json:"instId"`
	Market string         `json:"market"`
	From   int64          `json:"from"`
	To     int64          `json:"to"`
	Bid    WallSideStats  `json:"bid"`
	Ask    WallSideStats  `json:"ask"`
	Active []store.Wall   `json:"active,omitempty"` // standing now
	Events []store.Wall   `json:"events,omitempty"`
	Book   []BookBarStats `json:"book,omitempty"`
	Note   string         `json:"note,omitempty"`
}

// BookBarStats averages the recorded per-minute book shape over a bar.
type BookBarStats struct {
	T       int64   `json:"t"`
	Mid     float64 `json:"mid"`
	Spread  float64 `json:"spreadBps"`
	Bid1    float64 `json:"bid1"`
	Ask1    float64 `json:"ask1"`
	Imb1    float64 `json:"imb1"`
	Bid2    float64 `json:"bid2"`
	Ask2    float64 `json:"ask2"`
	WallBid float64 `json:"wallBid"`
	WallAsk float64 `json:"wallAsk"`
}

// approachBps: price came this close to a wall (within reach of being traded).
const approachBps = 30

// pulledNear: cancelled when price had come within approachBps and at least halfway from where
// it started — the spoof signature (market makers re-quoting far away do not show it).
func pulledNear(w store.Wall) bool {
	return w.Outcome == "pulled" && w.EndDistBps <= approachBps && w.EndDistBps <= 0.5*w.Dist0Bps
}

func wallSide(ws []store.Wall) WallSideStats {
	var s WallSideStats
	var lives []float64
	chains := map[int64]int{}
	for _, w := range ws {
		s.N++
		life := float64(w.T1-w.T0) / 6e4
		lives = append(lives, life)
		if life >= 30 {
			s.LongLivedN++
		}
		chains[w.Chain]++
		if w.MinDistBps <= approachBps {
			s.ApproachedN++
			if w.Outcome == "pulled" {
				s.ApproachedPul++
			}
		}
		switch w.Outcome {
		case "filled":
			s.FilledN++
			s.FilledUsd += w.PeakUsd
		case "pulled":
			s.PulledN++
			s.PulledUsd += w.PeakUsd
			if pulledNear(w) {
				s.PulledNearN++
				s.PulledNearUsd += w.PeakUsd
			}
		case "mixed":
			s.MixedN++
		case "lost":
			s.LostN++
		}
	}
	for _, n := range chains {
		if n >= 3 {
			s.MovingChains++
		}
	}
	sort.Float64s(lives)
	s.MedLifeMin = round2(quantile(lives, 0.5))
	s.FilledUsd, s.PulledUsd, s.PulledNearUsd = math.Round(s.FilledUsd), math.Round(s.PulledUsd), math.Round(s.PulledNearUsd)
	return s
}

// WallHistory reports recorded walls of a swap or spot id that ended in [from, to).
// Walls standing less than minLifeSec are left out: market makers re-quote large levels every few
// seconds, and counting those would make every book look spoofed.
func (e *Engine) WallHistory(id string, from, to int64, barMs int64, minUsd float64, minLifeSec, top int, events bool) (*WallReport, error) {
	if e.tape == nil {
		return nil, errNoTape
	}
	r := &WallReport{InstID: id, Market: "spot", From: from, To: to}
	if len(id) > 5 && id[len(id)-5:] == "-SWAP" {
		r.Market = "swap"
	}
	ws, err := e.tape.st.Walls(id, from, to)
	if err != nil {
		return nil, err
	}
	var bids, asks, big []store.Wall
	for _, w := range ws {
		if w.PeakUsd < minUsd || w.T1-w.T0 < int64(minLifeSec)*1000 {
			continue
		}
		big = append(big, w)
		if w.Bid {
			bids = append(bids, w)
		} else {
			asks = append(asks, w)
		}
	}
	r.Bid, r.Ask = wallSide(bids), wallSide(asks)
	r.Bid.MinLifeSec, r.Ask.MinLifeSec = minLifeSec, minLifeSec
	if act, ok := e.walls.ActiveWalls(id); ok {
		for _, a := range act {
			if a.EndUsd >= minUsd && len(r.Active) < top {
				r.Active = append(r.Active, roundWall(a))
			}
		}
	} else {
		r.Note = "this book is not tracked live right now (only the top of the pool, the watchlist and recently viewed / asked-about coins are); asking about it starts tracking for 6 h"
	}
	if events {
		sort.Slice(big, func(i, j int) bool { return big[i].PeakUsd > big[j].PeakUsd })
		for _, w := range big[:min(len(big), top)] {
			r.Events = append(r.Events, roundWall(w))
		}
	}
	if barMs > 0 {
		mins, err := e.tape.st.BookMins(id, from, to)
		if err != nil {
			return nil, err
		}
		r.Book = bookBars(mins, barMs)
	}
	return r, nil
}

func roundWall(w store.Wall) store.Wall {
	w.FirstUsd, w.PeakUsd, w.EndUsd, w.FilledUsd = math.Round(w.FirstUsd), math.Round(w.PeakUsd), math.Round(w.EndUsd), math.Round(w.FilledUsd)
	w.Dist0Bps, w.MinDistBps, w.EndDistBps = math.Round(w.Dist0Bps), math.Round(w.MinDistBps), math.Round(w.EndDistBps)
	return w
}

func bookBars(mins []store.BookMin, bar int64) []BookBarStats {
	var out []BookBarStats
	var n float64
	flush := func() {
		if n == 0 {
			return
		}
		b := &out[len(out)-1]
		b.Spread, b.Bid1, b.Ask1, b.Bid2, b.Ask2 = round2(b.Spread/n), math.Round(b.Bid1/n), math.Round(b.Ask1/n), math.Round(b.Bid2/n), math.Round(b.Ask2/n)
		b.WallBid, b.WallAsk = math.Round(b.WallBid/n), math.Round(b.WallAsk/n)
		if t := b.Bid1 + b.Ask1; t > 0 {
			b.Imb1 = round2((b.Bid1 - b.Ask1) / t)
		}
	}
	for _, m := range mins {
		t := floorTo(m.Ts, bar)
		if len(out) == 0 || out[len(out)-1].T != t {
			flush()
			out = append(out, BookBarStats{T: t})
			n = 0
		}
		b := &out[len(out)-1]
		n++
		b.Mid = m.Mid
		b.Spread += m.SpreadBps
		b.Bid1 += m.Bid1
		b.Ask1 += m.Ask1
		b.Bid2 += m.Bid2
		b.Ask2 += m.Ask2
		b.WallBid += m.WallBid
		b.WallAsk += m.WallAsk
	}
	flush()
	return out
}
