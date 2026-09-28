package market

import (
	"context"
	"time"

	"crypto-trader-go/internal/features"
)

// PreviewEvery is the intraday refresh cadence of the fact panel and scores (user request).
const PreviewEvery = 10 * time.Second

// previewSeries treats the still-forming 1m bar as if it closed at T (= end of the current
// minute): Close(T) is the freshest price, the forming bar's turnover is included, and OI/funding
// use the latest pushed values. Preview values repaint; only minute-confirmed values are recorded
// into rule track records.
type previewSeries struct {
	liveSeries
	T   int64
	now int64
}

func (s previewSeries) formingOK() bool { return s.in.live.Ts == s.T-minuteMs }

func (s previewSeries) Close(t int64) (float64, bool) {
	if t == s.T {
		if p := s.in.price(); p > 0 && (s.formingOK() || s.now-s.in.tickTs < 15_000) {
			return p, true
		}
		return 0, false
	}
	return s.liveSeries.Close(t)
}

func (s previewSeries) Quote(from, to int64) (float64, bool) {
	if to != s.T {
		return s.liveSeries.Quote(from, to)
	}
	if !s.formingOK() {
		return 0, false
	}
	q, ok := s.liveSeries.Quote(from, to-minuteMs)
	return q + s.in.live.Quote, ok
}

func (s previewSeries) OI(t int64) (float64, bool) {
	if t == s.T && s.in.oiNow.Ts > 0 && s.now-s.in.oiNow.Ts < 2*minuteMs {
		return s.in.oiNow.OiCcy, true
	}
	return s.liveSeries.OI(t)
}

// previewLoop recomputes values, percentiles and scores for active ∪ watched rows every
// PreviewEvery. Pool membership itself still changes only on confirmed minutes.
func (e *Engine) previewLoop(ctx context.Context) {
	t := time.NewTicker(PreviewEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if e.Phase() == "live" {
				e.computePreview()
			}
		}
	}
}

func (e *Engine) computePreview() {
	old := e.snap.Load()
	now := time.Now().UnixMilli()
	if old == nil || len(old.Rows) == 0 || now-old.ComputedAt < 3000 {
		return // a confirmed computation just happened
	}
	T := now/minuteMs*minuteMs + minuteMs
	s := *old
	s.Rows = append([]Row(nil), old.Rows...)
	e.mu.RLock()
	btc := e.insts[BTC]
	for i := range s.Rows {
		r := &s.Rows[i]
		r.Watch = e.watch[r.InstID]
		if !r.Active && !r.Watch {
			continue
		}
		in := e.insts[r.InstID]
		if in == nil {
			continue
		}
		var bs features.Series
		if btc != nil && in.ID != BTC { // BTC vs itself carries no information
			bs = previewSeries{liveSeries{btc}, T, now}
		}
		v := features.Compute(previewSeries{liveSeries{in}, T, now}, bs, T)
		in.liveValues(v, r.Turnover12h)
		r.Values = v
		r.Last = in.price()
	}
	e.mu.RUnlock()
	e.fillPercentiles(s.Rows)
	s.Seq = e.seq.Add(1)
	s.Preview, s.PreviewAt = true, now
	s.Phase = e.Phase()
	s.Provisional = s.Phase != "live"
	e.scoreRows(&s)
	e.snap.Store(&s)
	e.broadcast(Event{Type: "universe", Data: map[string]any{"seq": s.Seq, "asOfMinute": s.AsOfMinute, "phase": s.Phase, "active": s.Coverage.Active, "preview": true}})
}
