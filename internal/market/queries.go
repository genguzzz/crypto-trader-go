package market

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/refdata"
	"crypto-trader-go/internal/rules"
	"crypto-trader-go/internal/store"
)

type Candle struct {
	T         int64   `json:"t"` // open, ms
	O         float64 `json:"o"`
	H         float64 `json:"h"`
	L         float64 `json:"l"`
	C         float64 `json:"c"`
	Quote     float64 `json:"q"`
	Confirmed bool    `json:"x"`
}

var barMs = map[string]int64{"1m": minuteMs, "5m": 5 * minuteMs, "15m": 15 * minuteMs, "1H": hourMs, "4H": 4 * hourMs}

// Candles returns swap candles for chart display (recorded 1m / hourly bars, aggregated
// server-side in UTC) including the forming bar marked unconfirmed.
func (e *Engine) Candles(id, bar string, limit int) ([]Candle, error) {
	step, ok := barMs[bar]
	if !ok {
		return nil, fmt.Errorf("bar must be one of 1m,5m,15m,1H,4H")
	}
	limit = max(10, min(limit, 3000))
	now := time.Now().UnixMilli()
	from := now/step*step - int64(limit-1)*step
	base := map[int64]store.Bar{}
	var baseStep int64 = minuteMs
	table := "candles_1m"
	if step >= hourMs {
		baseStep, table = hourMs, "candles_1h"
	}
	if db, err := e.st.Bars(table, id, from, now); err == nil {
		for _, b := range db {
			base[b.Ts] = b
		}
	}
	in := e.instFor(id)
	if in == nil {
		return nil, fmt.Errorf("unknown instrument")
	}
	e.mu.RLock()
	if baseStep == hourMs {
		for ts, b := range in.hourly {
			if ts >= from {
				base[ts] = b
			}
		}
	} else {
		in.bars.each(from, func(b store.Bar) { base[b.Ts] = b })
	}
	live := in.live
	liveOK := live.Ts > 0
	// forming hour from 1m bars plus the forming minute (so 1H/4H move with every tick)
	var formingHour store.Bar
	if baseStep == hourMs {
		h0 := now / hourMs * hourMs
		n := 0
		add := func(x store.Bar) {
			if n == 0 {
				formingHour = store.Bar{Ts: h0, O: x.O, H: x.H, L: x.L}
			}
			formingHour.H, formingHour.L, formingHour.C = math.Max(formingHour.H, x.H), math.Min(formingHour.L, x.L), x.C
			formingHour.Quote += x.Quote
			n++
		}
		for ts := h0; ts < now; ts += minuteMs {
			if x, ok := in.bars.get(ts); ok {
				add(x)
			}
		}
		if liveOK && live.Ts >= h0 {
			if _, closed := in.bars.get(live.Ts); !closed {
				add(live)
			}
		}
		if n == 0 {
			formingHour.Ts = 0
		}
	}
	e.mu.RUnlock()
	if baseStep == minuteMs && liveOK {
		if _, have := base[live.Ts]; !have {
			base[live.Ts] = live
		}
	}
	if formingHour.Ts > 0 {
		// a REST 1H bar for the forming hour has the true open but is up to minutes old: keep its
		// open and extremes, take close/high/low/turnover progress from the 1m stream
		if h, have := base[formingHour.Ts]; have {
			formingHour.O = h.O
			formingHour.H, formingHour.L = math.Max(formingHour.H, h.H), math.Min(formingHour.L, h.L)
			formingHour.Quote = math.Max(formingHour.Quote, h.Quote)
		}
		base[formingHour.Ts] = formingHour
	}
	return aggregate(base, step, from, now), nil
}

func aggregate(base map[int64]store.Bar, step, from, now int64) []Candle {
	keys := make([]int64, 0, len(base))
	for ts := range base {
		keys = append(keys, ts)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var out []Candle
	for _, ts := range keys {
		b := base[ts]
		bucket := ts / step * step
		if bucket < from {
			continue
		}
		n := len(out)
		if n == 0 || out[n-1].T != bucket {
			out = append(out, Candle{T: bucket, O: b.O, H: b.H, L: b.L, C: b.C, Quote: b.Quote})
			continue
		}
		c := &out[n-1]
		c.H, c.L, c.C = math.Max(c.H, b.H), math.Min(c.L, b.L), b.C
		c.Quote += b.Quote
	}
	for i := range out {
		out[i].Confirmed = out[i].T+step <= now/minuteMs*minuteMs
	}
	return out
}

// SpotCandles proxies OKX spot candles for the overlay (cached 20s; not recorded).
func (e *Engine) SpotCandles(ctx context.Context, id, bar string, limit int) ([]Candle, error) {
	in := e.instFor(id)
	spot := ""
	if in != nil {
		e.mu.RLock()
		spot = in.SpotID
		e.mu.RUnlock()
	}
	if spot == "" {
		return nil, fmt.Errorf("no OKX USDT spot mapping for %s", id)
	}
	key := fmt.Sprintf("%s|%s|%d", spot, bar, limit)
	spotCache.Lock()
	if c, ok := spotCache.m[key]; ok && time.Since(c.at) < 20*time.Second {
		spotCache.Unlock()
		return c.v, nil
	}
	spotCache.Unlock()
	var out []Candle
	after := int64(0)
	for len(out) < limit {
		cs, err := e.rest.Candles(ctx, spot, bar, after, min(300, limit-len(out)))
		if err != nil {
			return nil, err
		}
		if len(cs) == 0 {
			break
		}
		for _, c := range cs {
			out = append(out, Candle{T: c.Ts, O: c.O, H: c.H, L: c.L, C: c.C, Quote: c.VolCcyQuote, Confirmed: c.Confirmed})
		}
		after = cs[len(cs)-1].Ts
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	spotCache.Lock()
	spotCache.m[key] = spotEntry{time.Now(), out}
	spotCache.Unlock()
	return out, nil
}

type spotEntry struct {
	at time.Time
	v  []Candle
}

var spotCache = struct {
	sync.Mutex
	m map[string]spotEntry
}{m: map[string]spotEntry{}}

type ComparePoint struct {
	T int64              `json:"t"`
	V map[string]float64 `json:"v"`
}

// Compare normalizes the instrument, BTC and ETH swap closes to 100 at the first common bar.
func (e *Engine) Compare(id, bar string, limit int) ([]ComparePoint, []string, error) {
	ids := []string{id, BTC, "ETH-USDT-SWAP"}
	if id == BTC || id == "ETH-USDT-SWAP" {
		ids = []string{BTC, "ETH-USDT-SWAP"}
	}
	series := map[string]map[int64]float64{}
	for _, x := range ids {
		cs, err := e.Candles(x, bar, limit)
		if err != nil {
			return nil, nil, err
		}
		m := map[int64]float64{}
		for _, c := range cs {
			m[c.T] = c.C
		}
		series[x] = m
	}
	var common []int64
	for t := range series[ids[0]] {
		ok := true
		for _, x := range ids[1:] {
			if _, has := series[x][t]; !has {
				ok = false
			}
		}
		if ok {
			common = append(common, t)
		}
	}
	sort.Slice(common, func(i, j int) bool { return common[i] < common[j] })
	if len(common) == 0 {
		return nil, ids, nil
	}
	t0 := common[0]
	out := make([]ComparePoint, 0, len(common))
	for _, t := range common {
		p := ComparePoint{T: t, V: map[string]float64{}}
		for _, x := range ids {
			p.V[x] = series[x][t] / series[x][t0] * 100
		}
		out = append(out, p)
	}
	return out, ids, nil
}

type SeriesPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
	U float64 `json:"u,omitempty"`
}

// Series returns recorded OI (coin & USD) or funding observations for the detail page.
func (e *Engine) Series(id, field string, hours int) ([]SeriesPoint, error) {
	now := time.Now().UnixMilli()
	from := now - int64(max(1, min(hours, 24*30)))*hourMs
	switch field {
	case "oi":
		xs, err := e.st.OI(id, from, now)
		if err != nil {
			return nil, err
		}
		out := make([]SeriesPoint, 0, len(xs))
		for _, x := range xs {
			out = append(out, SeriesPoint{T: x.Ts, V: x.OiCcy, U: x.OiUsd})
		}
		return out, nil
	case "funding":
		xs, err := e.st.Funding(id, from-7*24*hourMs, now)
		if err != nil {
			return nil, err
		}
		var out []SeriesPoint
		for _, x := range xs {
			if x.Settled {
				out = append(out, SeriesPoint{T: x.FundingTime, V: x.Rate})
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("field must be oi or funding")
}

type InstrumentInfo struct {
	InstID      string        `json:"instId"`
	Category    string        `json:"category"`
	SpotID      string        `json:"spotId,omitempty"`
	ListTime    int64         `json:"listTime"`
	Last        float64       `json:"last"`
	Bid         float64       `json:"bid"`
	Ask         float64       `json:"ask"`
	TickTs      int64         `json:"tickTs"`
	SpotLast    float64       `json:"spotLast,omitempty"`
	FundingRate float64       `json:"fundingRate"`
	FundingTime int64         `json:"fundingTime"`
	IntervalH   float64       `json:"fundingIntervalH"`
	OiCcy       float64       `json:"oiCcy"`
	OiUsd       float64       `json:"oiUsd"`
	OiTs        int64         `json:"oiTs"`
	Watch       bool          `json:"watch"`
	Eligible    bool          `json:"eligible"` // in the full-pool recording universe
	Tier        string        `json:"tier"`     // retention tier: none | base | viewed | pool | watch
	Syncing     bool          `json:"syncing"`  // on-demand history download running
	SyncMsg     string        `json:"syncMsg,omitempty"`
	Row         *Row          `json:"row,omitempty"`
	Coin        *refdata.Coin `json:"coin,omitempty"` // CoinGecko market cap / supply (crypto only)
}

// Instrument returns the detail for any catalog instrument; opening it counts as a view (starts
// live streaming and background history download for instruments outside the recording universe).
func (e *Engine) Instrument(id string) (InstrumentInfo, bool) {
	in := e.Touch(id)
	if in == nil {
		return InstrumentInfo{}, false
	}
	e.mu.RLock()
	info := InstrumentInfo{InstID: id, Category: in.Category, SpotID: in.SpotID, ListTime: in.ListTime, Last: in.price(),
		Bid: in.bid, Ask: in.ask, TickTs: max(in.tickTs, in.liveRecv), SpotLast: in.spotLast, FundingRate: in.fund.Rate,
		FundingTime: in.fund.FundingTime, IntervalH: in.fund.IntervalH, OiCcy: in.oiNow.OiCcy, OiUsd: in.oiNow.OiUsd, OiTs: in.oiNow.Ts,
		Watch: e.watch[id], Eligible: in.Eligible, Tier: tierNames[e.tierOf(id, time.Now().UnixMilli())], Syncing: in.syncing, SyncMsg: in.syncMsg}
	e.mu.RUnlock()
	if e.Ref != nil && info.Category == "1" {
		if c, ok := e.Ref.Get(strings.SplitN(id, "-", 2)[0]); ok {
			info.Coin = &c
		}
	}
	snap := e.snap.Load()
	var row *Row
	for _, r := range snap.Rows {
		if r.InstID == id {
			r := r
			row = &r
			break
		}
	}
	if row == nil {
		row = e.adhocRow(id, snap)
	}
	if row != nil {
		r := *row
		if r.Scores == nil { // not active/watched: score on demand for the detail page
			e.rmu.RLock()
			r.Scores = map[string]rules.Score{}
			for rid, rule := range e.rules {
				pct := func(k string) (float64, bool) { p, ok := r.Pct[k]; return p, ok }
				r.Scores[rid] = rule.Apply(pct, r.Values)
			}
			e.rmu.RUnlock()
		}
		info.Row = &r
	}
	return info, true
}

type Status struct {
	Phase         string           `json:"phase"`
	Uptime        string           `json:"uptime"`
	BackfillDone  int64            `json:"backfillDone"`
	BackfillTotal int64            `json:"backfillTotal"`
	RestRequests  int64            `json:"restRequests"`
	RestErrors    int64            `json:"restErrors"`
	RestRateHits  int64            `json:"restRateLimited"`
	WS            []WSStatus       `json:"ws"`
	Coverage      Coverage         `json:"coverage"`
	AsOfMinute    int64            `json:"asOfMinute"`
	ComputedAt    int64            `json:"computedAt"`
	Replay        string           `json:"replay"`
	History       string           `json:"history"`
	DB            map[string]int64 `json:"db"`
	Config        map[string]any   `json:"config"`
	Mem           MemStats         `json:"mem"`
}

// MemStats is the process memory view for leak checks (status page, deploy verification).
type MemStats struct {
	HeapAllocMB float64 `json:"heapAllocMB"` // live heap
	HeapSysMB   float64 `json:"heapSysMB"`
	SysMB       float64 `json:"sysMB"` // total from the OS
	Goroutines  int     `json:"goroutines"`
	NumGC       uint32  `json:"numGC"`
	Insts       int     `json:"insts"` // instruments held in memory
	FlowErrs    int64   `json:"flowDecodeErrors"`
}

func (e *Engine) memStats() MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	e.mu.RLock()
	n := len(e.insts)
	e.mu.RUnlock()
	mb := func(b uint64) float64 { return math.Round(float64(b)/(1<<20)*10) / 10 }
	return MemStats{HeapAllocMB: mb(m.HeapAlloc), HeapSysMB: mb(m.HeapSys), SysMB: mb(m.Sys), Goroutines: runtime.NumGoroutine(), NumGC: m.NumGC, Insts: n, FlowErrs: flowDecodeErrs.Load()}
}

type WSStatus struct {
	Name          string `json:"name"`
	Connected     bool   `json:"connected"`
	Subscriptions int    `json:"subscriptions"`
	Messages      int64  `json:"messages"`
	Reconnects    int64  `json:"reconnects"`
	LastMsgAgoMs  int64  `json:"lastMsgAgoMs"`
	SubErrors     int64  `json:"subErrors"`
}

// dbCounts caches table row counts. COUNT(*) scans millions of rows (seconds), so it is refreshed
// in the background at most once a minute and requests never wait for it.
var dbCounts = struct {
	sync.Mutex
	at      time.Time
	running bool
	v       map[string]int64
}{}

func (e *Engine) cachedCounts() map[string]int64 {
	dbCounts.Lock()
	defer dbCounts.Unlock()
	if time.Since(dbCounts.at) > time.Minute && !dbCounts.running {
		dbCounts.running = true
		go func() {
			v := e.st.Counts()
			dbCounts.Lock()
			dbCounts.v, dbCounts.at, dbCounts.running = v, time.Now(), false
			dbCounts.Unlock()
		}()
	}
	return dbCounts.v
}

func (e *Engine) Status() Status {
	s := e.snap.Load()
	now := time.Now().UnixMilli()
	var wss []WSStatus
	for _, c := range []*okx.WS{e.biz, e.pub} {
		wss = append(wss, WSStatus{Name: c.Name, Connected: c.Connected.Load(), Subscriptions: c.Subscriptions(),
			Messages: c.Messages.Load(), Reconnects: c.Reconnects.Load(), LastMsgAgoMs: now - c.LastMsgAt.Load(), SubErrors: c.SubErrors.Load()})
	}
	counts := e.cachedCounts()
	cats := []string{}
	for k := range e.conf().Categories {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	return Status{Phase: e.Phase(), Uptime: time.Since(e.started).Round(time.Second).String(), BackfillDone: e.bfDone.Load(),
		BackfillTotal: e.bfTotal.Load(), RestRequests: e.rest.Requests.Load(), RestErrors: e.rest.Errors.Load(),
		RestRateHits: e.rest.RateHits.Load(), WS: wss, Coverage: s.Coverage, AsOfMinute: s.AsOfMinute, ComputedAt: s.ComputedAt,
		Replay: e.replayMsg.Load().(string), History: e.historyMsg.Load().(string), DB: counts,
		Config: map[string]any{"threshold": e.conf().Threshold, "topN": e.conf().TopN, "categories": cats, "replayHours": e.conf().ReplayHours,
			"storageCapMB": e.conf().StorageCap >> 20, "takerFeeBps": 5, "replaySpreadBpsAssumed": ReplaySpreadBps}, Mem: e.memStats()}
}
