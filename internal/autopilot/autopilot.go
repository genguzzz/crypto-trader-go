// Package autopilot is the trading desk: one standing AI chat that the server runs on its own.
//
//	strategy scans (pool-wide signals)  ──fires──▶ inbox ──batched, paced──▶ run ─┐
//	schedule (every N hours, a few minutes before the hour, UTC+8) ──────────────▶ run ─┤
//	owner ("run now", or simply writing in the chat) ────────────────────────────▶ run ─┘
//	                                                                                     ▼
//	                 brief (signals, positions, pending proposals, recent decisions, memory) → the desk's chat
//	                                                                                     ▼
//	                 agent: fresh data → desk_journal (every decision) → propose_trade (owner approves) → desk_review / desk_remember
//
// The desk never trades: every action is a proposal the owner approves (internal/realtrade). What it
// adds is the loop around the agent — when it runs, what it is told, and a journal of what it decided:
// each decision is stored with the market state at that moment and followed afterwards (price after
// 1 h / 4 h / 24 h, best and worst excursion, the linked proposal's fate), so decisions can be reviewed
// and counted later. Lessons distilled from reviews are the desk's long-term memory and travel in every
// brief, so the agent's session can be restarted (ResetSession) without losing what it learned.
//
// Cost control: scans are edge-triggered with a per-coin cooldown; fires are collected in an inbox and
// delivered together, at most one signal run per MinGapMin and MaxRunsDay per 24 h; what does not fit
// waits for the next scheduled run or goes stale. docs/trading-design.md §9.
package autopilot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"crypto-trader-go/internal/aigate"
	"crypto-trader-go/internal/realtrade"
	"crypto-trader-go/internal/signals"
	"crypto-trader-go/internal/store"
)

const schema = `
CREATE TABLE IF NOT EXISTS desk_runs(id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, trigger TEXT NOT NULL,
  slot INTEGER NOT NULL DEFAULT 0, label TEXT NOT NULL DEFAULT '', chat_id INTEGER NOT NULL DEFAULT 0, after_msg INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', signals INTEGER NOT NULL DEFAULT 0, insts TEXT NOT NULL DEFAULT '',
  new_session INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL DEFAULT 0, answer_msg INTEGER NOT NULL DEFAULT 0,
  summary TEXT NOT NULL DEFAULT '', cost_usd REAL NOT NULL DEFAULT 0, tokens INTEGER NOT NULL DEFAULT 0, latency_ms INTEGER NOT NULL DEFAULT 0,
  decisions INTEGER NOT NULL DEFAULT 0, proposals INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS desk_runs_ts ON desk_runs(ts);
CREATE TABLE IF NOT EXISTS desk_inbox(id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, fire_id INTEGER NOT NULL DEFAULT 0,
  sub_id INTEGER NOT NULL DEFAULT 0, strategy TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '', inst_id TEXT NOT NULL DEFAULT '',
  px REAL NOT NULL DEFAULT 0, value REAL NOT NULL DEFAULT 0, text TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, run_id INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS desk_inbox_status ON desk_inbox(status, ts);
CREATE TABLE IF NOT EXISTS desk_strategy_subs(key TEXT PRIMARY KEY, sub_id INTEGER NOT NULL, hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS desk_decisions(id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, run_id INTEGER NOT NULL DEFAULT 0,
  chat_id INTEGER NOT NULL DEFAULT 0, inst_id TEXT NOT NULL, action TEXT NOT NULL, side TEXT NOT NULL DEFAULT '',
  conviction TEXT NOT NULL DEFAULT '', thesis TEXT NOT NULL DEFAULT '', entry REAL NOT NULL DEFAULT 0, sl REAL NOT NULL DEFAULT 0,
  tp REAL NOT NULL DEFAULT 0, invalidation TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '', strategy TEXT NOT NULL DEFAULT '',
  trigger TEXT NOT NULL DEFAULT '', proposal_id INTEGER NOT NULL DEFAULT 0, px REAL NOT NULL DEFAULT 0, snapshot TEXT NOT NULL DEFAULT '',
  px_1h REAL, px_4h REAL, px_24h REAL, ret_1h REAL, ret_4h REAL, ret_24h REAL, mfe_24h REAL, mae_24h REAL,
  outcome_at INTEGER NOT NULL DEFAULT 0, proposal_status TEXT NOT NULL DEFAULT '', pnl_usdt REAL NOT NULL DEFAULT 0,
  closed_at INTEGER NOT NULL DEFAULT 0, close_kind TEXT NOT NULL DEFAULT '',
  review TEXT NOT NULL DEFAULT '', verdict TEXT NOT NULL DEFAULT '', reviewed_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS desk_decisions_ts ON desk_decisions(ts);
CREATE INDEX IF NOT EXISTS desk_decisions_inst ON desk_decisions(inst_id, ts);
CREATE INDEX IF NOT EXISTS desk_decisions_run ON desk_decisions(run_id);
CREATE TABLE IF NOT EXISTS desk_memory(id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, updated INTEGER NOT NULL,
  kind TEXT NOT NULL, inst_id TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, by TEXT NOT NULL DEFAULT 'ai',
  source_decision INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, retired_reason TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS desk_memory_status ON desk_memory(status, updated);
`

// ChatTitle is the desk chat's title in the conversation list.
const ChatTitle = "交易台 · 全自动"

// Run triggers and states.
const (
	TriggerSchedule = "schedule"
	TriggerSignal   = "signal"
	TriggerManual   = "manual"

	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"

	InboxPending   = "pending"
	InboxDelivered = "delivered"
	InboxExpired   = "expired" // waited longer than StaleMin for a run
)

const (
	tickEvery     = 15 * time.Second
	scheduleGrace = 30 * time.Minute // a slot missed by more than this (server down, agent down) is skipped
	retryGap      = 2 * time.Minute  // after a run could not start
	runTimeout    = aigate.ChatTimeout + 2*time.Minute
)

var tz8 = time.FixedZone("UTC+8", 8*3600)

// Chats is the part of the AI gate the desk drives.
type Chats interface {
	EnsureChat(kind, title string) (int64, error)
	SteerChatWith(chat int64, role, message, model, effort string) error
	ChatRunning(chat int64) bool
	LastAnswer(chat, afterMsg int64) (aigate.Answer, bool)
	LastMsgID(chat int64) int64
	ResetSession(chat int64) error
}

// Scans is the part of the signal service the desk's strategies live in.
type Scans interface {
	SubscribeContext(ctx context.Context, in signals.SubscribeInput) (signals.Subscription, error)
	Cancel(id int64, by, reason string) (signals.Subscription, error)
	DeskSubs() (map[string]signals.Subscription, error)
	Get(id int64) (signals.Subscription, error)
}

// Trades is the part of the proposal service the desk reads (it never approves or executes).
type Trades interface {
	Get(id int64) (realtrade.Proposal, error)
	List(q realtrade.ListQuery) ([]realtrade.Proposal, error)
	WakeContext(chat int64, insts []string, posID int64) string
}

// Market is the price and fact-panel data a decision is stamped with and followed on.
type Market interface {
	Known(instID string) bool
	Quote(instID string) (px float64, ts int64, ok bool)
	Values(instID string) (map[string]float64, bool)
	// Bars1m are the confirmed 1m bars with from ≤ open time < to, oldest first.
	Bars1m(instID string, from, to int64) []signals.Bar
}

// Options wire the service.
type Options struct {
	Now  func() time.Time
	Logf func(format string, args ...any)
	// TradeMode is shown in the status and the brief ("dryrun" | "live").
	TradeMode func() string
}

// Service is the trading desk.
type Service struct {
	st     *store.Store
	db     *sql.DB
	chats  Chats
	scans  Scans
	trades Trades
	mkt    Market
	opts   Options
	now    func() time.Time
	logf   func(string, ...any)

	mu        sync.Mutex
	cfg       Config
	chatID    int64
	stratErr  map[string]string // strategy key → why its scan is not running
	lastTry   time.Time         // last attempt to start a run that failed
	lastError string
	onChange  func(kind string)

	runMu sync.Mutex // one run decision at a time (tick, manual run)
	nudge chan struct{}
}

// New creates the tables and loads the configuration.
func New(st *store.Store, chats Chats, scans Scans, trades Trades, mkt Market, opts Options) (*Service, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	s := &Service{st: st, db: st.DB, chats: chats, scans: scans, trades: trades, mkt: mkt, opts: opts, now: opts.Now, logf: opts.Logf,
		stratErr: map[string]string{}, nudge: make(chan struct{}, 1)}
	if _, err := s.db.Exec(schema); err != nil {
		return nil, fmt.Errorf("autopilot schema: %w", err)
	}
	s.cfg = DefaultConfig()
	if raw, ok := st.Setting(configKey); ok && raw != "" {
		var c Config
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			s.logf("autopilot: ignoring unreadable config: %v", err)
		} else if c, err = c.normalize(); err != nil {
			s.logf("autopilot: ignoring invalid stored config: %v", err)
		} else {
			s.cfg = c
		}
	}
	// a run that was in flight when the server stopped never reports back
	s.db.Exec(`UPDATE desk_runs SET status=?, error='服务重启，运行中断', finished_at=? WHERE status=?`, RunFailed, s.now().UnixMilli(), RunRunning)
	return s, nil
}

// SetOnChange sets the hook called after any change (kind: config | run | inbox | journal | memory;
// forwarded to SSE); must not block.
func (s *Service) SetOnChange(f func(kind string)) { s.mu.Lock(); s.onChange = f; s.mu.Unlock() }

func (s *Service) changed(kind string) {
	s.mu.Lock()
	f := s.onChange
	s.mu.Unlock()
	if f != nil {
		f(kind)
	}
}

// Config returns the current configuration.
func (s *Service) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.clone()
}

// ChatID is the desk's chat (0 until the desk was enabled once).
func (s *Service) ChatID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatID
}

// IsDeskChat reports whether chat is the desk's.
func (s *Service) IsDeskChat(chat int64) bool { return chat > 0 && chat == s.ChatID() }

// ensureChat creates the desk's chat on first use.
func (s *Service) ensureChat() (int64, error) {
	if id := s.ChatID(); id > 0 {
		return id, nil
	}
	id, err := s.chats.EnsureChat(aigate.KindDesk, ChatTitle)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.chatID = id
	s.mu.Unlock()
	return id, nil
}

// SetConfig validates, stores and applies a configuration.
func (s *Service) SetConfig(c Config) (Config, error) {
	c, err := c.normalize()
	if err != nil {
		return Config{}, err
	}
	raw, _ := json.Marshal(c)
	if err := s.st.PutSetting(configKey, string(raw)); err != nil {
		return Config{}, err
	}
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	if c.Enabled {
		if _, err := s.ensureChat(); err != nil {
			return c, err
		}
	}
	s.reconcile(context.Background())
	s.changed("config")
	s.poke()
	return c, nil
}

func (s *Service) poke() {
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

// ChatDone is called when any chat turn ends: a desk run may have finished.
func (s *Service) ChatDone(chat int64) {
	if s.IsDeskChat(chat) {
		s.poke()
	}
}

// OnFire receives a fire of one of the desk's scans (signals.DeskSink): each coin becomes an inbox
// item. Called from the signal loop, so it only writes and nudges.
func (s *Service) OnFire(d signals.Delivery, sub signals.Subscription) {
	st, _ := s.Config().strategy(sub.Label)
	label := st.Label
	if label == "" {
		label = sub.Label
	}
	n := 0
	for _, h := range d.Hits {
		text := h.Text
		if text == "" {
			text = d.Matched
		}
		if _, err := s.db.Exec(`INSERT INTO desk_inbox(ts, fire_id, sub_id, strategy, label, inst_id, px, value, text, status) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			d.Ts, d.FireID, d.SubID, sub.Label, label, h.InstID, h.Px, h.Value, text, InboxPending); err != nil {
			s.logf("autopilot: inbox: %v", err)
			continue
		}
		n++
	}
	if n > 0 {
		s.changed("inbox")
		s.poke()
	}
}

// Run drives the desk until ctx ends.
func (s *Service) Run(ctx context.Context) {
	if id, ok := s.standingChat(); ok {
		s.mu.Lock()
		s.chatID = id
		s.mu.Unlock()
	}
	if s.Config().Enabled {
		if _, err := s.ensureChat(); err != nil {
			s.logf("autopilot: chat: %v", err)
		}
	}
	// the scans need the pool's data, which arrives a little after start: retried on the slow tick
	s.reconcile(ctx)
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	slow := time.NewTicker(time.Minute)
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.step(ctx)
		case <-s.nudge:
			s.step(ctx)
		case <-slow.C:
			s.follow()
			if s.strategiesBroken() {
				s.reconcile(ctx)
			}
		}
	}
}

// standingChat finds a desk chat created earlier (the Chats implementation may know it).
func (s *Service) standingChat() (int64, bool) {
	if f, ok := s.chats.(interface {
		StandingChat(kind string) (int64, bool)
	}); ok {
		return f.StandingChat(aigate.KindDesk)
	}
	return 0, false
}

// step is one pass of the loop: settle a finished run, then start one if something is due.
func (s *Service) step(ctx context.Context) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	now := s.now()
	s.settle(now)
	cfg := s.Config()
	s.expireInbox(cfg, now)
	if !cfg.Enabled {
		return
	}
	if s.running() != nil {
		return
	}
	s.mu.Lock()
	wait := now.Sub(s.lastTry) < retryGap
	s.mu.Unlock()
	if wait {
		return
	}
	if slot, ok := s.scheduleDue(cfg, now); ok {
		s.start(ctx, cfg, TriggerSchedule, slot, "", now)
		return
	}
	if s.signalDue(cfg, now) {
		s.start(ctx, cfg, TriggerSignal, 0, "", now)
	}
}

// running returns the run in flight.
func (s *Service) running() *RunRow {
	r, err := s.runWhere(`status=? ORDER BY id DESC LIMIT 1`, RunRunning)
	if err != nil {
		return nil
	}
	return &r
}

// slotOf is the schedule slot a moment belongs to and when that slot's run is due: slots start
// LeadMin before every multiple of EveryHours on the UTC+8 clock (23:50, 01:50, … for 2 h / 10 min).
func slotOf(cfg Config, now time.Time) (slot int64, due time.Time) {
	every := int64(cfg.Schedule.EveryHours) * 3_600_000
	lead := int64(cfg.Schedule.LeadMin) * 60_000
	const off = 8 * 3_600_000
	slot = (now.UnixMilli() + lead + off) / every
	return slot, time.UnixMilli(slot*every - lead - off)
}

// NextSchedule is when the next scheduled run is due (zero when the schedule is off).
func (s *Service) NextSchedule() time.Time {
	cfg := s.Config()
	if !cfg.Enabled || !cfg.Schedule.Enabled {
		return time.Time{}
	}
	now := s.now()
	slot, due := slotOf(cfg, now)
	var last int64
	s.db.QueryRow(`SELECT COALESCE(MAX(slot),0) FROM desk_runs WHERE trigger=?`, TriggerSchedule).Scan(&last)
	if slot > last && now.Sub(due) <= scheduleGrace {
		return due // due now, waiting for the agent
	}
	return due.Add(time.Duration(cfg.Schedule.EveryHours) * time.Hour)
}

// scheduleDue reports whether the current slot still needs its run.
func (s *Service) scheduleDue(cfg Config, now time.Time) (int64, bool) {
	if !cfg.Schedule.Enabled {
		return 0, false
	}
	slot, due := slotOf(cfg, now)
	if now.Sub(due) > scheduleGrace {
		return 0, false
	}
	var last int64
	s.db.QueryRow(`SELECT COALESCE(MAX(slot),0) FROM desk_runs WHERE trigger=?`, TriggerSchedule).Scan(&last)
	return slot, slot > last
}

// signalDue reports whether pending inbox items may start a run now: the oldest has waited BatchSec
// (so fires of the same minute travel together), the last run started at least MinGapMin ago and
// fewer than MaxRunsDay signal runs started in the last 24 h.
func (s *Service) signalDue(cfg Config, now time.Time) bool {
	if !cfg.Signals.Enabled {
		return false
	}
	var oldest sql.NullInt64
	s.db.QueryRow(`SELECT MIN(ts) FROM desk_inbox WHERE status=?`, InboxPending).Scan(&oldest)
	if !oldest.Valid || now.UnixMilli()-oldest.Int64 < int64(cfg.Signals.BatchSec)*1000 {
		return false
	}
	var last int64
	s.db.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM desk_runs WHERE status<>?`, RunFailed).Scan(&last)
	if last > 0 && now.UnixMilli()-last < int64(cfg.Signals.MinGapMin)*60_000 {
		return false
	}
	return s.signalRuns(now) < cfg.Signals.MaxRunsDay
}

// signalRuns counts signal runs started in the last 24 h.
func (s *Service) signalRuns(now time.Time) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM desk_runs WHERE trigger=? AND status<>? AND ts>=?`, TriggerSignal, RunFailed, now.Add(-24*time.Hour).UnixMilli()).Scan(&n)
	return n
}

// expireInbox drops pending items that waited too long: their move is old news.
func (s *Service) expireInbox(cfg Config, now time.Time) {
	res, err := s.db.Exec(`UPDATE desk_inbox SET status=? WHERE status=? AND ts<?`, InboxExpired, InboxPending,
		now.Add(-time.Duration(cfg.Signals.StaleMin)*time.Minute).UnixMilli())
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			s.changed("inbox")
		}
	}
}

// ErrDisabled: the desk is off.
var ErrDisabled = errors.New("交易台未启用")

// ErrRunning: a run is already in flight.
var ErrRunning = errors.New("交易台正在运行，等这一轮结束")

// RunNow starts a run on the owner's request (note is added to the brief).
func (s *Service) RunNow(ctx context.Context, note string) (RunRow, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	cfg := s.Config()
	if !cfg.Enabled {
		return RunRow{}, ErrDisabled
	}
	now := s.now()
	s.settle(now)
	if s.running() != nil {
		return RunRow{}, ErrRunning
	}
	id, err := s.start(ctx, cfg, TriggerManual, 0, note, now)
	if err != nil {
		return RunRow{}, err
	}
	return s.runByID(id)
}

// start builds the brief and delivers it to the desk's chat. Caller holds runMu.
func (s *Service) start(_ context.Context, cfg Config, trigger string, slot int64, note string, now time.Time) (int64, error) {
	fail := func(err error) (int64, error) {
		s.mu.Lock()
		s.lastTry, s.lastError = now, err.Error()
		s.mu.Unlock()
		s.logf("autopilot: %s run not started: %v", trigger, err)
		return 0, err
	}
	chat, err := s.ensureChat()
	if err != nil {
		return fail(err)
	}
	items := s.pendingInbox()
	newSession := false
	if cfg.SessionRuns > 0 && !s.chats.ChatRunning(chat) && s.runsInSession() >= cfg.SessionRuns {
		if err := s.chats.ResetSession(chat); err == nil {
			newSession = true
		}
	}
	insts := []string{}
	for _, it := range items {
		if !slices.Contains(insts, it.InstID) {
			insts = append(insts, it.InstID)
		}
	}
	label := triggerLabel(trigger, items)
	res, err := s.db.Exec(`INSERT INTO desk_runs(ts, trigger, slot, label, chat_id, after_msg, status, signals, insts, new_session) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		now.UnixMilli(), trigger, slot, label, chat, s.chats.LastMsgID(chat), RunRunning, len(items), joinIDs(insts), boolInt(newSession))
	if err != nil {
		return fail(err)
	}
	runID, _ := res.LastInsertId()
	msg := s.brief(cfg, briefInput{RunID: runID, Trigger: trigger, Now: now, Items: items, Insts: insts, Chat: chat, Note: note, NewSession: newSession})
	if err := s.chats.SteerChatWith(chat, aigate.RoleSignal, msg, cfg.Model, cfg.Effort); err != nil {
		// a run that never reached the agent is not a run: no row (the slot stays due and is retried, the
		// inbox keeps its items); the status carries why
		s.db.Exec(`DELETE FROM desk_runs WHERE id=?`, runID)
		return fail(errors.New(runErrText(err)))
	}
	if len(items) > 0 {
		ids := make([]any, 0, len(items)+2)
		ids = append(ids, InboxDelivered, runID)
		q := `UPDATE desk_inbox SET status=?, run_id=? WHERE id IN (`
		for i, it := range items {
			if i > 0 {
				q += ","
			}
			q += "?"
			ids = append(ids, it.ID)
		}
		s.db.Exec(q+")", ids...)
	}
	s.mu.Lock()
	s.lastTry, s.lastError = time.Time{}, ""
	s.mu.Unlock()
	s.logf("autopilot: run #%d started (%s, %d signals)", runID, trigger, len(items))
	s.changed("run")
	return runID, nil
}

// ErrAgentDown: the run could not be delivered because the agent is not available.
var ErrAgentDown = errors.New("AI 未就绪（未授权、worker 未连接或代理不可用），稍后自动重试")

func runErrText(err error) string {
	switch {
	case errors.Is(err, aigate.ErrChatBusy):
		return "会话正忙，稍后自动重试"
	case errors.Is(err, aigate.ErrUnavailable), errors.Is(err, aigate.ErrWorkerGone):
		return ErrAgentDown.Error()
	}
	return err.Error()
}

// runsInSession counts the runs since the agent's session last started fresh.
func (s *Service) runsInSession() int {
	var since int64
	s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM desk_runs WHERE new_session=1`).Scan(&since)
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM desk_runs WHERE id>=? AND status<>?`, since, RunFailed).Scan(&n)
	return n
}

// settle closes the run in flight once its chat is idle again (or it overran).
func (s *Service) settle(now time.Time) {
	r := s.running()
	if r == nil {
		return
	}
	if s.chats.ChatRunning(r.ChatID) && now.Sub(time.UnixMilli(r.Ts)) < runTimeout {
		return
	}
	a, ok := s.chats.LastAnswer(r.ChatID, r.afterMsg)
	status, errText := RunDone, ""
	switch {
	case !ok:
		status, errText = RunFailed, "没有得到回答"
	case a.Error != "":
		status, errText = RunFailed, a.Error
	}
	var decisions, proposals int
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(proposal_id>0),0) FROM desk_decisions WHERE run_id=?`, r.ID).Scan(&decisions, &proposals)
	s.db.Exec(`UPDATE desk_runs SET status=?, error=?, finished_at=?, answer_msg=?, summary=?, cost_usd=?, tokens=?, latency_ms=?, decisions=?, proposals=? WHERE id=?`,
		status, errText, now.UnixMilli(), a.MsgID, clip(a.Text, 600), a.CostUSD, a.Tokens, a.LatencyMs, decisions, proposals, r.ID)
	s.logf("autopilot: run #%d %s (%d decisions, %d proposals, $%.3f)", r.ID, status, decisions, proposals, a.CostUSD)
	s.changed("run")
}

// currentRun is the run a journal entry made now belongs to (0: the owner's own conversation).
func (s *Service) currentRun() (int64, string) {
	if r := s.running(); r != nil {
		return r.ID, r.Trigger
	}
	return 0, ""
}

// SourceLabel names the desk run a proposal made in chat belongs to (for the proposal's source);
// false when chat is not the desk's or no run started in the last 15 minutes.
func (s *Service) SourceLabel(chat int64) (string, bool) {
	if !s.IsDeskChat(chat) {
		return "", false
	}
	r, err := s.runWhere(`ts>=? AND status<>? ORDER BY id DESC LIMIT 1`, s.now().Add(-15*time.Minute).UnixMilli(), RunFailed)
	if err != nil {
		return "", false
	}
	return "交易台 · " + r.Label, true
}
