package market

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// ---------- catalog: every live OKX USDT-margined perpetual (search) ----------
//
// The catalog is only instrument metadata + the latest 30s ticker (≈500 rows, kept in memory).
// Nothing is downloaded for an instrument until its detail is opened or it is added to the
// watchlist; then syncInst fetches ~7 days of history and it streams live while it is being viewed.

// CatalogItem is one search entry.
type CatalogItem struct {
	InstID    string  `json:"instId"`
	Category  string  `json:"category"`
	ListTime  int64   `json:"listTime"`
	PreMarket bool    `json:"preMarket,omitempty"`
	Recorded  bool    `json:"recorded"` // in the full-pool recording universe (settings categories)
	Active    bool    `json:"active,omitempty"`
	Rank      int     `json:"rank,omitempty"`
	Watch     bool    `json:"watch,omitempty"`
	Last      float64 `json:"last"`
	Open24h   float64 `json:"open24h"`
}

func catalogOK(i okx.Instrument) bool {
	return i.State == "live" && i.SettleCcy == "USDT" && i.CtType == "linear"
}

func (e *Engine) Catalog() []CatalogItem {
	snap := e.snap.Load()
	rank := map[string]int{}
	for _, r := range snap.Rows {
		if r.Active {
			rank[r.InstID] = r.Rank
		}
	}
	e.mu.RLock()
	out := make([]CatalogItem, 0, len(e.catalog))
	for id, i := range e.catalog {
		t := e.tickers[id]
		in := e.insts[id]
		out = append(out, CatalogItem{InstID: id, Category: i.Category, ListTime: i.ListTime, PreMarket: i.RuleType == "pre_market",
			Recorded: in != nil && in.Eligible, Active: rank[id] > 0, Rank: rank[id], Watch: e.watch[id], Last: t.Last, Open24h: t.Open24h})
	}
	e.mu.RUnlock()
	sort.Slice(out, func(a, b int) bool { return out[a].InstID < out[b].InstID })
	return out
}

// instFor returns the instrument's state, creating it from the catalog (and restoring its stored
// history) if it is outside the recording universe. Unknown IDs return nil.
func (e *Engine) instFor(id string) *Inst {
	e.mu.RLock()
	in := e.insts[id]
	meta, ok := e.catalog[id]
	e.mu.RUnlock()
	if in != nil {
		return in
	}
	if !ok {
		return nil
	}
	now := time.Now().UnixMilli()
	fresh := newInst(id)
	fresh.Category, fresh.ListTime, fresh.CtVal, fresh.TickSz = meta.Category, meta.ListTime, meta.CtVal, meta.TickSz
	e.restoreInst(fresh, now)
	e.mu.Lock()
	defer e.mu.Unlock()
	if in := e.insts[id]; in != nil { // raced with another request
		return in
	}
	if e.spotIDs[spotOf(id)] {
		fresh.SpotID = spotOf(id)
	}
	if t, ok := e.tickers[id]; ok {
		fresh.last, fresh.bid, fresh.ask, fresh.tickTs = t.Last, t.Bid, t.Ask, t.Ts
	}
	e.insts[id] = fresh
	return fresh
}

// restoreInst loads one instrument's stored series into memory (same depths as loadFromDB).
func (e *Engine) restoreInst(in *Inst, now int64) {
	if bs, err := e.st.Bars("candles_1m", in.ID, now-keep1m*minuteMs, now); err == nil {
		for _, b := range bs {
			in.putBar(b)
		}
	}
	if bs, err := e.st.Bars("candles_1h", in.ID, now-(HourlyDepth+48)*hourMs, now); err == nil {
		for _, b := range bs {
			in.hourly[b.Ts] = b
		}
	}
	if xs, err := e.st.OI(in.ID, now-keepOI, now); err == nil {
		for _, x := range xs {
			in.addOI(x)
		}
	}
	if fs, err := e.st.Funding(in.ID, now-60*24*hourMs, now); err == nil {
		for _, f := range fs {
			if f.Settled {
				in.fund.addSettled(f)
			}
		}
	}
}

// recording reports whether an instrument streams live (WS). Caller holds e.mu.
func (e *Engine) recording(in *Inst, now int64) bool {
	return in.Eligible || e.watch[in.ID] || now-e.views[in.ID] < RecordViewed.Milliseconds()
}

// Touch marks a detail view: it (re)starts live streaming for instruments outside the recording
// universe and downloads missing history in the background.
func (e *Engine) Touch(id string) *Inst {
	in := e.instFor(id)
	if in == nil {
		return nil
	}
	now := time.Now().UnixMilli()
	e.mu.Lock()
	wasRecording := e.recording(in, now) && in.flow != nil
	prev := e.views[id]
	e.views[id] = now
	start := !in.syncing && now-in.syncedAt > 30*minuteMs
	if start {
		in.syncing, in.syncMsg = true, "排队中"
	}
	e.mu.Unlock()
	if now-prev > 10*minuteMs {
		if err := e.st.PutView(id, now); err != nil {
			log.Printf("view %s: %v", id, err)
		}
	}
	if !wasRecording {
		e.updateSubscriptions()
	}
	if start {
		go e.syncInst(context.Background(), id)
	}
	return in
}

// syncInst fills one instrument on demand: the last 24h of 1m (fast endpoint), 30 days of 1H,
// recent OI and settled funding, then up to 7 days of 1m history (bounded by its retention tier).
func (e *Engine) syncInst(ctx context.Context, id string) {
	set := func(msg string) {
		e.mu.Lock()
		if in := e.insts[id]; in != nil {
			in.syncMsg = msg
		}
		e.mu.Unlock()
	}
	defer func() {
		e.mu.Lock()
		if in := e.insts[id]; in != nil {
			in.syncing, in.syncedAt = false, time.Now().UnixMilli()
		}
		e.mu.Unlock()
	}()
	set("拉取最近 24h 1m 与 1H K 线")
	e.fillMinutes(ctx, id, 1440)
	e.fillHours(ctx, id)
	set("拉取持仓量、资金费与主动买卖量历史")
	e.fetchOI5m(ctx, id)
	e.fetchFundingHistory(ctx, id)
	from := time.Now().Add(-HistoryDays * day).UnixMilli()
	e.syncTaker(ctx, id, "5m", from)
	e.syncTaker(ctx, id, "1H", time.Now().Add(-30*day).UnixMilli())
	e.mu.RLock()
	keep := e.keep1mFor(e.tierOf(id, time.Now().UnixMilli()))
	e.mu.RUnlock()
	set(fmt.Sprintf("下载近 %s 1m 历史", fmtAge(keep)))
	n, err := e.historyFill(ctx, id, keep, true)
	if err != nil {
		set("历史下载失败：" + err.Error())
		return
	}
	nf, err := e.backfillFlow(ctx, id, set)
	if err != nil {
		log.Printf("orderflow backfill %s: %v", id, err)
		set(fmt.Sprintf("已就绪（本次补 %d 根 1m；订单流回补中断：%v）", n, err))
		return
	}
	set(fmt.Sprintf("已就绪（本次补 %d 根 1m、%d 分钟订单流）", n, nf))
}

// historyFill pages /market/history-candles backwards until `keep` of 1m bars is stored. With
// full=false it starts below the earliest stored bar (bulk pool backfill: cheap, assumes no holes);
// with full=true it re-pages from now when coverage is below 99% so holes in the middle are fixed.
func (e *Engine) historyFill(ctx context.Context, id string, keep time.Duration, full bool) (int, error) {
	now := time.Now().UnixMilli()
	e.mu.RLock()
	in := e.insts[id]
	lt := int64(0)
	if in != nil {
		lt = in.ListTime
	}
	e.mu.RUnlock()
	lo := max(now/minuteMs*minuteMs-keep.Milliseconds(), lt/minuteMs*minuteMs)
	want := int((now - lo) / minuteMs)
	if want <= 0 {
		return 0, nil
	}
	earliest, n, err := e.st.Span("candles_1m", id, lo)
	if err != nil {
		return 0, err
	}
	if n >= want*99/100 {
		return 0, nil
	}
	after := now
	if !full && earliest > 0 {
		if earliest <= lo+minuteMs {
			return 0, nil
		}
		after = earliest
	}
	wrote := 0
	for after > lo {
		cs, err := e.rest.HistoryCandles(ctx, id, "1m", after, 300)
		if err != nil {
			return wrote, err
		}
		if len(cs) == 0 {
			break
		}
		batch := make([]store.InstBar, 0, len(cs))
		for _, c := range cs {
			if c.Confirmed && c.Ts >= lo {
				batch = append(batch, store.InstBar{InstID: id, Bar: store.Bar{Ts: c.Ts, O: c.O, H: c.H, L: c.L, C: c.C, Vol: c.Vol, VolCcy: c.VolCcy, Quote: c.VolCcyQuote}})
			}
		}
		if err := e.st.PutBars("candles_1m", batch); err != nil {
			return wrote, err
		}
		wrote += len(batch)
		after = cs[len(cs)-1].Ts
	}
	return wrote, nil
}

// adhocRow builds a fact-panel row for an instrument that is not in the snapshot (outside the
// recording universe), with percentiles against the current active pool like any other row.
func (e *Engine) adhocRow(id string, snap *Snapshot) *Row {
	now := time.Now().UnixMilli()
	m := now/minuteMs*minuteMs - minuteMs
	e.mu.RLock()
	in := e.insts[id]
	if in == nil {
		e.mu.RUnlock()
		return nil
	}
	r := e.buildRow(in, m, now)
	e.mu.RUnlock()
	fns := activePctFns(snap.Rows)
	r.Pct = map[string]float64{}
	for k, v := range r.Values {
		if fn, ok := fns[k]; ok && finite(v) {
			r.Pct[k] = roundPct(fn(v))
		}
	}
	return &r
}

// buildRow computes status, 12h turnover and live values for one instrument at confirmed minute m.
// Caller holds e.mu (read); no side effects.
func (e *Engine) buildRow(in *Inst, m, now int64) Row {
	T := m + minuteMs
	sum, have := in.window12h(m)
	r := Row{InstID: in.ID, Category: in.Category, Have: have, HasSpot: in.SpotID != "", ListTime: in.ListTime,
		Watch: e.watch[in.ID], Last: in.price(), Eligible: in.Eligible}
	if in.lastBar > 0 {
		r.Freshness = now - (in.lastBar + minuteMs)
	}
	switch {
	case in.ListTime > m-719*minuteMs:
		r.Status = "new"
	case have == 720:
		r.Status = "live"
		r.Turnover12h = sum
	case !in.backfilled && in.Eligible:
		r.Status = "warming"
		if in.provTurnover > 0 {
			r.Turnover12h, r.Provisional = in.provTurnover, true
		}
	default:
		r.Status = "stale"
	}
	if r.Status == "live" || r.Status == "stale" || r.Provisional {
		var bs features.Series
		if btc := e.insts[BTC]; btc != nil && in.ID != BTC { // BTC vs itself carries no information
			bs = liveSeries{btc}
		}
		r.Values = features.Compute(liveSeries{in}, bs, T)
		in.liveValues(r.Values, r.Turnover12h)
	}
	return r
}
