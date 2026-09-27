package market

import (
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
)

// Opening an instrument outside the recording universe (found via search) creates it from the
// catalog, streams it and downloads ~7 days of 1m history on demand; nothing is fetched before.
func TestTouchDownloadsHistoryOnDemand(t *testing.T) {
	st := openStore(t)
	var err error
	last := time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs
	reqs := 0
	srv := fakeOKX(t, last, &reqs)
	defer srv.Close()
	rest := okx.NewREST()
	rest.BaseURL = srv.URL
	e := New(Config{Threshold: 1, TopN: 10, StorageCap: 1 << 30}, rest, st)
	e.phase.Store("live")
	runWriter(t, e)
	id := "ADHOC-USDT-SWAP"
	e.catalog[id] = okx.Instrument{InstID: id, Category: "3", CtVal: 1}

	if items := e.Catalog(); len(items) != 1 || items[0].Recorded {
		t.Fatalf("catalog: %+v", items)
	}
	if reqs != 0 {
		t.Fatalf("catalog listing must not download data (%d requests)", reqs)
	}
	if e.Touch("NOPE-USDT-SWAP") != nil {
		t.Fatal("unknown instrument accepted")
	}
	in := e.Touch(id)
	if in == nil || in.Eligible {
		t.Fatalf("adhoc instrument not created: %+v", in)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		e.mu.RLock()
		busy := in.syncing
		e.mu.RUnlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sync did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// the 24h fast-path bars reach SQLite via the batched writer (every 2s; slower under -race)
	var n int
	for flush := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		_, n, err = st.Span("candles_1m", id, last-int64(HistoryDays)*24*hourMs+minuteMs)
		if err == nil && n >= HistoryDays*1440*99/100 {
			break
		}
		if time.Now().After(flush) {
			t.Fatalf("7-day history not stored: %d bars (%v)", n, err)
		}
	}
	e.mu.RLock()
	tier := e.tierOf(id, time.Now().UnixMilli())
	rec := e.recording(in, time.Now().UnixMilli())
	e.mu.RUnlock()
	if tier != TierViewed || !rec {
		t.Fatalf("tier %s recording %v", tierNames[tier], rec)
	}
	info, ok := e.Instrument(id)
	if !ok || info.Eligible || info.Row == nil || info.Row.Status != "live" {
		t.Fatalf("detail: ok %v %+v", ok, info.Row)
	}
}
