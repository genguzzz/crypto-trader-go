package trade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// instRT is the runtime of an instance that has a worker (live) or a backtest session.
type instRT struct {
	s *Service
	b *book

	mu       sync.Mutex
	inst     Instance
	ver      StrategyVersion
	sess     Session
	q        []sessCall
	universe []string
	paused   bool
	hbSaved  int64
	// consecutive events whose strategy handler raised (3 → paused)
	handlerErrs int
	state       []byte

	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

type sessCall struct {
	name    string
	fn      func(context.Context, Session) error
	timeout time.Duration // 0 = Options.SessionTimeout (a batch of bars gets more)
}

func newInstRT(s *Service, b *book, inst Instance, ver StrategyVersion) *instRT {
	return &instRT{s: s, b: b, inst: inst, ver: ver, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (ri *instRT) instance() Instance {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	return ri.inst
}

func (ri *instRT) session() Session {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	return ri.sess
}

func (ri *instRT) status() string {
	if sess := ri.session(); sess != nil {
		return sess.Status()
	}
	return ""
}

func (ri *instRT) timeframe() string { return InstanceTimeframe(ri.instance(), ri.ver) }

func (ri *instRT) currentUniverse() []string {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	return append([]string(nil), ri.universe...)
}

func (ri *instRT) push(c sessCall) {
	ri.mu.Lock()
	ri.q = append(ri.q, c)
	ri.mu.Unlock()
	select {
	case ri.wake <- struct{}{}:
	default:
	}
}

func (ri *instRT) pop() (sessCall, bool) {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	if len(ri.q) == 0 {
		return sessCall{}, false
	}
	c := ri.q[0]
	ri.q[0] = sessCall{}
	ri.q = ri.q[1:]
	return c, true
}

// enqueue delivers an event to an instance's strategy after the event it is handling now
// (never inline: the strategy may be waiting for our answer to its own query or signal).
func (s *Service) enqueue(instanceID int64, name string, fn func(context.Context, Session) error) {
	if ri := s.instRT(instanceID); ri != nil {
		ri.push(sessCall{name: name, fn: fn})
	}
}

// ---------- CRUD ----------

// InstanceInput creates an instance; InstancePatch edits one (nil = unchanged).
type InstanceInput struct {
	VersionID string             `json:"versionId"`
	Name      string             `json:"name"`
	Params    map[string]float64 `json:"params"`
	Universe  *Universe          `json:"universe"`
	Exec      ExecMode           `json:"exec"`
}

type InstancePatch struct {
	VersionID *string            `json:"versionId"` // another version of the same strategy (upgrade / downgrade)
	Name      *string            `json:"name"`
	Params    map[string]float64 `json:"params"`
	Universe  *Universe          `json:"universe"`
	Exec      *ExecMode          `json:"exec"`
}

var instIDRe = regexp.MustCompile(`^[A-Z0-9]{1,20}-USDT-SWAP$`)

func (s *Service) validUniverse(u Universe) (Universe, error) {
	if (len(u.Static) == 0) == (u.Screener == nil) {
		return u, errBad("标的池需为固定列表或筛选器之一")
	}
	if u.Screener != nil {
		q := *u.Screener
		q.Side = strings.ToLower(q.Side)
		if q.Side != "" && q.Side != "long" && q.Side != "short" && q.Side != "both" {
			return u, errBad("筛选方向需为 long / short / both")
		}
		if q.Top < 1 || q.Top > 50 {
			return u, errBad("筛选数量 top 需在 1–50")
		}
		if q.MinScore < 0 || q.MinScore > 100 {
			return u, errBad("最低分需在 0–100")
		}
		if _, err := refreshMillis(q.Refresh); err != nil {
			return u, errBad(err.Error())
		}
		for k, r := range q.Filter {
			if !finite(r[0]) || !finite(r[1]) || r[0] > r[1] {
				return u, errBad(fmt.Sprintf("过滤条件 %s 范围无效", k))
			}
		}
		u.Screener = &q
		return u, nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range u.Static {
		id = strings.ToUpper(strings.TrimSpace(id))
		if !instIDRe.MatchString(id) {
			return u, errBad(fmt.Sprintf("合约 %q 需为 XXX-USDT-SWAP 形式", id))
		}
		if s.md != nil {
			if _, ok := s.md.Spec(id); !ok {
				return u, errBad("未知或已下线的合约 " + id)
			}
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > 50 {
		return u, errBad("固定标的最多 50 个")
	}
	u.Static = ids
	return u, nil
}

func validExec(e ExecMode) (ExecMode, error) {
	switch e {
	case "":
		return ExecAuto, nil
	case ExecAuto, ExecAI, ExecRecord:
		return e, nil
	}
	return e, errBad("执行模式需为 auto / ai / record")
}

func (s *Service) CreateInstance(accountID int64, in InstanceInput) (Instance, error) {
	b, err := s.book(accountID)
	if err != nil {
		return Instance{}, err
	}
	if b.snapshot().Archived {
		return Instance{}, errBad("账户已归档")
	}
	ver, err := s.version(in.VersionID)
	if err != nil {
		return Instance{}, err
	}
	params, err := validParams(ver.Params, in.Params)
	if err != nil {
		return Instance{}, err
	}
	u := ver.DefaultUniverse
	if in.Universe != nil {
		u = *in.Universe
	}
	if u, err = s.validUniverse(u); err != nil {
		return Instance{}, err
	}
	exec, err := validExec(in.Exec)
	if err != nil {
		return Instance{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = ver.Name
	}
	if utf8.RuneCountInString(name) > 40 {
		return Instance{}, errBad("名称最多 40 个字")
	}
	now := time.Now().UnixMilli()
	inst := Instance{AccountID: accountID, VersionID: ver.ID, Name: name, Params: params, Universe: u, Exec: exec, State: StCreated, ConfigVersion: 1, CreatedAt: now}
	err = s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO trade_instances(account_id, version_id, name, params, universe, exec, state, config_version, created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			accountID, ver.ID, name, mustJSON(params), mustJSON(u), exec, StCreated, 1, now)
		if err != nil {
			return err
		}
		if inst.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return insertConfig(tx, inst, now)
	})
	if err != nil {
		return Instance{}, err
	}
	s.changed(accountID, "instance")
	return inst, nil
}

func insertConfig(tx *sql.Tx, in Instance, now int64) error {
	_, err := tx.Exec(`INSERT INTO trade_instance_configs VALUES(?,?,?,?,?,?,?)`, in.ID, in.ConfigVersion, in.VersionID, mustJSON(in.Params), mustJSON(in.Universe), in.Exec, now)
	return err
}

// UpdateInstance stores a new config version; a running live instance is restarted with it
// (its saved strategy state is kept).
func (s *Service) UpdateInstance(id int64, p InstancePatch) (Instance, error) {
	inst, err := s.loadInstance(id)
	if err != nil {
		return inst, err
	}
	if ri := s.instRT(id); ri != nil {
		inst = ri.instance()
		if ri.b.barModel {
			return inst, errConflict("回测运行中，不能修改实例")
		}
	}
	ver, err := s.version(inst.VersionID)
	if err != nil {
		return inst, err
	}
	next := inst
	newVer := p.VersionID != nil && *p.VersionID != inst.VersionID
	if newVer {
		nv, err := s.version(*p.VersionID)
		if err != nil {
			return inst, err
		}
		if nv.Name != ver.Name {
			return inst, errBad(fmt.Sprintf("只能换成同一策略（%s）的其他版本", ver.Name))
		}
		// keep the values the new version still accepts; new or changed params start at their default
		kept := map[string]float64{}
		for _, sp := range nv.Params {
			if v, ok := inst.Params[sp.Name]; ok && checkParam(sp, v) == nil {
				kept[sp.Name] = v
			}
		}
		ver, next.VersionID, next.Params = nv, nv.ID, kept
		if next.Params, err = validParams(ver.Params, next.Params); err != nil {
			return inst, err
		}
	}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || utf8.RuneCountInString(n) > 40 {
			return inst, errBad("名称需为 1–40 个字")
		}
		next.Name = n
	}
	if p.Params != nil {
		merged := map[string]float64{}
		for k, v := range inst.Params {
			merged[k] = v
		}
		for k, v := range p.Params {
			merged[k] = v
		}
		if next.Params, err = validParams(ver.Params, merged); err != nil {
			return inst, err
		}
	}
	if p.Universe != nil {
		if next.Universe, err = s.validUniverse(*p.Universe); err != nil {
			return inst, err
		}
	}
	if p.Exec != nil {
		if next.Exec, err = validExec(*p.Exec); err != nil {
			return inst, err
		}
	}
	config := newVer || p.Params != nil || p.Universe != nil || p.Exec != nil
	if config {
		next.ConfigVersion++
	}
	now := time.Now().UnixMilli()
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE trade_instances SET version_id=?, name=?, params=?, universe=?, exec=?, config_version=? WHERE id=?`,
			next.VersionID, next.Name, mustJSON(next.Params), mustJSON(next.Universe), next.Exec, next.ConfigVersion, id); err != nil {
			return err
		}
		if config {
			return insertConfig(tx, next, now)
		}
		return nil
	})
	if err != nil {
		return inst, err
	}
	if ri := s.instRT(id); ri != nil {
		if newVer || p.Params != nil || p.Universe != nil {
			paused := ri.isPaused()
			s.stopWorker(ri)
			next.State = StRunning
			if paused {
				next.State = StPaused
			}
			s.startWorker(ri.b, next, ver, paused)
		} else {
			ri.mu.Lock()
			ri.inst.Name, ri.inst.Exec, ri.inst.ConfigVersion = next.Name, next.Exec, next.ConfigVersion
			ri.mu.Unlock()
		}
	}
	s.changed(inst.AccountID, "instance")
	return s.instanceView(id)
}

func (ri *instRT) isPaused() bool {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	return ri.paused
}

// Instances lists an account's instances with live state.
func (s *Service) Instances(accountID int64) ([]Instance, error) {
	if _, err := s.book(accountID); err != nil {
		return nil, err
	}
	xs, err := s.accountInstances(accountID)
	if err != nil {
		return nil, err
	}
	for i := range xs {
		if ri := s.instRT(xs[i].ID); ri != nil {
			in := ri.instance()
			xs[i].Heartbeat = max(xs[i].Heartbeat, in.Heartbeat)
			xs[i].Status = ri.status()
		}
	}
	if xs == nil {
		xs = []Instance{}
	}
	return xs, nil
}

func (s *Service) instanceView(id int64) (Instance, error) {
	in, err := s.loadInstance(id)
	if err != nil {
		return in, err
	}
	if ri := s.instRT(id); ri != nil {
		in.Heartbeat = max(in.Heartbeat, ri.instance().Heartbeat)
		in.Status = ri.status()
	}
	return in, nil
}

// ---------- lifecycle ----------

// InstanceAction applies start | pause | resume | stop to a live instance.
func (s *Service) InstanceAction(id int64, action string) (Instance, error) {
	inst, err := s.loadInstance(id)
	if err != nil {
		return inst, err
	}
	b, err := s.book(inst.AccountID)
	if err != nil {
		return inst, err
	}
	if b.barModel {
		return inst, errBad("回测账户的实例由回测运行驱动（POST /trade/backtests/{id}/run）")
	}
	ri := s.instRT(id)
	switch action {
	case "start":
		if ri != nil {
			return inst, errConflict("实例已在运行")
		}
		if b.snapshot().Archived {
			return inst, errBad("账户已归档")
		}
		runner, _, _ := s.deps()
		if runner == nil {
			s.setInstanceState(id, inst.State, "策略运行器未接入，无法启动")
			s.changed(inst.AccountID, "instance")
			return inst, errUnavailable("策略运行器未接入，无法启动")
		}
		ver, err := s.version(inst.VersionID)
		if err != nil {
			return inst, err
		}
		if tf := InstanceTimeframe(inst, ver); !validTF(tf) {
			return inst, errBad("策略周期 " + tf + " 不受支持")
		}
		inst.State, inst.StateReason = StRunning, ""
		s.setInstanceState(id, StRunning, "")
		s.startWorker(b, inst, ver, false)
	case "pause", "resume":
		if ri == nil {
			return inst, errConflict("实例未运行")
		}
		pause := action == "pause"
		ri.mu.Lock()
		ri.paused = pause
		ri.inst.State = StRunning
		if pause {
			ri.inst.State = StPaused
		}
		st := ri.inst.State
		ri.mu.Unlock()
		s.setInstanceState(id, st, "")
	case "stop":
		if ri != nil {
			s.stopWorker(ri)
		}
		s.setInstanceState(id, StStopped, "")
	default:
		return inst, errNotFound("未知操作 " + action)
	}
	s.updateKeep()
	s.changed(inst.AccountID, "instance")
	return s.instanceView(id)
}

func (s *Service) startWorker(b *book, inst Instance, ver StrategyVersion, paused bool) *instRT {
	ri := newInstRT(s, b, inst, ver)
	ri.paused = paused
	ctx, cancel := context.WithCancel(s.baseCtx())
	ri.cancel = cancel
	s.mu.Lock()
	s.insts[inst.ID] = ri
	s.mu.Unlock()
	go s.runLive(ctx, ri)
	return ri
}

func (s *Service) stopWorker(ri *instRT) {
	ri.cancel()
	select {
	case <-ri.done:
	case <-time.After(15 * time.Second):
		log.Printf("trade: instance %d worker did not stop in time", ri.inst.ID)
	}
	s.mu.Lock()
	if s.insts[ri.inst.ID] == ri {
		delete(s.insts, ri.inst.ID)
	}
	s.mu.Unlock()
}

func (s *Service) setRTState(ri *instRT, st InstanceState, reason string) {
	ri.mu.Lock()
	changed := ri.inst.State != st || ri.inst.StateReason != reason
	ri.inst.State, ri.inst.StateReason = st, reason
	id, acct, name := ri.inst.ID, ri.inst.AccountID, ri.inst.Name
	ri.mu.Unlock()
	if !changed {
		return
	}
	s.setInstanceState(id, st, reason)
	s.changed(acct, "instance")
	if st == StFaulted || st == StStopped {
		title := map[InstanceState]string{StFaulted: "策略实例故障", StStopped: "策略实例已停止"}[st]
		ri.b.notify(ri.b.event("instance", title+" · #"+fmt.Sprint(id), "", time.Now().UnixMilli(), name, reason))
	}
}

// healthyRun is how long a session must run for its next fault to count as the first.
const healthyRun = 10 * time.Minute

// runLive keeps one live instance's strategy process running: start (waiting while the runner
// is offline), deliver queued events in order, restart up to 3 times after faults with the saved
// state, then give up (stopped).
func (s *Service) runLive(ctx context.Context, ri *instRT) {
	defer close(ri.done)
	faults := 0
	for {
		runner, _, _ := s.deps()
		if runner == nil || !runner.Available() {
			ri.mu.Lock()
			st := ri.inst.State
			ri.mu.Unlock()
			s.setRTState(ri, st, "等待策略运行器连接")
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.opts.RunnerRetry):
				continue
			}
		}
		inst := ri.instance()
		sess, err := runner.Start(ctx, inst, ri.ver, ModeLive, s.loadState(inst.ID), s.md, s)
		if err == nil {
			ri.mu.Lock()
			ri.sess, ri.universe = sess, nil
			ri.mu.Unlock()
			st := StRunning
			if ri.isPaused() {
				st = StPaused
			}
			s.setRTState(ri, st, "")
			s.refreshUniverse(ri, s.md.Now(), true)
			s.updateKeep()
			began := time.Now()
			err = s.pump(ctx, ri, sess)
			if time.Since(began) > healthyRun {
				faults = 0 // a fault after a long healthy run starts the count again
			}
			ri.mu.Lock()
			ri.sess = nil
			ri.mu.Unlock()
			s.persistState(ri, sess, true)
		}
		if ctx.Err() != nil {
			if sess != nil {
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				sess.Stop(sctx)
				cancel()
			}
			return
		}
		faults++
		if faults > 3 {
			s.setRTState(ri, StStopped, "连续故障 3 次后停止："+errText(err))
			s.mu.Lock()
			if s.insts[ri.inst.ID] == ri {
				delete(s.insts, ri.inst.ID)
			}
			s.mu.Unlock()
			s.updateKeep()
			return
		}
		s.setRTState(ri, StFaulted, fmt.Sprintf("%s；%d 秒后重启（%d/3）", errText(err), 2*faults, faults))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(2*faults) * time.Second):
		}
	}
}

func errText(err error) string {
	if err == nil {
		return "进程退出"
	}
	return err.Error()
}

// pump delivers queued events until the session dies or ctx ends.
func (s *Service) pump(ctx context.Context, ri *instRT, sess Session) error {
	for {
		c, ok := ri.pop()
		if !ok {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-sess.Done():
				if err := sess.Err(); err != nil {
					return err
				}
				return ErrFaulted
			case <-ri.wake:
				continue
			}
		}
		if err := s.call(ctx, ri, sess, c); err != nil {
			return err
		}
	}
}

// call runs one session event; faults (timeout, dead process) end the session, other errors are
// logged and the event is dropped.
func (s *Service) call(ctx context.Context, ri *instRT, sess Session, c sessCall) error {
	d := max(s.opts.SessionTimeout, c.timeout)
	cctx, cancel := context.WithTimeout(ctx, d)
	err := c.fn(cctx, sess)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-sess.Done():
			return err
		default:
		}
		if errors.Is(err, ErrFaulted) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// a strategy handler raised: the process is alive and acknowledged the event
		log.Printf("trade: instance %d %s: %v", ri.inst.ID, c.name, err)
		ri.mu.Lock()
		ri.handlerErrs++
		pause := ri.handlerErrs >= 3 && !ri.paused
		if pause {
			ri.paused, ri.handlerErrs = true, 0
		}
		ri.mu.Unlock()
		if pause {
			if ri.b.barModel {
				return fmt.Errorf("策略连续 3 次处理异常：%w", err)
			}
			s.setRTState(ri, StPaused, "策略连续 3 次处理异常，已暂停："+err.Error())
		}
	} else {
		ri.mu.Lock()
		ri.handlerErrs = 0
		ri.mu.Unlock()
	}
	now := ri.b.market().Now()
	ri.mu.Lock()
	ri.inst.Heartbeat = now
	save := now-ri.hbSaved > 30_000 && !ri.b.barModel
	if save {
		ri.hbSaved = now
	}
	id := ri.inst.ID
	ri.mu.Unlock()
	if save {
		s.db.Exec(`UPDATE trade_instances SET heartbeat=? WHERE id=?`, now, id)
	}
	s.persistState(ri, sess, !ri.b.barModel)
	return nil
}

// persistState stores the strategy's ctx.state when it changed (backtests keep it in memory
// until the run ends).
func (s *Service) persistState(ri *instRT, sess Session, write bool) {
	st := sess.State()
	if st == nil {
		return
	}
	ri.mu.Lock()
	same := string(st) == string(ri.state)
	ri.state = append(ri.state[:0], st...)
	id := ri.inst.ID
	ri.mu.Unlock()
	if !same && write {
		if err := s.saveState(id, st, time.Now().UnixMilli()); err != nil {
			log.Printf("trade: save state of instance %d: %v", id, err)
		}
	}
}

// refreshUniverse re-evaluates a screener universe when due (or always with force) and queues
// the change for the strategy.
func (s *Service) refreshUniverse(ri *instRT, now int64, force bool) error {
	inst := ri.instance()
	var cur []string
	if q := inst.Universe.Screener; q != nil {
		every, _ := refreshMillis(q.Refresh)
		if !force && now%every != 0 {
			return nil
		}
		rows, err := ri.b.market().Screener(*q)
		if err != nil {
			return fmt.Errorf("筛选器：%w", err)
		}
		for _, r := range rows {
			cur = append(cur, r.InstID)
		}
	} else {
		if !force {
			return nil
		}
		cur = append(cur, inst.Universe.Static...)
	}
	sort.Strings(cur)
	ri.mu.Lock()
	added, removed := diffSets(ri.universe, cur)
	if !force && len(added) == 0 && len(removed) == 0 {
		ri.mu.Unlock()
		return nil
	}
	ri.universe = cur
	ri.mu.Unlock()
	cp := append([]string{}, cur...)
	ri.push(sessCall{name: "universe", fn: func(ctx context.Context, x Session) error { return x.Universe(ctx, added, removed, cp) }})
	return nil
}

func diffSets(old, cur []string) (added, removed []string) {
	o, c := map[string]bool{}, map[string]bool{}
	for _, x := range old {
		o[x] = true
	}
	for _, x := range cur {
		c[x] = true
		if !o[x] {
			added = append(added, x)
		}
	}
	for _, x := range old {
		if !c[x] {
			removed = append(removed, x)
		}
	}
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	return
}

// resumeInstances restarts live instances that were running, paused or faulted before a restart.
func (s *Service) resumeInstances(ctx context.Context) {
	rows, err := s.db.Query(`SELECT `+instanceCols+` FROM trade_instances WHERE state IN (?,?,?)`, StRunning, StPaused, StFaulted)
	if err != nil {
		return
	}
	var xs []Instance
	for rows.Next() {
		if in, err := scanInstance(rows); err == nil {
			xs = append(xs, in)
		}
	}
	rows.Close()
	for _, in := range xs {
		b, err := s.book(in.AccountID)
		if err != nil || b.barModel || s.instRT(in.ID) != nil {
			continue
		}
		ver, err := s.version(in.VersionID)
		if err != nil {
			s.setInstanceState(in.ID, StStopped, "策略版本缺失")
			continue
		}
		s.startWorker(b, in, ver, in.State == StPaused)
	}
}
