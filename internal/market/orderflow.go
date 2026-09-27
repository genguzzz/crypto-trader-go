package market

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// ---------- order flow: taker buy/sell (delta) and footprint from public data ----------
//
// Two sources, merged per bar by the query:
//   - trades: OKX public "trades" WS (one message per taker order per fill price, side = taker
//     side). Aggregated per minute into buy/sell USDT notional + per-price-level footprint, for
//     instruments in the active pool, the watchlist or being viewed. A minute counts only if the
//     stream was subscribed for all of it (no partial minutes after (re)subscribe/reconnect).
//   - rubik: OKX taker-volume history (5m for a few days, 1H for weeks) — delta for any
//     instrument and for time before recording started; no footprint.

var flowDecodeErrs atomic.Int64 // surfaced in status: a format change must not silently zero the data

const (
	flowGrace     = 3 * time.Second // minute m is final at m+60s+grace (late trade messages)
	flowLevelsMax = 40              // footprint levels per bar after re-bucketing
	flowLevelBars = 400             // footprint only for the most recent bars (payload bound)
)

type flowState struct {
	minute    int64 // open ms of the accumulating minute
	since     int64 // minutes opening at/after this were fully subscribed
	step      float64
	buy, sell float64
	n         int
	levels    map[int64][2]float64 // level index (px/step) → buy, sell
}

func newFlow(now int64) *flowState {
	return &flowState{minute: now / minuteMs * minuteMs, since: now + 2000, levels: map[int64][2]float64{}}
}

// niceStep rounds up to 1/2/5 × 10^k.
func niceStep(x float64) float64 {
	if x <= 0 || !finite(x) {
		return 0
	}
	p := math.Pow(10, math.Floor(math.Log10(x)))
	for _, m := range []float64{1, 2, 5} {
		if m*p >= x*(1-1e-9) {
			return m * p
		}
	}
	return 10 * p
}

// flowStepFor is the base footprint level size: ~1 bp of price, never finer than the tick.
func flowStepFor(px, tick float64) float64 {
	s := niceStep(px * 0.0001)
	if t := niceStep(tick); t > s {
		s = t
	}
	return s
}

// advanceFlow closes every minute before `to`, returning the complete ones as rows. Caller holds e.mu.
func advanceFlow(in *Inst, to int64) []store.FlowRow {
	fs := in.flow
	var out []store.FlowRow
	for fs.minute < to {
		if fs.minute >= fs.since {
			out = append(out, store.FlowRow{InstID: in.ID, Ts: fs.minute, Buy: fs.buy, Sell: fs.sell, N: fs.n, Step: fs.step, Levels: packLevels(fs.levels)})
		}
		fs.buy, fs.sell, fs.n = 0, 0, 0
		if len(fs.levels) > 0 {
			fs.levels = map[int64][2]float64{}
		}
		fs.minute += minuteMs
		if to-fs.minute > 10*minuteMs { // long idle (should not happen): skip ahead, those minutes are unknown
			fs.minute, fs.since = to, max(fs.since, to)
		}
	}
	return out
}

// wsTrade is one OKX "trades" push entry (strings except seqId, which is a JSON number — a
// map[string]string would fail to decode the whole message).
type wsTrade struct {
	Px   string `json:"px"`
	Sz   string `json:"sz"`
	Side string `json:"side"`
	Ts   string `json:"ts"`
}

func (e *Engine) onTrades(m okx.Message) {
	var xs []wsTrade
	if err := json.Unmarshal(m.Data, &xs); err != nil {
		flowDecodeErrs.Add(1)
		return
	}
	var done []store.FlowRow
	e.mu.Lock()
	in := e.insts[m.Arg.InstID]
	if in == nil || in.flow == nil || in.CtVal <= 0 {
		e.mu.Unlock()
		return
	}
	fs := in.flow
	for _, x := range xs {
		ts, px, sz := atoi(x.Ts), atof(x.Px), atof(x.Sz)
		mt := ts / minuteMs * minuteMs
		if px <= 0 || sz <= 0 || mt < fs.minute { // late for a closed minute: dropped
			continue
		}
		if mt > fs.minute {
			done = append(done, advanceFlow(in, mt)...)
		}
		if fs.step == 0 {
			fs.step = flowStepFor(px, in.TickSz)
		}
		q := px * sz * in.CtVal
		idx := int64(math.Floor(px/fs.step + 1e-9))
		lv := fs.levels[idx]
		if x.Side == "buy" {
			fs.buy += q
			lv[0] += q
		} else {
			fs.sell += q
			lv[1] += q
		}
		fs.levels[idx] = lv
		fs.n++
	}
	e.mu.Unlock()
	e.persistFlow(done)
}

// flowLoop closes quiet minutes (no trade arrived to roll them over).
func (e *Engine) flowLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			to := now.Add(-flowGrace).UnixMilli() / minuteMs * minuteMs
			down := !e.pub.Connected.Load() // no trades can arrive: nothing from now on is complete
			var done []store.FlowRow
			e.mu.Lock()
			for _, in := range e.insts {
				if in.flow != nil {
					if down {
						in.flow.since = max(in.flow.since, now.UnixMilli()+2000)
					}
					done = append(done, advanceFlow(in, to)...)
				}
			}
			e.mu.Unlock()
			e.persistFlow(done)
		}
	}
}

func (e *Engine) persistFlow(rows []store.FlowRow) {
	for _, r := range rows {
		select {
		case e.flowCh <- r:
		default:
		}
	}
}

// onPublicReconnect: trades may have been missed while the public WS was down.
func (e *Engine) onPublicReconnect(time.Time) {
	now := time.Now().UnixMilli()
	e.mu.Lock()
	for _, in := range e.insts {
		if in.flow != nil {
			in.flow.since = now + 2000
		}
	}
	e.mu.Unlock()
}

// flowIDs is the set whose trades are recorded: active pool ∪ watchlist ∪ viewed recently.
// Caller holds e.mu (read).
func (e *Engine) flowIDs(now int64) map[string]bool {
	ids := map[string]bool{}
	for id := range e.watch {
		ids[id] = true
	}
	for id, t := range e.views {
		if now-t < RecordViewed.Milliseconds() {
			ids[id] = true
		}
	}
	for _, r := range e.snap.Load().Rows {
		if r.Active {
			ids[r.InstID] = true
		}
	}
	for id := range ids {
		if e.insts[id] == nil {
			delete(ids, id)
		}
	}
	return ids
}

// syncFlowStates starts/stops per-instrument aggregation to match the subscribed set.
func (e *Engine) syncFlowStates(ids map[string]bool) {
	now := time.Now().UnixMilli()
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, in := range e.insts {
		switch {
		case ids[id] && in.flow == nil:
			in.flow = newFlow(now)
		case !ids[id] && in.flow != nil:
			in.flow = nil
		}
	}
}

// ---------- level packing ----------

func packLevels(m map[int64][2]float64) []byte {
	if len(m) == 0 {
		return nil
	}
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	b := make([]byte, 0, 12*len(keys))
	for _, k := range keys {
		v := m[k]
		b = binary.LittleEndian.AppendUint32(b, uint32(int32(k)))
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(float32(v[0])))
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(float32(v[1])))
	}
	return b
}

// eachLevel visits packed levels as (low price, buy, sell).
func eachLevel(b []byte, step float64, fn func(px, buy, sell float64)) {
	for i := 0; i+12 <= len(b); i += 12 {
		k := int32(binary.LittleEndian.Uint32(b[i:]))
		fn(float64(k)*step, float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i+4:]))),
			float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i+8:]))))
	}
}

// ---------- rubik taker volume history ----------

var takerSync = struct {
	sync.Mutex
	busy map[string]chan struct{}
	at   map[string]time.Time
}{busy: map[string]chan struct{}{}, at: map[string]time.Time{}}

// syncTaker stores rubik taker volume for [from, now] incrementally (newer periods, then older ones
// if the stored range doesn't reach `from`). Concurrent calls for the same key share one fetch.
func (e *Engine) syncTaker(ctx context.Context, id, period string, from int64) {
	key := id + "|" + period
	takerSync.Lock()
	if ch, ok := takerSync.busy[key]; ok {
		takerSync.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return
	}
	fresh := time.Since(takerSync.at[key]) < 2*time.Minute
	ch := make(chan struct{})
	takerSync.busy[key] = ch
	takerSync.Unlock()
	defer func() {
		takerSync.Lock()
		delete(takerSync.busy, key)
		takerSync.at[key] = time.Now()
		takerSync.Unlock()
		close(ch)
	}()
	pstep := int64(5 * minuteMs)
	pages := 30 // 100 × 5m per page ≈ 8h; OKX keeps a few days
	if period == "1H" {
		pstep, pages = hourMs, 8 // ≈ 33 days
	}
	now := time.Now().UnixMilli()
	newest, oldest := e.st.TakerSpan(id, period)
	fetch := func(end, stopAt int64) {
		for p := 0; p < pages; p++ {
			xs, err := e.rest.TakerVolumeHistory(ctx, id, period, end, 100)
			if err != nil || len(xs) == 0 {
				return
			}
			var rows []store.TakerRow
			any := false
			for _, x := range xs {
				if x.Buy == 0 && x.Sell == 0 {
					continue // beyond OKX's history (or no trading)
				}
				any = true
				if x.Ts+pstep <= now { // closed periods only
					rows = append(rows, store.TakerRow{Ts: x.Ts, Buy: x.Buy, Sell: x.Sell})
				}
			}
			e.st.PutTaker(id, period, rows)
			end = xs[len(xs)-1].Ts
			if !any || end <= stopAt {
				return
			}
		}
	}
	if fresh && newest+2*pstep > now {
		return // synced within the last 2 minutes
	}
	fetch(0, max(newest, from))       // newer periods
	if oldest == 0 || oldest > from { // older ones, down to `from` or the end of OKX's history
		if _, o := e.st.TakerSpan(id, period); o > from {
			fetch(o, from)
		}
	}
}

// ---------- query ----------

type FlowBar struct {
	T      int64        `json:"t"`
	Buy    float64      `json:"buy"`
	Sell   float64      `json:"sell"`
	N      int          `json:"n,omitempty"`
	Src    string       `json:"src"`              // trades | rubik
	Levels [][3]float64 `json:"levels,omitempty"` // [level low price, buy, sell]
	Step   float64      `json:"step,omitempty"`
}

type FlowResp struct {
	Bars      []FlowBar `json:"bars"`
	Recording bool      `json:"recording"` // trades for this instrument are being recorded now
	Since     int64     `json:"since"`     // earliest recorded trade minute in range (0 = none)
}

// OrderFlow returns per-bar taker buy/sell (and optionally the footprint) aligned with Candles.
func (e *Engine) OrderFlow(ctx context.Context, id, bar string, limit int, levels bool) (FlowResp, error) {
	step, ok := barMs[bar]
	if !ok {
		return FlowResp{}, fmt.Errorf("bar must be one of 1m,5m,15m,1H,4H")
	}
	in := e.instFor(id)
	if in == nil {
		return FlowResp{}, fmt.Errorf("unknown instrument")
	}
	limit = max(10, min(limit, 3000))
	now := time.Now().UnixMilli()
	from := now/step*step - int64(limit-1)*step
	nowMin := now / minuteMs * minuteMs
	rows, err := e.st.Flow(id, from, now, levels)
	if err != nil {
		return FlowResp{}, err
	}
	var resp FlowResp
	e.mu.RLock()
	if fs := in.flow; fs != nil {
		resp.Recording = true
		if fs.minute >= fs.since && fs.minute >= from { // forming minute (repaints)
			rows = append(rows, store.FlowRow{InstID: id, Ts: fs.minute, Buy: fs.buy, Sell: fs.sell, N: fs.n, Step: fs.step, Levels: packLevels(fs.levels)})
		}
	}
	e.mu.RUnlock()

	type acc struct {
		bar     FlowBar
		minutes int
		rows    []store.FlowRow
	}
	buckets := map[int64]*acc{}
	for _, r := range rows {
		if resp.Since == 0 || r.Ts < resp.Since {
			resp.Since = r.Ts
		}
		b := r.Ts / step * step
		a := buckets[b]
		if a == nil {
			a = &acc{bar: FlowBar{T: b, Src: "trades"}}
			buckets[b] = a
		}
		a.bar.Buy += r.Buy
		a.bar.Sell += r.Sell
		a.bar.N += r.N
		a.minutes++
		a.rows = append(a.rows, r)
	}
	out := map[int64]FlowBar{}
	for b, a := range buckets {
		want := int(min(step, nowMin+minuteMs-b) / minuteMs)
		if a.minutes >= want {
			out[b] = a.bar
		}
	}
	// rubik fills bars ≥ 5m that recorded trades don't fully cover
	if step >= 5*minuteMs {
		period, pstep := "5m", int64(5*minuteMs)
		if step >= hourMs {
			period, pstep = "1H", hourMs
		}
		e.syncTaker(ctx, id, period, from)
		tk, err := e.st.Taker(id, period, from, now)
		if err == nil {
			sub := map[int64]*FlowBar{}
			cnt := map[int64]int{}
			for _, x := range tk {
				b := x.Ts / step * step
				if sub[b] == nil {
					sub[b] = &FlowBar{T: b, Src: "rubik"}
				}
				sub[b].Buy += x.Buy
				sub[b].Sell += x.Sell
				cnt[b]++
			}
			for b, fb := range sub {
				if _, have := out[b]; have || b+step > now || cnt[b] < int(step/pstep) {
					continue // trades win; forming or partial bars are unknown
				}
				out[b] = *fb
			}
		}
	}
	keys := make([]int64, 0, len(out))
	for b := range out {
		keys = append(keys, b)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var common float64
	if levels {
		var groups [][]store.FlowRow
		for i, b := range keys {
			if out[b].Src == "trades" && i >= len(keys)-flowLevelBars {
				groups = append(groups, buckets[b].rows)
			}
		}
		common = commonLevelStep(groups)
	}
	for i, b := range keys {
		fb := out[b]
		if levels && fb.Src == "trades" && i >= len(keys)-flowLevelBars {
			fb.Levels, fb.Step = mergeLevels(buckets[b].rows, common)
		}
		resp.Bars = append(resp.Bars, fb)
	}
	return resp, nil
}

// levelMults are the level-size multipliers used when coarsening; each divides the next, so a
// coarser footprint is always an exact regrouping of a finer one (the chart regroups further by the
// same rule when zoomed out).
var levelMults = []float64{1, 2, 10, 20, 100, 200, 1000, 2000, 10000}

// commonLevelStep is one level size for every bar in a response (so rows line up across bars):
// the base minute step coarsened until the 90th-percentile bar fits in flowLevelsMax levels.
// Outlier bars wider than that are coarsened further on their own by mergeLevels.
func commonLevelStep(groups [][]store.FlowRow) float64 {
	base := 0.0
	var spans []float64
	for _, rows := range groups {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, r := range rows {
			base = math.Max(base, r.Step)
			eachLevel(r.Levels, r.Step, func(px, _, _ float64) { lo, hi = math.Min(lo, px), math.Max(hi, px) })
		}
		if hi >= lo {
			spans = append(spans, hi-lo)
		}
	}
	if base == 0 || len(spans) == 0 {
		return base
	}
	sort.Float64s(spans)
	p90 := spans[len(spans)*9/10]
	for _, m := range levelMults {
		if p90/(base*m)+1 <= flowLevelsMax {
			return base * m
		}
	}
	return base * levelMults[len(levelMults)-1]
}

// mergeLevels combines minute footprints into one bar at level size `want` (0 = the minute step),
// coarsening further (by levelMults of the minute step) while more than flowLevelsMax levels remain.
func mergeLevels(rows []store.FlowRow, want float64) ([][3]float64, float64) {
	base, lo, hi := 0.0, math.Inf(1), math.Inf(-1)
	for _, r := range rows {
		if r.Step > base {
			base = r.Step
		}
		eachLevel(r.Levels, r.Step, func(px, _, _ float64) { lo, hi = math.Min(lo, px), math.Max(hi, px) })
	}
	if base == 0 || math.IsInf(lo, 1) {
		return nil, 0
	}
	step := base
	for _, m := range levelMults {
		step = base * m
		if step >= want*(1-1e-9) && (hi-lo)/step+1 <= flowLevelsMax {
			break
		}
	}
	m := map[int64][2]float64{}
	for _, r := range rows {
		eachLevel(r.Levels, r.Step, func(px, buy, sell float64) {
			k := int64(math.Floor(px/step + 1e-6))
			v := m[k]
			v[0] += buy
			v[1] += sell
			m[k] = v
		})
	}
	out := make([][3]float64, 0, len(m))
	for k, v := range m {
		out = append(out, [3]float64{float64(k) * step, v[0], v[1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out, step
}

// migrate runs one-time data fixes (flag in settings).
func (e *Engine) migrate() {
	// 2026-09-27: the first order-flow build failed to decode every OKX trade push (numeric seqId)
	// and stored all-zero minutes; everything recorded before the fixed build is invalid.
	const key, fixedAt = "migrated.orderflow-seqid", int64(1_790_526_600_000) // 2026-09-27 16:30 UTC, just after the fixed build went live
	if _, done := e.st.Setting(key); done {
		return
	}
	if _, err := e.st.DB.Exec(`DELETE FROM orderflow_1m WHERE ts < ?`, fixedAt); err != nil {
		log.Printf("migrate %s: %v", key, err)
		return
	}
	e.st.PutSetting(key, "1")
	log.Printf("migrate %s: removed order-flow minutes recorded before the decode fix", key)
}
