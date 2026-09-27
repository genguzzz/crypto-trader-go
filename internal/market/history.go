package market

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync/atomic"
	"time"
)

// HistoryDays of 1m candles backfilled (SQLite only) for instruments that were in the pool during
// the replay window, plus watchlist and current pool — used by charts and future research. The
// actual depth per instrument is capped by its retention tier (retention.go).
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
	var done, bars atomic.Int64
	start := time.Now()
	e.historyMsg.Store(fmt.Sprintf("1m 回补 0/%d 个合约", len(ids)))
	parallel(ids, 4, func(id string) {
		defer func() {
			d := done.Add(1)
			e.historyMsg.Store(fmt.Sprintf("1m 回补 %d/%d 个合约（已写 %d 根，%s）", d, len(ids), bars.Load(), time.Since(start).Round(time.Second)))
		}()
		// depth follows the retention tier (pool/watch 7 days, others less) so it never fights eviction
		e.mu.RLock()
		keep := e.keep1mFor(e.tierOf(id, time.Now().UnixMilli()))
		e.mu.RUnlock()
		n, err := e.historyFill(ctx, id, keep, false)
		if err != nil {
			log.Printf("history %s: %v", id, err)
		}
		bars.Add(int64(n))
	})
	e.historyMsg.Store(fmt.Sprintf("完成：%d 个合约近 %d 天 1m K 线，新写入 %d 根，用时 %s", len(ids), HistoryDays, bars.Load(), time.Since(start).Round(time.Second)))
	log.Printf("deep history: %d instruments, %d bars in %s", len(ids), bars.Load(), time.Since(start).Round(time.Second))
}
