package trade

import (
	"context"
	"strings"
	"testing"
)

func TestReverseIgnoresCooldown(t *testing.T) {
	s, _, ri := liveSetup(t, DefaultLimits(), ExecAuto)
	submitWait(t, s, openLong(ri, btc, 99000))
	rev := Signal{InstanceID: ri.inst.ID, InstID: btc, Action: OpenShort, StopLoss: 101000, Reverse: true, Ts: t0, Reason: "flip"}
	id, out, reason := submitWait(t, s, rev)
	if out != OutOrdered || !strings.Contains(reason, "反手") {
		t.Fatalf("reverse: %s %q", out, reason)
	}
	p, ok := s.Position(ri.inst.ID, btc)
	if !ok || p.PosSide != "short" || p.Qty <= 0 {
		t.Fatalf("after reverse: %+v ok=%v", p, ok)
	}
	if _, r := outcome(s, id); !strings.Contains(r, "反手开仓：") {
		t.Fatalf("reverse note: %q", r)
	}
	// a reverse whose new side would be refused keeps the position
	bad := Signal{InstanceID: ri.inst.ID, InstID: btc, Action: OpenLong, Reverse: true, Ts: t0} // no stop: the account requires one
	if _, out, reason := submitWait(t, s, bad); out != OutRejected || !strings.Contains(reason, "反手被拒") {
		t.Fatalf("refused reverse: %s %q", out, reason)
	}
	if p, ok := s.Position(ri.inst.ID, btc); !ok || p.PosSide != "short" {
		t.Fatalf("position lost by a refused reverse: %+v %v", p, ok)
	}
	// a plain open right after a close is still held back by the cooldown
	submitWait(t, s, Signal{InstanceID: ri.inst.ID, InstID: btc, Action: Close, Ts: t0})
	if _, out, reason := submitWait(t, s, openLong(ri, btc, 99000)); out != OutRejected || !strings.Contains(reason, "冷却中") {
		t.Fatalf("plain open after close: %s %q", out, reason)
	}
}

func TestTrailingStop(t *testing.T) {
	s, md, ri := liveSetup(t, DefaultLimits(), ExecAuto)
	sg := openLong(ri, btc, 99000)
	sg.TrailDist = 500
	submitWait(t, s, sg)
	b := ri.b
	stopPx := func() float64 {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, o := range b.ordersFor(posKey{ri.inst.ID, btc}, isProtect) {
			if o.Type == OrdStop {
				return o.TriggerPx
			}
		}
		return 0
	}
	p, _ := s.Position(ri.inst.ID, btc)
	if got := stopPx(); !approx(got, p.EntryPx-500, 1e-6) { // tighter than the 99000 stop: entry − 500 at once
		t.Fatalf("initial stop %v, entry %v", got, p.EntryPx)
	}
	b.mu.Lock()
	b.onQuote(btc, btcQuote(101000), md.Now())
	b.mu.Unlock()
	if got := stopPx(); got != 100500 {
		t.Fatalf("stop after 101000: %v", got)
	}
	b.mu.Lock()
	b.onQuote(btc, btcQuote(100800), md.Now()) // pulls back: the stop stays
	b.mu.Unlock()
	if got := stopPx(); got != 100500 {
		t.Fatalf("stop after pullback: %v", got)
	}
	b.mu.Lock()
	b.onQuote(btc, btcQuote(100400), md.Now())
	b.mu.Unlock()
	if _, ok := s.Position(ri.inst.ID, btc); ok {
		t.Fatal("trailing stop did not fire")
	}
}

func TestTimeLimit(t *testing.T) {
	s, md, ri := liveSetup(t, DefaultLimits(), ExecAuto)
	sg := openLong(ri, btc, 99000)
	sg.TimeLimitMs = hourMs
	submitWait(t, s, sg)
	b := ri.b
	b.mu.Lock()
	b.expire(md.Now() + 30*minuteMs)
	b.mu.Unlock()
	if _, ok := s.Position(ri.inst.ID, btc); !ok {
		t.Fatal("closed before its time limit")
	}
	b.mu.Lock()
	b.expire(md.Now() + hourMs)
	b.mu.Unlock()
	if _, ok := s.Position(ri.inst.ID, btc); ok {
		t.Fatal("still open after its time limit")
	}
	trades, err := s.Trades(b.acc.ID)
	if err != nil || len(trades) != 1 || trades[0].ExitKind != "time" {
		t.Fatalf("trades %+v %v", trades, err)
	}
}

func TestNotifyLimits(t *testing.T) {
	l := newRunLog()
	if ok, _ := l.allow(1, "a", 0); !ok {
		t.Fatal("first message refused")
	}
	if ok, why := l.allow(1, "a", 5*minuteMs); ok || !strings.Contains(why, "重复") {
		t.Fatalf("duplicate within 10 minutes: %v %q", ok, why)
	}
	if ok, _ := l.allow(1, "a", 11*minuteMs); !ok {
		t.Fatal("duplicate after 10 minutes refused")
	}
	for i := 0; i < notifyPerHour-2; i++ {
		if ok, _ := l.allow(1, string(rune('b'+i)), 12*minuteMs); !ok {
			t.Fatalf("message %d refused", i)
		}
	}
	if ok, why := l.allow(1, "late", 13*minuteMs); ok || !strings.Contains(why, "每小时") {
		t.Fatalf("over the hourly cap: %v %q", ok, why)
	}
	if ok, _ := l.allow(2, "late", 13*minuteMs); !ok {
		t.Fatal("limits are per instance")
	}
}

func TestStrategyNotifyLiveOnly(t *testing.T) {
	s, _, ri := liveSetup(t, DefaultLimits(), ExecAuto)
	inter := &fakeInteractor{}
	s.SetInteractor(inter)
	s.Notify(ri.inst.ID, "warn", "hello", map[string]any{"px": 1.5})
	waitFor(t, "strategy message", func() bool { return len(inter.titles()) == 1 })
	inter.mu.Lock()
	ev := inter.events[0]
	inter.mu.Unlock()
	if ev.Kind != "strategy" || !strings.Contains(ev.Title, "策略警告") || ev.Data["text"] != "hello" ||
		strings.Join(ev.Lines, "|") != "hello|px: 1.5" {
		t.Fatalf("event %+v", ev)
	}
	logs := s.RunLog(ri.inst.ID)
	if len(logs) != 1 || logs[0].Kind != "notify" || !logs[0].Sent {
		t.Fatalf("run log %+v", logs)
	}
}

func TestTypedParamsAndTimeframe(t *testing.T) {
	specs := []ParamSpec{{Name: "tf", Type: PTTimeframe, Default: 1, Options: []string{"1H", "4H"}},
		{Name: "on", Type: PTBool, Default: 0}, {Name: "mode", Type: PTChoice, Default: 0, Options: []string{"a", "b"}},
		{Name: "n", Type: PTInt, Default: 3, Min: ptr(1.0), Max: ptr(9.0)}}
	got, err := validParams(specs, map[string]float64{"tf": 0, "on": 1})
	if err != nil || got["tf"] != 0 || got["on"] != 1 || got["mode"] != 0 || got["n"] != 3 {
		t.Fatalf("defaults %v %v", got, err)
	}
	for _, bad := range []map[string]float64{{"tf": 2}, {"on": 0.5}, {"mode": -1}, {"n": 2.5}, {"n": 10}} {
		if _, err := validParams(specs, bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	ver := StrategyVersion{Timeframe: "4H", Params: specs}
	if tf := InstanceTimeframe(Instance{Params: map[string]float64{"tf": 0}}, ver); tf != "1H" {
		t.Fatalf("timeframe param: %s", tf)
	}
	if tf := InstanceTimeframe(Instance{}, ver); tf != "4H" {
		t.Fatalf("timeframe default: %s", tf)
	}
}

const userCode = "from ctg import Strategy\nclass UserX(Strategy):\n    name = \"user-x\"\n    timeframe = \"5m\"\n"

func TestSourcesPublishAndTrial(t *testing.T) {
	ver := testVersion
	ver.ID, ver.Timeframe = "bt5m0000000b", "5m"
	s, st, r, _ := btSetup(t, ver, nil)
	r.check = func(code string) CheckResult {
		return CheckResult{Diagnostics: []Diagnostic{}, Strategies: []StrategyVersion{{Name: sourceName(code), Timeframe: "5m",
			Params: []ParamSpec{}, DefaultUniverse: Universe{Static: []string{btc}}}}}
	}
	src, err := s.CreateSource(userCode, "")
	if err != nil || src.Name != "user-x" || !src.Dirty {
		t.Fatalf("create: %+v %v", src, err)
	}
	pub, err := s.PublishSource(context.Background(), src.ID)
	if err != nil || pub.Version == nil || pub.Version.Meta.SourceID != src.ID || pub.Version.ID != VersionID(userCode) {
		t.Fatalf("publish: %+v %v", pub, err)
	}
	if src, _ = s.Source(src.ID); src.Dirty {
		t.Fatal("still dirty after publish")
	}
	// a second source cannot take the name
	other, _ := s.CreateSource(userCode+"\n# copy\n", "")
	if _, err := s.PublishSource(context.Background(), other.ID); err == nil || !strings.Contains(err.Error(), "已被") {
		t.Fatalf("duplicate name published: %v", err)
	}
	// once the owner is deleted, the name is free again
	if err := s.DeleteSource(src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishSource(context.Background(), other.ID); err != nil {
		t.Fatalf("name of a deleted strategy still taken: %v", err)
	}
	src = other
	// built-in strategies are copied under a new name
	cp, err := s.CreateSource("", ver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Source(cp.ID); got.Code != ver.Code {
		t.Fatalf("copy: %q", got.Code) // test code has no name attribute: unchanged
	}

	now := s.md.Now() / hourMs * hourMs
	seedMinutes(t, st, btc, now-2*dayMs, now)
	opened := false
	r.onBar = func(x *fakeSession, inst, tf string, b Bar) {
		if !opened {
			opened = true
			x.sink.Submit(context.Background(), Signal{InstanceID: x.inst.ID, InstID: inst, Action: OpenLong, StopLoss: b.C * 0.99,
				Ts: x.md.Now(), Price: b.C, Reason: "trial"})
		}
		x.sink.Log(x.inst.ID, "info", "bar "+inst)
	}
	res, err := s.Trial(context.Background(), TrialInput{SourceID: src.ID, Days: 1})
	if err != nil || res.Error != "" {
		t.Fatalf("trial: %v %q", err, res.Error)
	}
	if len(res.Signals) != 1 || res.Signals[0].Outcome != OutOrdered || len(res.Trades) != 1 || len(res.Logs) == 0 || res.Candles == nil {
		t.Fatalf("trial result: %d signals %d trades %d logs candles=%v", len(res.Signals), len(res.Trades), len(res.Logs), res.Candles != nil)
	}
	for _, a := range s.Accounts() {
		if isTrial(a.Name) {
			t.Fatal("trial account listed")
		}
	}
	if _, ok := s.books[res.Signals[0].AccountID]; ok {
		t.Fatal("trial account not deleted")
	}
}

func TestBacktestReverse(t *testing.T) {
	ver := testVersion
	ver.ID, ver.Timeframe = "bt5m0000000c", "5m"
	s, st, r, acc := btSetup(t, ver, nil)
	seedMinutes(t, st, btc, t0-dayMs, t0+4*hourMs)
	n := 0
	r.onBar = func(x *fakeSession, inst, tf string, b Bar) {
		n++
		sg := Signal{InstanceID: x.inst.ID, InstID: inst, Ts: x.md.Now(), Price: b.C, Reason: "t"}
		switch n {
		case 1:
			sg.Action, sg.StopLoss = OpenLong, b.C*0.98
		case 6:
			sg.Action, sg.StopLoss, sg.Reverse = OpenShort, b.C*1.02, true
		default:
			return
		}
		x.sink.Submit(context.Background(), sg)
	}
	if err := s.RunBacktest(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	if a := waitBacktest(t, s, acc.ID); a.Backtest.State != "done" {
		t.Fatalf("backtest %+v", a.Backtest)
	}
	trades, _ := s.Trades(acc.ID)
	if len(trades) != 2 || trades[0].PosSide != "long" || trades[0].ExitKind != "signal" || trades[1].PosSide != "short" {
		t.Fatalf("trades %+v", trades)
	}
	if trades[1].OpenTs != trades[0].CloseTs || !approx(trades[1].EntryPx, trades[0].ExitPx, 1e-9) {
		t.Fatalf("reopen not at the close: %+v / %+v", trades[0], trades[1])
	}
}

func TestUpgradeInstanceVersion(t *testing.T) {
	s, _, ri := liveSetup(t, DefaultLimits(), ExecAuto)
	v2 := testVersion
	v2.ID, v2.Code = "abc123def457", "pass # v2"
	v2.Params = []ParamSpec{{Name: "fast", Default: 10, Min: ptr(1.0), Max: ptr(20.0)}, {Name: "mode", Type: PTChoice, Default: 1, Options: []string{"a", "b"}}}
	addVersion(t, s, v2)
	inst, err := s.CreateInstance(ri.b.acc.ID, InstanceInput{VersionID: testVersion.ID, Params: map[string]float64{"fast": 30}}) // not running
	if err != nil {
		t.Fatal(err)
	}
	id := v2.ID
	in, err := s.UpdateInstance(inst.ID, InstancePatch{VersionID: &id})
	if err != nil || in.VersionID != v2.ID || in.Params["fast"] != 10 || in.Params["mode"] != 1 {
		t.Fatalf("upgrade: %+v %v", in, err) // fast=30 is out of the new range: back to its default
	}
	other := "nope00000000"
	if _, err := s.UpdateInstance(inst.ID, InstancePatch{VersionID: &other}); err == nil {
		t.Fatal("unknown version accepted")
	}
}
