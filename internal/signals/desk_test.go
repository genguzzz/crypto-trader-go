package signals

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

// A combo scan fires a coin only when every leg holds for it: the replay equals a brute force that
// ANDs the two measurements computed straight from the bars.
func TestReplayComboMatchesBruteForce(t *testing.T) {
	h, from, to := fixtureHistory(t)
	raw := `{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":15,"dir":"up","pct":1.2},{"metric":"range","windowMin":5,"pct":0.8}]}`
	rep, err := ReplaySpec(poolSpec(raw), h, from, to, 30)
	if err != nil {
		t.Fatal(err)
	}
	// encode "both legs known and true" as one value: the move when the range leg holds, else 0
	val := func(bars []Bar, idx map[int64]int, m int64) (float64, bool) {
		mv, ok := bruteMove(bars, idx, m, 15)
		rg, ok2 := bruteRange(bars, idx, m, 5)
		if !ok || !ok2 {
			return 0, false
		}
		if rg < 0.8 {
			return 0, true
		}
		return mv, true
	}
	want := brutePool(h, from, to, 30, val, func(v float64) bool { return v >= 1.2 })
	if len(want) == 0 {
		t.Fatal("the case should fire on this history")
	}
	if rep.FireCount != len(want) {
		t.Fatalf("fires %d, brute force %d", rep.FireCount, len(want))
	}
	got := rep.Fires
	want = want[len(want)-len(got):]
	for i := range got {
		var ids []string
		for _, x := range got[i].Hits {
			ids = append(ids, x.InstID)
			if !strings.Contains(x.Text, "；") || !strings.Contains(x.Text, "振幅") {
				t.Fatalf("hit text should carry both legs: %q", x.Text)
			}
		}
		sort.Strings(ids)
		if got[i].T != want[i].T || !slices.Equal(ids, want[i].Insts) {
			t.Fatalf("fire %d: got %s %v, want %s %v", i, fmtTime(got[i].T), ids, fmtTime(want[i].T), want[i].Insts)
		}
	}
	// the single move leg alone fires at least as often
	alone, err := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"dir":"up","pct":1.2}`), h, from, to, 30)
	if err != nil {
		t.Fatal(err)
	}
	if alone.FireCount < rep.FireCount {
		t.Fatalf("combo fired %d times, its first leg alone %d", rep.FireCount, alone.FireCount)
	}
}

func TestComboValidation(t *testing.T) {
	_, from, _ := fixtureHistory(t)
	s, _, _ := newLiveEnv(t, from+60*60_000, Options{})
	bad := map[string]string{
		`{"type":"pool","metric":"combo"}`:                                                                                                            "2–4 legs",
		`{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":15,"pct":2}]}`:                                                          "2–4 legs",
		`{"type":"pool","metric":"combo","pct":2,"legs":[{"metric":"move","windowMin":15,"pct":2},{"metric":"range","windowMin":5,"pct":1}]}`:         "only legs",
		`{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":15,"pct":2},{"metric":"combo"}]}`:                                       "leg 2",
		`{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":15,"pct":2},{"metric":"range","windowMin":5,"pct":1,"scope":"watch"}]}`: "leg 2",
		`{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":15,"pct":2},{"metric":"field","field":"nope","op":">","value":1}]}`:     "unknown field",
		`{"type":"pool","metric":"move","windowMin":15,"pct":2,"legs":[{"metric":"range","windowMin":5,"pct":1}]}`:                                    "legs belong to metric combo",
	}
	for raw, want := range bad {
		_, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(raw), Intent: "看异动的币能不能做"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error containing %q", raw, err, want)
		}
	}
	spec := poolSpec(`{"type":"pool","top":50,"metric":"combo","legs":[{"metric":"move","windowMin":60,"dir":"up","pct":4},{"metric":"range","windowMin":15,"pct":2}]}`)
	sub, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec, Intent: "看异动的币能不能做"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "监控池前 50任一币同时满足：1 小时内涨 ≥ 4% + 15 分钟内振幅 ≥ 2%"; sub.Desc != want {
		t.Fatalf("desc %q, want %q", sub.Desc, want)
	}
	// a field leg has no history: the replay refuses it
	withField := poolSpec(`{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":15,"pct":2},{"metric":"field","field":"oi_chg_1h","op":">","value":0.02}]}`)
	h, a, b := fixtureHistory(t)
	if _, err := ReplaySpec(withField, h, a, b, 30); err == nil || !strings.Contains(err.Error(), "no history") {
		t.Fatalf("replay of a combo with a field leg: %v", err)
	}
}

// A desk subscription is standing: outside the caps, never used up, and its fires go to the desk's
// sink instead of waking a chat or pushing a note.
func TestDeskSubscription(t *testing.T) {
	_, from, to := fixtureHistory(t)
	start := from + 60*60_000
	s, f, st := newLiveEnv(t, start, Options{MaxActive: 1})
	var got []Delivery
	s.SetDesk(func(d Delivery, sub Subscription) {
		if sub.Kind != KindDesk || (sub.Label != "surge" && sub.Label != "other") {
			t.Errorf("sink got %s %q", sub.Kind, sub.Label)
		}
		got = append(got, d)
	})
	spec := `{"type":"pool","scope":"list","instIds":["ENA","PEPE","BTC"],"metric":"move","windowMin":15,"pct":1.2}`
	sub, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(spec), Intent: "交易台策略扫描", CooldownMin: 30, Desk: "surge"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Kind != KindDesk || sub.Subscriber != "交易台 · surge" || sub.ExpiresAt < to+50*365*86_400_000 {
		t.Fatalf("desk sub %+v", sub)
	}
	// it does not use the agent's cap: with MaxActive 1 a chat can still subscribe, and a second desk scan too
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(spec), Intent: "看异动的币能不能做"}); err != nil {
		t.Fatalf("chat subscription beside a desk scan: %v", err)
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: poolSpec(spec), Intent: "交易台策略扫描", CooldownMin: 60, Desk: "other"}); err != nil {
		t.Fatalf("second desk scan: %v", err)
	}
	if c := s.Caps(); c.Active != 1 {
		t.Fatalf("caps count %d active, want 1 (desk scans excluded)", c.Active)
	}
	// and they are not among the subscriptions the app and the agent list, unless asked for by kind
	if all, _ := s.List(ListQuery{Status: StatusActive}); len(all) != 1 || all[0].Kind != KindChat {
		t.Fatalf("default list %+v", all)
	}
	if desk, _ := s.List(ListQuery{Kind: KindDesk}); len(desk) != 2 {
		t.Fatalf("list by kind desk: %d", len(desk))
	}
	subs, err := s.DeskSubs()
	if err != nil || len(subs) != 2 || subs["surge"].ID != sub.ID {
		t.Fatalf("DeskSubs %v %v", subs, err)
	}
	for m := start + 60_000; m < to; m += 60_000 {
		f.minuteAt(s, m)
	}
	mine := 0
	for _, d := range got {
		if d.SubID == sub.ID {
			mine++
			if d.FireID == 0 || len(d.Hits) == 0 || d.Final {
				t.Fatalf("delivery %+v", d)
			}
		}
	}
	if mine < 12 {
		t.Fatalf("desk scan delivered %d fires over the history, want well past any fire limit", mine)
	}
	x, _ := s.Get(sub.ID)
	if x.Status != StatusActive || x.FireCount != mine {
		t.Fatalf("after %d fires: status %s, count %d", mine, x.Status, x.FireCount)
	}
	var woke, desk int
	st.DB.QueryRow(`SELECT COALESCE(SUM(wake IN ('queued','started')),0), COALESCE(SUM(wake='desk'),0) FROM signal_fires WHERE sub_id=?`, sub.ID).Scan(&woke, &desk)
	if woke != 0 || desk != mine {
		t.Fatalf("fires: %d woke a chat, %d handed to the desk (want 0 and %d)", woke, desk, mine)
	}
	for _, n := range f.notes {
		if strings.Contains(n, "交易台") {
			t.Fatalf("a desk fire was pushed: %s", n)
		}
	}
	// the desk's own cancel ends it
	if _, err := s.Cancel(sub.ID, "desk", "策略参数已修改"); err != nil {
		t.Fatal(err)
	}
	if subs, _ := s.DeskSubs(); len(subs) != 1 {
		t.Fatalf("after cancel: %v", subs)
	}
}
