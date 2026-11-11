package signals

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	ena  = "ENA-USDT-SWAP"
	pepe = "PEPE-USDT-SWAP"
	btc  = "BTC-USDT-SWAP"
)

func poolSpec(raw string) Spec {
	s, err := ParseSpec(raw)
	if err != nil {
		panic(err)
	}
	return s
}

func TestPoolSpecValidation(t *testing.T) {
	h, from, _ := fixtureHistory(t)
	src := newHistSource(h)
	src.now = from + 60*60_000
	now := time.UnixMilli(src.now)
	good := []struct{ raw, desc string }{
		{`{"type":"pool","metric":"move","windowMin":15,"dir":"up","pct":3}`, "监控池任一币 15 分钟内涨 ≥ 3%"},
		{`{"type":"pool","scope":"pool","top":50,"metric":"move","windowMin":60,"pct":5}`, "监控池前 50任一币 1 小时内涨或跌 ≥ 5%"},
		{`{"type":"pool","scope":"watch","metric":"range","windowMin":1,"pct":2}`, "自选任一币 1 分钟内振幅 ≥ 2%"},
		{`{"type":"pool","scope":"list","instIds":["ena","PEPE"],"metric":"move","windowMin":1440,"dir":"down","pct":10}`, "ENA、PEPE 中任一币 1 天内跌 ≥ 10%"},
		{`{"type":"pool","metric":"field","field":"funding_hourly","op":">","value":0.0005,"exclude":["BTC"]}`, "监控池（除 BTC）任一币"},
	}
	for _, g := range good {
		s := poolSpec(g.raw)
		if err := s.normalize(src, now, 0); err != nil {
			t.Fatalf("%s: %v", g.raw, err)
		}
		if d := s.Describe(); !strings.HasPrefix(d, g.desc) {
			t.Errorf("%s: describe %q, want prefix %q", g.raw, d, g.desc)
		}
	}
	bad := map[string]string{
		`{"type":"pool","metric":"move","windowMin":0,"pct":3}`:                                                    "windowMin",
		`{"type":"pool","metric":"move","windowMin":1441,"pct":3}`:                                                 "windowMin",
		`{"type":"pool","metric":"move","windowMin":5,"pct":0}`:                                                    "pct",
		`{"type":"pool","metric":"move","windowMin":5,"pct":3,"dir":"sideways"}`:                                   "dir",
		`{"type":"pool","metric":"range","windowMin":5,"pct":3,"dir":"up"}`:                                        "no dir",
		`{"type":"pool","metric":"field","field":"nope","op":">","value":1}`:                                       "unknown field",
		`{"type":"pool","metric":"field","field":"turnover12h","op":">","value":1}`:                                "unknown field",
		`{"type":"pool","metric":"field","field":"funding_hourly","op":">"}`:                                       "value",
		`{"type":"pool","metric":"vol","windowMin":5,"pct":3}`:                                                     "metric",
		`{"type":"pool","scope":"list","metric":"move","windowMin":5,"pct":3}`:                                     "instIds",
		`{"type":"pool","scope":"list","instIds":["NOPE"],"metric":"move","windowMin":5,"pct":3}`:                  "unknown instrument",
		`{"type":"pool","scope":"watch","top":5,"metric":"move","windowMin":5,"pct":3}`:                            "scope watch",
		`{"type":"pool","instId":"ENA","metric":"move","windowMin":5,"pct":3}`:                                     "pool takes",
		`{"type":"pool","metric":"move","windowMin":5,"pct":3,"every":"5m"}`:                                       "every",
		`{"type":"any","of":[{"type":"pool","metric":"move","windowMin":5,"pct":3},{"type":"time","afterMin":5}]}`: "cannot sit inside",
	}
	for raw, want := range bad {
		s := poolSpec(raw)
		err := s.normalize(src, now, 0)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", raw, err, want)
		}
	}
}

// The replay of a pool scan equals the hand-computed edges on real history, for moves of several
// windows, directions and cooldowns, and for ranges.
func TestReplayPoolMatchesBruteForce(t *testing.T) {
	h, from, to := fixtureHistory(t)
	cases := []struct {
		raw  string
		cool int
		w    int
		rng  bool
		hit  func(float64) bool
	}{
		{`{"type":"pool","metric":"move","windowMin":15,"dir":"both","pct":2}`, 60, 15, false, func(v float64) bool { return math.Abs(v) >= 2 }},
		{`{"type":"pool","metric":"move","windowMin":15,"dir":"up","pct":1.2}`, 30, 15, false, func(v float64) bool { return v >= 1.2 }},
		{`{"type":"pool","metric":"move","windowMin":15,"dir":"down","pct":1.2}`, 5, 15, false, func(v float64) bool { return v <= -1.2 }},
		{`{"type":"pool","metric":"move","windowMin":1,"pct":0.8}`, 5, 1, false, func(v float64) bool { return math.Abs(v) >= 0.8 }},
		{`{"type":"pool","metric":"move","windowMin":240,"dir":"up","pct":3}`, 240, 240, false, func(v float64) bool { return v >= 3 }},
		{`{"type":"pool","metric":"range","windowMin":5,"pct":1.5}`, 60, 5, true, func(v float64) bool { return v >= 1.5 }},
		{`{"type":"pool","metric":"range","windowMin":30,"pct":2.5}`, 120, 30, true, func(v float64) bool { return v >= 2.5 }},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			rep, err := ReplaySpec(poolSpec(c.raw), h, from, to, c.cool)
			if err != nil {
				t.Fatal(err)
			}
			val := func(bars []Bar, idx map[int64]int, m int64) (float64, bool) { return bruteMove(bars, idx, m, c.w) }
			if c.rng {
				val = func(bars []Bar, idx map[int64]int, m int64) (float64, bool) { return bruteRange(bars, idx, m, c.w) }
			}
			want := brutePool(h, from, to, c.cool, val, c.hit)
			total := 0
			for _, w := range want {
				total += len(w.Insts)
			}
			if rep.FireCount != len(want) {
				t.Fatalf("fires %d, brute force %d", rep.FireCount, len(want))
			}
			if total == 0 && c.cool < 240 {
				t.Fatalf("the case should fire on this history")
			}
			// compare the newest 50 fires member by member
			got := rep.Fires
			want = want[len(want)-len(got):]
			for i := range got {
				var ids []string
				for _, x := range got[i].Hits {
					ids = append(ids, x.InstID)
				}
				sort.Strings(ids)
				if got[i].T != want[i].T || !slices.Equal(ids, want[i].Insts) {
					t.Fatalf("fire %d: got %s %v, want %s %v", i, fmtTime(got[i].T), ids, fmtTime(want[i].T), want[i].Insts)
				}
			}
			sum := 0
			for _, n := range rep.ByInst {
				sum += n
			}
			if sum != total {
				t.Fatalf("byInst sums to %d, brute force %d member fires", sum, total)
			}
		})
	}
}

// 2% in 15 minutes: only ENA and PEPE ever do it in this history; the majors stay quiet.
func TestReplayPoolOnlyVolatileCoinsFire(t *testing.T) {
	h, from, to := fixtureHistory(t)
	rep, err := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":2}`), h, from, to, 60)
	if err != nil {
		t.Fatal(err)
	}
	for id := range rep.ByInst {
		if id != ena && id != pepe {
			t.Fatalf("%s fired; only ENA and PEPE move 2%% in 15 min here (%v)", id, rep.ByInst)
		}
	}
	if rep.ByInst[pepe] == 0 || rep.ByInst[ena] == 0 {
		t.Fatalf("ENA and PEPE should both fire: %v", rep.ByInst)
	}
	if rep.Members != 10 || rep.Minutes != 1800 {
		t.Fatalf("members %d minutes %d", rep.Members, rep.Minutes)
	}
	// top / list / exclude narrow the scan
	rep2, _ := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":2,"exclude":["PEPE"]}`), h, from, to, 60)
	if rep2.ByInst[pepe] != 0 || rep2.ByInst[ena] != rep.ByInst[ena] {
		t.Fatalf("exclude: %v", rep2.ByInst)
	}
	rep3, _ := ReplaySpec(poolSpec(`{"type":"pool","scope":"list","instIds":["ENA","BTC"],"metric":"move","windowMin":15,"pct":2}`), h, from, to, 60)
	if rep3.ByInst[pepe] != 0 || rep3.ByInst[ena] != rep.ByInst[ena] || rep3.Members != 2 {
		t.Fatalf("list: %+v", rep3)
	}
}

// Checked on ticks the replay gives the same answer (a replayed tick is the minute's close).
func TestReplayPoolTickEqualsMinute(t *testing.T) {
	h, from, to := fixtureHistory(t)
	a, _ := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":1.5}`), h, from, to, 30)
	b, err := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":1.5,"every":"tick"}`), h, from, to, 30)
	if err != nil {
		t.Fatal(err)
	}
	if a.FireCount != b.FireCount || a.FireCount == 0 {
		t.Fatalf("minute %d vs tick %d", a.FireCount, b.FireCount)
	}
}

// Single-instrument types on real history: a last-price cross, a 15m close cross and a zone entry
// fire exactly where the bars say.
func TestReplayPriceZoneMatchBruteForce(t *testing.T) {
	h, from, to := fixtureHistory(t)
	bars := h.Bars[btc]
	level := 0.0
	for _, b := range bars {
		level += b.C
	}
	level = math.Round(level / float64(len(bars))) // BTC trades around its mean many times
	// last price above level, cooldown 30: brute force on closes
	rep, err := ReplaySpec(poolSpec(fmt.Sprintf(`{"type":"price","instId":"BTC","op":"above","level":%g}`, level)), h, from, to, 30)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	prev, last := -1, int64(0)
	for _, b := range bars {
		x := 0
		if b.C >= level {
			x = 1
		}
		now := b.T + 60_000
		if prev == 0 && x == 1 && (last == 0 || now-last >= 30*60_000) {
			want = append(want, now)
			last = now
		}
		prev = x
	}
	if rep.FireCount != len(want) || len(want) < 3 {
		t.Fatalf("price: %d fires, brute force %d", rep.FireCount, len(want))
	}
	for i, f := range rep.Fires {
		if f.T != want[len(want)-len(rep.Fires)+i] {
			t.Fatalf("price fire %d at %s, want %s", i, fmtTime(f.T), fmtTime(want[i]))
		}
	}
	// 15m close above the level
	rep15, err := ReplaySpec(poolSpec(fmt.Sprintf(`{"type":"price","instId":"BTC","op":"above","level":%g,"bar":"15m"}`, level)), h, from, to, 5)
	if err != nil {
		t.Fatal(err)
	}
	var want15 []int64 // closes of UTC 15m buckets (the history does not start on a boundary)
	prev = -1
	for i := 0; i < len(bars); i++ {
		if i+1 < len(bars) && bars[i+1].T/900_000 == bars[i].T/900_000 {
			continue // not the bucket's last bar
		}
		x := 0
		if bars[i].C >= level {
			x = 1
		}
		if prev == 0 && x == 1 {
			want15 = append(want15, (bars[i].T/900_000+1)*900_000)
		}
		prev = x
	}
	if rep15.FireCount != len(want15) || len(want15) == 0 {
		t.Fatalf("15m: %d fires, brute force %d", rep15.FireCount, len(want15))
	}
	for i, f := range rep15.Fires {
		if f.T != want15[i] || !strings.Contains(f.Matched, "15m 收盘") {
			t.Fatalf("15m fire %d: %s %q, want %s", i, fmtTime(f.T), f.Matched, fmtTime(want15[i]))
		}
	}
	// zone 0.2% below the level: a close inside, or (on the minute check) a wick through it
	lo, hi := level*0.997, level*0.999
	repZ, err := ReplaySpec(poolSpec(fmt.Sprintf(`{"type":"zone","instId":"BTC","lo":%g,"hi":%g}`, lo, hi)), h, from, to, 60)
	if err != nil {
		t.Fatal(err)
	}
	var wantZ []int64
	prev, last = -1, 0
	for _, b := range bars {
		x := 0
		if (b.C >= lo && b.C <= hi) || (b.L <= hi && b.H >= lo) {
			x = 1
		}
		now := b.T + 60_000
		if prev == 0 && x == 1 && (last == 0 || now-last >= 60*60_000) {
			wantZ = append(wantZ, now)
			last = now
		}
		prev = 0
		if b.C >= lo && b.C <= hi { // the tick state stays true only while the close is inside
			prev = 1
		}
	}
	if repZ.FireCount != len(wantZ) || len(wantZ) == 0 {
		t.Fatalf("zone: %d fires, brute force %d", repZ.FireCount, len(wantZ))
	}
}

func TestReplayRejectsWhatHasNoHistory(t *testing.T) {
	h, from, to := fixtureHistory(t)
	for raw, want := range map[string]string{
		`{"type":"field","instId":"BTC","field":"funding_hourly","op":">","value":0}`:           "no history",
		`{"type":"moverisk","instId":"BTC","side":"top","metric":"pTail","op":"<","value":0.4}`: "no history",
		`{"type":"pool","metric":"field","field":"funding_hourly","op":">","value":0}`:          "no history",
		`{"type":"script"}`: "test_signal_script",
		`{"type":"all","of":[{"type":"price","instId":"BTC","op":"above","level":80000},{"type":"field","instId":"BTC","field":"basis","op":">","value":0}]}`: "no history",
	} {
		if _, err := ReplaySpec(poolSpec(raw), h, from, to, 60); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	// composites of replayable leaves work
	if _, err := ReplaySpec(poolSpec(`{"type":"any","of":[{"type":"price","instId":"BTC","op":"above","level":79000},{"type":"time","afterMin":600}]}`), h, from, to, 60); err != nil {
		t.Fatal(err)
	}
}

// ---------- the live service on the same history ----------

// A pool subscription driven minute by minute fires exactly where the replay does, delivers one
// message per fire with a line per coin, and keeps going until maxFires.
func TestPoolLiveMatchesReplay(t *testing.T) {
	h, from, to := fixtureHistory(t)
	start := from + 120*60_000 // two hours in: 15-minute windows are filled
	s, f, _ := newLiveEnv(t, start, Options{})
	spec := `{"type":"pool","metric":"move","windowMin":15,"pct":1.2}`
	sub, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(spec), Intent: "挑最强的币看是否追多或做空", ExpiresHours: 48, MaxFires: 50, CooldownMin: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sub.Desc, "15 分钟内涨或跌 ≥ 1.2%") || sub.MaxFires != 50 {
		t.Fatalf("sub %+v", sub)
	}
	for m := start + 60_000; m < to; m += 60_000 {
		f.minuteAt(s, m)
	}
	got, err := s.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := ReplaySpec(poolSpec(spec), h, start, to, 30)
	if err != nil {
		t.Fatal(err)
	}
	brute := brutePool(h, start, to, 30, func(bars []Bar, idx map[int64]int, m int64) (float64, bool) { return bruteMove(bars, idx, m, 15) },
		func(v float64) bool { return math.Abs(v) >= 1.2 })
	if rep.FireCount != len(brute) || len(brute) < 10 {
		t.Fatalf("replay %d fires, brute force %d", rep.FireCount, len(brute))
	}
	want := brute[:min(len(brute), 50)] // live stops at maxFires 50
	if len(got.History) != len(want) {
		t.Fatalf("live fired %d times, expected %d", len(got.History), len(want))
	}
	for i, fr := range got.History {
		d := fr.Delivery
		if d == nil || d.V != DeliveryVersion || d.Type != "pool" || d.Metric != "move" || d.FireNo != i+1 || d.FireID != fr.ID || d.SubID != sub.ID {
			t.Fatalf("fire %d delivery %+v", i, d)
		}
		var ids []string
		for _, x := range d.Hits {
			ids = append(ids, x.InstID)
		}
		sort.Strings(ids)
		if fr.Ts != want[i].T || d.More != 0 || !slices.Equal(ids, want[i].Insts) {
			t.Fatalf("fire %d: live %s %v, brute force %s %v", i, fmtTime(fr.Ts), ids, fmtTime(want[i].T), want[i].Insts)
		}
		for j := 1; j < len(d.Hits); j++ {
			if math.Abs(d.Hits[j].Value) > math.Abs(d.Hits[j-1].Value) {
				t.Fatalf("hits not sorted by size: %+v", d.Hits)
			}
		}
		for _, x := range d.Hits {
			if math.Abs(x.Value) < 1.2 || x.Px <= 0 || !strings.Contains(x.Text, "15 分钟内") {
				t.Fatalf("hit %+v", x)
			}
		}
	}
	if len(want) == 50 && (got.Status != StatusFired || !got.History[49].Delivery.Final) {
		t.Fatalf("maxFires 50 reached but status %s", got.Status)
	}
	// every fire woke the chat with a line per coin, up to the daily wake cap; the rest are recorded as skipped
	if len(f.woken) != min(len(got.History), DefaultWakesPerDay) {
		t.Fatalf("woken %d for %d fires", len(f.woken), len(got.History))
	}
	for i, fr := range got.History {
		if want := WakeStarted; i >= DefaultWakesPerDay && fr.Wake != WakeSkipped || i < DefaultWakesPerDay && fr.Wake != want {
			t.Fatalf("fire %d wake %s (%s)", i, fr.Wake, fr.Error)
		}
	}
	msg := f.woken[0]
	for _, x := range got.History[0].Delivery.Hits {
		if !strings.Contains(msg, "· "+x.Text) {
			t.Fatalf("wake message lacks %q:\n%s", x.Text, msg)
		}
	}
	if !strings.Contains(msg, "触发的币") || !strings.Contains(msg, "全池扫描信号") || !strings.Contains(msg, "挑最强的币") {
		t.Fatalf("wake message:\n%s", msg)
	}
	if !strings.Contains(f.notes[0], "⚡ 信号 #") || !strings.Contains(f.notes[0], "%") {
		t.Fatalf("push %q", f.notes[0])
	}
}

// Per-member state across the edge cases: priming at subscribe, a member leaving and rejoining the
// scope, per-member cooldown, maxFires ending the scan, and a restart that continues the state.
func TestPoolLiveLifecycle(t *testing.T) {
	_, from, to := fixtureHistory(t)
	start := from + 60*60_000
	opts := Options{}
	s, f, st := newLiveEnv(t, start, opts)
	spec := `{"type":"pool","scope":"list","instIds":["ENA","PEPE","BTC"],"metric":"move","windowMin":15,"pct":1.5}`
	sub, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(spec), Intent: "看异动的币能不能做", ExpiresHours: 48, MaxFires: 4, CooldownMin: 120})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Progress == nil || !strings.Contains(sub.Progress.Text, "监控 3 个币") {
		t.Fatalf("progress %+v", sub.Progress)
	}
	if !slices.Equal(sub.InstIDs, []string{ena, pepe, btc}) {
		t.Fatalf("instIds %v", sub.InstIDs)
	}
	var fireTimes []int64
	m := start + 60_000
	for ; m < to; m += 60_000 {
		f.minuteAt(s, m)
		x, _ := s.Get(sub.ID)
		if len(x.History) > len(fireTimes) {
			fireTimes = append(fireTimes, x.History[len(x.History)-1].Ts)
		}
		if len(fireTimes) == 2 {
			break
		}
	}
	if len(fireTimes) != 2 {
		t.Fatal("no second fire")
	}
	// per member: no coin fires twice within 120 minutes
	x, _ := s.Get(sub.ID)
	seen := map[string]int64{}
	for _, fr := range x.History {
		for _, h := range fr.Delivery.Hits {
			if t0, ok := seen[h.InstID]; ok && fr.Ts-t0 < 120*60_000 {
				t.Fatalf("%s fired twice within the cooldown", h.InstID)
			}
			seen[h.InstID] = fr.Ts
		}
	}
	// restart: the per-member state is reloaded, nothing re-primes or fires spuriously
	s2, err := New(st, f, Options{Now: f.clock, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	s2.SetWaker(f)
	l := s2.live[sub.ID]
	if l == nil || l.pool == nil || len(l.pool.State) != 3 || len(l.pool.Fired) == 0 {
		t.Fatalf("reloaded pool state %+v", l)
	}
	s2.evaluate(event{kind: "init"})
	if y, _ := s2.Get(sub.ID); len(y.History) != 2 {
		t.Fatalf("init after restart fired: %d", len(y.History))
	}
	// run to the end on the restarted service: maxFires 4 ends it
	for m += 60_000; m < to; m += 60_000 {
		f.minuteAt(s2, m)
	}
	y, _ := s2.Get(sub.ID)
	if y.Status != StatusFired || y.FireCount != 4 || len(y.History) != 4 || !y.History[3].Delivery.Final {
		t.Fatalf("status %s fires %d", y.Status, y.FireCount)
	}
	if _, ok := s2.live[sub.ID]; ok {
		t.Fatal("still live after maxFires")
	}
}

// A member that leaves the scope is forgotten and primes again when it returns: a coin that joins the
// pool while already moving does not fire on arrival.
func TestPoolMembershipChanges(t *testing.T) {
	h, from, to := fixtureHistory(t)
	spec := poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":2}`)
	// find a minute where PEPE's 15m move is ≥ 2% (it would fire if it had been watched false before)
	idx := map[int64]int{}
	for i, b := range h.Bars[pepe] {
		idx[b.T] = i
	}
	var hot int64
	for m := from + 30*60_000; m < to; m += 60_000 {
		if v, ok := bruteMove(h.Bars[pepe], idx, m, 15); ok && math.Abs(v) >= 2 {
			if p, ok := bruteMove(h.Bars[pepe], idx, m-60_000, 15); ok && math.Abs(p) < 2 {
				hot = m
				break
			}
		}
	}
	if hot == 0 {
		t.Fatal("no PEPE move in the fixture")
	}
	s, f, _ := newLiveEnv(t, hot-10*60_000, Options{})
	f.members = []string{btc, ena}
	sub, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec, Intent: "看池子异动的币", ExpiresHours: 48, CooldownMin: 60})
	if err != nil {
		t.Fatal(err)
	}
	for m := hot - 9*60_000; m < hot; m += 60_000 {
		f.minuteAt(s, m)
	}
	f.mu.Lock()
	f.members = []string{btc, ena, pepe} // PEPE joins exactly when its move crosses 2%
	f.mu.Unlock()
	f.minuteAt(s, hot)
	x, _ := s.Get(sub.ID)
	for _, fr := range x.History {
		for _, hh := range fr.Delivery.Hits {
			if hh.InstID == pepe {
				t.Fatalf("PEPE fired on arrival at %s", fmtTime(fr.Ts))
			}
		}
	}
	if st := s.live[sub.ID].pool.State[pepe]; st != 1 {
		t.Fatalf("PEPE should be primed true, got %d", st)
	}
	// leaving forgets it
	f.mu.Lock()
	f.members = []string{btc, ena}
	f.mu.Unlock()
	f.minuteAt(s, hot+60_000)
	if _, ok := s.live[sub.ID].pool.State[pepe]; ok {
		t.Fatal("PEPE state kept after leaving the scope")
	}
}

func TestPoolSubscribeRules(t *testing.T) {
	_, from, _ := fixtureHistory(t)
	s, f, _ := newLiveEnv(t, from+60*60_000, Options{})
	// already moving members are fine (they are primed), the subscription is not rejected
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":0.01}`), Intent: "全部异动都看看"}); err != nil {
		t.Fatalf("a scan whose members already hold must be accepted: %v", err)
	}
	f.members = []string{}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":2}`), Intent: "全部异动都看看"}); err == nil ||
		!strings.Contains(err.Error(), "no instruments") {
		t.Fatalf("empty scope: %v", err)
	}
	f.members = nil
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":2}`), Intent: "全部异动都看看", MaxFires: 51}); err == nil {
		t.Fatal("maxFires 51 accepted")
	}
	// no data at all (clock far after the history): rejected
	f.now = from + 400*24*60*60_000
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":2}`), Intent: "全部异动都看看"}); err == nil ||
		!strings.Contains(err.Error(), "no current data") {
		t.Fatalf("stale data: %v", err)
	}
}

// Deliveries of single-instrument types carry the instrument and its price; the API returns them.
func TestDeliveryForSingleTypesAndHTTP(t *testing.T) {
	h, from, to := fixtureHistory(t)
	start := from + 30*60_000
	s, f, _ := newLiveEnv(t, start, Options{})
	px := h.Bars[btc][31].C
	sub, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(fmt.Sprintf(`{"type":"price","instId":"BTC","op":"above","level":%g,"bar":"1m"}`, px*1.002)),
		Intent: "BTC 站上就看多", ExpiresHours: 48})
	if err != nil {
		t.Fatal(err)
	}
	for m := start + 60_000; m < to; m += 60_000 {
		f.minuteAt(s, m)
		if x, _ := s.Get(sub.ID); x.Status != StatusActive {
			break
		}
	}
	x, _ := s.Get(sub.ID)
	if len(x.History) != 1 {
		t.Fatalf("history %d", len(x.History))
	}
	d := x.History[0].Delivery
	if d == nil || d.Type != "price" || len(d.Hits) != 1 || d.Hits[0].InstID != btc || d.Hits[0].Px <= 0 || !d.Final || d.Matched == "" || d.Intent != "BTC 站上就看多" {
		t.Fatalf("delivery %+v", d)
	}
	if !strings.Contains(f.woken[0], "触发值：") || strings.Contains(f.woken[0], "全池扫描") {
		t.Fatalf("single-type wake message:\n%s", f.woken[0])
	}
	mux := http.NewServeMux()
	s.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/signals/%d", sub.ID), nil))
	var got struct {
		History []struct {
			Delivery map[string]any `json:"delivery"`
		} `json:"history"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.History) != 1 || got.History[0].Delivery["v"] != float64(1) || got.History[0].Delivery["hits"] == nil {
		t.Fatalf("GET signal: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/signals/catalog", nil))
	if !strings.Contains(rec.Body.String(), `"type":"pool"`) || !strings.Contains(rec.Body.String(), `"deliveryVersion":1`) {
		t.Fatalf("catalog: %s", rec.Body.String()[:200])
	}
}

// Every catalog example with concrete values parses; the replayable flags agree with replayable().
func TestCatalog(t *testing.T) {
	for _, c := range Catalog() {
		if !strings.HasPrefix(c.Example, "{") || strings.Contains(c.Example, "…") || strings.Contains(c.Example, "+") {
			continue
		}
		s, err := ParseSpec(c.Example)
		if err != nil {
			t.Fatalf("%s example: %v", c.Type, err)
		}
		if (replayable(s) == nil) != c.Replayable {
			t.Errorf("%s: catalog replayable %v disagrees", c.Type, c.Replayable)
		}
	}
}

// The replay tool and REST route on live-shaped history.
func TestSpecReplayToolAndHTTP(t *testing.T) {
	_, _, to := fixtureHistory(t)
	s, _, _ := newLiveEnv(t, to-60_000, Options{})
	out, err := s.TestSpecTool(t.Context(), 12, map[string]string{"spec": `{"type":"pool","metric":"move","windowMin":15,"pct":2}`, "hours": "24", "cooldownMin": "60"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["fireCount"].(int) == 0 || m["members"].(int) != 10 || len(m["lastFires"].([]map[string]any)) == 0 {
		t.Fatalf("tool %+v", m)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/signals/test", strings.NewReader(`{"spec":{"type":"pool","metric":"range","windowMin":5,"pct":1.5},"hours":12}`)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"fireCount"`) {
		t.Fatalf("POST test: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/signals/test", strings.NewReader(`{"spec":{"type":"pool","metric":"move","windowMin":15,"pct":2},"hours":100}`)))
	if rec.Code != 400 {
		t.Fatalf("hours 100: %d", rec.Code)
	}
}
