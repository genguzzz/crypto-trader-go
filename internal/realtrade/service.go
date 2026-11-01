package realtrade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
	"crypto-trader-go/internal/store"
)

// Limits are the caps every proposal is checked against at creation and again at approval.
type Limits struct {
	okxorder.Limits
	MaxOpen       int     `json:"maxOpen"`       // AI positions open (or on their way) at once
	DailyLossUSDT float64 `json:"dailyLossUsdt"` // realised loss of AI positions today (UTC+8) that stops new opens and adds
	MaxDriftPct   float64 `json:"maxDriftPct"`   // price move between proposal and approval of a market open / add, %
	ExpiryMin     int     `json:"expiryMin"`     // default time to approve
	MaxPending    int     `json:"maxPending"`    // proposals waiting at once
	MaxAdds       int     `json:"maxAdds"`       // adds per position
	LimitMaxMin   int     `json:"limitMaxMin"`   // the longest a limit order rests before the server cancels it, minutes
}

var DefaultLimits = Limits{Limits: okxorder.DefaultLimits, MaxOpen: 2, DailyLossUSDT: 50, MaxDriftPct: 1, ExpiryMin: 15, MaxPending: 5,
	MaxAdds: 2, LimitMaxMin: 1440}

// LimitsFromEnv reads CTG_TRADE_MIN_LEVERAGE, CTG_TRADE_MAX_LEVERAGE, CTG_TRADE_MIN_MARGIN, CTG_TRADE_MAX_MARGIN,
// CTG_TRADE_MIN_RR, CTG_TRADE_MAX_OPEN, CTG_TRADE_DAILY_LOSS, CTG_TRADE_MAX_DRIFT_PCT, CTG_TRADE_EXPIRY_MIN,
// CTG_TRADE_MAX_ADDS, CTG_TRADE_LIMIT_MAX_MIN (≤ 1440) and CTG_TRADE_MAX_LIMIT_PCT; unset or invalid keeps the default.
func LimitsFromEnv() Limits {
	l := DefaultLimits
	f := func(k string, dst *float64) {
		if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil && v > 0 && !math.IsInf(v, 0) {
			*dst = v
		}
	}
	i := func(k string, dst *int) {
		if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
			*dst = v
		}
	}
	i("CTG_TRADE_MIN_LEVERAGE", &l.MinLeverage)
	i("CTG_TRADE_MAX_LEVERAGE", &l.MaxLeverage)
	f("CTG_TRADE_MIN_MARGIN", &l.MinMarginUSDT)
	f("CTG_TRADE_MAX_MARGIN", &l.MaxMarginUSDT)
	f("CTG_TRADE_MIN_RR", &l.MinRR)
	i("CTG_TRADE_MAX_OPEN", &l.MaxOpen)
	f("CTG_TRADE_DAILY_LOSS", &l.DailyLossUSDT)
	f("CTG_TRADE_MAX_DRIFT_PCT", &l.MaxDriftPct)
	i("CTG_TRADE_EXPIRY_MIN", &l.ExpiryMin)
	i("CTG_TRADE_MAX_ADDS", &l.MaxAdds)
	i("CTG_TRADE_LIMIT_MAX_MIN", &l.LimitMaxMin)
	l.LimitMaxMin = min(l.LimitMaxMin, 1440)
	f("CTG_TRADE_MAX_LIMIT_PCT", &l.MaxLimitPct)
	return l
}

// Account is the owner's OKX account, read-only (okxacct.Service).
type Account interface {
	Enabled() bool
	Snapshot(ctx context.Context, instID string) (okxacct.Snapshot, error)
	Read(ctx context.Context, path string, q url.Values) (any, error)
}

// Notifier receives every proposal event (proposed, approved, rejected, expired, invalidated, executed,
// failed, filled / canceled (a limit order), closed). It must not block.
type Notifier interface {
	ProposalEvent(kind string, p Proposal)
}

// Options wire the service. Quote and Spec are required.
type Options struct {
	Limits Limits
	// Quote is the latest trade price and its time (ms); Spec the instrument's sizing metadata.
	Quote func(instID string) (px float64, ts int64, ok bool)
	Spec  func(instID string) (okxorder.Spec, bool)
	// PublicURL is the web origin for 🔍 links, e.g. https://ctg.gugenzzz.top.
	PublicURL string
	// Settle is how long after a live market execution the chat is told (the fill and its TP/SL then show
	// in the account snapshot); 0 = 3 s, negative = at once.
	Settle time.Duration
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

const (
	quoteMaxAge   = 90 * time.Second
	sweepEvery    = 5 * time.Second
	followEvery   = 20 * time.Second
	minExpiry     = 3 * time.Minute
	maxExpiry     = 240 * time.Minute
	posModeTTL    = 10 * time.Minute
	execTimeout   = 25 * time.Second
	closeSettle   = time.Minute // a live position may not show in the snapshot right after the fill
	closeWaitHist = 5 * time.Minute
	minOrderMin   = 5  // a limit order rests at least this long
	defOrderMin   = 60 // and this long unless the proposal says
	lostOrder     = 10 * time.Minute
)

var tz8 = time.FixedZone("UTC+8", 8*3600)

// Service owns proposals, their approval and execution.
type Service struct {
	st     *store.Store
	db     *sql.DB
	orders okxorder.Client
	opts   Options
	now    func() time.Time
	logf   func(string, ...any)

	mu        sync.Mutex
	acct      Account
	notifiers []Notifier
	onChange  func()
	posMode   string
	posModeAt time.Time
	missing   map[int64]time.Time // live positions gone from the snapshot, waiting for their history row
	execMu    sync.Mutex          // one execution at a time: the open-position caps hold across approvals

	triggers *triggers
	bot      *Bot
	signalOf func(chat int64) (label string, ok bool)
	steerer  Steerer
	sigBrief func(chat int64, instID string, posID int64) string // the signals linked to a position (internal/signals)
	onClosed func(posID int64, reason string) []int64            // a position closed: cancel its signals, return their ids
	steerMu  sync.Mutex                                          // one delivery pass at a time
}

// New creates the tables and settles proposals a restart interrupted: approved ones are invalidated
// (nothing was sent) and executing ones failed (the owner checks OKX; never retried, never doubled).
func New(st *store.Store, orders okxorder.Client, opts Options) (*Service, error) {
	if orders == nil {
		orders = okxorder.NewDryRun()
	}
	if opts.Quote == nil || opts.Spec == nil {
		return nil, errors.New("realtrade: Quote and Spec are required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.Limits.MaxOpen == 0 {
		opts.Limits = DefaultLimits
	}
	if opts.Limits.MaxAdds <= 0 {
		opts.Limits.MaxAdds = DefaultLimits.MaxAdds
	}
	if opts.Limits.LimitMaxMin < minOrderMin {
		opts.Limits.LimitMaxMin = DefaultLimits.LimitMaxMin
	}
	if opts.Settle == 0 {
		opts.Settle = 3 * time.Second
	}
	opts.Limits.Limits = opts.Limits.Limits.WithDefaults()
	opts.PublicURL = strings.TrimRight(opts.PublicURL, "/")
	s := &Service{st: st, db: st.DB, orders: orders, opts: opts, now: opts.Now, logf: opts.Logf, missing: map[int64]time.Time{}}
	if err := migrate(s.db); err != nil {
		return nil, fmt.Errorf("realtrade schema: %w", err)
	}
	s.db.Exec(`UPDATE trade_steers SET state='queued' WHERE state='sending'`) // a restart mid-delivery
	s.triggers = newTriggers(s)
	nothing := "服务重启时尚未发送，未下单"
	if _, err := s.transitionAll(StatusApproved, StatusInvalidated, nothing); err != nil {
		return nil, err
	}
	if orders.Mode() == okxorder.Live {
		nothing = "执行中服务重启，请在 OKX 核对是否已成交（不会自动重试）"
	}
	// a proposal is approved in the mode it was made in: after a switch (dry run → live) the waiting
	// ones are void, so approving one made as a dry run can never send a real order
	if _, err := s.voidOtherMode(); err != nil {
		return nil, err
	}
	if _, err := s.transitionAll(StatusExecuting, StatusFailed, nothing); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) voidOtherMode() (int, error) {
	rows, err := s.db.Query(`SELECT id FROM trade_proposals WHERE status=? AND mode<>?`, StatusPending, string(s.Mode()))
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	msg := modeSwitched(s.Mode())
	for _, id := range ids {
		if ok, _ := s.transition(id, []string{StatusPending}, StatusInvalidated, map[string]any{"error": msg}, "mode<>?", string(s.Mode())); ok {
			s.audit(id, StatusInvalidated, msg, "system")
		}
	}
	return len(ids), nil
}

func modeSwitched(m okxorder.Mode) string {
	if m == okxorder.Live {
		return "已切换为实盘，演练时的提议作废，请让 AI 重新提议"
	}
	return "执行模式已切换，提议作废，请让 AI 重新提议"
}

func (s *Service) transitionAll(from, to, msg string) (int, error) {
	rows, err := s.db.Query(`SELECT id FROM trade_proposals WHERE status=?`, from)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if ok, _ := s.transition(id, []string{from}, to, map[string]any{"error": msg}, ""); ok {
			s.audit(id, to, msg, "system")
		}
	}
	return len(ids), nil
}

// Mode is the execution mode (fixed at start from CTG_TRADE_MODE).
func (s *Service) Mode() okxorder.Mode { return s.orders.Mode() }

// Limits returns the caps in force.
func (s *Service) Limits() Limits { return s.opts.Limits }

// SetAccount gives the service the owner's account for position checks and close detection.
func (s *Service) SetAccount(a Account) {
	s.mu.Lock()
	s.acct = a
	s.mu.Unlock()
}

// AddNotifier registers a channel for proposal events (the Telegram trade bot).
func (s *Service) AddNotifier(n Notifier) {
	s.mu.Lock()
	s.notifiers = append(s.notifiers, n)
	s.mu.Unlock()
}

// SetSignalSource tells proposals made in a chat that a signal subscription just woke (internal/signals)
// apart from the owner's own questions: they are recorded with source signal and that label.
func (s *Service) SetSignalSource(f func(chat int64) (label string, ok bool)) {
	s.mu.Lock()
	s.signalOf = f
	s.mu.Unlock()
}

func (s *Service) signalSource(chat int64) (string, bool) {
	s.mu.Lock()
	f := s.signalOf
	s.mu.Unlock()
	if f == nil || chat <= 0 {
		return "", false
	}
	return f(chat)
}

// SetOnChange sets the hook called after any proposal changes (forward to SSE); must not block.
func (s *Service) SetOnChange(f func()) {
	s.mu.Lock()
	s.onChange = f
	s.mu.Unlock()
}

func (s *Service) account() Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acct == nil || !s.acct.Enabled() {
		return nil
	}
	return s.acct
}

// emit audits nothing; it tells the notifiers and the change hook.
func (s *Service) emit(kind string, id int64) {
	p, err := s.get(id)
	if err != nil {
		s.logf("realtrade: load %d: %v", id, err)
		return
	}
	s.mu.Lock()
	ns := append([]Notifier(nil), s.notifiers...)
	f := s.onChange
	s.mu.Unlock()
	for _, n := range ns {
		n.ProposalEvent(kind, p)
	}
	if f != nil {
		f()
	}
}

// Link opens the proposal (K-line with its levels, approve / reject) in the app: Telegram URL buttons
// take only http(s), so it points at a public bounce page that hands over to okxhelper://proposal/<id>.
func (s *Service) Link(p Proposal) string {
	if s.opts.PublicURL == "" {
		return ""
	}
	return s.opts.PublicURL + "/go/proposal/" + strconv.FormatInt(p.ID, 10)
}

// ---------- approval ----------

// Approve resolves a pending proposal as approved (first answer wins; an expired one cannot be
// approved), then re-validates and executes it. It returns the final state; ErrClosed (with the
// current state) when someone else answered first or it expired.
func (s *Service) Approve(ctx context.Context, id int64, by, via string) (Proposal, error) {
	now := s.now().UnixMilli()
	ok, err := s.transition(id, []string{StatusPending}, StatusApproved, map[string]any{"resolved_by": by, "via": via}, "expires_at > ? AND mode = ?", now, string(s.Mode()))
	if err != nil {
		return Proposal{}, err
	}
	if !ok {
		p, err := s.get(id)
		if err != nil {
			return p, err
		}
		if p.Status == StatusPending && p.ExpiresAt <= now {
			s.expire(id)
			p, _ = s.get(id)
		} else if p.Status == StatusPending && p.Mode != string(s.Mode()) {
			msg := modeSwitched(s.Mode())
			if ok, _ := s.transition(id, []string{StatusPending}, StatusInvalidated, map[string]any{"error": msg}, "mode<>?", string(s.Mode())); ok {
				s.audit(id, StatusInvalidated, msg, "system")
				s.emit(StatusInvalidated, id)
			}
			p, _ = s.get(id)
		}
		return p, ErrClosed
	}
	s.audit(id, StatusApproved, "同意", by)
	s.emit(StatusApproved, id)
	return s.execute(context.WithoutCancel(ctx), id) // the owner closing the page must not abort an approved trade
}

// Reject resolves a pending proposal as rejected (first answer wins).
func (s *Service) Reject(id int64, by, via string) (Proposal, error) {
	ok, err := s.transition(id, []string{StatusPending}, StatusRejected, map[string]any{"resolved_by": by, "via": via}, "")
	if err != nil {
		return Proposal{}, err
	}
	p, gerr := s.get(id)
	if gerr != nil {
		return p, gerr
	}
	if !ok {
		return p, ErrClosed
	}
	s.audit(id, StatusRejected, "拒绝", by)
	s.emit(StatusRejected, id)
	return p, nil
}

func (s *Service) expire(id int64) {
	ok, err := s.transition(id, []string{StatusPending}, StatusExpired, map[string]any{"resolved_by": "timeout", "via": "timeout"}, "")
	if err != nil {
		s.logf("realtrade: expire %d: %v", id, err)
	}
	if ok {
		s.audit(id, StatusExpired, "超时未审核，已过期（不会自动执行）", "timeout")
		s.emit(StatusExpired, id)
	}
}

// expireDue expires pending proposals past their deadline.
func (s *Service) expireDue() {
	rows, err := s.db.Query(`SELECT id FROM trade_proposals WHERE status='pending' AND expires_at<=?`, s.now().UnixMilli())
	if err != nil {
		s.logf("realtrade: expire: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.expire(id)
	}
}

// positionMode reads the account's posMode (cached); without an account it assumes long_short_mode,
// which only a dry run can get to.
func (s *Service) positionMode(ctx context.Context) (string, error) {
	a := s.account()
	if a == nil {
		if s.Mode() == okxorder.Live {
			return "", errors.New("OKX account not configured")
		}
		return okxorder.LongShortMode, nil
	}
	s.mu.Lock()
	if s.posMode != "" && s.now().Sub(s.posModeAt) < posModeTTL {
		m := s.posMode
		s.mu.Unlock()
		return m, nil
	}
	s.mu.Unlock()
	v, err := a.Read(ctx, "/api/v5/account/config", nil)
	if err != nil {
		return "", fmt.Errorf("account config: %v", err)
	}
	rows, _ := v.([]any)
	m := ""
	if len(rows) > 0 {
		if r, ok := rows[0].(map[string]any); ok {
			m, _ = r["posMode"].(string)
		}
	}
	if m != okxorder.LongShortMode && m != okxorder.NetMode {
		return "", fmt.Errorf("unknown account position mode %q", m)
	}
	s.mu.Lock()
	s.posMode, s.posModeAt = m, s.now()
	s.mu.Unlock()
	return m, nil
}

// Run expires pending proposals, follows resting orders and AI positions, delivers the chat messages
// (steers) and runs the triggers until ctx ends.
func (s *Service) Run(ctx context.Context) {
	go s.triggers.run(ctx)
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	fol := time.NewTicker(followEvery)
	defer fol.Stop()
	s.expireDue()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			s.expireDue()
			s.deliverSteers(ctx)
		case <-fol.C:
			s.follow(ctx)
		}
	}
}
