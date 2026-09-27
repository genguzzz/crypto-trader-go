package market

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync/atomic"
	"time"

	"crypto-trader-go/internal/store"
)

// HistoryDays of 1m candles backfilled (SQLite only) for instruments that were in the pool during
// the replay window, plus watchlist and current pool — used by charts and future research.
const HistoryDays = 7

// deepHistory pages /market/history-candles backwards from the earliest stored 1m bar down to
// now−HistoryDays. It uses its own rate budget and runs in the background after go-live.
func (e *Engine) deepHistory(ctx context.Context, cands []string) {
	e.histMu.Lock()
	if e.histRunning {
		e.histPending = append(e.histPending, cands...)
		e.histMu.Unlock()
		return
	}
	e.histRunning = true
	e.histMu.Unlock()
	defer func() {
		e.histMu.Lock()
		next := e.histPending
		e.histPending, e.histRunning = nil, false
		e.histMu.Unlock()
		if len(next) > 0 {
			go e.deepHistory(ctx, next)
		}
	}()
	set := map[string]bool{}
	for _, id := range cands {
		set[id] = true
	}
	e.mu.RLock()
	for id := range e.watch {
		set[id] = true
	}
	for _, r := range e.snap.Load().Rows {
		if r.Active {
			set[r.InstID] = true
		}
	}
	e.mu.RUnlock()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	now := time.Now().UnixMilli()
	floor := now/minuteMs*minuteMs - int64(HistoryDays)*24*hourMs
	want := HistoryDays * 1440
	var done, bars atomic.Int64
	start := time.Now()
	e.historyMsg.Store(fmt.Sprintf("1m 回补 0/%d 个合约", len(ids)))
	parallel(ids, 4, func(id string) {
		defer func() {
			d := done.Add(1)
			e.historyMsg.Store(fmt.Sprintf("1m 回补 %d/%d 个合约（已写 %d 根，%s）", d, len(ids), bars.Load(), time.Since(start).Round(time.Second)))
		}()
		e.mu.RLock()
		lt := e.insts[id].ListTime
		e.mu.RUnlock()
		lo := max(floor, lt)
		earliest, n, err := e.st.Span("candles_1m", id, lo)
		if err != nil || (n >= want*99/100) || (earliest > 0 && earliest <= lo+minuteMs && n > 0) {
			return
		}
		after := earliest
		if after == 0 {
			after = now
		}
		for after > lo {
			cs, err := e.rest.HistoryCandles(ctx, id, "1m", after, 300)
			if err != nil || len(cs) == 0 {
				return
			}
			batch := make([]store.InstBar, 0, len(cs))
			for _, c := range cs {
				if c.Confirmed && c.Ts >= lo {
					batch = append(batch, store.InstBar{InstID: id, Bar: store.Bar{Ts: c.Ts, O: c.O, H: c.H, L: c.L, C: c.C, Vol: c.Vol, VolCcy: c.VolCcy, Quote: c.VolCcyQuote}})
				}
			}
			if err := e.st.PutBars("candles_1m", batch); err != nil {
				log.Printf("history %s: %v", id, err)
				return
			}
			bars.Add(int64(len(batch)))
			after = cs[len(cs)-1].Ts
		}
	})
	e.historyMsg.Store(fmt.Sprintf("完成：%d 个合约近 %d 天 1m K 线，新写入 %d 根，用时 %s", len(ids), HistoryDays, bars.Load(), time.Since(start).Round(time.Second)))
	log.Printf("deep history: %d instruments, %d bars in %s", len(ids), bars.Load(), time.Since(start).Round(time.Second))
}
