package signals

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/store"
)

// fake is the market, plans and alert rules, and the agent.
type fake struct {
	mu     sync.Mutex
	now    time.Time
	px     map[string]float64
	bars   map[string]Bar // instId|bar → last confirmed bar
	vals   map[string]map[string]float64
	risk   map[string]MoveRisk
	plans  map[int64]Plan
	kept   []string
	busy   bool
	woken  []string // "chat:message"
	starts []string
	chats  map[int64]string
	notes  []string
}

func (f *fake) Known(id string) bool {
	return strings.HasSuffix(id, "-USDT-SWAP") && id != "NOPE-USDT-SWAP"
}
func (f *fake) Quote(id string) (float64, int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.px[id]
	return v, f.now.UnixMilli(), ok
}
func (f *fake) Closed(id, bar string) (Bar, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bars[id+"|"+bar]
	return b, ok
}
func (f *fake) Values(id string) (map[string]float64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vals[id]
	return v, ok
}
func (f *fake) MoveRisk(id string) (MoveRisk, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.risk[id]
	return r, ok
}
func (f *fake) Plan(id int64) (Plan, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.plans[id]
	return p, ok
}
func (f *fake) AlertRule(id int64) (string, bool) { return "放量突破", id == 7 }
func (f *fake) Keep(ids []string)                 { f.mu.Lock(); f.kept = ids; f.mu.Unlock() }

func (f *fake) Wake(chat int64, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.chats[chat]; !ok {
		return errors.New("chat not found")
	}
	if f.busy {
		return ErrBusy
	}
	f.woken = append(f.woken, msg)
	return nil
}
func (f *fake) Start(title, msg, inst string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, title)
	return 99, nil
}
func (f *fake) ChatTitle(chat int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.chats[chat]
	return t, ok
}

func (f *fake) set(inst string, px float64) { f.mu.Lock(); f.px[inst] = px; f.mu.Unlock() }
func (f *fake) advance(d time.Duration)     { f.mu.Lock(); f.now = f.now.Add(d); f.mu.Unlock() }
func (f *fake) clock() time.Time            { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fake) wokenN() int                 { f.mu.Lock(); defer f.mu.Unlock(); return len(f.woken) }

const aave = "AAVE-USDT-SWAP"

func newEnv(t *testing.T, opts Options) (*Service, *fake, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	f := &fake{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), px: map[string]float64{aave: 139}, bars: map[string]Bar{},
		vals: map[string]map[string]float64{}, risk: map[string]MoveRisk{}, plans: map[int64]Plan{}, chats: map[int64]string{12: "AAVE 分析", 13: "别的对话"}}
	opts.Now, opts.Logf = f.clock, t.Logf
	s, err := New(st, f, opts)
	if err != nil {
		t.Fatal(err)
	}
	s.SetWaker(f)
	s.SetNotifier(func(text, link string) { f.mu.Lock(); f.notes = append(f.notes, text+"|"+link); f.mu.Unlock() })
	return s, f, st
}

func spec(t *testing.T, raw string) Spec {
	t.Helper()
	s, err := ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sub(t *testing.T, s *Service, chat int64, raw string, mod ...func(*SubscribeInput)) Subscription {
	t.Helper()
	in := SubscribeInput{ChatID: chat, Spec: spec(t, raw), Intent: "回踩入场区做多，止损 136.8，止盈 144 / 148"}
	for _, m := range mod {
		m(&in)
	}
	x, err := s.Subscribe(in)
	if err != nil {
		t.Fatalf("subscribe %s: %v", raw, err)
	}
	return x
}

func status(t *testing.T, s *Service, id int64) Subscription {
	t.Helper()
	x, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestSpecValidationAndChinese(t *testing.T) {
	s, f, _ := newEnv(t, Options{})
	f.vals[aave] = map[string]float64{"funding_hourly": -0.00002, "oi_chg_1h": 0.01}
	f.bars[aave+"|1H"] = Bar{T: 0, C: 139}
	f.risk[aave] = MoveRisk{Top: MoveSide{Extended: true, PTail: 0.43}}
	f.plans[34] = Plan{ID: 34, InstID: aave, Side: "long", Status: "pending"}
	cases := map[string]string{
		`{"type":"price","instId":"aave","op":"above","level":140.2,"bar":"1H"}`:                 "AAVE 1H 收盘站上 140.2",
		`{"type":"price","instId":"AAVE-USDT-SWAP","op":"below","level":136.8}`:                  "AAVE 价格跌破 136.8",
		`{"type":"zone","instId":"AAVE","lo":140.2,"hi":139.5}`:                                  "AAVE 进入 139.5–140.2",
		`{"type":"field","instId":"AAVE","field":"funding_hourly","op":">","value":0}`:           "AAVE 资金费转正",
		`{"type":"field","instId":"AAVE","field":"oi_chg_1h","op":">=","value":0.05}`:            "AAVE OI 变化 1h ≥ +5.13%",
		`{"type":"moverisk","instId":"AAVE","side":"top","metric":"pTail","op":"<","value":0.3}`: "AAVE 上涨端 P_tail < 30%",
		`{"type":"moverisk","instId":"AAVE","side":"top","metric":"extended","value":0}`:         "AAVE 上涨端离开超伸状态",
		`{"type":"alert","ruleId":7,"instId":"AAVE"}`:                                            "告警规则 #7 在 AAVE 触发",
		`{"type":"time","at":"2026-11-02 10:00"}`:                                                "到 11-02 10:00",
		`{"type":"plan","planId":34,"state":"active"}`:                                           "计划 #34 触及入场区",
		`{"type":"all","of":[{"type":"price","instId":"AAVE","op":"above","level":140.2,"bar":"15m"},{"type":"field","instId":"AAVE","field":"funding_hourly","op":">","value":0}]}`: "AAVE 15m 收盘站上 140.2 且 AAVE 资金费转正",
	}
	for raw, want := range cases {
		sp := spec(t, raw)
		if err := sp.normalize(s.src, f.now, 0); err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if got := sp.Describe(); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
	bad := map[string]string{
		`{"type":"price","instId":"NOPE","op":"above","level":1}`:                               "unknown instrument",
		`{"type":"price","instId":"AAVE","op":"over","level":141}`:                              "op must be",
		`{"type":"price","instId":"AAVE","op":"above","level":300}`:                             "50%",
		`{"type":"price","instId":"AAVE","op":"above","level":141,"bar":"2H"}`:                  "bar must be",
		`{"type":"zone","instId":"AAVE","lo":100,"hi":140}`:                                     "wider than 20%",
		`{"type":"field","instId":"AAVE","field":"funding","op":">","value":0}`:                 "unknown field",
		`{"type":"field","instId":"AAVE","field":"funding_hourly","op":">"}`:                    "value is required",
		`{"type":"moverisk","instId":"AAVE","side":"top","metric":"pTail","op":"<","value":40}`: "probability",
		`{"type":"alert","ruleId":8}`:                                                           "unknown alert rule",
		`{"type":"time","at":"2026-10-01 10:00"}`:                                               "already past",
		`{"type":"plan","planId":35,"state":"active"}`:                                          "unknown plan",
		`{"type":"all","of":[{"type":"time","afterMin":5}]}`:                                    "at least two",
		`{"type":"any","of":[{"type":"all","of":[{"type":"all","of":[{"type":"time","afterMin":5},{"type":"time","afterMin":6}]},{"type":"time","afterMin":7}]},{"type":"time","afterMin":8}]}`: "nest at most",
		`{"type":"vibes"}`: "unknown signal type",
	}
	for raw, want := range bad {
		sp := spec(t, raw)
		if err := sp.normalize(s.src, f.now, 0); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", raw, err, want)
		}
	}
	if _, err := ParseSpec(`{"type":"price","instId":"AAVE","price":140}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown key accepted: %v", err)
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"price","instId":"AAVE","op":"above","level":140}`), Intent: "x"}); err == nil {
		t.Error("empty intent accepted")
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"price","instId":"AAVE","op":"above","level":140}`), Intent: "等站上再说再说", ExpiresHours: 200}); err == nil {
		t.Error("8-day expiry accepted")
	}
}

// A level crossing fires once on the edge, wakes the subscribing chat with the signal, then the
// subscription is done; an already-true condition is refused.
func TestPriceCrossWakesChatOnce(t *testing.T) {
	s, f, _ := newEnv(t, Options{PublicURL: "https://ctg.example"})
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"price","instId":"AAVE","op":"below","level":140}`), Intent: "跌破就不做了吧"}); err == nil ||
		!strings.Contains(err.Error(), "already holds") {
		t.Fatalf("already-true condition: %v", err)
	}
	x := sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140.2}`)
	if x.Desc != "AAVE 价格上破 140.2" || x.Subscriber != "AI 对话 #12 · AAVE 分析" || len(f.kept) != 1 || x.Progress == nil || !strings.Contains(x.Progress.Text, "还差 0.86%") {
		t.Fatalf("subscription %+v kept %v progress %+v", x, f.kept, x.Progress)
	}
	f.set(aave, 139.9)
	s.evaluate(event{kind: "tick"})
	if f.wokenN() != 0 {
		t.Fatal("fired below the level")
	}
	f.set(aave, 140.3)
	s.evaluate(event{kind: "tick"})
	s.evaluate(event{kind: "tick"})
	f.set(aave, 139)
	s.evaluate(event{kind: "tick"})
	f.set(aave, 141)
	s.evaluate(event{kind: "tick"})
	if f.wokenN() != 1 {
		t.Fatalf("woken %d times", f.wokenN())
	}
	msg := f.woken[0]
	for _, want := range []string{"⚡ 信号 #1 触发：AAVE 价格上破 140.2", "现价 140.3", "你订阅时的计划：回踩入场区做多", "propose_trade", "这是自动触发的信号会话"} {
		if !strings.Contains(msg, want) {
			t.Errorf("wake message lacks %q:\n%s", want, msg)
		}
	}
	got := status(t, s, x.ID)
	if got.Status != StatusFired || got.FireCount != 1 || len(got.History) != 1 || got.History[0].Wake != WakeStarted || got.History[0].WakeChat != 12 {
		t.Fatalf("after fire %+v", got)
	}
	if len(f.notes) != 1 || !strings.Contains(f.notes[0], "AI 正在评估|https://ctg.example/go/chat/12") || len(f.kept) != 0 {
		t.Fatalf("notes %v kept %v", f.notes, f.kept)
	}
	if id, ok := s.WokenBy(12); !ok || id != x.ID {
		t.Fatalf("woken by %d %v", id, ok)
	}
}

func TestEachSignalTypeFires(t *testing.T) {
	type step struct {
		do func(f *fake)
		ev event
	}
	cases := []struct {
		name  string
		setup func(f *fake)
		raw   string
		steps []step
	}{
		{"1H close", func(f *fake) { f.bars[aave+"|1H"] = Bar{T: 0, C: 139} }, `{"type":"price","instId":"AAVE","op":"above","level":140.2,"bar":"1H"}`, []step{
			{func(f *fake) { f.set(aave, 141) }, event{kind: "tick"}}, // the last price alone does not count
			{func(f *fake) { f.bars[aave+"|1H"] = Bar{T: 3_600_000, C: 140.5} }, event{kind: "minute", minute: 2*3_600_000 - 60_000}},
		}},
		{"zone wick", nil, `{"type":"zone","instId":"AAVE","lo":137,"hi":138}`, []step{
			{func(f *fake) { f.bars[aave+"|1m"] = Bar{T: 600_000, L: 137.8, H: 139.2, C: 139} }, event{kind: "minute", minute: 600_000}},
		}},
		{"funding turns positive", func(f *fake) { f.vals[aave] = map[string]float64{"funding_hourly": -0.00001} }, `{"type":"field","instId":"AAVE","field":"funding_hourly","op":">","value":0}`, []step{
			{func(f *fake) { f.vals[aave] = map[string]float64{"funding_hourly": 0.00002} }, event{kind: "tick"}}, // fields are read on minutes only
			{nil, event{kind: "minute", minute: 60_000}},
		}},
		{"pTail drops", func(f *fake) { f.risk[aave] = MoveRisk{T: 0, Top: MoveSide{PTail: 0.43}} }, `{"type":"moverisk","instId":"AAVE","side":"top","metric":"pTail","op":"<","value":0.3}`, []step{
			{func(f *fake) { f.risk[aave] = MoveRisk{T: 3_600_000, Top: MoveSide{PTail: 0.25}} }, event{kind: "minute", minute: 2*3_600_000 - 60_000}},
		}},
		{"alert with price", nil, `{"type":"all","of":[{"type":"alert","ruleId":7},{"type":"price","instId":"AAVE","op":"above","level":138}]}`, []step{
			{nil, event{kind: "alert", alert: &AlertHit{RuleID: 7, InstID: aave, Rule: "放量突破", Message: "4h 放量 3.1×"}}},
		}},
		{"time", nil, `{"type":"time","afterMin":30}`, []step{
			{func(f *fake) { f.advance(31 * time.Minute) }, event{kind: "tick"}},
		}},
		{"plan entry touched", func(f *fake) { f.plans[34] = Plan{ID: 34, InstID: aave, Side: "long", Status: "pending"} }, `{"type":"plan","planId":34,"state":"active"}`, []step{
			{func(f *fake) { f.plans[34] = Plan{ID: 34, InstID: aave, Side: "long", Status: "tp1", EnteredAt: 1} }, event{kind: "minute", minute: 60_000}},
		}},
	}
	for _, c := range cases {
		s, f, _ := newEnv(t, Options{})
		if c.setup != nil {
			c.setup(f)
		}
		x := sub(t, s, 12, c.raw)
		for i, st := range c.steps {
			if st.do != nil {
				st.do(f)
			}
			s.evaluate(st.ev)
			if want := i == len(c.steps)-1; (f.wokenN() == 1) != want {
				t.Fatalf("%s step %d: woken %d", c.name, i, f.wokenN())
			}
		}
		if got := status(t, s, x.ID); got.Status != StatusFired || got.History[0].Matched == "" {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestMaxFiresCooldownExpiry(t *testing.T) {
	s, f, _ := newEnv(t, Options{})
	x := sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140}`, func(in *SubscribeInput) { in.MaxFires, in.CooldownMin = 2, 30 })
	cross := func() {
		f.set(aave, 139)
		s.evaluate(event{kind: "tick"})
		f.set(aave, 141)
		s.evaluate(event{kind: "tick"})
	}
	cross()
	f.advance(10 * time.Minute)
	cross() // within the cooldown
	if f.wokenN() != 1 || status(t, s, x.ID).Status != StatusActive {
		t.Fatalf("cooldown: woken %d", f.wokenN())
	}
	f.advance(25 * time.Minute)
	cross()
	if got := status(t, s, x.ID); f.wokenN() != 2 || got.Status != StatusFired || got.FireCount != 2 {
		t.Fatalf("second fire: woken %d %+v", f.wokenN(), got)
	}
	if !strings.Contains(f.woken[0], "第 1/2 次触发；订阅仍有效") || !strings.Contains(f.woken[1], "第 2/2 次触发；订阅已结束") {
		t.Fatalf("messages %q", f.woken)
	}

	y := sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"below","level":130}`, func(in *SubscribeInput) { in.ExpiresHours = 1 })
	f.advance(61 * time.Minute)
	f.set(aave, 129)
	s.evaluate(event{kind: "tick"})
	s.expireDue()
	if got := status(t, s, y.ID); got.Status != StatusExpired || got.FireCount != 0 || f.wokenN() != 2 {
		t.Fatalf("expiry %+v", got)
	}
}

func TestCancelOwnership(t *testing.T) {
	s, _, _ := newEnv(t, Options{})
	x := sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140}`)
	if _, err := s.CancelFor(13, x.ID, "不要了", false); !errors.Is(err, ErrNotYours) {
		t.Fatalf("another chat cancelled it: %v", err)
	}
	o := sub(t, s, 0, `{"type":"price","instId":"AAVE","op":"above","level":145}`)
	if _, err := s.CancelFor(12, o.ID, "不要了", false); !errors.Is(err, ErrNotYours) {
		t.Fatalf("the agent cancelled the owner's: %v", err)
	}
	if got, err := s.CancelFor(13, x.ID, "主人说不用等了", true); err != nil || got.Status != StatusCancelled || got.EndedBy != "chat:13（主人要求）" {
		t.Fatalf("owner-asked cancel %+v %v", got, err)
	}
	y := sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":141}`)
	if got, err := s.CancelFor(12, y.ID, "结构破坏", false); err != nil || got.Reason != "结构破坏" {
		t.Fatalf("own cancel %+v %v", got, err)
	}
	if _, err := s.Cancel(y.ID, "owner", "x"); !errors.Is(err, ErrEnded) {
		t.Fatalf("double cancel %v", err)
	}
	if list, _ := s.List(ListQuery{Status: "ended"}); len(list) != 2 {
		t.Fatalf("ended list %d", len(list))
	}
}

// A busy chat queues the wake-up until its turn ends; the daily cap records but wakes nobody.
func TestWakeQueueAndDailyCap(t *testing.T) {
	s, f, _ := newEnv(t, Options{WakesPerDay: 1})
	sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140}`)
	f.busy = true
	f.set(aave, 141)
	s.evaluate(event{kind: "tick"})
	fs, _ := s.Get(1)
	if f.wokenN() != 0 || fs.History[0].Wake != WakeQueued || !strings.Contains(f.notes[0], "对话正忙") {
		t.Fatalf("busy: %+v %v", fs.History, f.notes)
	}
	s.processQueue() // still busy
	f.busy = false
	s.ChatDone(12)
	<-s.wakes
	s.processQueue()
	if fs, _ = s.Get(1); f.wokenN() != 1 || fs.History[0].Wake != WakeStarted {
		t.Fatalf("after the turn: %+v", fs.History)
	}

	f.set(aave, 139)
	sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140}`)
	f.set(aave, 141)
	s.evaluate(event{kind: "tick"})
	if fs, _ = s.Get(2); f.wokenN() != 1 || fs.History[0].Wake != WakeSkipped || !strings.Contains(fs.History[0].Error, "每日唤醒上限") {
		t.Fatalf("cap: %+v", fs.History)
	}

	// a queued wake-up gives up after two hours
	f.set(aave, 139)
	s.opts.WakesPerDay = 10
	sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140}`)
	f.busy = true
	f.set(aave, 141)
	s.evaluate(event{kind: "tick"})
	f.advance(3 * time.Hour)
	s.processQueue()
	if fs, _ = s.Get(3); fs.History[0].Wake != WakeFailed {
		t.Fatalf("queue timeout: %+v", fs.History)
	}
}

func TestOwnerSubscriptionStartsChatAndCaps(t *testing.T) {
	s, f, _ := newEnv(t, Options{MaxActive: 6})
	x := sub(t, s, 0, `{"type":"zone","instId":"AAVE","lo":136,"hi":137}`)
	if x.Kind != KindOwner || x.Subscriber != "主人" {
		t.Fatalf("owner sub %+v", x)
	}
	f.set(aave, 136.5)
	s.evaluate(event{kind: "tick"})
	if got := status(t, s, x.ID); len(f.starts) != 1 || !strings.HasPrefix(f.starts[0], "信号 · AAVE 进入 136–137") || got.History[0].WakeChat != 99 {
		t.Fatalf("owner fire: %v %+v", f.starts, got.History)
	}
	for i := range MaxPerChat {
		sub(t, s, 12, `{"type":"time","afterMin":`+string(rune('1'+i))+`0}`)
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"time","afterMin":90}`), Intent: "再看一眼结构"}); err == nil || !strings.Contains(err.Error(), "already has 5") {
		t.Fatalf("per-chat cap: %v", err)
	}
	sub(t, s, 13, `{"type":"time","afterMin":90}`)
	if _, err := s.Subscribe(SubscribeInput{ChatID: 13, Spec: spec(t, `{"type":"time","afterMin":95}`), Intent: "再看一眼结构"}); err == nil || !strings.Contains(err.Error(), "cap is 6") {
		t.Fatalf("total cap: %v", err)
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 77, Spec: spec(t, `{"type":"time","afterMin":90}`), Intent: "再看一眼结构"}); err == nil {
		t.Fatal("unknown chat accepted")
	}
}

// A restart re-reads the stored state: a condition that turned true while the server was down fires once.
func TestRestartCatchUp(t *testing.T) {
	s, f, st := newEnv(t, Options{})
	sub(t, s, 12, `{"type":"price","instId":"AAVE","op":"above","level":140}`)
	f.set(aave, 141)
	s2, err := New(st, f, Options{Now: f.clock, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	s2.SetWaker(f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s2.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for f.wokenN() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if f.wokenN() != 1 {
		t.Fatalf("catch-up woken %d", f.wokenN())
	}
}

func TestTools(t *testing.T) {
	s, _, _ := newEnv(t, Options{})
	ctx := context.Background()
	if _, err := s.SubscribeTool(ctx, 0, map[string]string{"spec": `{"type":"time","afterMin":60}`, "intent": "一小时后复查"}); err == nil {
		t.Fatal("subscribe without a chat")
	}
	out, err := s.SubscribeTool(ctx, 12, map[string]string{"spec": `{"type":"price","instId":"AAVE","op":"above","level":140.2,"bar":"1H"}`,
		"intent": "1H 收盘站上 140.2 且净买就追多", "expiresHours": "48"})
	if err == nil {
		t.Fatalf("1H close without bars accepted: %v", out)
	}
	out, err = s.SubscribeTool(ctx, 12, map[string]string{"spec": `{"type":"price","instId":"AAVE","op":"above","level":140.2}`, "intent": "站上 140.2 再评估追多", "expiresHours": "48"})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.(map[string]any); m["signalId"] != int64(1) || m["condition"] != "AAVE 价格上破 140.2" || !strings.Contains(m["note"].(string), "never that it will trade") {
		t.Fatalf("subscribe result %v", m)
	}
	list, _ := s.ListTool(ctx, 13, map[string]string{"scope": "mine"})
	if n := len(list.(map[string]any)["signals"].([]map[string]any)); n != 0 {
		t.Fatalf("chat 13 sees %d of its own", n)
	}
	list, _ = s.ListTool(ctx, 13, map[string]string{"scope": "all"})
	if rows := list.(map[string]any)["signals"].([]map[string]any); len(rows) != 1 || rows[0]["mine"] != false {
		t.Fatalf("all: %v", rows)
	}
	if _, err := s.UnsubscribeTool(ctx, 13, map[string]string{"id": "1", "reason": "不要了"}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("unsubscribe by another chat: %v", err)
	}
	if _, err := s.UnsubscribeTool(ctx, 12, map[string]string{"id": "1"}); err == nil {
		t.Fatal("unsubscribe without a reason")
	}
	if r, err := s.UnsubscribeTool(ctx, 12, map[string]string{"id": "1", "reason": "资金费继续转负，放弃"}); err != nil || r.(map[string]any)["status"] != StatusCancelled {
		t.Fatalf("unsubscribe %v %v", r, err)
	}
}

// A plan written in the same answer as the subscription is linked to it and quoted on wake-up.
func TestLinkPlan(t *testing.T) {
	s, f, _ := newEnv(t, Options{})
	x := sub(t, s, 12, `{"type":"zone","instId":"AAVE","lo":137,"hi":138}`)
	other := sub(t, s, 13, `{"type":"zone","instId":"AAVE","lo":136,"hi":137}`)
	f.plans[34] = Plan{ID: 34, InstID: aave, Side: "long", EntryLo: 137, EntryHi: 138, Stop: 135.5, TP1: 142, TP2: 146, Invalidate: "跌破 135 且 OI 增", Status: "pending"}
	s.LinkPlan(12, aave, 34)
	if status(t, s, x.ID).PlanID != 34 || status(t, s, other.ID).PlanID != 0 {
		t.Fatal("plan linked to the wrong subscriptions")
	}
	f.set(aave, 137.5)
	s.evaluate(event{kind: "tick"})
	if f.wokenN() != 1 || !strings.Contains(f.woken[0], "关联计划 #34：做多 AAVE · 入场 137–138 · 止损 135.5 · 止盈 142 / 146 · 失效：跌破 135 且 OI 增") {
		t.Fatalf("wake %q", f.woken)
	}
}

func TestHTTP(t *testing.T) {
	s, _, _ := newEnv(t, Options{})
	mux := http.NewServeMux()
	s.Register(mux)
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	code, out := do("POST", "/api/v1/signals", `{"spec":{"type":"zone","instId":"AAVE","lo":136,"hi":137},"intent":"回踩 136–137 提醒我看多","expiresHours":12}`)
	if code != 200 || !strings.Contains(out, `"kind":"owner"`) || !strings.Contains(out, `"desc":"AAVE 进入 136–137"`) || !strings.Contains(out, `"levels":[`) {
		t.Fatalf("create %d %s", code, out)
	}
	if code, out = do("POST", "/api/v1/signals", `{"spec":{"type":"zone","instId":"AAVE","lo":136,"hi":137,"x":1},"intent":"回踩提醒我看多"}`); code != 400 {
		t.Fatalf("unknown key %d %s", code, out)
	}
	if code, out = do("GET", "/api/v1/signals?status=active", ""); code != 200 || !strings.Contains(out, `"progress":{`) || !strings.Contains(out, `"maxActive":30`) {
		t.Fatalf("list %d %s", code, out)
	}
	if code, _ = do("GET", "/api/v1/signals?status=bogus", ""); code != 400 {
		t.Fatalf("bad status %d", code)
	}
	if code, out = do("POST", "/api/v1/signals/1/cancel", `{"reason":"不等了"}`); code != 200 || !strings.Contains(out, `"status":"cancelled"`) || !strings.Contains(out, `"endedBy":"owner"`) {
		t.Fatalf("cancel %d %s", code, out)
	}
	if code, _ = do("POST", "/api/v1/signals/1/cancel", ``); code != 409 {
		t.Fatalf("cancel again %d", code)
	}
	if code, out = do("GET", "/api/v1/signals/1", ""); code != 200 || !strings.Contains(out, `"reason":"不等了"`) {
		t.Fatalf("get %d %s", code, out)
	}
	if code, _ = do("GET", "/api/v1/signals/9", ""); code != 404 {
		t.Fatalf("missing %d", code)
	}
	if code, out = do("GET", "/go/chat/12", ""); code != 200 || !strings.Contains(out, "okxhelper://ai?chat=12") {
		t.Fatalf("bounce %d %s", code, out)
	}
}

// Two alerts in the same minute make two edges (an alert is a moment, not a state).
func TestAlertIsAMoment(t *testing.T) {
	s, f, _ := newEnv(t, Options{})
	sub(t, s, 12, `{"type":"alert","ruleId":7}`, func(in *SubscribeInput) { in.MaxFires, in.CooldownMin = 2, 5 })
	hit := event{kind: "alert", alert: &AlertHit{RuleID: 7, InstID: aave, Rule: "放量突破", Message: "x"}}
	s.evaluate(hit)
	f.advance(6 * time.Minute)
	s.evaluate(event{kind: "alert", alert: &AlertHit{RuleID: 8, InstID: aave}})
	s.evaluate(hit)
	if f.wokenN() != 2 {
		t.Fatalf("woken %d", f.wokenN())
	}
}

func TestCheckInterval(t *testing.T) {
	s, f, _ := newEnv(t, Options{})
	for _, bad := range []string{"2m", "1d"} {
		if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"price","instId":"AAVE","op":"above","level":140.2}`), Intent: "上破再评估做多", Every: bad}); err == nil {
			t.Fatalf("every %q accepted", bad)
		}
	}
	if _, err := s.Subscribe(SubscribeInput{ChatID: 12, Intent: "上破再评估做多",
		Spec: spec(t, `{"type":"any","of":[{"type":"price","instId":"AAVE","op":"above","level":140.2,"every":"5m"},{"type":"time","afterMin":60}]}`)}); err == nil {
		t.Fatal("every inside a composite accepted")
	}
	x, err := s.Subscribe(SubscribeInput{ChatID: 12, Spec: spec(t, `{"type":"price","instId":"AAVE","op":"above","level":140.2}`), Intent: "上破再评估做多", Every: "5m"})
	if err != nil || x.Desc != "AAVE 价格上破 140.2（每 5 分钟检查）" {
		t.Fatalf("subscribe %+v %v", x, err)
	}
	f.set(aave, 141)
	s.evaluate(event{kind: "tick"}) // not a tick subscription
	m := time.Date(2026, 11, 1, 6, 1, 0, 0, time.UTC).UnixMilli() // the 06:01 bar closes at 06:02: not a 5-minute mark
	s.evaluate(event{kind: "minute", minute: m})
	if f.wokenN() != 0 {
		t.Fatal("checked outside its interval")
	}
	s.evaluate(event{kind: "minute", minute: m + 3*60_000}) // closes at 06:05
	if f.wokenN() != 1 {
		t.Fatalf("woken %d at the 5-minute check", f.wokenN())
	}
}
