package market

import (
	"context"
	"log"
	"math"
	"sort"
	"time"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/rules"
	"crypto-trader-go/internal/store"
)

const removedTTL = 30 * minuteMs

// universeLoop recomputes the pool once per confirmed minute: when every instrument that had the
// previous bar has pushed bar m, or at m+65s at the latest (stragglers are marked stale).
func (e *Engine) universeLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m := now.UnixMilli()/minuteMs*minuteMs - minuteMs
			e.mu.RLock()
			due := m > e.lastMin
			if due && now.UnixMilli() < m+minuteMs+65_000 {
				prev, cur := 0, 0
				for _, in := range e.insts {
					if !in.Eligible {
						continue
					}
					if in.bars.has(m - minuteMs) {
						prev++
						if in.bars.has(m) {
							cur++
						}
					}
				}
				due = prev > 0 && cur >= prev
			}
			e.mu.RUnlock()
			if due {
				e.computeUniverse(m)
			}
		}
	}
}

func (e *Engine) computeUniverse(m int64) {
	T := m + minuteMs
	now := time.Now().UnixMilli()
	phase := e.Phase()
	e.mu.Lock()
	if m <= e.lastMin && phase == "live" {
		e.mu.Unlock()
		return
	}
	e.lastMin = m
	// minute OI sample from the latest WS/REST value (bounded staleness)
	var oiBatch []store.OISample
	for _, in := range e.insts {
		if e.recording(in, now) && in.oiNow.Ts > 0 && T-in.oiNow.Ts < 2*minuteMs {
			s := store.OISample{InstID: in.ID, Ts: T, OiCcy: in.oiNow.OiCcy, OiUsd: in.oiNow.OiUsd}
			in.addOI(s)
			oiBatch = append(oiBatch, s)
		}
		if m%(10*minuteMs) == 0 {
			in.trim(now)
		}
	}
	hourClose := T%hourMs == 0
	var hourBatch []store.InstBar
	if hourClose {
		for _, in := range e.insts {
			if b, ok := aggregateHour(in, T-hourMs); ok {
				in.hourly[b.Ts] = b
				hourBatch = append(hourBatch, store.InstBar{InstID: in.ID, Bar: b})
			}
		}
	}

	rows := make([]Row, 0, len(e.insts))
	var gapIDs []string
	var cov Coverage
	ids := e.eligibleIDs()
	for id, in := range e.insts { // watched instruments outside the recording universe get a row too
		if !in.Eligible && e.watch[id] {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		in := e.insts[id]
		r := e.buildRow(in, m, now)
		if in.Eligible {
			cov.Eligible++
			switch r.Status {
			case "new":
				cov.New++
			case "live":
				cov.Ready++
			case "warming":
				cov.Warming++
			case "stale":
				cov.Stale++
			}
		}
		if r.Status == "stale" && now-in.gapQueuedAt > 2*minuteMs {
			in.gapQueuedAt = now
			gapIDs = append(gapIDs, id)
		}
		rows = append(rows, r)
	}
	e.mu.Unlock()

	// pool: threshold strictly greater, turnover desc, instId asc, cap N
	var cand []int
	for i, r := range rows {
		if r.Eligible && (r.Status == "live" || r.Provisional) && r.Turnover12h > e.conf().Threshold {
			cand = append(cand, i)
		}
	}
	sort.Slice(cand, func(a, b int) bool {
		ra, rb := rows[cand[a]], rows[cand[b]]
		if ra.Turnover12h != rb.Turnover12h {
			return ra.Turnover12h > rb.Turnover12h
		}
		return ra.InstID < rb.InstID
	})
	if len(cand) > e.conf().TopN {
		cand = cand[:e.conf().TopN]
	}
	active := map[string]float64{}
	var actIDs []string
	var actTurn []float64
	for k, i := range cand {
		rows[i].Active, rows[i].Rank = true, k+1
		active[rows[i].InstID] = rows[i].Turnover12h
		actIDs = append(actIDs, rows[i].InstID)
		actTurn = append(actTurn, rows[i].Turnover12h)
	}
	cov.Active = len(cand)
	e.fillPercentiles(rows)

	// membership transitions
	e.mu.Lock()
	for id := range active {
		e.lastPool[id] = T
	}
	for id, turn := range e.prevAct {
		if _, still := active[id]; !still {
			reason := "跌出前 N"
			for _, r := range rows {
				if r.InstID == id {
					switch {
					case r.Status == "stale":
						reason = "数据缺口（待补洞）"
					case r.Turnover12h <= e.conf().Threshold:
						reason = "12h 成交额低于阈值"
					}
				}
			}
			if e.insts[id] != nil && !e.insts[id].Eligible {
				reason = "合约状态变更/下线"
			}
			e.removed[id] = Removed{InstID: id, Reason: reason, At: T, Last: turn}
		}
	}
	for id := range active {
		delete(e.removed, id)
	}
	var removed []Removed
	for id, r := range e.removed {
		if T-r.At > removedTTL {
			delete(e.removed, id)
			continue
		}
		removed = append(removed, r)
	}
	membershipChanged := len(active) != len(e.prevAct)
	for id := range active {
		if _, ok := e.prevAct[id]; !ok {
			membershipChanged = true
		}
	}
	e.prevAct = active
	e.mu.Unlock()
	sort.Slice(removed, func(a, b int) bool { return removed[a].At > removed[b].At })

	snap := &Snapshot{Seq: e.seq.Add(1), AsOfMinute: m, DecisionTs: T, ComputedAt: now, Phase: phase,
		Provisional: phase != "live", Threshold: e.conf().Threshold, TopN: e.conf().TopN, Coverage: cov, Rows: rows, Removed: removed}
	e.scoreRows(snap)
	e.snap.Store(snap)
	e.broadcast(Event{Type: "universe", Data: map[string]any{"seq": snap.Seq, "asOfMinute": m, "phase": phase, "active": cov.Active}})
	if phase == "live" {
		go e.notifyConfirmed(snap)
	}

	go func() {
		if len(oiBatch) > 0 {
			e.st.PutOI(oiBatch)
		}
		if len(hourBatch) > 0 {
			e.st.PutBars("candles_1h", hourBatch)
		}
		if phase == "live" {
			e.st.PutUniverse(T, actIDs, actTurn)
		}
	}()
	if membershipChanged {
		e.updateSubscriptions()
	}
	if hourClose && phase == "live" {
		go e.recordLiveScores(snap)
	}
	if len(gapIDs) > 0 && phase == "live" {
		go func() {
			for _, id := range gapIDs {
				e.fillMinutes(context.Background(), id, 780)
			}
		}()
	}
}

func aggregateHour(in *Inst, open int64) (store.Bar, bool) {
	var b store.Bar
	for k := int64(0); k < 60; k++ {
		x, ok := in.bars.get(open + k*minuteMs)
		if !ok {
			return b, false
		}
		if k == 0 {
			b = store.Bar{Ts: open, O: x.O, H: x.H, L: x.L}
		}
		b.H, b.L = math.Max(b.H, x.H), math.Min(b.L, x.L)
		b.C = x.C
		b.Vol += x.Vol
		b.VolCcy += x.VolCcy
		b.Quote += x.Quote
	}
	return b, true
}

// price is the freshest known perp price: ticker last or live 1m close.
func (in *Inst) price() float64 {
	if in.live.Ts > 0 && in.liveRecv > in.tickTs && in.live.C > 0 {
		return in.live.C
	}
	if in.last > 0 {
		return in.last
	}
	if in.lastBar > 0 {
		b, _ := in.bars.get(in.lastBar)
		return b.C
	}
	return 0
}

// liveValues adds live-only fields (not available in hourly replay).
func (in *Inst) liveValues(v features.Values, turnover float64) {
	now := time.Now().UnixMilli()
	if in.fund.IntervalH > 0 && in.fund.Ts > 0 {
		v["funding_pred_hourly"] = in.fund.Rate / in.fund.IntervalH
	}
	px := in.price()
	if in.SpotID != "" && in.spotLast > 0 && px > 0 && now-in.spotTs < 2*minuteMs {
		v["basis"] = math.Log(px / in.spotLast)
	}
	if in.bid > 0 && in.ask >= in.bid && now-in.tickTs < 2*minuteMs {
		v["spread_bps"] = (in.ask - in.bid) / ((in.ask + in.bid) / 2) * 1e4
	}
	if turnover > 0 {
		v["turnover12h"] = turnover
	}
}

// fillPercentiles ranks every row's values against the active pool's distribution.
func (e *Engine) fillPercentiles(rows []Row) {
	fns := activePctFns(rows)
	for i := range rows {
		rows[i].Pct = map[string]float64{}
		for k, v := range rows[i].Values {
			if fn, ok := fns[k]; ok && finite(v) {
				rows[i].Pct[k] = roundPct(fn(v))
			}
		}
	}
}

func roundPct(p float64) float64 { return math.Round(p*10) / 10 }

// activePctFns returns, per field, the percentile function of the active pool's values.
func activePctFns(rows []Row) map[string]func(float64) float64 {
	pools := map[string][]float64{}
	for _, r := range rows {
		if r.Active {
			for k, v := range r.Values {
				if finite(v) {
					pools[k] = append(pools[k], v)
				}
			}
		}
	}
	fns := map[string]func(float64) float64{}
	for k, xs := range pools {
		fns[k] = features.Percentiles(xs)
	}
	return fns
}

func (e *Engine) scoreRows(s *Snapshot) {
	e.rmu.RLock()
	defer e.rmu.RUnlock()
	for i := range s.Rows {
		r := &s.Rows[i]
		if !r.Active && !r.Watch {
			r.Scores = nil
			continue
		}
		r.Scores = map[string]rules.Score{}
		for id, rule := range e.rules {
			pct := func(k string) (float64, bool) { p, ok := r.Pct[k]; return p, ok }
			r.Scores[id] = rule.Apply(pct, r.Values)
		}
	}
}

// rescore republishes the current snapshot with fresh rule scores / watch flags.
func (e *Engine) rescore() {
	old := e.snap.Load()
	if old == nil || len(old.Rows) == 0 {
		return
	}
	s := *old
	s.Rows = make([]Row, 0, len(old.Rows)+1)
	have := map[string]bool{}
	e.mu.RLock()
	for _, r := range old.Rows {
		r.Watch = e.watch[r.InstID]
		if !r.Eligible && !r.Watch { // unwatched instrument outside the universe: drop its row
			continue
		}
		have[r.InstID] = true
		s.Rows = append(s.Rows, r)
	}
	var add []string
	for id := range e.watch {
		if in := e.insts[id]; in != nil && !in.Eligible && !have[id] {
			add = append(add, id)
		}
	}
	e.mu.RUnlock()
	for _, id := range add { // newly watched outside the universe: add its row now
		if r := e.adhocRow(id, old); r != nil {
			s.Rows = append(s.Rows, *r)
		}
	}
	s.Seq = e.seq.Add(1)
	e.scoreRows(&s)
	e.snap.Store(&s)
	e.broadcast(Event{Type: "universe", Data: map[string]any{"seq": s.Seq, "asOfMinute": s.AsOfMinute, "phase": s.Phase, "active": s.Coverage.Active}})
}

// recordLiveScores writes the immutable hourly record for every rule (active pool only).
func (e *Engine) recordLiveScores(s *Snapshot) {
	e.rmu.RLock()
	defer e.rmu.RUnlock()
	var out []store.ScoreRow
	e.mu.RLock()
	for _, r := range s.Rows {
		if !r.Active {
			continue
		}
		in := e.insts[r.InstID]
		c, ok := liveSeries{in}.Close(s.DecisionTs)
		if !ok {
			continue
		}
		for id, rule := range e.rules {
			sc := r.Scores[id]
			out = append(out, store.ScoreRow{Kind: "live", RuleID: id, RuleVersion: rule.Version, Ts: s.DecisionTs, InstID: r.InstID,
				Long: sc.Long.Score, Short: sc.Short.Score, Close: c, Spread: r.Values["spread_bps"]})
		}
	}
	e.mu.RUnlock()
	if err := e.st.PutScores(out); err != nil {
		log.Printf("record scores: %v", err)
	}
	e.trackMu.Lock()
	e.trackCache = map[string]trackEntry{}
	e.trackMu.Unlock()
}

// OnConfirmed registers fn to run (off the universe loop, serially) with each confirmed live
// snapshot. The snapshot is shared and must be treated as read-only.
func (e *Engine) OnConfirmed(fn func(*Snapshot)) {
	e.confirmedMu.Lock()
	e.onConfirmed = append(e.onConfirmed, fn)
	e.confirmedMu.Unlock()
}

func (e *Engine) notifyConfirmed(s *Snapshot) {
	e.confirmedMu.Lock() // serialises listeners so minutes are seen in order
	defer e.confirmedMu.Unlock()
	for _, fn := range e.onConfirmed {
		fn(s)
	}
}

// ---------- SSE fan-out ----------

func (e *Engine) Subscribe() chan Event {
	ch := make(chan Event, 32)
	e.subMu.Lock()
	e.subs[ch] = true
	e.subMu.Unlock()
	return ch
}

func (e *Engine) Unsubscribe(ch chan Event) {
	e.subMu.Lock()
	delete(e.subs, ch)
	e.subMu.Unlock()
}

func (e *Engine) broadcast(ev Event) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- ev:
		default: // slow client: it will resync on the next universe event
		}
	}
}

func (e *Engine) broadcastPrices() {
	s := e.snap.Load()
	e.mu.RLock()
	px := map[string][2]float64{}
	for _, r := range s.Rows {
		if r.Active || r.Watch {
			if in := e.insts[r.InstID]; in != nil {
				ts := max(in.tickTs, in.liveRecv)
				px[r.InstID] = [2]float64{in.price(), float64(ts)}
			}
		}
	}
	e.mu.RUnlock()
	e.broadcast(Event{Type: "prices", Data: px})
}
