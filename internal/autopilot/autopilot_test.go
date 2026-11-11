package autopilot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/aigate"
	"crypto-trader-go/internal/realtrade"
	"crypto-trader-go/internal/signals"
	"crypto-trader-go/internal/store"
)

// ---------- fakes ----------

type steer struct{ msg, model, effort string }

type fakeChats struct {
	mu      sync.Mutex
	chat    int64
	msgID   int64
	running bool
	steers  []steer
	err     error // returned by the next SteerChatWith
	answer  *aigate.Answer
	resets  int
}

func (f *fakeChats) EnsureChat(kind, title string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kind != aigate.KindDesk || title != ChatTitle {
		return 0, fmt.Errorf("unexpected chat %s %q", kind, title)
	}
	f.chat = 7
	return 7, nil
}
func (f *fakeChats) SteerChatWith(chat int64, role, msg, model, effort string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if chat != 7 || role != aigate.RoleSignal {
		return fmt.Errorf("unexpected steer chat %d role %s", chat, role)
	}
	f.msgID++
	f.steers = append(f.steers, steer{msg, model, effort})
	f.running, f.answer = true, nil
	return nil
}
func (f *fakeChats) ChatRunning(int64) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.running }
func (f *fakeChats) LastAnswer(_, after int64) (aigate.Answer, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answer == nil || f.answer.MsgID <= after {
		return aigate.Answer{}, false
	}
	return *f.answer, true
}
func (f *fakeChats) LastMsgID(int64) int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.msgID }
func (f *fakeChats) ResetSession(int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets++
	return nil
}

// finish ends the running turn with an answer.
func (f *fakeChats) finish(text string, cost float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgID++
	f.running = false
	f.answer = &aigate.Answer{MsgID: f.msgID, Text: text, CostUSD: cost, Tokens: 1234, LatencyMs: 900}
}
func (f *fakeChats) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.steers[len(f.steers)-1].msg
}
func (f *fakeChats) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.steers) }

type fakeScans struct {
	mu     sync.Mutex
	next   int64
	subs   map[int64]signals.Subscription
	fail   map[string]error
	cancel []string
}

func (f *fakeScans) SubscribeContext(_ context.Context, in signals.SubscribeInput) (signals.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[in.Desk]; err != nil {
		return signals.Subscription{}, err
	}
	if in.Desk == "" || in.ChatID != 7 || in.Spec.Type != "pool" {
		return signals.Subscription{}, fmt.Errorf("unexpected subscribe %+v", in)
	}
	f.next++
	sub := signals.Subscription{ID: f.next, Kind: signals.KindDesk, Label: in.Desk, Spec: in.Spec, CooldownMin: in.CooldownMin, Status: signals.StatusActive, Desc: in.Desk}
	if f.subs == nil {
		f.subs = map[int64]signals.Subscription{}
	}
	f.subs[sub.ID] = sub
	return sub, nil
}
func (f *fakeScans) Cancel(id int64, by, reason string) (signals.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sub, ok := f.subs[id]
	if !ok {
		return sub, signals.ErrEnded
	}
	delete(f.subs, id)
	f.cancel = append(f.cancel, sub.Label+":"+by)
	return sub, nil
}
func (f *fakeScans) DeskSubs() (map[string]signals.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]signals.Subscription{}
	for _, s := range f.subs {
		out[s.Label] = s
	}
	return out, nil
}
func (f *fakeScans) Get(id int64) (signals.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.subs[id]; ok {
		return s, nil
	}
	return signals.Subscription{}, signals.ErrNotFound
}
func (f *fakeScans) keys() map[string]int64 {
	out := map[string]int64{}
	subs, _ := f.DeskSubs()
	for k, s := range subs {
		out[k] = s.ID
	}
	return out
}

type fakeTrades struct {
	mu sync.Mutex
	ps map[int64]realtrade.Proposal
}

func (f *fakeTrades) Get(id int64) (realtrade.Proposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.ps[id]
	if !ok {
		return p, errors.New("not found")
	}
	return p, nil
}
func (f *fakeTrades) List(q realtrade.ListQuery) ([]realtrade.Proposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []realtrade.Proposal
	for _, p := range f.ps {
		if q.Status == "open" && p.Status != realtrade.StatusPending {
			continue
		}
		if q.Status == realtrade.StatusExecuted && p.Status != realtrade.StatusExecuted {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeTrades) WakeContext(_ int64, insts []string, _ int64) string {
	if len(insts) == 0 {
		return ""
	}
	return "仓位块：" + strings.Join(insts, ",")
}
func (f *fakeTrades) put(p realtrade.Proposal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ps == nil {
		f.ps = map[int64]realtrade.Proposal{}
	}
	f.ps[p.ID] = p
}

const (
	ena = "ENA-USDT-SWAP"
	btc = "BTC-USDT-SWAP"
)

type fakeMarket struct {
	mu   sync.Mutex
	now  func() time.Time
	px   map[string]float64
	bars map[string][]signals.Bar
}

func (f *fakeMarket) Known(id string) bool { return id == ena || id == btc }
func (f *fakeMarket) Quote(id string) (float64, int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	px, ok := f.px[id]
	return px, f.now().UnixMilli(), ok
}
func (f *fakeMarket) Values(id string) (map[string]float64, bool) {
	return map[string]float64{"ret_1h": 0.031, "funding_hourly": 0.0001}, id == ena
}
func (f *fakeMarket) Bars1m(id string, from, to int64) []signals.Bar {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []signals.Bar
	for _, b := range f.bars[id] {
		if b.T >= from && b.T < to {
			out = append(out, b)
		}
	}
	return out
}

type env struct {
	t      *testing.T
	s      *Service
	chats  *fakeChats
	scans  *fakeScans
	trades *fakeTrades
	mkt    *fakeMarket
	now    time.Time
	mu     sync.Mutex
}

func (e *env) clock() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
func (e *env) set(t time.Time)  { e.mu.Lock(); e.now = t; e.mu.Unlock() }
func (e *env) add(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}
func (e *env) step() { e.s.step(context.Background()) }

// at is a UTC+8 wall-clock time on 2026-11-11.
func at(h, m int) time.Time { return time.Date(2026, 11, 11, h, m, 0, 0, tz8) }

func newEnv(t *testing.T, start time.Time) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	e := &env{t: t, chats: &fakeChats{}, scans: &fakeScans{}, trades: &fakeTrades{}, now: start}
	e.mkt = &fakeMarket{now: e.clock, px: map[string]float64{ena: 0.5, btc: 100000}, bars: map[string][]signals.Bar{}}
	e.s, err = New(st, e.chats, e.scans, e.trades, e.mkt, Options{Now: e.clock, Logf: func(string, ...any) {}, TradeMode: func() string { return "dryrun" }})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) enable(mod func(*Config)) Config {
	e.t.Helper()
	c := e.s.Config()
	c.Enabled = true
	if mod != nil {
		mod(&c)
	}
	c, err := e.s.SetConfig(c)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) fire(strategy, inst, text string) {
	e.s.OnFire(signals.Delivery{SubID: 3, FireID: 9, Ts: e.clock().UnixMilli(), Hits: []signals.Hit{{InstID: inst, Px: 0.5, Value: 3.2, Text: text}}},
		signals.Subscription{ID: 3, Kind: signals.KindDesk, Label: strategy})
}

func (e *env) runs() []RunRow {
	e.t.Helper()
	rs, err := e.s.Runs(100, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return rs
}

// ---------- tests ----------

func TestDefaultsAndValidation(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled || !c.Schedule.Enabled || c.Schedule.EveryHours != 2 || c.Schedule.LeadMin != 10 || len(c.Strategies) != len(Strategies) {
		t.Fatalf("defaults %+v", c)
	}
	for _, sc := range c.Strategies {
		d, _ := strategyDef(sc.Key)
		if len(sc.Params) != len(d.Params) || sc.Enabled != d.On {
			t.Fatalf("strategy %s: %+v", sc.Key, sc)
		}
		for _, sd := range d.build(sc.Params) { // every built-in scan is a valid pool spec shape
			if sd.spec.Type != "pool" || sd.cooldown < 30 {
				t.Fatalf("%s builds %+v", sc.Key, sd)
			}
		}
	}
	bad := []func(*Config){
		func(c *Config) { c.Schedule.EveryHours = 3 },
		func(c *Config) { c.Schedule.LeadMin = 45 },
		func(c *Config) { c.Signals.MinGapMin = 2 },
		func(c *Config) { c.Signals.MaxRunsDay = 100 },
		func(c *Config) { c.Model = "gpt" },
		func(c *Config) { c.Strategies[0].Params["pct"] = 500 },
		func(c *Config) { c.Strategies = append(c.Strategies, StrategyConfig{Key: "nope"}) },
		func(c *Config) {
			c.Custom = []CustomStrategy{{Key: "custom:a", Label: "x", Spec: []byte(`{"type":"price"}`)}}
		},
		func(c *Config) { c.Custom = []CustomStrategy{{Key: "a", Label: "x", Spec: []byte(`{"type":"pool"}`)}} },
		func(c *Config) { c.Focus = strings.Repeat("长", 601) },
	}
	for i, f := range bad {
		c := DefaultConfig()
		f(&c)
		if _, err := c.normalize(); err == nil {
			t.Errorf("bad config %d accepted", i)
		}
	}
	// the default schedule: ten minutes before every even hour of the UTC+8 clock
	cfg := DefaultConfig()
	for _, c := range []struct{ now, due time.Time }{
		{at(13, 49), at(11, 50)}, {at(13, 50), at(13, 50)}, {at(15, 49), at(13, 50)}, {at(23, 55), at(23, 50)}, {at(0, 5), at(0, 0).Add(-10 * time.Minute)},
	} {
		if _, due := slotOf(cfg, c.now); !due.Equal(c.due) {
			t.Errorf("slot of %s is due %s, want %s", c.now.Format("15:04"), due.In(tz8).Format("15:04"), c.due.Format("15:04"))
		}
	}
	if nf := nextFunding(at(15, 50)); !nf.Equal(at(16, 0)) {
		t.Errorf("next funding after 15:50 is %s", nf)
	}
	if nf := nextFunding(at(16, 0)); !nf.Equal(at(0, 0).Add(24 * time.Hour)) {
		t.Errorf("next funding after 16:00 is %s", nf)
	}
}

func TestScheduleRuns(t *testing.T) {
	e := newEnv(t, at(13, 40))
	e.step()
	if e.chats.count() != 0 {
		t.Fatal("a disabled desk ran")
	}
	e.enable(func(c *Config) { c.Model, c.Effort, c.Focus = "claude-opus-5-5", "high", "只做前 30 的币" })
	if e.s.ChatID() != 7 {
		t.Fatalf("chat %d", e.s.ChatID())
	}
	// 13:40 is 110 minutes into the 11:50 slot: missed by more than the grace, so it is not run late
	e.step()
	if e.chats.count() != 0 {
		t.Fatal("a slot missed by 110 minutes was run")
	}
	if next := e.s.NextSchedule(); !next.Equal(at(13, 50)) {
		t.Fatalf("next schedule %s", next.In(tz8))
	}
	e.set(at(13, 50).Add(5 * time.Second))
	e.step()
	if e.chats.count() != 1 {
		t.Fatal("no run at 13:50")
	}
	msg, st := e.chats.last(), e.chats.steers[0]
	for _, want := range []string{"🤖 交易台 #1 · 定时巡检（11-11 13:50 UTC+8）", "下次 8 小时资金费结算 16:00（还有 2 小时 10 分）", "模拟（dry run）",
		"【主人的长期指示】只做前 30 的币", "自上次运行以来没有策略信号触发", "【AI 仓位与挂单】无", "desk_journal", "先看大盘", "不能下单"} {
		if !strings.Contains(msg, want) {
			t.Errorf("brief lacks %q:\n%s", want, msg)
		}
	}
	if st.model != "claude-opus-5-5" || st.effort != "high" {
		t.Fatalf("run used %+v", st)
	}
	e.step() // the run is in flight: nothing else starts
	e.add(3 * time.Minute)
	e.step()
	if e.chats.count() != 1 || e.s.Status().Running == nil {
		t.Fatal("a second run started while the first is in flight")
	}
	e.chats.finish("结论：没有机会。", 0.12)
	e.s.ChatDone(7)
	e.step()
	rs := e.runs()
	if len(rs) != 1 || rs[0].Status != RunDone || rs[0].CostUSD != 0.12 || rs[0].Tokens != 1234 || rs[0].Summary != "结论：没有机会。" || rs[0].Trigger != TriggerSchedule {
		t.Fatalf("run %+v", rs)
	}
	// the same slot is not run twice; the next one is
	e.set(at(15, 49))
	e.step()
	if e.chats.count() != 1 {
		t.Fatal("slot ran twice")
	}
	e.set(at(15, 51))
	e.step()
	if e.chats.count() != 2 {
		t.Fatal("no run at 15:50")
	}
	st2 := e.s.Status()
	if st2.Runs24h != 2 || st2.Running == nil || st2.LastRun == nil || st2.LastRun.ID != 1 || st2.NextScheduleAt != at(17, 50).UnixMilli() {
		t.Fatalf("status %+v", st2)
	}
}

func TestRunThatCannotStartIsRetried(t *testing.T) {
	e := newEnv(t, at(13, 50))
	e.enable(nil)
	e.chats.err = aigate.ErrUnavailable
	e.step()
	if rs := e.runs(); len(rs) != 0 || !strings.Contains(e.s.Status().LastError, "AI 未就绪") {
		t.Fatalf("a run that never started: rows %+v, status error %q", rs, e.s.Status().LastError)
	}
	e.chats.err = nil
	e.add(time.Minute) // within the retry gap
	e.step()
	if e.chats.count() != 0 {
		t.Fatal("retried before the gap")
	}
	e.add(2 * time.Minute)
	e.step()
	if e.chats.count() != 1 {
		t.Fatal("the slot was lost after a failed start")
	}
	if e.s.Status().LastError != "" {
		t.Fatal("the error outlived a successful start")
	}
	// too late: a slot that still cannot start after the grace is skipped
	e2 := newEnv(t, at(13, 50))
	e2.enable(nil)
	e2.chats.err = aigate.ErrChatBusy
	e2.step()
	e2.chats.err = nil
	e2.add(31 * time.Minute)
	e2.step()
	if e2.chats.count() != 0 {
		t.Fatal("a slot was run 31 minutes late")
	}
}

func TestSignalRunsAreBatchedAndPaced(t *testing.T) {
	e := newEnv(t, at(14, 25)) // past the grace of the 13:50 slot
	e.enable(func(c *Config) { c.Signals.MaxRunsDay = 2 })
	e.fire("surge", ena, "ENA 15 分钟内 +3.20%（0.48 → 0.5）")
	e.add(30 * time.Second)
	e.fire("momo_up", ena, "ENA 1 小时内 +4.50%；量能 2.4 倍")
	e.fire("surge", btc, "BTC 15 分钟内 -3.10%")
	e.step()
	if e.chats.count() != 0 {
		t.Fatal("ran before the batch window: fires of the same minute must travel together")
	}
	if e.s.Status().Pending != 3 {
		t.Fatalf("pending %d", e.s.Status().Pending)
	}
	e.add(61 * time.Second) // 91 s after the first fire
	e.step()
	if e.chats.count() != 1 {
		t.Fatal("no signal run after the batch window")
	}
	msg := e.chats.last()
	for _, want := range []string{"信号 · 急涨急跌、放量上攻", "自上次运行以来 3 条，涉及 2 个币", "· ENA：[急涨急跌] 15 分钟内 +3.20%（0.48 → 0.5）（14:25）；[放量上攻] 1 小时内 +4.50%；量能 2.4 倍（14:25）",
		"· BTC：[急涨急跌] 15 分钟内 -3.10%", "现价 0.5", "↳ 急涨急跌：", "↳ 放量上攻：", "仓位块：ENA-USDT-SWAP,BTC-USDT-SWAP", "只看上面触发信号的币", "信号运行 1/2 次（含本轮）"} {
		if !strings.Contains(msg, want) {
			t.Errorf("brief lacks %q:\n%s", want, msg)
		}
	}
	r := e.runs()[0]
	if r.Trigger != TriggerSignal || r.Signals != 3 || len(r.InstIDs) != 2 || e.s.Status().Pending != 0 {
		t.Fatalf("run %+v", r)
	}
	e.chats.finish("ok", 0.05)
	e.step()
	// a fire right after: held until MinGapMin since the last run started
	e.add(2 * time.Minute)
	e.fire("surge", ena, "ENA 15 分钟内 +5%")
	e.add(10 * time.Minute)
	e.step()
	if e.chats.count() != 1 {
		t.Fatal("a signal run started inside the minimum gap")
	}
	e.add(9 * time.Minute) // 21.5 min after the run started
	e.step()
	if e.chats.count() != 2 {
		t.Fatal("no signal run after the gap")
	}
	e.chats.finish("ok", 0.05)
	e.step()
	// the daily cap: the third fire waits, and the scheduled run takes it along
	e.add(30 * time.Minute)
	e.fire("oi_build", ena, "ENA OI +7%")
	e.add(5 * time.Minute)
	e.step()
	if e.chats.count() != 2 {
		t.Fatal("a signal run started over the daily cap")
	}
	e.set(at(15, 50))
	e.step()
	if e.chats.count() != 3 {
		t.Fatal("no scheduled run")
	}
	if msg := e.chats.last(); !strings.Contains(msg, "定时巡检") || !strings.Contains(msg, "[持仓激增] OI +7%") || !strings.Contains(msg, "先看大盘（BTC、ETH）和已有仓位，再看上面触发信号的币") {
		t.Fatalf("the scheduled run did not carry the waiting signal:\n%s", msg)
	}
	e.chats.finish("ok", 0.05)
	e.step()
	// stale: over the cap, nothing scheduled soon, the fire is dropped after StaleMin
	e.fire("surge", btc, "BTC 15 分钟内 +3%")
	e.add(119 * time.Minute)
	e.set(at(17, 40))
	e.step()
	if e.s.Status().Pending != 1 {
		t.Fatalf("pending %d before it is stale", e.s.Status().Pending)
	}
	e.set(at(17, 50).Add(30 * time.Second)) // 120.5 minutes old when the next slot comes
	e.step()
	if e.s.Status().Pending != 0 {
		t.Fatal("a fire older than StaleMin is still pending")
	}
	if its := e.s.inbox(`status=?`, InboxExpired); len(its) != 1 || its[0].InstID != btc {
		t.Fatalf("expired items %+v", its)
	}
}

func TestSessionRotation(t *testing.T) {
	e := newEnv(t, at(13, 50))
	e.enable(func(c *Config) { c.SessionRuns = 2 })
	for i := range 3 {
		e.set(at(13, 50).Add(time.Duration(i) * 2 * time.Hour))
		e.step()
		e.chats.finish("ok", 0)
		e.step()
	}
	if e.chats.count() != 3 || e.chats.resets != 1 {
		t.Fatalf("%d runs, %d session resets (want 3 and 1)", e.chats.count(), e.chats.resets)
	}
	if !strings.Contains(e.chats.steers[2].msg, "这是一段新的上下文") || strings.Contains(e.chats.steers[1].msg, "这是一段新的上下文") {
		t.Fatal("the third run (and only it) should say its context is new")
	}
	if rs := e.runs(); !rs[0].NewSession || rs[1].NewSession {
		t.Fatalf("runs %+v", rs)
	}
}

// However much context a brief carries, its instructions (the last part) are never cut off.
func TestBriefKeepsInstructions(t *testing.T) {
	e := newEnv(t, at(14, 25))
	e.enable(func(c *Config) { c.Focus = strings.Repeat("长", 600) })
	for i := range 40 {
		if _, err := e.s.Remember("lesson", "", fmt.Sprintf("第 %d 条经验：%s", i, strings.Repeat("很长的教训", 50)), "ai", 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.Record(JournalInput{ChatID: 7, InstID: ena, Action: "skip", Side: "long", Thesis: strings.Repeat("理由很长", 120)}); err != nil {
			t.Fatal(err)
		}
	}
	e.trades.put(realtrade.Proposal{ID: 9, InstID: ena, Kind: realtrade.KindOpen, Side: "short", Status: realtrade.StatusPending, ExpiresAt: at(15, 0).UnixMilli()})
	for i := range 30 {
		e.fire("surge", ena, fmt.Sprintf("ENA 15 分钟内 +%d.00%%（%s）", i, strings.Repeat("细节", 40)))
	}
	if _, err := e.s.RunNow(context.Background(), strings.Repeat("要求", 250)); err != nil {
		t.Fatal(err)
	}
	msg := e.chats.last()
	if n := len([]rune(msg)); n > 8000 {
		t.Fatalf("brief is %d runes: the chat takes 8000", n)
	}
	if !strings.HasSuffix(msg, "你不能下单，只能提交待审核的提议。）") || !strings.Contains(msg, "【这一轮要做的】") || !strings.Contains(msg, "desk_journal") {
		t.Fatalf("the instructions were cut off:\n…%s", msg[len(msg)-600:])
	}
	if !strings.Contains(msg, "· #9 ENA 开仓空，等待主人批准（15:00 前有效）") {
		t.Fatalf("pending proposal line missing:\n%s", msg[:3000])
	}
}

func TestManualRun(t *testing.T) {
	e := newEnv(t, at(14, 5))
	if _, err := e.s.RunNow(context.Background(), ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	e.enable(nil)
	r, err := e.s.RunNow(context.Background(), "重点看 ENA 的回踩")
	if err != nil || r.Trigger != TriggerManual || r.Status != RunRunning {
		t.Fatalf("manual run %+v %v", r, err)
	}
	if !strings.Contains(e.chats.last(), "【主人这次的要求】重点看 ENA 的回踩") || !strings.Contains(e.chats.last(), "手动运行") {
		t.Fatalf("brief:\n%s", e.chats.last())
	}
	if _, err := e.s.RunNow(context.Background(), ""); !errors.Is(err, ErrRunning) {
		t.Fatalf("second manual run: %v", err)
	}
	if label, ok := e.s.SourceLabel(7); !ok || label != "交易台 · 手动运行" {
		t.Fatalf("source label %q %v", label, ok)
	}
	if _, ok := e.s.SourceLabel(8); ok {
		t.Fatal("another chat got the desk's label")
	}
}

func TestReconcileScans(t *testing.T) {
	e := newEnv(t, at(14, 5))
	e.s.reconcile(context.Background())
	if len(e.scans.keys()) != 0 {
		t.Fatal("a disabled desk subscribed scans")
	}
	e.enable(nil)
	keys := e.scans.keys()
	for _, k := range []string{"surge", "momo_up", "flush_down", "oi_build", "funding_extreme:pos", "funding_extreme:neg"} {
		if keys[k] == 0 {
			t.Fatalf("scan %s missing: %v", k, keys)
		}
	}
	if len(keys) != 6 {
		t.Fatalf("scans %v", keys)
	}
	// unchanged config: nothing is touched
	e.enable(nil)
	if again := e.scans.keys(); fmt.Sprint(again) != fmt.Sprint(keys) || len(e.scans.cancel) != 0 {
		t.Fatalf("scans changed without a config change: %v → %v (%v)", keys, again, e.scans.cancel)
	}
	// a parameter change replaces that scan only; switching one off cancels it
	e.enable(func(c *Config) {
		c.Strategies[0].Params["pct"] = 5
		c.Strategies[3].Enabled = false
		c.Custom = []CustomStrategy{{Key: "custom:mine", Label: "我的共振", Enabled: true,
			Spec: []byte(`{"type":"pool","metric":"combo","legs":[{"metric":"move","windowMin":30,"dir":"up","pct":3},{"metric":"field","field":"funding_hourly","op":"<","value":0}]}`)}}
	})
	after := e.scans.keys()
	if after["surge"] == keys["surge"] || after["momo_up"] != keys["momo_up"] || after["oi_build"] != 0 || after["custom:mine"] == 0 {
		t.Fatalf("after the change: %v (before %v)", after, keys)
	}
	if got := e.scans.subs[after["surge"]].Spec.Pct; got != 5 {
		t.Fatalf("surge threshold %v", got)
	}
	// a scan that cannot start is reported on its strategy and retried
	e.scans.fail = map[string]error{"oi_build": errors.New("no current data")}
	e.enable(func(c *Config) { c.Strategies[3].Enabled = true })
	var view StrategyView
	for _, v := range e.s.StrategyViews() {
		if v.Key == "oi_build" {
			view = v
		}
	}
	if view.Error != "no current data" || len(view.Scans) != 0 || !e.s.strategiesBroken() {
		t.Fatalf("broken strategy view %+v", view)
	}
	e.scans.fail = nil
	e.s.reconcile(context.Background())
	if e.scans.keys()["oi_build"] == 0 || e.s.strategiesBroken() {
		t.Fatal("the scan was not retried")
	}
	// signals off (or the desk off): every scan is cancelled
	e.enable(func(c *Config) { c.Signals.Enabled = false })
	if len(e.scans.keys()) != 0 {
		t.Fatalf("scans left after turning signals off: %v", e.scans.keys())
	}
}

func TestJournalFollowAndStats(t *testing.T) {
	e := newEnv(t, at(14, 25))
	e.enable(nil)
	e.fire("surge", ena, "ENA 15 分钟内 +3.20%")
	e.add(2 * time.Minute)
	e.step() // signal run #1 in flight
	ctx := context.Background()
	if _, err := e.s.JournalTool(ctx, 8, map[string]string{"instId": "ENA", "action": "skip", "thesis": "没有机会不值得做"}); err == nil {
		t.Fatal("desk_journal worked outside the desk chat")
	}
	for _, bad := range []map[string]string{
		{"instId": "NOPE", "action": "skip", "thesis": "没有机会不值得做"},
		{"instId": "ENA", "action": "buy", "thesis": "没有机会不值得做"},
		{"instId": "ENA", "action": "open", "thesis": "突破回踩确认后做多"},                                     // a trade needs its side
		{"instId": "ENA", "action": "skip", "thesis": "短"},                                             // no reason
		{"instId": "ENA", "action": "open", "side": "long", "thesis": "突破回踩确认后做多", "proposalId": "99"}, // unknown proposal
	} {
		if _, err := e.s.JournalTool(ctx, 7, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	e.trades.put(realtrade.Proposal{ID: 5, InstID: ena, Kind: realtrade.KindOpen, Side: "short", Status: realtrade.StatusPending})
	out, err := e.s.JournalTool(ctx, 7, map[string]string{"instId": "ena", "action": "open", "side": "short", "conviction": "medium",
		"thesis": "急涨后量能衰减，资金费转正，做空回落", "entry": "0.5", "sl": "0.52", "tp": "0.46", "invalidation": "15m 收盘站上 0.52", "tags": "逆势,资金费", "proposalId": "5"})
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]any)["decisionId"].(int64)
	d, _ := e.s.Decision(id)
	if d.RunID != 1 || d.Trigger != TriggerSignal || d.Strategy != "surge" || d.Px != 0.5 || d.Conviction != "medium" || d.ProposalStatus != "pending" ||
		!strings.Contains(string(d.Snapshot), `"ret_1h":0.031`) || !strings.Contains(string(d.Snapshot), `"btcPx":100000`) {
		t.Fatalf("decision %+v", d)
	}
	// a pass with a view, recorded by the owner's own conversation in the desk chat after the run
	e.chats.finish("ok", 0.2)
	e.step()
	if r := e.runs()[0]; r.Decisions != 1 || r.Proposals != 1 {
		t.Fatalf("run counts %+v", r)
	}
	pass, err := e.s.Record(JournalInput{ChatID: 7, InstID: btc, Action: "watch", Side: "long", Conviction: "低", Thesis: "等回踩 98000 再看"})
	if err != nil || pass.RunID != 0 || pass.Trigger != "" || pass.Conviction != "low" {
		t.Fatalf("pass %+v %v", pass, err)
	}
	// prices afterwards: ENA falls 4% in the first hour, then recovers to -1% at 4 h and +2% at 24 h
	t0 := d.Ts / 60_000 * 60_000
	bar := func(inst string, min int, c float64) {
		e.mkt.bars[inst] = append(e.mkt.bars[inst], signals.Bar{T: t0 + int64(min)*60_000, H: c * 1.001, L: c * 0.999, C: c})
	}
	bar(ena, 30, 0.47) // the low: 6% in the short's favour
	bar(ena, 60, 0.48)
	bar(ena, 240, 0.495)
	bar(ena, 600, 0.53) // the high: 6% against
	bar(ena, 1440, 0.51)
	bar(btc, 60, 101000)
	e.add(30 * time.Minute)
	e.s.follow()
	if d, _ = e.s.Decision(id); d.Px1h != nil {
		t.Fatal("the 1 h outcome was filled before it was due")
	}
	e.add(35 * time.Minute)
	e.s.follow()
	d, _ = e.s.Decision(id)
	if d.Px1h == nil || *d.Px1h != 0.48 || *d.Ret1h != 4 || d.Px4h != nil || d.OutcomeAt != 0 {
		t.Fatalf("after 1 h: %+v", d)
	}
	if p, _ := e.s.Decision(pass.ID); p.Ret1h == nil || *p.Ret1h != 1 {
		t.Fatalf("the pass (long view, BTC +1%%): %+v", p.Ret1h)
	}
	// the owner approved; the position later closed at a loss
	e.trades.put(realtrade.Proposal{ID: 5, InstID: ena, Kind: realtrade.KindOpen, Side: "short", Status: realtrade.StatusExecuted})
	e.s.follow()
	if d, _ = e.s.Decision(id); d.ProposalStatus != realtrade.StatusExecuted || d.ClosedAt != 0 {
		t.Fatalf("after approval: %+v", d)
	}
	if len(e.s.reviewsDue(5)) != 0 {
		t.Fatal("a review is due before any outcome")
	}
	e.add(24 * time.Hour)
	closed := e.clock().UnixMilli()
	e.trades.put(realtrade.Proposal{ID: 5, InstID: ena, Kind: realtrade.KindOpen, Side: "short", Status: realtrade.StatusExecuted, ClosedAt: closed, CloseKind: "sl", PnlUSDT: -12.5})
	e.s.follow()
	d, _ = e.s.Decision(id)
	if *d.Ret4h != 1 || *d.Ret24h != -2 || d.OutcomeAt == 0 || d.ClosedAt != closed || d.CloseKind != "sl" || d.PnlUSDT != -12.5 {
		t.Fatalf("after 24 h: %+v", d)
	}
	if *d.MFE24h < 6 || *d.MFE24h > 6.2 || *d.MAE24h > -6 || *d.MAE24h < -6.2 {
		t.Fatalf("excursions %+v / %+v (want about +6 / -6)", *d.MFE24h, *d.MAE24h)
	}
	line := d.line()
	for _, want := range []string{"ENA 开仓空（中） @0.5", "1h +4.0%", "4h +1.0%", "24h -2.0%", "提议 #5 executed，已平仓 -12.50 USDT"} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q: %s", want, line)
		}
	}
	// the review is due; the pass on BTC (+1% only) is not worth one
	due := e.s.reviewsDue(5)
	if len(due) != 1 || due[0].ID != id {
		t.Fatalf("reviews due %+v", due)
	}
	if _, err := e.s.ReviewTool(ctx, 7, map[string]string{"decisionId": fmt.Sprint(id), "verdict": "great", "review": "方向对了但止损太近"}); err == nil {
		t.Fatal("unknown verdict accepted")
	}
	if _, err := e.s.ReviewTool(ctx, 7, map[string]string{"decisionId": fmt.Sprint(id), "verdict": "mixed", "review": "方向对了一小时，止损放在震荡区间内被扫"}); err != nil {
		t.Fatal(err)
	}
	if len(e.s.reviewsDue(5)) != 0 {
		t.Fatal("a reviewed decision is still due")
	}
	// memory: add, rewrite, retire; the brief carries the active ones, rules first
	m, err := e.s.RememberTool(ctx, 7, map[string]string{"kind": "lesson", "text": "急涨后的逆势空，止损要放在区间外", "decisionId": fmt.Sprint(id)})
	if err != nil {
		t.Fatal(err)
	}
	mid := m.(map[string]any)["memoryId"].(int64)
	if _, err := e.s.Remember("rule", "", "单笔保证金不超过 50 USDT", "owner", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Remember("coin", "", "没有指定币", "ai", 0, 0); err == nil {
		t.Fatal("a coin memory without a coin was accepted")
	}
	if _, err := e.s.RememberTool(ctx, 7, map[string]string{"kind": "lesson", "text": "急涨后的逆势空：止损放区间外，仓位减半", "replaces": fmt.Sprint(mid)}); err != nil {
		t.Fatal(err)
	}
	if ms := e.s.memoryFor(nil, 10); len(ms) != 2 || ms[0].Kind != "rule" || ms[1].Text != "急涨后的逆势空：止损放区间外，仓位减半" || ms[1].SourceDecision != id {
		t.Fatalf("memory %+v", ms)
	}
	// the next brief shows the journal, the memory and nothing to review
	e.set(at(15, 50).Add(24 * time.Hour))
	e.step()
	msg := e.chats.last()
	for _, want := range []string{"【最近决策】", "ENA 开仓空（中）", "→ 部分正确", "BTC 观望等待多（低）", "【长期记忆】", "[规则 #2] 单笔保证金不超过 50 USDT", "[教训 #1] 急涨后的逆势空：止损放区间外，仓位减半"} {
		if !strings.Contains(msg, want) {
			t.Errorf("brief lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "【待复盘】结果已出") {
		t.Errorf("nothing is due for review:\n%s", msg)
	}
	if _, err := e.s.RememberTool(ctx, 7, map[string]string{"retire": fmt.Sprint(mid), "reason": "已并入规则"}); err != nil {
		t.Fatal(err)
	}
	if len(e.s.Memory(MemoryActive)) != 1 || len(e.s.Memory("all")) != 2 {
		t.Fatalf("memory after retiring: %+v", e.s.Memory("all"))
	}
	// statistics
	st, err := e.s.Stats(30)
	if err != nil {
		t.Fatal(err)
	}
	if st.Decisions != 2 || st.Runs != 2 || st.Directional.N != 2 || st.Directional.Followed != 1 || *st.Directional.WinRate4h != 1 ||
		st.Proposals.Made != 1 || st.Proposals.Executed != 1 || st.Proposals.Closed != 1 || st.Proposals.Wins != 0 || st.Proposals.PnlUSDT != -12.5 ||
		st.Verdicts["mixed"] != 1 || st.ByStrategy[0].N != 1 || len(st.ByAction) != 2 {
		t.Fatalf("stats %+v", st)
	}
	// desk_recall reads it from any conversation
	rec, err := e.s.RecallTool(ctx, 99, map[string]string{"what": "journal", "action": "trades"})
	if err != nil || len(rec.(map[string]any)["decisions"].([]map[string]any)) != 1 {
		t.Fatalf("recall %+v %v", rec, err)
	}
	if _, err := e.s.RecallTool(ctx, 99, map[string]string{"what": "nope"}); err == nil {
		t.Fatal("unknown recall accepted")
	}
}

// A decision with no price to measure from stays unknown instead of being scored against zero.
func TestFollowWithoutPrice(t *testing.T) {
	e := newEnv(t, at(14, 0))
	e.enable(nil)
	delete(e.mkt.px, ena)
	d, err := e.s.Record(JournalInput{ChatID: 7, InstID: ena, Action: "skip", Thesis: "没有报价，先不看"})
	if err != nil || d.Px != 0 {
		t.Fatalf("%+v %v", d, err)
	}
	e.add(2 * time.Hour)
	e.s.follow()
	if d, _ = e.s.Decision(d.ID); d.Ret1h != nil || d.OutcomeAt == 0 {
		t.Fatalf("a decision without a price was scored: %+v", d)
	}
}

func TestRestartClosesInterruptedRun(t *testing.T) {
	e := newEnv(t, at(13, 50))
	e.enable(nil)
	e.step()
	s2, err := New(e.s.st, e.chats, e.scans, e.trades, e.mkt, Options{Now: e.clock, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Config().Enabled {
		t.Fatal("the configuration did not survive a restart")
	}
	rs, _ := s2.Runs(10, 0)
	if len(rs) != 1 || rs[0].Status != RunFailed || !strings.Contains(rs[0].Error, "重启") {
		t.Fatalf("interrupted run %+v", rs)
	}
}
