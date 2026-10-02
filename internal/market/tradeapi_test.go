package market

import (
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

func TestBarsTF(t *testing.T) {
	st := openStore(t)
	e := New(Config{Threshold: 1, TopN: 10}, okx.NewREST(), st)
	id := "X-USDT-SWAP"
	in := newInst(id)
	e.insts[id] = in
	e.catalog[id] = okx.Instrument{InstID: id, CtVal: 1, TickSz: 0.01, LotSz: 1, MinSz: 1, MaxLever: 50}
	now := time.Now().UnixMilli()
	h0 := now/hourMs*hourMs - 2*hourMs
	// two full hours in memory plus 7 minutes of the current one; the first hour also as a stored 1H bar
	for ts := h0; ts < h0+2*hourMs+7*minuteMs; ts += minuteMs {
		k := float64((ts - h0) / minuteMs)
		in.putBar(store.Bar{Ts: ts, O: k, H: k + 0.5, L: k - 0.5, C: k + 0.25, Quote: 1})
	}
	st.PutBars("candles_1h", []store.InstBar{{InstID: id, Bar: store.Bar{Ts: h0, O: 0, H: 59.5, L: -0.5, C: 59.25, Quote: 60}}})
	e.lastMin = h0 + 2*hourMs + 6*minuteMs // pool clock: minute 6 of the forming hour confirmed

	bars, err := e.BarsTF(id, "5m", 3)
	if err != nil || len(bars) != 3 {
		t.Fatalf("5m: %v %v", bars, err)
	}
	last := bars[2]
	if last.Ts != h0+2*hourMs || last.O != 120 || last.C != 124.25 || last.H != 124.5 || last.Quote != 5 {
		t.Fatalf("last confirmed 5m bar %+v", last)
	}
	hs, _ := e.BarsTF(id, "1H", 5)
	if len(hs) != 2 || hs[1].Ts != h0+hourMs || hs[1].O != 60 || hs[1].C != 119.25 || hs[1].Quote != 60 {
		t.Fatalf("1H bars %+v (forming hour must be excluded, missing 1H built from 1m)", hs)
	}
	if _, err := e.BarsTF(id, "2H", 1); err == nil {
		t.Fatal("unsupported timeframe accepted")
	}

	in.fund.addSettled(store.FundingSample{InstID: id, Ts: h0, Rate: 0.0003, FundingTime: h0, Settled: true})
	if r, ok := e.SettledFunding(id, h0); !ok || r != 0.0003 {
		t.Fatalf("funding %v %v", r, ok)
	}
	if _, ok := e.SettledFunding(id, h0+hourMs); ok {
		t.Fatal("funding at a non-settlement hour")
	}

	e.Keep(id, true)
	if !e.recording(in, now) || !e.keep[id] {
		t.Fatal("kept instrument not recording")
	}
	e.Keep(id, false)
	if e.keep[id] {
		t.Fatal("keep not released")
	}
	if w, _ := st.Watchlist(); len(w) != 0 {
		t.Fatal("keep touched the watchlist")
	}
	if m, ok := e.InstrumentMeta(id); !ok || m.LotSz != 1 || m.MaxLever != 50 {
		t.Fatalf("meta %+v", m)
	}
}
