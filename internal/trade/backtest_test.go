package trade

import (
	"context"
	"strings"
	"testing"

	"crypto-trader-go/internal/store"
)

// seedMinutes stores 1m bars for [from, to): open rises 10 per minute.
func seedMinutes(t *testing.T, st *store.Store, inst string, from, to int64) {
	t.Helper()
	var xs []store.InstBar
	for ts := from; ts < to; ts += minuteMs {
		o := 100000 + float64((ts-t0)/minuteMs)*10
		xs = append(xs, store.InstBar{InstID: inst, Bar: store.Bar{Ts: ts, O: o, H: o + 8, L: o - 3, C: o + 5, Vol: 1, Quote: 1000}})
	}
	if err := st.PutBars("candles_1m", xs); err != nil {
		t.Fatal(err)
	}
}

func minuteOpen(ts int64) float64 { return 100000 + float64((ts-t0)/minuteMs)*10 }

func btSetup(t *testing.T, ver StrategyVersion, u *Universe) (*Service, *store.Store, *fakeRunner, Account) {
	t.Helper()
	md := newFakeMD(t0 + 10*dayMs)
	s, st := newSvc(t, md)
	r := &fakeRunner{versions: []StrategyVersion{ver}}
	s.SetRunner(r)
	if _, err := s.ReloadStrategies(context.Background()); err != nil {
		t.Fatal(err)
	}
	acc := mustAccount(t, s, AccountInput{Name: "bt", Mode: ModeBacktest, Deposit: 10000, From: t0, To: t0 + 3*hourMs})
	if _, err := s.CreateInstance(acc.ID, InstanceInput{VersionID: ver.ID, Universe: u}); err != nil {
		t.Fatal(err)
	}
	return s, st, r, acc
}

func waitBacktest(t *testing.T, s *Service, id int64) Account {
	t.Helper()
	var a Account
	waitFor(t, "backtest end", func() bool {
		a, _ = s.Account(id)
		return a.Backtest != nil && (a.Backtest.State == "done" || a.Backtest.State == "failed")
	})
	return a
}

func TestBacktestLockstepNoLookAhead(t *testing.T) {
	ver := testVersion
	ver.ID, ver.Timeframe = "bt5m0000000a", "5m"
	s, st, r, acc := btSetup(t, ver, nil)
	seedMinutes(t, st, btc, t0-dayMs, t0+4*hourMs) // data after To must stay invisible
	st.PutFunding([]store.FundingSample{{InstID: btc, Ts: t0 + hourMs, Rate: 0.0001, FundingTime: t0 + hourMs, Settled: true}})

	var opened, closed int64
	nBar := 0
	r.onBar = func(x *fakeSession, inst, tf string, b Bar) {
		nBar++
		now := x.md.Now()
		if now != b.Ts+5*minuteMs {
			x.fail("bar %d delivered at %d, closes at %d", b.Ts, now, b.Ts+5*minuteMs)
		}
		if b.O != minuteOpen(b.Ts) || b.C != minuteOpen(b.Ts+4*minuteMs)+5 {
			x.fail("bar %d aggregated wrong: %+v", b.Ts, b)
		}
		bars, _ := x.md.Bars(inst, "5m", 200) // reaches before From (warm-up history)
		if len(bars) != 200 || bars[len(bars)-1].Ts != b.Ts {
			x.fail("history at %d: %d bars, last %d", now, len(bars), bars[len(bars)-1].Ts)
		}
		for _, tf := range []string{"1m", "15m", "1H"} {
			xs, _ := x.md.Bars(inst, tf, 5)
			step, _ := TFMillis(tf)
			if len(xs) > 0 && xs[len(xs)-1].Ts+step > now {
				x.fail("%s bar %d not closed at %d", tf, xs[len(xs)-1].Ts, now)
			}
		}
		if q, err := x.md.Quote(context.Background(), inst); err != nil || q.Last != b.C {
			x.fail("quote %v %v at %d, want close %v", q.Last, err, now, b.C)
		}
		if _, ok := x.md.FundingRate(inst, t0+hourMs); ok != (now >= t0+hourMs) {
			x.fail("funding visible=%v at %d", ok, now-t0)
		}
		switch nBar {
		case 1:
			opened, _ = x.sink.Submit(context.Background(), Signal{InstanceID: x.inst.ID, InstID: inst, Action: OpenLong, StopLoss: b.C - 2000, Ts: now})
		case 30: // 2.5h in: held through the 1h and 2h settlements
			closed, _ = x.sink.Submit(context.Background(), Signal{InstanceID: x.inst.ID, InstID: inst, Action: Close, Ts: now})
		}
	}
	if err := s.RunBacktest(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	a := waitBacktest(t, s, acc.ID)
	if a.Backtest.State != "done" || a.Backtest.Progress != 1 {
		t.Fatalf("backtest %+v", a.Backtest)
	}
	sess := r.last()
	sess.mu.Lock()
	errs, nFills, res := sess.errs, len(sess.fills), sess.results
	sess.mu.Unlock()
	if len(errs) > 0 {
		t.Fatalf("look-ahead / lockstep violations: %v", errs)
	}
	if nBar != 36 {
		t.Fatalf("got %d 5m bars, want 36", nBar)
	}
	if nFills != 2 || res[opened] != OutOrdered || res[closed] != OutOrdered {
		t.Fatalf("fills %d results %v", nFills, res)
	}
	fills, _ := s.Fills(acc.ID, Page{})
	entry, exit := fills[1], fills[0]
	firstClose := t0 + 5*minuteMs // the signal at the first 5m close fills at the next 1m open
	o := minuteOpen(firstClose)
	if entry.Ts != firstClose || !approx(entry.Px, snapTick(o+o*1e-4+0.1, 0.1, true), 1e-6) {
		t.Fatalf("entry %+v, want %v @ %v", entry, firstClose, snapTick(o+o*1e-4+0.1, 0.1, true))
	}
	closeAt := t0 + 150*minuteMs
	o = minuteOpen(closeAt)
	if exit.Ts != closeAt || !approx(exit.Px, snapTick(o-o*1e-4-0.1, 0.1, false), 1e-6) {
		t.Fatalf("exit %+v, want %v @ %v", exit, closeAt, snapTick(o-o*1e-4-0.1, 0.1, false))
	}
	if f := ledgerSum(s, acc.ID, "funding"); f >= 0 {
		t.Fatalf("long paid no funding: %v", f)
	}
	rep, err := s.Report(acc.ID)
	if err != nil || rep.Trades != 1 || rep.WinRate == nil || *rep.WinRate != 1 {
		t.Fatalf("report %+v %v", rep, err)
	}
	var eqN int
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_equity WHERE account_id=?`, acc.ID).Scan(&eqN)
	if eqN != 180 {
		t.Fatalf("equity samples %d, want 180 (per 1m bar)", eqN)
	}

	// re-run wipes the previous records
	nBar = 0
	if err := s.RunBacktest(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	waitBacktest(t, s, acc.ID)
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_fills WHERE account_id=?`, acc.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("re-run left %d fills", n)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_ledger WHERE account_id=? AND kind='deposit'`, acc.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("deposits %d", n)
	}
}

func TestBacktestScreenerNeedsHistory(t *testing.T) {
	ver := testVersion
	ver.ID = "screen000001"
	s, _, _, acc := btSetup(t, ver, &Universe{Screener: &ScreenerQuery{Top: 5}})
	if err := s.RunBacktest(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	a := waitBacktest(t, s, acc.ID)
	if a.Backtest.State != "failed" || !strings.Contains(a.Backtest.Error, "universe_snapshots") {
		t.Fatalf("backtest %+v", a.Backtest)
	}
}

func TestBacktestMissingCandles(t *testing.T) {
	s, _, _, acc := btSetup(t, testVersion, nil)
	if err := s.RunBacktest(context.Background(), acc.ID); err != nil {
		t.Fatal(err)
	}
	a := waitBacktest(t, s, acc.ID)
	if a.Backtest.State != "failed" || !strings.Contains(a.Backtest.Error, "没有 BTC-USDT-SWAP 的 1m K 线") {
		t.Fatalf("backtest %+v", a.Backtest)
	}
}

func TestReplayerScores(t *testing.T) {
	st := openStore(t)
	l1, l2, s1 := 70.0, 80.0, 30.0
	st.PutScores([]store.ScoreRow{
		{Kind: "replay", RuleID: "r", RuleVersion: 1, Ts: t0, InstID: "A", Long: &l1, Short: &s1},
		{Kind: "live", RuleID: "r", RuleVersion: 1, Ts: t0, InstID: "A", Long: &l2, Short: &s1},
		{Kind: "replay", RuleID: "r", RuleVersion: 1, Ts: t0 + hourMs, InstID: "A", Long: &l1},
	})
	rp := newReplayer(st, nil, t0, t0+5*hourMs, hourMs)
	rp.setNow(t0 + 30*minuteMs)
	if l, s, ok := rp.Score("r", "A"); !ok || l != 80 || s != 30 {
		t.Fatalf("live record should win: %v %v %v", l, s, ok)
	}
	rp.setNow(t0 + hourMs - 1)
	if l, _, _ := rp.Score("r", "A"); l != 80 {
		t.Fatal("score from the future")
	}
	rp.setNow(t0 + 4*hourMs)
	if _, _, ok := rp.Score("r", "A"); ok {
		t.Fatal("stale score older than 2h")
	}
}
