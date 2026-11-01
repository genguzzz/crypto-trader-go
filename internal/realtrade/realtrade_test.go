package realtrade

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
	"crypto-trader-go/internal/store"
)

type env struct {
	s      *Service
	mu     sync.Mutex
	px     float64
	now    time.Time
	events []string
}

func (e *env) ProposalEvent(kind string, p Proposal) {
	e.mu.Lock()
	e.events = append(e.events, kind)
	e.mu.Unlock()
}

func (e *env) setPx(v float64) { e.mu.Lock(); e.px = v; e.mu.Unlock() }
func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}
func (e *env) clock() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }

func newEnv(t *testing.T, orders okxorder.Client) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	e := &env{px: 100000, now: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
	s, err := New(st, orders, Options{
		Quote: func(string) (float64, int64, bool) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.px, e.now.UnixMilli(), e.px > 0
		},
		Spec: func(id string) (okxorder.Spec, bool) {
			return okxorder.Spec{CtVal: 0.01, LotSz: 0.01, MinSz: 0.01, TickSz: 0.1, MaxLever: 100}, strings.HasSuffix(id, "-USDT-SWAP")
		},
		PublicURL: "https://ctg.example/",
		Now:       e.clock,
		Logf:      t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AddNotifier(e)
	e.s = s
	return e
}

func input() ProposeInput {
	return ProposeInput{ChatID: 7, InstID: "btc", Side: "long", MarginUSDT: 20, Leverage: 5, SL: 98000, TP: 104000,
		Rationale: "breakout above the range with rising OI", Invalidation: "back below 99k"}
}

func TestProposeApproveDryRun(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusPending || p.InstID != "BTC-USDT-SWAP" || p.Source != SourceChat || p.Mode != "dryrun" || p.Contracts != 0.1 ||
		p.RR < 1.99 || p.ExpiresAt != e.clock().Add(15*time.Minute).UnixMilli() {
		t.Fatalf("proposal %+v", p)
	}
	if link := e.s.Link(p); link != "https://ctg.example/go/proposal/1" {
		t.Fatalf("link %s", link)
	}
	e.setPx(100500) // 0.5% drift: within the 1% cap
	got, err := e.s.Approve(context.Background(), p.ID, "telegram:1", "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusExecuted || got.Mode != "dryrun" || got.ExecPx != 100500 || got.OrdID != "" || got.Via != "telegram" {
		t.Fatalf("executed %+v", got)
	}
	var reqs []okxorder.Request
	if err := json.Unmarshal(got.Request, &reqs); err != nil || len(reqs) != 2 || !strings.Contains(string(reqs[1].Body), `"clOrdId":"ctgp1"`) {
		t.Fatalf("stored request %s %v", got.Request, err)
	}
	if strings.Join(e.events, ",") != "proposed,approved,executed" {
		t.Fatalf("events %v", e.events)
	}
	full, _ := e.s.Get(p.ID)
	if len(full.Events) != 3 || full.Events[2].Kind != StatusExecuted {
		t.Fatalf("audit %+v", full.Events)
	}
	// dry run follows the price: TP closes it with an estimated P&L (0.09 contracts, re-sized at 100500, × 3500 × 0.01)
	e.setPx(104100)
	e.s.follow(context.Background())
	closed, _ := e.s.get(p.ID)
	if closed.ClosedAt == 0 || closed.CloseKind != "tp" || closed.PnlUSDT < 3.14 || closed.PnlUSDT > 3.16 {
		t.Fatalf("closed %+v", closed)
	}
}

func TestFirstAnswerWins(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var approved, rejected, closed atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = e.s.Approve(context.Background(), p.ID, "owner", "web")
				if err == nil {
					approved.Add(1)
				}
			} else {
				_, err = e.s.Reject(p.ID, "owner", "app")
				if err == nil {
					rejected.Add(1)
				}
			}
			if errors.Is(err, ErrClosed) {
				closed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if approved.Load()+rejected.Load() != 1 || closed.Load() != 19 {
		t.Fatalf("approved %d rejected %d closed %d", approved.Load(), rejected.Load(), closed.Load())
	}
	// a late answer gets the final state
	got, err := e.s.Reject(p.ID, "telegram:1", "telegram")
	if !errors.Is(err, ErrClosed) || (got.Status != StatusExecuted && got.Status != StatusRejected) {
		t.Fatalf("late answer: %+v %v", got, err)
	}
}

func TestExpiryNeverApproves(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	in := input()
	in.ExpiresMin = 5
	p, _ := e.s.Propose(context.Background(), in)
	e.advance(5*time.Minute + time.Second)
	got, err := e.s.Approve(context.Background(), p.ID, "owner", "web")
	if !errors.Is(err, ErrClosed) || got.Status != StatusExpired || got.Via != "timeout" {
		t.Fatalf("approve after expiry: %+v %v", got, err)
	}
	q, _ := e.s.Propose(context.Background(), func() ProposeInput { i := input(); i.InstID = "ETH-USDT-SWAP"; i.SL, i.TP = 98000, 104000; return i }())
	e.advance(16 * time.Minute)
	e.s.expireDue()
	if got, _ := e.s.get(q.ID); got.Status != StatusExpired {
		t.Fatalf("sweep: %+v", got)
	}
	if strings.Contains(strings.Join(e.events, ","), "executed") {
		t.Fatalf("expiry executed something: %v", e.events)
	}
}

func TestRevalidation(t *testing.T) {
	cases := []struct {
		name string
		prep func(e *env)
		want string
	}{
		{"drift", func(e *env) { e.setPx(101500) }, "price moved"},
		{"stop crossed", func(e *env) { e.setPx(97900) }, "price moved"},
		{"stale price", func(e *env) {
			e.s.opts.Quote = func(string) (float64, int64, bool) { return 100000, e.clock().Add(-5 * time.Minute).UnixMilli(), true }
		}, "old"},
		{"conflicting position", func(e *env) {
			e.s.SetAccount(fakeAcct{pos: []okxacct.Position{{InstID: "BTC-USDT-SWAP", Side: "short"}}})
		}, "already holds"},
		{"daily loss", func(e *env) {
			e.s.db.Exec(`INSERT INTO trade_proposals(created, updated, source, inst_id, side, margin, leverage, ref_px, sl, tp, expires_at, status, status_at, mode, closed_at, pnl)
				VALUES(1,1,'chat','SOL-USDT-SWAP','long',10,2,1,1,1,1,'executed',1,'dryrun',?,-60)`, e.clock().UnixMilli())
		}, "daily loss"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, okxorder.NewDryRun())
			p, err := e.s.Propose(context.Background(), input())
			if err != nil {
				t.Fatal(err)
			}
			c.prep(e)
			got, err := e.s.Approve(context.Background(), p.ID, "owner", "web")
			if err != nil || got.Status != StatusInvalidated || !strings.Contains(got.Error, c.want) || len(got.Request) != 0 {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestProposeRejects(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	bad := map[string]func(*ProposeInput){
		"no rationale":    func(i *ProposeInput) { i.Rationale = "" },
		"no invalidation": func(i *ProposeInput) { i.Invalidation = "" },
		"stop wrong side": func(i *ProposeInput) { i.SL = 101000 },
		"leverage cap":    func(i *ProposeInput) { i.Leverage = 20 },
		"margin cap":      func(i *ProposeInput) { i.MarginUSDT = 500 },
		"unknown inst":    func(i *ProposeInput) { i.InstID = "BTC-USD-SWAP" },
	}
	for name, mod := range bad {
		in := input()
		mod(&in)
		if _, err := e.s.Propose(context.Background(), in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := e.s.Propose(context.Background(), input()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Propose(context.Background(), input()); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("second proposal for the same instrument: %v", err)
	}
}

func TestMaxOpen(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	for i, inst := range []string{"BTC", "ETH", "SOL"} {
		in := input()
		in.InstID = inst
		p, err := e.s.Propose(context.Background(), in)
		if i == 2 {
			if err == nil || !strings.Contains(err.Error(), "cap is 2") {
				t.Fatalf("third open position: %v", err)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := e.s.Approve(context.Background(), p.ID, "owner", "web"); got.Status != StatusExecuted {
			t.Fatalf("%s: %+v", inst, got)
		}
	}
}

func TestTriggerSource(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	var prompt, title string
	e.s.SetRunner(func(ti, p, inst string) (int64, error) { title, prompt = ti, p; return 42, nil })
	if _, err := e.s.SaveTriggers([]TriggerDef{{ID: "alerts", Enabled: true, RuleIDs: []int64{3}}}); err != nil {
		t.Fatal(err)
	}
	d := e.s.triggers.snapshot()[1]
	if kinds[d.Kind].Match(d, Signal{Type: "alert", Vars: map[string]string{"ruleId": "4"}}) {
		t.Fatal("other rule matched")
	}
	e.s.triggers.fire(d, Signal{Type: "alert", InstID: "BTC-USDT-SWAP", Vars: map[string]string{"ruleId": "3", "rule": "急涨", "message": "5m +3%"}})
	if title != "信号 · 告警信号 · BTC" || !strings.Contains(prompt, "规则「急涨」在 BTC-USDT-SWAP 触发：5m +3%") || !strings.Contains(prompt, "propose_trade") {
		t.Fatalf("title %q prompt %q", title, prompt)
	}
	in := input()
	in.ChatID = 42
	p, err := e.s.Propose(context.Background(), in)
	if err != nil || p.Source != SourceSignal || p.Trigger != "告警信号" {
		t.Fatalf("signal proposal %+v %v", p, err)
	}
	// cooldown: no second run within the hour
	title = ""
	e.s.triggers.fire(d, Signal{Type: "alert", Vars: map[string]string{"ruleId": "3"}})
	if title != "" {
		t.Fatal("fired inside the cooldown")
	}
	sched := e.s.triggers.snapshot()[0]
	if sched.Enabled {
		t.Fatal("schedule trigger enabled by default")
	}
	h := time.Date(2026, 11, 1, 3, 59, 0, 0, time.UTC)
	if !kinds["schedule"].Due(sched, h, h.Add(2*time.Minute)) || kinds["schedule"].Due(sched, h.Add(2*time.Minute), h.Add(3*time.Minute)) {
		t.Fatal("schedule due")
	}
}

func TestRestartSettles(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	p, _ := e.s.Propose(context.Background(), input())
	e.s.db.Exec(`UPDATE trade_proposals SET status='executing' WHERE id=?`, p.ID)
	s2, err := New(e.s.st, okxorder.NewDryRun(), e.s.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s2.get(p.ID); got.Status != StatusFailed {
		t.Fatalf("after restart: %+v", got)
	}
}

type fakeAcct struct{ pos []okxacct.Position }

func (fakeAcct) Enabled() bool { return true }
func (f fakeAcct) Snapshot(context.Context, string) (okxacct.Snapshot, error) {
	return okxacct.Snapshot{Enabled: true, Positions: f.pos}, nil
}
func (fakeAcct) Read(context.Context, string, url.Values) (any, error) {
	return []any{map[string]any{"posMode": "long_short_mode"}}, nil
}

// The trade bot: a card with buttons and a 🔍 link, 同意 asks to confirm, only the owner is served,
// and the card is edited on resolution.
func TestBot(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	var mu sync.Mutex
	var calls []string
	var bodies []map[string]any
	updates := make(chan []map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		mu.Lock()
		calls = append(calls, method)
		bodies = append(bodies, body)
		mu.Unlock()
		switch method {
		case "getMe":
			w.Write([]byte(`{"ok":true,"result":{"id":9,"username":"ctg_trade_bot"}}`))
		case "getUpdates":
			select {
			case ups := <-updates:
				b, _ := json.Marshal(map[string]any{"ok": true, "result": ups})
				w.Write(b)
			case <-time.After(100 * time.Millisecond):
				w.Write([]byte(`{"ok":true,"result":[]}`))
			}
		case "sendMessage":
			w.Write([]byte(`{"ok":true,"result":{"message_id":55,"chat":{"id":1001}}}`))
		default:
			w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer srv.Close()
	b := NewBot(e.s, srv.Client(), srv.URL, "123:secret", 1001)
	b.timeout = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	waitFor := func(what string, f func() bool) {
		t.Helper()
		for i := 0; i < 200 && !f(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if !f() {
			t.Fatalf("timeout waiting for %s; calls %v", what, calls)
		}
	}
	find := func(method, substr string) bool {
		mu.Lock()
		defer mu.Unlock()
		for i, c := range calls {
			if raw, _ := json.Marshal(bodies[i]); c == method && strings.Contains(string(raw), substr) {
				return true
			}
		}
		return false
	}
	waitFor("card", func() bool { return find("sendMessage", "ok:1") })
	if !find("sendMessage", "/go/proposal/1") || !find("sendMessage", "[DRY RUN]") {
		t.Fatal("card without link or dry-run marker")
	}
	cb := func(from int64, data string) map[string]any {
		return map[string]any{"update_id": time.Now().UnixNano() % 1e9, "callback_query": map[string]any{"id": "c", "from": map[string]any{"id": from},
			"message": map[string]any{"message_id": 55, "chat": map[string]any{"id": 1001}}, "data": data}}
	}
	updates <- []map[string]any{cb(666, "go:1")} // a stranger
	updates <- []map[string]any{cb(1001, "ok:1")}
	waitFor("confirm step", func() bool { return find("editMessageText", "go:1") })
	if got, _ := e.s.get(p.ID); got.Status != StatusPending {
		t.Fatalf("stranger or first tap changed the proposal: %+v", got)
	}
	updates <- []map[string]any{cb(1001, "go:1")}
	waitFor("executed", func() bool { got, _ := e.s.get(p.ID); return got.Status == StatusExecuted })
	waitFor("push", func() bool { return find("sendMessage", "将会下单（未发送）") })
	if got, _ := e.s.get(p.ID); got.By != "telegram:1001" {
		t.Fatalf("by %q", got.By)
	}
	mu.Lock()
	for _, c := range calls {
		if strings.Contains(c, "secret") {
			t.Fatal("token leaked")
		}
	}
	mu.Unlock()
}

func TestHTTP(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	mux := http.NewServeMux()
	e.s.Register(mux)
	if _, err := e.s.Propose(context.Background(), input()); err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := do("POST", "/api/v1/realtrade/proposals/1/approve", `{"via":"app"}`); w.Code != 400 {
		t.Fatalf("approve without confirm: %d", w.Code)
	}
	if w := do("POST", "/api/v1/realtrade/proposals/1/reject", `{"via":"app"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"via":"app"`) {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/v1/realtrade/proposals/1/approve", `{"confirm":true}`); w.Code != 409 || !strings.Contains(w.Body.String(), `"proposal"`) {
		t.Fatalf("late approve: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/v1/realtrade/proposals/1", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"events"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/v1/realtrade/proposals?status=pending", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"proposals":[]`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w := do("GET", "/api/v1/realtrade/config", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"dryrun"`) || !strings.Contains(w.Body.String(), `"maxLeverage":5`) ||
		!strings.Contains(w.Body.String(), `"state":"disabled"`) {
		t.Fatalf("config: %s", w.Body)
	}
	if w := do("PUT", "/api/v1/realtrade/triggers", `[{"id":"scan","enabled":true,"everyMin":5}]`); w.Code != 400 {
		t.Fatalf("too frequent schedule accepted: %d", w.Code)
	}
	if w := do("PUT", "/api/v1/realtrade/triggers", `[{"id":"scan","enabled":true,"everyMin":60}]`); w.Code != 200 || !strings.Contains(w.Body.String(), `"everyMin":60`) {
		t.Fatalf("triggers: %d %s", w.Code, w.Body)
	}
}

// A proposal from a chat a signal subscription just woke is recorded as coming from that signal.
func TestProposalFromSignalWake(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	e.s.SetSignalSource(func(chat int64) (string, bool) { return "#4 BTC 进入 99000–99500", chat == 7 })
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != SourceSignal || p.Trigger != "#4 BTC 进入 99000–99500" || sourceText(p) != "信号 · #4 BTC 进入 99000–99500" {
		t.Fatalf("source %s %q", p.Source, p.Trigger)
	}
}

// noSend stands in for the live client: any order it is asked to send fails the test.
type noSend struct{ t *testing.T }

func (noSend) Mode() okxorder.Mode { return okxorder.Live }
func (n noSend) Place(context.Context, okxorder.Order) (okxorder.Result, error) {
	n.t.Fatal("a dry-run proposal reached the live client")
	return okxorder.Result{}, nil
}

func TestModeSwitchVoidsWaitingProposals(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	p, err := e.s.Propose(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	// approving a dry-run proposal on a live service (as if made just before the switch) is refused
	live, err := New(e.s.st, noSend{t}, e.s.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := live.get(p.ID); got.Status != StatusInvalidated || !strings.Contains(got.Error, "已切换为实盘") {
		t.Fatalf("after switching to live: %+v", got)
	}
	e.s.db.Exec(`UPDATE trade_proposals SET status='pending', error='' WHERE id=?`, p.ID) // made while this process runs
	if got, err := live.Approve(context.Background(), p.ID, "owner", "test"); !errors.Is(err, ErrClosed) || got.Status != StatusInvalidated {
		t.Fatalf("approve across modes: %+v %v", got, err)
	}
}
