package trade

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"time"
)

// btRun is a backtest in progress.
type btRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (b *book) running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bt != nil
}

// RunBacktest (re)runs a backtest account in the background: its previous records are wiped,
// every instance of the account is started in backtest mode against the replayer, and the range
// is replayed bar by bar in lockstep. Progress is in Account.Backtest.
func (s *Service) RunBacktest(ctx context.Context, accountID int64) error {
	b, err := s.book(accountID)
	if err != nil {
		return err
	}
	if !b.barModel {
		return errBad("不是回测账户")
	}
	runner, _, _ := s.deps()
	if runner == nil || !runner.Available() {
		return errUnavailable("策略运行器未连接，无法回测")
	}
	insts, err := s.accountInstances(accountID)
	if err != nil {
		return err
	}
	if len(insts) == 0 {
		return errBad("账户没有策略实例")
	}
	vers := map[string]StrategyVersion{}
	for _, in := range insts {
		v, err := s.version(in.VersionID)
		if err != nil {
			return err
		}
		if _, ok := TFMillis(v.Timeframe); !ok {
			return errBad(fmt.Sprintf("策略 %s 的周期 %q 不受支持", v.Name, v.Timeframe))
		}
		vers[v.ID] = v
	}
	rctx, cancel := context.WithCancel(ctx)
	run := &btRun{cancel: cancel, done: make(chan struct{})}
	b.mu.Lock()
	if b.bt != nil {
		b.mu.Unlock()
		cancel()
		return errConflict("回测正在运行")
	}
	b.bt = run
	b.acc.Backtest = &BacktestStatus{State: "running", StartedAt: time.Now().UnixMilli(), Clock: b.acc.From}
	st := *b.acc.Backtest
	b.mu.Unlock()
	s.saveBacktest(accountID, st)
	go s.runBacktest(rctx, b, run, insts, vers)
	return nil
}

// CancelBacktest stops a running backtest (it ends as failed with "已取消").
func (s *Service) CancelBacktest(accountID int64) error {
	b, err := s.book(accountID)
	if err != nil {
		return err
	}
	b.mu.Lock()
	run := b.bt
	b.mu.Unlock()
	if run == nil {
		return errConflict("回测未在运行")
	}
	run.cancel()
	<-run.done
	return nil
}

func (s *Service) saveBacktest(id int64, st BacktestStatus) {
	s.db.Exec(`UPDATE trade_accounts SET backtest=? WHERE id=?`, mustJSON(st), id)
	s.changed(id, "backtest")
}

func (b *book) setBacktest(fn func(*BacktestStatus)) BacktestStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.acc.Backtest == nil {
		b.acc.Backtest = &BacktestStatus{}
	}
	fn(b.acc.Backtest)
	return *b.acc.Backtest
}

// reset wipes a backtest account back to its deposit at From.
func (s *Service) resetBacktest(b *book) error {
	b.mu.Lock()
	dep := b.deposit
	from := b.acc.From
	id := b.acc.ID
	b.mu.Unlock()
	err := s.tx(func(tx *sql.Tx) error {
		if err := wipeAccount(tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE trade_instances SET state=?, state_reason='', heartbeat=0 WHERE account_id=?`, StCreated, id); err != nil {
			return err
		}
		_, err := insertLedger(tx, LedgerEntry{AccountID: id, Kind: "deposit", Amount: dep, Ts: from})
		return err
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.balance, b.deposit = dep, dep
	b.pos, b.orders, b.groups = map[posKey]*posState{}, map[int64]*Order{}, map[string]groupSpec{}
	b.lastClose, b.pending, b.marks = map[string]int64{}, map[posKey]int64{}, map[string]float64{}
	b.day = dayState{key: dayKey(from), start: dep}
	b.eqBuf = nil
	b.mu.Unlock()
	return saveDayState(s.db, id, dayState{key: dayKey(from), start: dep})
}

func (s *Service) runBacktest(ctx context.Context, b *book, run *btRun, insts []Instance, vers map[string]StrategyVersion) {
	defer close(run.done)
	defer run.cancel()
	acc := b.snapshot()
	var rts []*instRT
	finish := func(state, msg string) {
		for _, ri := range rts {
			if sess := ri.session(); sess != nil {
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				sess.Stop(sctx)
				cancel()
				if st := sess.State(); st != nil {
					s.saveState(ri.inst.ID, st, time.Now().UnixMilli())
				}
			}
			s.mu.Lock()
			if s.insts[ri.inst.ID] == ri {
				delete(s.insts, ri.inst.ID)
			}
			s.mu.Unlock()
			ist, reason := StDone, ""
			if state != "done" {
				ist, reason = StStopped, msg
			}
			s.setInstanceState(ri.inst.ID, ist, reason)
		}
		b.mu.Lock()
		s.flushEquity(b)
		for _, p := range b.pos { // keep the final marks so the account reads the same after a restart
			p.MarkPx = b.mark(p.InstID, p.EntryPx)
			savePosition(s.db, p)
		}
		b.bt = nil
		b.mu.Unlock()
		st := b.setBacktest(func(x *BacktestStatus) {
			x.State, x.Error, x.FinishedAt = state, msg, time.Now().UnixMilli()
			if state == "done" {
				x.Progress = 1
			}
		})
		s.saveBacktest(acc.ID, st)
		s.changed(acc.ID, "instance")
		title := "回测完成 · " + acc.Name
		if state != "done" {
			title = "回测失败 · " + acc.Name
		}
		lines := []string{msg}
		if state == "done" {
			if rep, err := s.Report(acc.ID); err == nil {
				lines = []string{fmt.Sprintf("净收益 %s USDT · 最大回撤 %.1f%% · 交易 %d 笔", fmtSigned(rep.Net), rep.MaxDrawdown*100, rep.Trades)}
			}
		}
		s.emit(Event{Kind: "instance", AccountID: acc.ID, Account: acc.Name, Mode: ModeBacktest, Title: title, Lines: lines, Ts: time.Now().UnixMilli()})
	}
	fail := func(err error) {
		if ctx.Err() != nil {
			err = fmt.Errorf("已取消")
		}
		log.Printf("trade: backtest %d: %v", b.acc.ID, err)
		finish("failed", err.Error())
	}
	if err := s.resetBacktest(b); err != nil {
		fail(err)
		return
	}
	base := hourMs
	for _, in := range insts {
		if step, _ := TFMillis(vers[in.VersionID].Timeframe); step < hourMs {
			base = minuteMs
		}
	}
	var specs func(string) (Spec, bool)
	if s.md != nil {
		specs = s.md.Spec
	}
	rp := newReplayer(s.st, specs, acc.From, acc.To, base)
	b.setMarket(rp)
	for _, in := range insts {
		if in.Universe.Screener != nil {
			continue
		}
		for _, id := range in.Universe.Static {
			if _, ok := rp.Spec(id); !ok {
				fail(fmt.Errorf("缺少 %s 的合约规格（已下线或未知）", id))
				return
			}
			if rp.coverage(id) == 0 {
				fail(fmt.Errorf("回测区间内没有 %s 的 %s K 线数据", id, map[int64]string{minuteMs: "1m", hourMs: "1H"}[base]))
				return
			}
		}
	}
	runner, _, _ := s.deps()
	if runner == nil {
		fail(fmt.Errorf("策略运行器未连接"))
		return
	}
	for _, in := range insts {
		in.State, in.StateReason = StRunning, ""
		ri := newInstRT(s, b, in, vers[in.VersionID])
		s.mu.Lock()
		s.insts[in.ID] = ri
		s.mu.Unlock()
		rts = append(rts, ri)
		sess, err := runner.Start(ctx, in, ri.ver, ModeBacktest, nil, rp, s)
		if err != nil {
			fail(fmt.Errorf("启动实例 #%d：%w", in.ID, err))
			return
		}
		ri.mu.Lock()
		ri.sess = sess
		ri.mu.Unlock()
		s.setInstanceState(in.ID, StRunning, "")
	}
	s.changed(b.acc.ID, "instance")
	drain := func(ri *instRT) error {
		for {
			c, ok := ri.pop()
			if !ok {
				return nil
			}
			if err := s.call(ctx, ri, ri.session(), c); err != nil {
				return fmt.Errorf("实例 #%d %s：%w", ri.inst.ID, c.name, err)
			}
		}
	}
	start := (acc.From + base - 1) / base * base
	span := float64(acc.To - acc.From)
	lastReport := time.Now()
	for t := start; t+base <= acc.To; t += base {
		if ctx.Err() != nil {
			fail(ctx.Err())
			return
		}
		T := t + base
		b.mu.Lock()
		for _, id := range b.activeInsts() {
			if bar, ok := rp.barAt(id, t); ok {
				b.processBar(id, bar)
			}
		}
		rp.setNow(T)
		if T%hourMs == 0 {
			b.settleFunding(T, T, 0)
		}
		b.checkBreaker(T)
		b.eqBuf = append(b.eqBuf, [2]float64{float64(T), b.equity()})
		if len(b.eqBuf) >= 2000 {
			s.flushEquity(b)
		}
		b.mu.Unlock()
		for _, ri := range rts {
			if err := drain(ri); err != nil {
				fail(err)
				return
			}
			if err := s.refreshUniverse(ri, T, t == start); err != nil {
				fail(fmt.Errorf("实例 #%d %w", ri.inst.ID, err))
				return
			}
			if err := drain(ri); err != nil {
				fail(err)
				return
			}
			step, _ := TFMillis(ri.ver.Timeframe)
			if T%step != 0 {
				continue
			}
			for _, id := range ri.currentUniverse() {
				bars, err := rp.Bars(id, ri.ver.Timeframe, 1)
				if err != nil || len(bars) == 0 || bars[len(bars)-1].Ts != T-step {
					continue
				}
				bar, inst, tf := bars[len(bars)-1], id, ri.ver.Timeframe
				ri.push(sessCall{"bar", func(ctx context.Context, x Session) error { return x.Bar(ctx, inst, tf, bar) }})
				if err := drain(ri); err != nil {
					fail(err)
					return
				}
			}
		}
		if time.Since(lastReport) > time.Second {
			lastReport = time.Now()
			st := b.setBacktest(func(x *BacktestStatus) { x.Progress, x.Clock = round(float64(T-acc.From)/span, 4), T })
			s.saveBacktest(b.acc.ID, st)
		}
	}
	b.setBacktest(func(x *BacktestStatus) { x.Clock = acc.To })
	finish("done", "")
}

// activeInsts lists instruments with positions or open orders (sorted). Caller holds b.mu.
func (b *book) activeInsts() []string {
	seen := map[string]bool{}
	for k := range b.pos {
		seen[k.id] = true
	}
	for _, o := range b.orders {
		seen[o.InstID] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// flushEquity writes buffered backtest equity samples. Caller holds b.mu.
func (s *Service) flushEquity(b *book) {
	if len(b.eqBuf) == 0 {
		return
	}
	err := s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO trade_equity VALUES(?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range b.eqBuf {
			if _, err := st.Exec(b.acc.ID, int64(x[0]), x[1]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("trade: backtest equity: %v", err)
	}
	b.eqBuf = b.eqBuf[:0]
}
