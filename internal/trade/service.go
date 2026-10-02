package trade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"crypto-trader-go/internal/store"
)

// Options tune the service; zero values take the defaults.
type Options struct {
	PricePoll       time.Duration // live stop/target/limit/liquidation polling (default 1.5 s)
	MaintMarginRate float64       // flat maintenance margin rate (default 0.5%; TODO OKX tiers)
	SessionTimeout  time.Duration // upper bound per strategy event (default 10 s; the host kills at 5 s)
	FundingGrace    time.Duration // live: how long to wait for a settled rate after a settlement hour (default 10 min)
	RunnerRetry     time.Duration // live: wait between start attempts while the runner is offline (default 10 s)
}

func (o *Options) defaults() {
	if o.PricePoll <= 0 {
		o.PricePoll = 1500 * time.Millisecond
	}
	if o.MaintMarginRate <= 0 {
		o.MaintMarginRate = 0.005
	}
	if o.SessionTimeout <= 0 {
		o.SessionTimeout = 10 * time.Second
	}
	if o.FundingGrace <= 0 {
		o.FundingGrace = 10 * time.Minute
	}
	if o.RunnerRetry <= 0 {
		o.RunnerRetry = 10 * time.Second
	}
}

// Service is the order layer: accounts, instances, the signal pipeline, the simulated broker,
// live paper trading and backtests. It implements SignalSink for the strategy host.
type Service struct {
	st   *store.Store
	db   *sql.DB
	md   MarketData // live market data (may be nil in tests that only backtest)
	opts Options

	mu       sync.RWMutex
	runner   Runner
	inter    Interactor
	rev      Reviewer
	onChange func(accountID int64, kind string)
	books    map[int64]*book
	insts    map[int64]*instRT // instances with a live worker or a running backtest session
	kept     map[string]bool
	ctx      context.Context
	lastMin  int64

	events chan Event
	logs   *runLog
}

var _ SignalSink = (*Service)(nil)

// New creates the trading tables and restores accounts, positions and open orders.
func New(st *store.Store, md MarketData, opts Options) (*Service, error) {
	opts.defaults()
	if _, err := st.DB.Exec(schema); err != nil {
		return nil, fmt.Errorf("trade schema: %w", err)
	}
	if err := migrate(st.DB); err != nil {
		return nil, fmt.Errorf("trade schema: %w", err)
	}
	s := &Service{st: st, db: st.DB, md: md, opts: opts, books: map[int64]*book{}, insts: map[int64]*instRT{},
		kept: map[string]bool{}, ctx: context.Background(), events: make(chan Event, 256), logs: newRunLog()}
	if err := s.loadBooks(); err != nil {
		return nil, err
	}
	s.dropTrialAccounts()
	go s.notifier()
	return s, nil
}

// SetRunner attaches the strategy runtime. Without it instances cannot start.
func (s *Service) SetRunner(r Runner) { s.mu.Lock(); s.runner = r; s.mu.Unlock() }

// SetInteractor attaches notifications and human confirmations (nil = neither).
func (s *Service) SetInteractor(i Interactor) { s.mu.Lock(); s.inter = i; s.mu.Unlock() }

// SetReviewer attaches the AI review layer (nil = AI-mode signals are skipped).
func (s *Service) SetReviewer(r Reviewer) { s.mu.Lock(); s.rev = r; s.mu.Unlock() }

// SetOnChange registers a callback for data changes (kind: account | instance | signal | order |
// fill | equity | backtest) so pages can refetch (forwarded to SSE as event type "trade"). It must
// not block.
func (s *Service) SetOnChange(fn func(accountID int64, kind string)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func (s *Service) deps() (Runner, Interactor, Reviewer) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runner, s.inter, s.rev
}

func (s *Service) baseCtx() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctx
}

func (s *Service) changed(accountID int64, kind string) {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()
	if fn != nil {
		fn(accountID, kind)
	}
}

// emit queues an event for the interaction layer; it never blocks the trading path.
func (s *Service) emit(ev Event) {
	select {
	case s.events <- ev:
	default:
		log.Printf("trade: notification queue full, dropping %q", ev.Title)
	}
}

func (s *Service) notifier() {
	for ev := range s.events {
		_, inter, _ := s.deps()
		if inter != nil {
			inter.Notify(ev)
		}
	}
}

func (s *Service) book(id int64) (*book, error) {
	s.mu.RLock()
	b := s.books[id]
	s.mu.RUnlock()
	if b == nil {
		return nil, errNotFound("账户不存在")
	}
	return b, nil
}

func (s *Service) liveBooks() []*book {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*book, 0, len(s.books))
	for _, b := range s.books {
		if b.acc.Mode == ModeLive {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].acc.ID < out[j].acc.ID })
	return out
}

func (s *Service) instRT(id int64) *instRT {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.insts[id]
}

// Run drives live trading until ctx ends: price polling, live instance workers (resumed from the
// database), and strategy discovery once the runner is connected. Call it in its own goroutine.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	s.resumeInstances(ctx)
	go s.discoverLoop(ctx)
	t := time.NewTicker(s.opts.PricePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollPrices(ctx)
		}
	}
}

// discoverLoop loads strategy versions as soon as the runner is available (then every hour).
func (s *Service) discoverLoop(ctx context.Context) {
	wait := s.opts.RunnerRetry
	for {
		if r, _, _ := s.deps(); r != nil && r.Available() {
			if _, err := s.ReloadStrategies(ctx); err != nil {
				log.Printf("trade: describe strategies: %v", err)
			} else {
				wait = time.Hour
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// ---------- errors with HTTP status ----------

type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return e.msg }

func errNotFound(msg string) error    { return &apiError{404, msg} }
func errBad(msg string) error         { return &apiError{400, msg} }
func errConflict(msg string) error    { return &apiError{409, msg} }
func errUnavailable(msg string) error { return &apiError{503, msg} }

func statusOf(err error) int {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.code
	}
	return 500
}
