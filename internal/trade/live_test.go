package trade

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"crypto-trader-go/internal/market"
)

func TestLiveLoop(t *testing.T) {
	md := newFakeMD(t0)
	md.setQuote(btc, btcQuote(100000))
	s, _ := newSvc(t, md)
	r := &fakeRunner{versions: []StrategyVersion{testVersion}}
	s.SetRunner(r)
	s.ReloadStrategies(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	acc := mustAccount(t, s, AccountInput{Name: "live", Mode: ModeLive, Deposit: 10000})
	inst, err := s.CreateInstance(acc.ID, InstanceInput{VersionID: testVersion.ID})
	if err != nil {
		t.Fatal(err)
	}
	var signalled atomic.Bool
	r.onBar = func(x *fakeSession, id, tf string, b Bar) {
		if signalled.CompareAndSwap(false, true) {
			x.sink.Submit(context.Background(), Signal{InstanceID: x.inst.ID, InstID: id, Action: OpenLong, StopLoss: 99000, Ts: x.md.Now()})
		}
	}
	if _, err := s.InstanceAction(inst.ID, "start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session", func() bool {
		x := r.last()
		if x == nil {
			return false
		}
		x.mu.Lock()
		defer x.mu.Unlock()
		return len(x.universe) == 1
	})
	waitFor(t, "keep", func() bool { md.mu.Lock(); defer md.mu.Unlock(); return md.kept[btc] })

	md.mu.Lock()
	md.bars[btc+"|1m"] = []Bar{{Ts: t0, O: 100000, H: 100010, L: 99990, C: 100000}}
	md.mu.Unlock()
	s.OnMinute(&market.Snapshot{AsOfMinute: t0, DecisionTs: t0 + minuteMs})
	s.OnMinute(&market.Snapshot{AsOfMinute: t0, DecisionTs: t0 + minuteMs}) // duplicate minute ignored
	sess := r.last()
	waitFor(t, "fill delivered after the bar", func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return len(sess.fills) == 1 && len(sess.results) == 1
	})
	sess.mu.Lock()
	ev := strings.Join(sess.events, ",")
	sess.mu.Unlock()
	if !strings.HasPrefix(ev, "universe:+1-0,bar:"+btc+",") || strings.Count(ev, "bar:") != 1 {
		t.Fatalf("events %s", ev)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_equity WHERE account_id=?`, acc.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("equity samples %d", n)
	}

	// stop loss triggered by the price loop
	md.setQuote(btc, btcQuote(98990))
	waitFor(t, "stop by price loop", func() bool { _, ok := s.Position(inst.ID, btc); return !ok })

	for _, a := range []struct {
		action string
		state  InstanceState
	}{{"pause", StPaused}, {"resume", StRunning}, {"stop", StStopped}} {
		in, err := s.InstanceAction(inst.ID, a.action)
		if err != nil || in.State != a.state {
			t.Fatalf("%s: %v %s", a.action, err, in.State)
		}
	}
	sess.mu.Lock()
	stopped := sess.stopped
	sess.mu.Unlock()
	if !stopped {
		t.Fatal("session not stopped")
	}
	waitFor(t, "unkeep", func() bool { md.mu.Lock(); defer md.mu.Unlock(); return !md.kept[btc] })
}

type faultSession struct{ *fakeSession }

func (f faultSession) Bars(context.Context, string, []InstBar) error { return ErrFaulted }

type faultRunner struct {
	*fakeRunner
	starts atomic.Int32
}

func (r *faultRunner) Start(ctx context.Context, inst Instance, ver StrategyVersion, mode Mode, state []byte, md MarketData, sink SignalSink) (Session, error) {
	r.starts.Add(1)
	s, _ := r.fakeRunner.Start(ctx, inst, ver, mode, state, md, sink)
	return faultSession{s.(*fakeSession)}, nil
}

func TestLiveFaultRestarts(t *testing.T) {
	md := newFakeMD(t0)
	s, _ := newSvc(t, md)
	r := &faultRunner{fakeRunner: &fakeRunner{versions: []StrategyVersion{testVersion}}}
	s.SetRunner(r)
	s.ReloadStrategies(context.Background())
	acc := mustAccount(t, s, AccountInput{Name: "live", Mode: ModeLive, Deposit: 1000})
	inst, _ := s.CreateInstance(acc.ID, InstanceInput{VersionID: testVersion.ID})
	if _, err := s.InstanceAction(inst.ID, "start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first start", func() bool { return r.starts.Load() == 1 })
	md.mu.Lock()
	md.bars[btc+"|1m"] = []Bar{{Ts: t0, C: 1}}
	md.mu.Unlock()
	s.OnMinute(&market.Snapshot{DecisionTs: t0 + minuteMs})
	waitFor(t, "faulted", func() bool {
		in, _ := s.loadInstance(inst.ID)
		return in.State == StFaulted && strings.Contains(in.StateReason, "1/3")
	})
	waitFor(t, "restarted", func() bool { return r.starts.Load() == 2 })
	if st := s.loadState(inst.ID); string(st) != `{"n":1}` {
		t.Fatalf("state not saved: %q", st)
	}
}

func TestStartWithoutRunner(t *testing.T) {
	md := newFakeMD(t0)
	s, _ := newSvc(t, md)
	addVersion(t, s, testVersion)
	s.SetRunner(nil)
	acc := mustAccount(t, s, AccountInput{Name: "live", Mode: ModeLive, Deposit: 1000})
	inst, _ := s.CreateInstance(acc.ID, InstanceInput{VersionID: testVersion.ID})
	_, err := s.InstanceAction(inst.ID, "start")
	if statusOf(err) != 503 {
		t.Fatalf("err %v", err)
	}
	in, _ := s.loadInstance(inst.ID)
	if in.State != StCreated || !strings.Contains(in.StateReason, "运行器") {
		t.Fatalf("instance %+v", in)
	}
}
