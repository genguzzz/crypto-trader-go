package trade

import (
	"context"
	"strings"
	"sync"
	"testing"
)

const btc = "BTC-USDT-SWAP"

type fakeReviewer struct {
	d   Decision
	err error
	n   int
	mu  sync.Mutex
}

func (r *fakeReviewer) Available() bool { return true }
func (r *fakeReviewer) Review(ctx context.Context, req ReviewRequest) (Decision, error) {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
	if req.Strategy.Code != "" {
		return Decision{}, nil
	}
	return r.d, r.err
}

type fakeInteractor struct {
	mu     sync.Mutex
	choice string
	events []Event
	asked  []Prompt
}

func (f *fakeInteractor) Notify(ev Event) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
}
func (f *fakeInteractor) Ask(ctx context.Context, p Prompt) (string, string, error) {
	f.mu.Lock()
	f.asked = append(f.asked, p)
	c := f.choice
	f.mu.Unlock()
	if c == "" {
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	return c, "web", nil
}
func (f *fakeInteractor) titles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.events {
		out = append(out, e.Title)
	}
	return out
}

func liveSetup(t *testing.T, limits Limits, exec ExecMode) (*Service, *fakeMD, *instRT) {
	t.Helper()
	md := newFakeMD(t0)
	md.setQuote(btc, btcQuote(100000))
	md.setQuote("ETH-USDT-SWAP", Quote{Last: 3000, Bid: 2999.99, Ask: 3000.01, Mark: 3000})
	s, _ := newSvc(t, md)
	addVersion(t, s, testVersion)
	acc := mustAccount(t, s, AccountInput{Name: "paper", Mode: ModeLive, Deposit: 10000, Limits: &limits})
	return s, md, attach(t, s, acc.ID, exec)
}

func openLong(ri *instRT, inst string, stop float64) Signal {
	return Signal{InstanceID: ri.inst.ID, InstID: inst, Action: OpenLong, StopLoss: stop, Ts: t0, Reason: "test"}
}

func TestSignalPipeline(t *testing.T) {
	type step struct {
		sg      func(ri *instRT) Signal
		out     SignalOutcome
		reason  string
		advance int64 // ms to move the clock before the step
	}
	noStop := DefaultLimits()
	noStop.RequireStop = false
	onePos := DefaultLimits()
	onePos.MaxPositions = 1
	cases := []struct {
		name   string
		limits Limits
		exec   ExecMode
		setup  func(s *Service, ri *instRT)
		steps  []step
		qty    float64
	}{
		{name: "ordered and filled", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered, reason: "10 张"}}, qty: 10},
		{name: "stop required", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 0) }, out: OutRejected, reason: "要求开仓信号带止损"}}},
		{name: "max positions", limits: onePos, steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered},
			{sg: func(ri *instRT) Signal { return openLong(ri, "ETH-USDT-SWAP", 2900) }, out: OutRejected, reason: "持仓数已达上限"}}, qty: 10},
		{name: "opposite position", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered},
			{sg: func(ri *instRT) Signal {
				return Signal{InstanceID: ri.inst.ID, InstID: btc, Action: OpenShort, StopLoss: 101000, Ts: t0}
			}, out: OutRejected, reason: "反向持仓"}}, qty: 10},
		{name: "cooldown after close", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered},
			{sg: func(ri *instRT) Signal { return Signal{InstanceID: ri.inst.ID, InstID: btc, Action: Close, Ts: t0} }, out: OutOrdered},
			{sg: func(ri *instRT) Signal { s := openLong(ri, btc, 99000); s.Ts = t0 + 10*minuteMs; return s }, out: OutRejected, reason: "冷却中", advance: 10 * minuteMs},
			{sg: func(ri *instRT) Signal { s := openLong(ri, btc, 99000); s.Ts = t0 + 31*minuteMs; return s }, out: OutOrdered, advance: 21 * minuteMs}}, qty: 9.98}, // equity after two fills' costs
		{name: "reduce half", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered},
			{sg: func(ri *instRT) Signal {
				return Signal{InstanceID: ri.inst.ID, InstID: btc, Action: Reduce, ReduceFrac: 0.5, Ts: t0}
			}, out: OutOrdered, reason: "5 张"}}, qty: 5},
		{name: "close without position", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return Signal{InstanceID: ri.inst.ID, InstID: btc, Action: Close, Ts: t0} }, out: OutRejected, reason: "无持仓"}}},
		{name: "record mode", limits: DefaultLimits(), exec: ExecRecord, steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutRecorded}}},
		{name: "expired", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { s := openLong(ri, btc, 99000); s.Ts = t0 - 6*minuteMs; return s }, out: OutExpired}}},
		{name: "unknown instrument", limits: DefaultLimits(), steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, "NOPE-USDT-SWAP", 1) }, out: OutRejected, reason: "合约不存在"}}},
		{name: "paused instance", limits: DefaultLimits(), setup: func(s *Service, ri *instRT) {
			ri.mu.Lock()
			ri.inst.State = StPaused
			ri.mu.Unlock()
		}, steps: []step{{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutRejected, reason: "实例未运行"}}},
		{name: "AI unavailable", limits: DefaultLimits(), exec: ExecAI, steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutSkippedAI, reason: "AI unavailable"}}},
		{name: "AI execute at half size", limits: DefaultLimits(), exec: ExecAI, setup: func(s *Service, ri *instRT) {
			s.SetReviewer(&fakeReviewer{d: Decision{ID: 7, Action: "execute", SizeScale: 0.5, Rationale: "ok"}})
		}, steps: []step{{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered}}, qty: 5},
		{name: "AI skip", limits: DefaultLimits(), exec: ExecAI, setup: func(s *Service, ri *instRT) {
			s.SetReviewer(&fakeReviewer{d: Decision{ID: 8, Action: "skip", Rationale: "trend weak"}})
		}, steps: []step{{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutSkippedAI, reason: "trend weak"}}},
		{name: "human half", limits: func() Limits { l := DefaultLimits(); l.Confirm = ConfirmOpen; return l }(), setup: func(s *Service, ri *instRT) {
			s.SetInteractor(&fakeInteractor{choice: "half"})
		}, steps: []step{{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutOrdered}}, qty: 5},
		{name: "human skip", limits: func() Limits { l := DefaultLimits(); l.Confirm = ConfirmAll; return l }(), setup: func(s *Service, ri *instRT) {
			s.SetInteractor(&fakeInteractor{choice: "skip"})
		}, steps: []step{{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutSkippedUs}}},
		{name: "confirmation timeout defaults to skip", limits: func() Limits { l := DefaultLimits(); l.Confirm = ConfirmAll; l.ConfirmTimeoutS = 1; return l }(), setup: func(s *Service, ri *instRT) {
			s.SetInteractor(&fakeInteractor{})
		}, steps: []step{{sg: func(ri *instRT) Signal { return openLong(ri, btc, 99000) }, out: OutSkippedUs, reason: "timeout"}}},
		{name: "no stop sizing by margin", limits: noStop, steps: []step{
			{sg: func(ri *instRT) Signal { return openLong(ri, btc, 0) }, out: OutOrdered}}, qty: 15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exec := c.exec
			if exec == "" {
				exec = ExecAuto
			}
			s, md, ri := liveSetup(t, c.limits, exec)
			if c.setup != nil {
				c.setup(s, ri)
			}
			for i, st := range c.steps {
				if st.advance > 0 {
					md.setNow(md.Now() + st.advance)
				}
				_, out, reason := submitWait(t, s, st.sg(ri))
				if out != st.out || !strings.Contains(reason, st.reason) {
					t.Fatalf("step %d: %s %q, want %s %q", i, out, reason, st.out, st.reason)
				}
			}
			p, ok := s.Position(ri.inst.ID, btc)
			if c.qty == 0 {
				if ok {
					t.Fatalf("unexpected position %+v", p)
				}
				return
			}
			if !ok || !approx(p.Qty, c.qty, 1e-9) {
				t.Fatalf("position %+v ok=%v, want qty %v", p, ok, c.qty)
			}
		})
	}
}

func TestSignalEventsQueuedForStrategy(t *testing.T) {
	s, _, ri := liveSetup(t, DefaultLimits(), ExecAuto)
	inter := &fakeInteractor{}
	s.SetInteractor(inter)
	submitWait(t, s, openLong(ri, btc, 99000))
	var names []string
	for {
		c, ok := ri.pop()
		if !ok {
			break
		}
		names = append(names, c.name)
	}
	if strings.Join(names, ",") != "fill,signal_result" {
		t.Fatalf("queued %v", names)
	}
	waitFor(t, "notifications", func() bool { return len(inter.titles()) >= 2 })
	got := strings.Join(inter.titles(), "|")
	if !strings.Contains(got, "开多成交") || !strings.Contains(got, "信号已下单") {
		t.Fatalf("events %s", got)
	}
}
