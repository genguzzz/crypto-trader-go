package market

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// ---------- order-flow backfill from OKX historical trades ----------
//
// Live recording only covers minutes after the trades stream was subscribed. When an instrument is
// opened, the missing minutes inside its order-flow retention window are rebuilt from public trades:
// complete UTC+8 days from OKX's daily trade files, the not-yet-published recent part from
// /market/history-trades (newest first, page-capped). A minute is written only when every trade in
// it was seen; minutes already stored (live recording) are never overwritten.

const (
	flowBackfillPages = 600 // history-trades pages per run (≈60k trades, ~100 s at the rate limit); the rest waits for the daily file
	utc8DayMs         = 24 * hourMs
)

var flowBackfillSem = make(chan struct{}, 1) // one at a time: trade files are large

// minuteAgg turns trades into per-minute order-flow rows (same shape as live recording).
type minuteAgg struct {
	step, ctVal float64
	m           map[int64]*flowState
}

func newMinuteAgg(step, ctVal float64) *minuteAgg {
	return &minuteAgg{step: step, ctVal: ctVal, m: map[int64]*flowState{}}
}

func (a *minuteAgg) add(t okx.Trade) {
	if t.Px <= 0 || t.Sz <= 0 {
		return
	}
	mt := t.Ts / minuteMs * minuteMs
	fs := a.m[mt]
	if fs == nil {
		fs = &flowState{minute: mt, step: a.step, levels: map[int64][2]float64{}}
		a.m[mt] = fs
	}
	q := t.Px * t.Sz * a.ctVal
	idx := int64(math.Floor(t.Px/a.step + 1e-9))
	lv := fs.levels[idx]
	if t.Buy {
		fs.buy += q
		lv[0] += q
	} else {
		fs.sell += q
		lv[1] += q
	}
	fs.levels[idx] = lv
	fs.n++
}

// rows returns one row per minute in [from, to) not in `have` (quiet minutes as zero rows: the
// source covered them, so they are known to have had no trades).
func (a *minuteAgg) rows(id string, from, to int64, have map[int64]bool) []store.FlowRow {
	var out []store.FlowRow
	for m := from; m < to; m += minuteMs {
		if have[m] {
			continue
		}
		r := store.FlowRow{InstID: id, Ts: m, Step: a.step}
		if fs := a.m[m]; fs != nil {
			r.Buy, r.Sell, r.N, r.Levels = fs.buy, fs.sell, fs.n, packLevels(fs.levels)
		}
		out = append(out, r)
	}
	return out
}

// missingRanges groups the minutes in [from, to) that are not in `have` into [a, b) ranges.
func missingRanges(from, to int64, have map[int64]bool) [][2]int64 {
	var out [][2]int64
	for m := from; m < to; m += minuteMs {
		if have[m] {
			continue
		}
		if n := len(out); n > 0 && out[n-1][1] == m {
			out[n-1][1] = m + minuteMs
		} else {
			out = append(out, [2]int64{m, m + minuteMs})
		}
	}
	return out
}

// keepFlowFor is how far back order flow is rebuilt for a tier (its retention age, at most 7 days).
func (e *Engine) keepFlowFor(t Tier) time.Duration {
	age := time.Duration(0)
	for _, r := range retentionRules {
		if r.Name == "orderflow_1m" {
			age = r.Age[t]
		}
	}
	if a := e.eff.Load(); a != nil {
		age = (*a)["orderflow_1m"][t]
	}
	return min(age, HistoryDays*day)
}

// backfillFlow rebuilds missing order-flow minutes of one instrument; returns minutes written.
func (e *Engine) backfillFlow(ctx context.Context, id string, set func(string)) (int, error) {
	now := time.Now().UnixMilli()
	in := e.instFor(id)
	e.mu.RLock()
	if in == nil || in.CtVal <= 0 {
		e.mu.RUnlock()
		return 0, nil
	}
	keep := e.keepFlowFor(e.tierOf(id, now))
	ctVal, tick, lt := in.CtVal, in.TickSz, in.ListTime
	hi := (now - flowGrace.Milliseconds() - minuteMs) / minuteMs * minuteMs // last minute surely closed
	step := 0.0
	if fs := in.flow; fs != nil {
		hi = min(hi, (fs.since+minuteMs-1)/minuteMs*minuteMs) // from here on live recording owns it
		step = fs.step
	}
	e.mu.RUnlock()
	lo := max(now/minuteMs*minuteMs-keep.Milliseconds(), (lt+minuteMs-1)/minuteMs*minuteMs)
	if keep <= 0 || hi <= lo {
		return 0, nil
	}
	have, err := e.st.FlowMinutes(id, lo, hi-1)
	if err != nil {
		return 0, err
	}
	if len(missingRanges(lo, hi, have)) == 0 {
		return 0, nil
	}
	select {
	case flowBackfillSem <- struct{}{}:
		defer func() { <-flowBackfillSem }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if step == 0 {
		step = e.st.LastFlowStep(id)
	}
	stepFor := func(px float64) float64 {
		if step == 0 {
			step = flowStepFor(px, tick)
		}
		return step
	}
	written := 0
	put := func(rows []store.FlowRow) error {
		if err := e.st.PutFlow(rows); err != nil {
			return err
		}
		written += len(rows)
		for _, r := range rows {
			have[r.Ts] = true
		}
		return nil
	}

	// 1) complete days from the daily files
	files, err := e.rest.TradeFiles(ctx, id, lo-utc8DayMs, hi)
	if err != nil {
		log.Printf("orderflow backfill %s: list files: %v", id, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Start < files[j].Start })
	filesEnd := int64(0)
	for i, f := range files {
		a, b := max(f.Start, lo), min(f.Start+utc8DayMs, hi)
		filesEnd = max(filesEnd, f.Start+utc8DayMs)
		if b <= a || len(missingRanges(a, b, have)) == 0 {
			continue
		}
		set(fmt.Sprintf("回补订单流：下载逐笔成交文件 %d/%d（%.0f MB）", i+1, len(files), f.SizeMB))
		var agg *minuteAgg
		err := e.rest.ReadTradeFile(ctx, f.URL, func(t okx.Trade) {
			if agg == nil {
				agg = newMinuteAgg(stepFor(t.Px), ctVal)
			}
			if t.Ts >= a && t.Ts < b {
				agg.add(t)
			}
		})
		if err != nil {
			log.Printf("orderflow backfill %s: %s: %v", id, f.URL, err)
			continue
		}
		if agg == nil {
			continue
		}
		if err := put(agg.rows(id, a, b, have)); err != nil {
			return written, err
		}
	}

	// 2) the rest (not yet published as a file) from history-trades, newest gap first
	rs := missingRanges(max(lo, filesEnd), hi, have)
	pages := 0
	for k := len(rs) - 1; k >= 0 && pages < flowBackfillPages; k-- {
		a, b := rs[k][0], rs[k][1]
		set(fmt.Sprintf("回补订单流：拉取最近逐笔成交（%s 缺口）", fmtAge(time.Duration(b-a)*time.Millisecond)))
		var agg *minuteAgg
		oldest, beforeID := b, int64(0)
		for pages < flowBackfillPages && oldest > a {
			xs, err := e.rest.HistoryTrades(ctx, id, beforeID, b, 100)
			pages++
			if err != nil {
				return written, err
			}
			if len(xs) == 0 {
				oldest = a // nothing older: the range had no trades (or is beyond OKX's history)
				break
			}
			for _, t := range xs {
				if agg == nil {
					agg = newMinuteAgg(stepFor(t.Px), ctVal)
				}
				if t.Ts >= a && t.Ts < b {
					agg.add(t)
				}
				oldest = min(oldest, t.Ts)
			}
			beforeID = xs[len(xs)-1].ID
		}
		// the minute holding the oldest trade seen may be partial unless we got below `a`
		from := a
		if oldest > a {
			from = oldest/minuteMs*minuteMs + minuteMs
		}
		if agg == nil {
			agg = newMinuteAgg(step, ctVal) // no trades: zero rows only
		}
		if from < b {
			if err := put(agg.rows(id, from, b, have)); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}
