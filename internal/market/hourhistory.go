package market

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"crypto-trader-go/internal/store"
)

// HourHistoryDays of 1H candles kept for every recorded instrument (retention.go) and backfilled
// from /market/history-candles, so strategy backtests can replay any window of the past year plus
// its warm-up. Settled funding is paged as far back as OKX serves it (about three months).
const HourHistoryDays = 400

const (
	hourHistoryEvery = 24 * time.Hour
	hourHistoryPage  = 100 // /market/history-candles returns at most 100 bars per request
	fundingPages     = 6   // funding-rate-history pages per instrument and run (100 settlements each)
)

// hourHistoryLoop runs the deep 1H backfill once the engine is live, then daily (new listings,
// holes left by outages). It shares the history-candles rate budget with the 1m deep history.
func (e *Engine) hourHistoryLoop(ctx context.Context) {
	for e.Phase() != "live" {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
	select { // let the 1m deep history (pool instruments, charts) go first
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	for {
		e.deepHourly(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(hourHistoryEvery):
		}
	}
}

// deepHourly fills 1H candles and settled funding for every recorded instrument.
func (e *Engine) deepHourly(ctx context.Context) {
	e.mu.RLock()
	ids := e.eligibleIDs()
	e.mu.RUnlock()
	var done, bars, funds atomic.Int64
	start := time.Now()
	e.hourHistMsg.Store(fmt.Sprintf("1H 回补 0/%d 个合约", len(ids)))
	parallel(ids, 2, func(id string) {
		if ctx.Err() != nil {
			return
		}
		n, err := e.hourFill(ctx, id)
		if err != nil {
			log.Printf("hour history %s: %v", id, err)
		}
		f, err := e.fundingFill(ctx, id)
		if err != nil {
			log.Printf("funding history %s: %v", id, err)
		}
		bars.Add(int64(n))
		funds.Add(int64(f))
		d := done.Add(1)
		e.hourHistMsg.Store(fmt.Sprintf("1H 回补 %d/%d 个合约（已写 %d 根 1H、%d 条资金费，%s）", d, len(ids), bars.Load(), funds.Load(),
			time.Since(start).Round(time.Second)))
	})
	e.hourHistMsg.Store(fmt.Sprintf("完成：%d 个合约近 %d 天 1H K 线，新写入 %d 根、资金费 %d 条，用时 %s（每天复查一次）",
		len(ids), HourHistoryDays, bars.Load(), funds.Load(), time.Since(start).Round(time.Second)))
	log.Printf("hour history: %d instruments, %d bars, %d funding in %s", len(ids), bars.Load(), funds.Load(), time.Since(start).Round(time.Second))
}

// hourFill pages 1H candles back to HourHistoryDays (or the listing). When older history is
// missing it pages down from the earliest stored bar; when coverage is still below 99 % (holes in
// the middle, e.g. downtime) it re-pages from now. Existing rows are replaced with identical data.
func (e *Engine) hourFill(ctx context.Context, id string) (int, error) {
	now := time.Now().UnixMilli()
	e.mu.RLock()
	lt := int64(0)
	if in := e.insts[id]; in != nil {
		lt = in.ListTime
	}
	e.mu.RUnlock()
	last := now/hourMs*hourMs - hourMs
	lo := max(last-(HourHistoryDays*24-1)*hourMs, (lt+hourMs-1)/hourMs*hourMs)
	want := int((last-lo)/hourMs) + 1
	if want <= 0 {
		return 0, nil
	}
	earliest, n, err := e.st.Span("candles_1h", id, lo)
	if err != nil {
		return 0, err
	}
	if n >= want*99/100 {
		return 0, nil
	}
	after := now
	if earliest > lo+hourMs {
		after = earliest
	}
	wrote := 0
	for after > lo {
		cs, err := e.rest.HistoryCandles(ctx, id, "1H", after, hourHistoryPage)
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
		if err := e.st.PutBars("candles_1h", batch); err != nil {
			return wrote, err
		}
		wrote += len(batch)
		next := cs[len(cs)-1].Ts
		if next >= after { // no progress: stop rather than loop
			break
		}
		after = next
	}
	return wrote, nil
}

// fundingFill pages settled funding back as far as OKX serves it (bounded per run). Rows are
// keyed by settlement time, so re-fetching only replaces identical samples.
func (e *Engine) fundingFill(ctx context.Context, id string) (int, error) {
	floor := time.Now().Add(-HourHistoryDays * day).UnixMilli()
	after, wrote := int64(0), 0
	for page := 0; page < fundingPages; page++ {
		xs, err := e.rest.FundingHistory(ctx, id, after, 100)
		if err != nil {
			return wrote, err
		}
		if len(xs) == 0 {
			break
		}
		batch := make([]store.FundingSample, 0, len(xs))
		oldest := xs[0].FundingTime
		for _, x := range xs {
			oldest = min(oldest, x.FundingTime)
			if x.FundingTime >= floor {
				batch = append(batch, store.FundingSample{InstID: id, Ts: x.FundingTime, Rate: x.Rate, FundingTime: x.FundingTime, Settled: true})
			}
		}
		if err := e.st.PutFunding(batch); err != nil {
			return wrote, err
		}
		wrote += len(batch)
		if len(xs) < 100 || oldest <= floor || (after > 0 && oldest >= after) {
			break
		}
		after = oldest
	}
	return wrote, nil
}
