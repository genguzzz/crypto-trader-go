package trade

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/store"
)

// ---------- fake market data ----------

type fakeMD struct {
	mu      sync.Mutex
	now     int64
	specs   map[string]Spec
	quotes  map[string]Quote // Quote returns it without depth; QuoteDepth with
	bars    map[string][]Bar // key inst|tf
	funding map[string]map[int64]float64
	kept    map[string]bool
	screen  []ScreenRow
}

func newFakeMD(now int64) *fakeMD {
	return &fakeMD{now: now, specs: map[string]Spec{
		"BTC-USDT-SWAP": {InstID: "BTC-USDT-SWAP", CtVal: 0.01, TickSz: 0.1, LotSz: 0.01, MinSz: 0.01, MaxLever: 100},
		"ETH-USDT-SWAP": {InstID: "ETH-USDT-SWAP", CtVal: 0.1, TickSz: 0.01, LotSz: 0.01, MinSz: 0.01, MaxLever: 100},
		"X-USDT-SWAP":   {InstID: "X-USDT-SWAP", CtVal: 1, TickSz: 0.01, LotSz: 1, MinSz: 1, MaxLever: 50},
	}, quotes: map[string]Quote{}, bars: map[string][]Bar{}, funding: map[string]map[int64]float64{}, kept: map[string]bool{}}
}

func (m *fakeMD) Now() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.now }
func (m *fakeMD) setNow(t int64) {
	m.mu.Lock()
	m.now = t
	m.mu.Unlock()
}
func (m *fakeMD) Spec(id string) (Spec, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.specs[id]
	return s, ok
}
func (m *fakeMD) setQuote(id string, q Quote) {
	m.mu.Lock()
	m.quotes[id] = q
	m.mu.Unlock()
}
func (m *fakeMD) Quote(_ context.Context, id string) (Quote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.quotes[id]
	if !ok {
		return Quote{}, fmt.Errorf("no quote")
	}
	q.Asks, q.Bids = nil, nil
	return q, nil
}
func (m *fakeMD) QuoteDepth(_ context.Context, id string) (Quote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.quotes[id]
	if !ok {
		return Quote{}, fmt.Errorf("no quote")
	}
	return q, nil
}
func (m *fakeMD) Bars(id, tf string, n int) ([]Bar, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	xs := m.bars[id+"|"+tf]
	if len(xs) > n {
		xs = xs[len(xs)-n:]
	}
	return xs, nil
}
func (m *fakeMD) Factors(string) (map[string]float64, bool) { return nil, false }
func (m *fakeMD) Screener(q ScreenerQuery) ([]ScreenRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return SelectScreen(append([]ScreenRow(nil), m.screen...), q), nil
}
func (m *fakeMD) Score(string, string) (float64, float64, bool) { return 0, 0, false }
func (m *fakeMD) FundingRate(id string, at int64) (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.funding[id][at]
	return r, ok
}
func (m *fakeMD) Keep(id string, on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if on {
		m.kept[id] = true
	} else {
		delete(m.kept, id)
	}
}

// ---------- fake strategy runtime ----------

type fakeRunner struct {
	mu       sync.Mutex
	versions []StrategyVersion
	sessions []*fakeSession
	// onBar is the strategy: it runs inside Session.Bar (like Python between the event and done)
	onBar   func(s *fakeSession, instID, tf string, b Bar)
	onStart func(s *fakeSession)
	check   func(code string) CheckResult
}

func (r *fakeRunner) Describe(context.Context) ([]StrategyVersion, error) { return r.versions, nil }
func (r *fakeRunner) Available() bool                                     { return true }
func (r *fakeRunner) Check(_ context.Context, code string) (CheckResult, error) {
	if r.check != nil {
		return r.check(code), nil
	}
	return CheckResult{Diagnostics: []Diagnostic{}, Strategies: []StrategyVersion{{Name: "user-x", Timeframe: "1H", Params: []ParamSpec{}}}}, nil
}
func (r *fakeRunner) Start(ctx context.Context, inst Instance, ver StrategyVersion, mode Mode, state []byte, md MarketData, sink SignalSink) (Session, error) {
	s := &fakeSession{r: r, inst: inst, mode: mode, md: md, sink: sink, done: make(chan struct{}), results: map[int64]SignalOutcome{}, startState: state}
	r.mu.Lock()
	r.sessions = append(r.sessions, s)
	r.mu.Unlock()
	if r.onStart != nil {
		r.onStart(s)
	}
	return s, nil
}

func (r *fakeRunner) last() *fakeSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) == 0 {
		return nil
	}
	return r.sessions[len(r.sessions)-1]
}

type fakeSession struct {
	r          *fakeRunner
	inst       Instance
	mode       Mode
	md         MarketData
	sink       SignalSink
	done       chan struct{}
	startState []byte

	mu       sync.Mutex
	events   []string
	bars     []Bar
	fills    []Fill
	results  map[int64]SignalOutcome
	universe []string
	stopped  bool
	errs     []string
}

func (s *fakeSession) log(e string) { s.mu.Lock(); s.events = append(s.events, e); s.mu.Unlock() }
func (s *fakeSession) fail(f string, a ...any) {
	s.mu.Lock()
	s.errs = append(s.errs, fmt.Sprintf(f, a...))
	s.mu.Unlock()
}
func (s *fakeSession) Bars(ctx context.Context, tf string, items []InstBar) error {
	for _, x := range items {
		s.mu.Lock()
		s.events = append(s.events, "bar:"+x.InstID)
		s.bars = append(s.bars, x.Bar)
		s.mu.Unlock()
		if s.r.onBar != nil {
			s.r.onBar(s, x.InstID, tf, x.Bar)
		}
	}
	return nil
}
func (s *fakeSession) Timer(ctx context.Context, name string) error {
	s.log("timer:" + name)
	return nil
}
func (s *fakeSession) Status() string { return "" }
func (s *fakeSession) Universe(ctx context.Context, added, removed, current []string) error {
	s.mu.Lock()
	s.universe = current
	s.events = append(s.events, fmt.Sprintf("universe:+%d-%d", len(added), len(removed)))
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) Fill(ctx context.Context, f Fill) error {
	s.mu.Lock()
	s.fills = append(s.fills, f)
	s.events = append(s.events, "fill")
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) SignalResult(ctx context.Context, id int64, o SignalOutcome, reason string) error {
	s.mu.Lock()
	s.results[id] = o
	s.events = append(s.events, "result:"+string(o))
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) Funding(ctx context.Context, instID string, rate, amount float64) error {
	s.log("funding")
	return nil
}
func (s *fakeSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.done)
	}
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) State() []byte         { return []byte(`{"n":1}`) }
func (s *fakeSession) Done() <-chan struct{} { return s.done }
func (s *fakeSession) Err() error            { return nil }

// ---------- service helpers ----------

const t0 = int64(1_790_000_000_000) / hourMs * hourMs // hour aligned (UTC)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	return st
}

func newSvc(t *testing.T, md *fakeMD) (*Service, *store.Store) {
	t.Helper()
	st := openStore(t)
	s, err := New(st, md, Options{PricePoll: 20 * time.Millisecond, RunnerRetry: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

var testVersion = StrategyVersion{ID: "abc123def456", Name: "test-strat", File: "library/t.py", Timeframe: "1m", Code: "pass",
	Params:          []ParamSpec{{Name: "fast", Default: 10, Min: ptr(1.0), Max: ptr(50.0)}},
	DefaultUniverse: Universe{Static: []string{"BTC-USDT-SWAP"}}}

func ptr[T any](v T) *T { return &v }

func addVersion(t *testing.T, s *Service, v StrategyVersion) {
	t.Helper()
	r := &fakeRunner{versions: []StrategyVersion{v}}
	s.SetRunner(r)
	if _, err := s.ReloadStrategies(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func mustAccount(t *testing.T, s *Service, in AccountInput) Account {
	t.Helper()
	a, err := s.CreateAccount(in)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// attach registers a running instance without a worker (signals are accepted, events queue).
func attach(t *testing.T, s *Service, accountID int64, exec ExecMode) *instRT {
	t.Helper()
	inst, err := s.CreateInstance(accountID, InstanceInput{VersionID: testVersion.ID, Exec: exec})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.book(accountID)
	inst.State = StRunning
	ri := newInstRT(s, b, inst, testVersion)
	s.mu.Lock()
	s.insts[inst.ID] = ri
	s.mu.Unlock()
	s.setInstanceState(inst.ID, StRunning, "")
	return ri
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func outcome(s *Service, id int64) (SignalOutcome, string) {
	var o, r string
	s.db.QueryRow(`SELECT outcome, outcome_reason FROM trade_signals WHERE id=?`, id).Scan(&o, &r)
	return SignalOutcome(o), r
}

// submitWait submits a signal and waits for its final outcome.
func submitWait(t *testing.T, s *Service, sg Signal) (int64, SignalOutcome, string) {
	t.Helper()
	id, err := s.Submit(context.Background(), sg)
	if err != nil {
		t.Fatal(err)
	}
	var o SignalOutcome
	var r string
	waitFor(t, "signal outcome", func() bool { o, r = outcome(s, id); return o != OutPending })
	return id, o, r
}

func btcQuote(px float64) Quote {
	return Quote{Last: px, Bid: px - 0.1, Ask: px + 0.1, Mark: px,
		Asks: [][2]float64{{px + 0.1, 1000}}, Bids: [][2]float64{{px - 0.1, 1000}}}
}

func approx(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
