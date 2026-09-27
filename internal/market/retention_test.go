package market

import (
	"fmt"
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

func retentionEngine(t *testing.T, capBytes int64) (*Engine, *store.Store) {
	t.Helper()
	st := openStore(t)
	e := New(Config{Threshold: 1, TopN: 10, StorageCap: capBytes}, okx.NewREST(), st)
	e.phase.Store("live")
	return e, st
}

func count1m(t *testing.T, st *store.Store, id string, olderThan int64) int {
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM candles_1m WHERE inst_id=? AND ts<?`, id, olderThan).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRetentionAgesPerTier(t *testing.T) {
	e, st := retentionEngine(t, 100<<30) // cap far away: ages only
	now := time.Now().UnixMilli()
	ages := []time.Duration{time.Hour, 36 * time.Hour, 3 * day, 10 * day, 40 * day}
	var bars []store.InstBar
	for _, id := range []string{"W", "P", "V", "B", "N"} {
		for _, a := range ages {
			bars = append(bars, store.InstBar{InstID: id, Bar: store.Bar{Ts: now - a.Milliseconds(), C: 1}})
		}
	}
	if err := st.PutBars("candles_1m", bars); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"W", "P", "V", "B", "N"} {
		e.catalog[id] = okx.Instrument{InstID: id}
	}
	e.insts["B"] = &Inst{ID: "B", Eligible: true}
	e.watch["W"] = true
	e.lastPool["P"] = now - day.Milliseconds()
	e.views["V"] = now - day.Milliseconds()
	e.runRetention()
	want := map[string]int{"W": 4, "P": 3, "V": 3, "B": 2, "N": 0}
	for id, n := range want {
		if got := count1m(t, st, id, now+1); got != n {
			t.Errorf("%s: kept %d bars, want %d", id, got, n)
		}
	}
	rep := e.Retention()
	if rep == nil || rep.Tiers["watch"] != 1 || rep.Tiers["none"] != 1 {
		t.Fatalf("report tiers: %+v", rep)
	}
}

func TestRetentionCapacityEvictsLowTierFirst(t *testing.T) {
	e, st := retentionEngine(t, 1<<20) // 1 MB cap → ~550 KB for 1m candles
	now := time.Now().UnixMilli() / minuteMs * minuteMs
	var bars []store.InstBar
	add := func(id string, minutes int) {
		for k := 0; k < minutes; k++ {
			bars = append(bars, store.InstBar{InstID: id, Bar: store.Bar{Ts: now - int64(k)*minuteMs, O: 1, H: 1, L: 1, C: 1, Quote: 1}})
		}
	}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("B%02d-USDT-SWAP", i)
		add(id, 2*1440) // 2 days: within the base age, so only capacity can cut it
		e.insts[id] = &Inst{ID: id, Eligible: true}
		e.catalog[id] = okx.Instrument{InstID: id}
	}
	add("W-USDT-SWAP", 4*1440)
	e.watch["W-USDT-SWAP"] = true
	e.catalog["W-USDT-SWAP"] = okx.Instrument{InstID: "W-USDT-SWAP"}
	if err := st.PutBars("candles_1m", bars); err != nil {
		t.Fatal(err)
	}
	e.runRetention()
	floorB := now - (26 * time.Hour).Milliseconds()
	if n := count1m(t, st, "B00-USDT-SWAP", floorB); n != 0 {
		t.Errorf("base tier not cut to its 26h floor: %d older bars", n)
	}
	if n := count1m(t, st, "B00-USDT-SWAP", now+1); n < 1500 {
		t.Errorf("base tier cut below its floor: %d bars left", n)
	}
	// watch is tightened last and never below 3 days
	if n := count1m(t, st, "W-USDT-SWAP", now+1); n < 3*1440-1 { // boundary minute is cut
		t.Errorf("watch tier cut below 3d floor: %d bars", n)
	}
	rep := e.Retention()
	if !rep.Tables[0].Tight || rep.Tables[0].Ages[TierBase] != "26h" {
		t.Errorf("report should show the squeezed base age: %+v", rep.Tables[0])
	}
}

func TestRetentionSkipsBeforeInstrumentsLoad(t *testing.T) {
	e, st := retentionEngine(t, 100<<30)
	e.phase.Store("starting")
	now := time.Now().UnixMilli()
	st.PutBars("candles_1m", []store.InstBar{{InstID: "X", Bar: store.Bar{Ts: now - 10*day.Milliseconds(), C: 1}}})
	e.runRetention()
	if count1m(t, st, "X", now) != 1 {
		t.Fatal("retention ran before the instrument list was loaded")
	}
}
