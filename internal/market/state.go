// Package market holds the in-memory market state (recorder side) and publishes immutable
// snapshots (universe, fact panel, rule scores) for the API.
package market

import (
	"sort"
	"strings"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/store"
)

const (
	minuteMs = features.Minute
	hourMs   = features.Hour
	keep1m   = 1500 // minutes of 1m bars kept in memory (24h window + margin)
	keepOI   = 26 * hourMs
)

type fundingState struct {
	Rate, IntervalH   float64
	FundingTime, Next int64
	Ts                int64
	settled           []store.FundingSample // ascending by FundingTime (Ts = settlement time)
}

// Inst is one instrument's recorded state. Guarded by Engine.mu.
type Inst struct {
	ID, Category, SpotID string
	ListTime             int64
	CtVal                float64 // contract value in base units (USDT notional = px × sz × ctVal)
	TickSz               float64
	Eligible             bool

	bars     *minuteRing // confirmed 1m bars, last keep1m minutes
	live     store.Bar   // latest unconfirmed 1m
	liveRecv int64
	hourly   map[int64]store.Bar // confirmed 1H, key = bar open ms
	lastBar  int64               // open ms of newest confirmed 1m

	oi       []store.OISample  // minute-ish samples, ascending
	oiHourly map[int64]float64 // rubik 1H oiCcy by snapshot ts (replay)
	oiNow    store.OISample

	fund fundingState

	last, bid, ask float64
	open24h        float64 // WS tickers (rolling 24h)
	high24h        float64
	low24h         float64
	tickTs         int64
	spotLast       float64
	spotTs         int64

	provTurnover float64 // 5m-bootstrap estimate of 12h turnover
	backfilled   bool
	gapQueuedAt  int64

	// on-demand sync (detail opened / watched; see catalog.go)
	syncing  bool
	syncedAt int64
	syncMsg  string

	flow *flowState // trade aggregation while in the order-flow set (orderflow.go)
}

func newInst(id string) *Inst {
	return &Inst{ID: id, bars: &minuteRing{}, hourly: map[int64]store.Bar{}, oiHourly: map[int64]float64{}}
}

func spotOf(swapID string) string { return strings.TrimSuffix(swapID, "-SWAP") }

// minuteRing holds the latest keep1m confirmed 1m bars in a fixed array (no per-bar allocation);
// slot = minute index mod keep1m, validated by the stored open time.
type minuteRing struct{ b [keep1m]store.Bar }

func (r *minuteRing) get(ts int64) (store.Bar, bool) {
	x := r.b[(ts/minuteMs)%keep1m]
	return x, x.Ts == ts && ts != 0
}

func (r *minuteRing) has(ts int64) bool { _, ok := r.get(ts); return ok }

func (r *minuteRing) put(b store.Bar) {
	slot := &r.b[(b.Ts/minuteMs)%keep1m]
	if b.Ts >= slot.Ts { // never let an older bar evict a newer one
		*slot = b
	}
}

func (r *minuteRing) del(ts int64) {
	if r.has(ts) {
		r.b[(ts/minuteMs)%keep1m] = store.Bar{}
	}
}

// each visits held bars with open time ≥ from.
func (r *minuteRing) each(from int64, fn func(store.Bar)) {
	for _, x := range r.b {
		if x.Ts != 0 && x.Ts >= from {
			fn(x)
		}
	}
}

func (in *Inst) putBar(b store.Bar) {
	in.bars.put(b)
	if b.Ts > in.lastBar {
		in.lastBar = b.Ts
	}
}

// keepHourly bounds the in-memory hourly maps (1H bars, replay OI): 30-day depth + 2 days margin.
const keepHourly = (HourlyDepth + 48) * hourMs

// keepSettled bounds settled funding in memory (same depth loadFromDB restores).
const keepSettled = 60 * 24 * hourMs

// trim drops in-memory history older than what any computation reads, so a long-running process
// stays flat (every map/slice here otherwise grows by one entry per hour/minute/funding period).
func (in *Inst) trim(now int64) {
	k := 0
	for _, x := range in.oi {
		if x.Ts >= now-keepOI {
			in.oi[k] = x
			k++
		}
	}
	clear(in.oi[k:])
	in.oi = in.oi[:k]
	for ts := range in.hourly {
		if ts < now-keepHourly {
			delete(in.hourly, ts)
		}
	}
	for ts := range in.oiHourly {
		if ts < now-keepHourly {
			delete(in.oiHourly, ts)
		}
	}
	s := in.fund.settled
	i := sort.Search(len(s), func(i int) bool { return s[i].FundingTime >= now-keepSettled })
	if i > 0 {
		in.fund.settled = append(s[:0:0], s[i:]...)
	}
}

func (in *Inst) addOI(x store.OISample) {
	n := len(in.oi)
	if n > 0 && in.oi[n-1].Ts >= x.Ts {
		if in.oi[n-1].Ts == x.Ts {
			in.oi[n-1] = x
		} else { // out of order (bootstrap history): insert sorted
			in.oi = append(in.oi, x)
			sort.Slice(in.oi, func(i, j int) bool { return in.oi[i].Ts < in.oi[j].Ts })
		}
		return
	}
	in.oi = append(in.oi, x)
}

// window12h returns the 720-minute turnover ending with bar open m, and whether all bars exist.
func (in *Inst) window12h(m int64) (sum float64, have int) {
	for ts := m - 719*minuteMs; ts <= m; ts += minuteMs {
		if b, ok := in.bars.get(ts); ok {
			sum += b.Quote
			have++
		}
	}
	return
}

// window24h sums the USDT turnover of the last 1440 confirmed 1m bars ending at m.
func (in *Inst) window24h(m int64) (sum float64, have int) {
	for ts := m - 1439*minuteMs; ts <= m; ts += minuteMs {
		if b, ok := in.bars.get(ts); ok {
			sum += b.Quote
			have++
		}
	}
	return
}

// ---- Series implementations ----

// liveSeries reads 1m bars first, falling back to hourly bars for hour-aligned requests.
type liveSeries struct{ in *Inst }

func (s liveSeries) Close(t int64) (float64, bool) {
	if b, ok := s.in.bars.get(t - minuteMs); ok {
		return b.C, true
	}
	if t%hourMs == 0 {
		if b, ok := s.in.hourly[t-hourMs]; ok {
			return b.C, true
		}
	}
	return 0, false
}

func (s liveSeries) Quote(from, to int64) (float64, bool) {
	sum, ok := 0.0, true
	for ts := from; ts < to; ts += minuteMs {
		b, has := s.in.bars.get(ts)
		if !has {
			ok = false
			break
		}
		sum += b.Quote
	}
	if ok {
		return sum, true
	}
	if from%hourMs == 0 && to%hourMs == 0 {
		return hourlyQuote(s.in.hourly, from, to)
	}
	return 0, false
}

func (s liveSeries) OI(t int64) (float64, bool) {
	oi := s.in.oi
	i := sort.Search(len(oi), func(i int) bool { return oi[i].Ts > t }) - 1
	if i >= 0 && t-oi[i].Ts <= 10*minuteMs {
		return oi[i].OiCcy, true
	}
	return 0, false
}

func (s liveSeries) FundingSettled(t int64) (float64, float64, bool) { return settledAt(&s.in.fund, t) }

// hourlySeries is the replay view: hourly bars, rubik 1H OI and settled funding only.
type hourlySeries struct{ in *Inst }

func (s hourlySeries) Close(t int64) (float64, bool) {
	if t%hourMs != 0 {
		return 0, false
	}
	b, ok := s.in.hourly[t-hourMs]
	return b.C, ok
}

func (s hourlySeries) Quote(from, to int64) (float64, bool) {
	if from%hourMs != 0 || to%hourMs != 0 {
		return 0, false
	}
	return hourlyQuote(s.in.hourly, from, to)
}

// OI uses the rubik snapshot stamped at the start of the bar that closes at t — conservative
// (never later than t) matching the research probe's alignment.
func (s hourlySeries) OI(t int64) (float64, bool) {
	v, ok := s.in.oiHourly[t-hourMs]
	return v, ok
}

func (s hourlySeries) FundingSettled(t int64) (float64, float64, bool) {
	return settledAt(&s.in.fund, t)
}

func hourlyQuote(h map[int64]store.Bar, from, to int64) (float64, bool) {
	sum := 0.0
	for ts := from; ts < to; ts += hourMs {
		b, ok := h[ts]
		if !ok {
			return 0, false
		}
		sum += b.Quote
	}
	return sum, true
}

func settledAt(f *fundingState, t int64) (float64, float64, bool) {
	xs := f.settled
	i := sort.Search(len(xs), func(i int) bool { return xs[i].FundingTime > t }) - 1
	if i < 0 {
		return 0, 0, false
	}
	iv := f.IntervalH
	if i > 0 {
		iv = float64(xs[i].FundingTime-xs[i-1].FundingTime) / float64(hourMs)
	}
	if iv <= 0 {
		return 0, 0, false
	}
	return xs[i].Rate, iv, true
}

func (f *fundingState) addSettled(x store.FundingSample) {
	n := len(f.settled)
	if n > 0 && f.settled[n-1].FundingTime >= x.FundingTime {
		for _, y := range f.settled {
			if y.FundingTime == x.FundingTime {
				return
			}
		}
		f.settled = append(f.settled, x)
		sort.Slice(f.settled, func(i, j int) bool { return f.settled[i].FundingTime < f.settled[j].FundingTime })
		return
	}
	f.settled = append(f.settled, x)
}
