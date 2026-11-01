package realtrade

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"unicode/utf8"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
	"crypto-trader-go/internal/store"
)

// Limits are the caps every proposal is checked against at creation and again at approval.
type Limits struct {
	okxorder.Limits
	MaxOpen       int     `json:"maxOpen"`       // AI positions open (or on their way) at once
	DailyLossUSDT float64 `json:"dailyLossUsdt"` // realised loss of AI positions today (UTC+8) that stops new ones
	MaxDriftPct   float64 `json:"maxDriftPct"`   // price move between proposal and approval, %
	ExpiryMin     int     `json:"expiryMin"`     // default time to approve
	MaxPending    int     `json:"maxPending"`    // proposals waiting at once
}

var DefaultLimits = Limits{Limits: okxorder.DefaultLimits, MaxOpen: 2, DailyLossUSDT: 50, MaxDriftPct: 1, ExpiryMin: 15, MaxPending: 5}

// LimitsFromEnv reads CTG_TRADE_MAX_LEVERAGE, CTG_TRADE_MAX_MARGIN, CTG_TRADE_MIN_RR, CTG_TRADE_MAX_OPEN,
// CTG_TRADE_DAILY_LOSS, CTG_TRADE_MAX_DRIFT_PCT and CTG_TRADE_EXPIRY_MIN; unset or invalid keeps the default.
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
	i("CTG_TRADE_MAX_LEVERAGE", &l.MaxLeverage)
	f("CTG_TRADE_MAX_MARGIN", &l.MaxMarginUSDT)
	f("CTG_TRADE_MIN_RR", &l.MinRR)
	i("CTG_TRADE_MAX_OPEN", &l.MaxOpen)
	f("CTG_TRADE_DAILY_LOSS", &l.DailyLossUSDT)
	f("CTG_TRADE_MAX_DRIFT_PCT", &l.MaxDriftPct)
	i("CTG_TRADE_EXPIRY_MIN", &l.ExpiryMin)
	return l
}

// Account is the owner's OKX account, read-only (okxacct.Service).
type Account interface {
	Enabled() bool
	Snapshot(ctx context.Context, instID string) (okxacct.Snapshot, error)
	Read(ctx context.Context, path string, q url.Values) (any, error)
}

// Notifier receives every proposal event (proposed, approved, rejected, expired, invalidated, executed,
// failed, closed). It must not block.
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
	Now       func() time.Time
	Logf      func(format string, args ...any)
}

const (
	quoteMaxAge   = 90 * time.Second
	sweepEvery    = 5 * time.Second
	followEvery   = 20 * time.Second
	minExpiry     = 3 * time.Minute
	maxExpiry     = 60 * time.Minute
	posModeTTL    = 10 * time.Minute
	execTimeout   = 25 * time.Second
	closeSettle   = time.Minute // a live position may not show in the snapshot right after the fill
	closeWaitHist = 5 * time.Minute
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
	opts.Limits.Limits = opts.Limits.Limits.WithDefaults()
	opts.PublicURL = strings.TrimRight(opts.PublicURL, "/")
	s := &Service{st: st, db: st.DB, orders: orders, opts: opts, now: opts.Now, logf: opts.Logf, missing: map[int64]time.Time{}}
	if _, err := s.db.Exec(schema); err != nil {
		return nil, fmt.Errorf("realtrade schema: %w", err)
	}
	s.triggers = newTriggers(s)
	nothing := "服务重启时尚未发送，未下单"
	if _, err := s.transitionAll(StatusApproved, StatusInvalidated, nothing); err != nil {
		return nil, err
	}
	if orders.Mode() == okxorder.Live {
		nothing = "执行中服务重启，请在 OKX 核对是否已成交（不会自动重试）"
	}
	if _, err := s.transitionAll(StatusExecuting, StatusFailed, nothing); err != nil {
		return nil, err
	}
	return s, nil
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

// ---------- creation ----------

// ProposeInput is what the agent submits (propose_trade).
type ProposeInput struct {
	ChatID       int64
	InstID       string
	Side         string
	MarginUSDT   float64
	Leverage     int
	SL, TP       float64
	Rationale    string
	Invalidation string
	ExpiresMin   int
}

// NormalizeInst accepts "BTC", "btc-usdt" or "BTC-USDT-SWAP".
func NormalizeInst(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.HasSuffix(s, "-USDT-SWAP"):
		return s
	case strings.HasSuffix(s, "-USDT"):
		return s + "-SWAP"
	case !strings.Contains(s, "-"):
		return s + "-USDT-SWAP"
	}
	return s
}

// Propose validates the trade against the current price and every cap and stores it as pending. The
// source is the chat's trigger (signal / schedule) when the chat was started by one, else chat.
func (s *Service) Propose(ctx context.Context, in ProposeInput) (Proposal, error) {
	in.InstID = NormalizeInst(in.InstID)
	in.Side = strings.ToLower(strings.TrimSpace(in.Side))
	in.Rationale, in.Invalidation = strings.TrimSpace(in.Rationale), strings.TrimSpace(in.Invalidation)
	switch {
	case utf8.RuneCountInString(in.Rationale) < 10:
		return Proposal{}, errors.New("rationale is required (why this trade, why now)")
	case utf8.RuneCountInString(in.Rationale) > 2000:
		return Proposal{}, errors.New("rationale is too long (≤ 2000 characters)")
	case utf8.RuneCountInString(in.Invalidation) < 4:
		return Proposal{}, errors.New("invalidation is required (what would prove the idea wrong)")
	case utf8.RuneCountInString(in.Invalidation) > 500:
		return Proposal{}, errors.New("invalidation is too long (≤ 500 characters)")
	}
	px, err := s.freshQuote(in.InstID)
	if err != nil {
		return Proposal{}, err
	}
	spec, ok := s.opts.Spec(in.InstID)
	if !ok {
		return Proposal{}, fmt.Errorf("unknown instrument %s", in.InstID)
	}
	o, err := okxorder.Build(s.intent(in.InstID, in.Side, in.MarginUSDT, in.Leverage, in.SL, in.TP, ""), spec, px, okxorder.LongShortMode, s.opts.Limits.Limits)
	if err != nil {
		return Proposal{}, err
	}
	if err := s.policy(ctx, in.InstID, 0, true); err != nil {
		return Proposal{}, err
	}
	source, label := SourceChat, ""
	if kind, l, ok := s.triggerRun(in.ChatID); ok {
		source, label = kind, l
	} else if l, ok := s.signalSource(in.ChatID); ok {
		source, label = SourceSignal, l
	}
	exp := time.Duration(s.opts.Limits.ExpiryMin) * time.Minute
	if in.ExpiresMin > 0 {
		exp = time.Duration(in.ExpiresMin) * time.Minute
	}
	exp = min(max(exp, minExpiry), maxExpiry)
	now := s.now()
	z := o.Sizing()
	res, err := s.db.Exec(`INSERT INTO trade_proposals(created, updated, source, trigger_label, chat_id, inst_id, side, margin, leverage,
		contracts, notional, ref_px, sl, tp, rationale, invalidation, expires_at, status, status_at, mode) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now.UnixMilli(), now.UnixMilli(), source, label, in.ChatID, in.InstID, in.Side, in.MarginUSDT, in.Leverage, z.Contracts, z.NotionalUSDT,
		px, in.SL, in.TP, in.Rationale, in.Invalidation, now.Add(exp).UnixMilli(), StatusPending, now.UnixMilli(), string(s.Mode()))
	if err != nil {
		return Proposal{}, err
	}
	id, _ := res.LastInsertId()
	p, err := s.get(id)
	if err != nil {
		return p, err
	}
	s.audit(id, "created", fmt.Sprintf("%s · %s", sourceText(p), summary(p)), sourceText(p))
	s.emit("proposed", id)
	return p, nil
}

func (s *Service) intent(inst, side string, margin float64, lever int, sl, tp float64, clientID string) okxorder.Intent {
	return okxorder.Intent{InstID: inst, Side: side, MarginUSDT: margin, Leverage: lever, SL: sl, TP: tp, ClientID: clientID}
}

func (s *Service) freshQuote(inst string) (float64, error) {
	px, ts, ok := s.opts.Quote(inst)
	if !ok || !(px > 0) {
		return 0, fmt.Errorf("no current price for %s", inst)
	}
	if ts <= 0 {
		return 0, fmt.Errorf("the price time of %s is unknown", inst)
	}
	if age := s.now().Sub(time.UnixMilli(ts)); age > quoteMaxAge {
		return 0, fmt.Errorf("the price of %s is %s old", inst, age.Round(time.Second))
	}
	return px, nil
}

// policy checks the account-level caps: no other open proposal or AI position in the instrument, no
// position of the owner's in it (proposals only open new positions), at most MaxOpen AI positions,
// the daily loss stop, and (creating) at most MaxPending waiting proposals. self is excluded.
func (s *Service) policy(ctx context.Context, inst string, self int64, creating bool) error {
	mode := string(s.Mode())
	var n int
	var other int64
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM trade_proposals WHERE inst_id=? AND id<>? AND
		(status IN ('pending','approved','executing') OR (status='executed' AND closed_at=0 AND mode=?))`, inst, self, mode).Scan(&n, &other)
	if n > 0 {
		return fmt.Errorf("proposal #%d for %s is still open; one at a time per instrument", other, inst)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM trade_proposals WHERE id<>? AND mode=? AND
		(status IN ('approved','executing') OR (status='executed' AND closed_at=0))`, self, mode).Scan(&n)
	if n >= s.opts.Limits.MaxOpen {
		return fmt.Errorf("%d AI positions are open; the cap is %d", n, s.opts.Limits.MaxOpen)
	}
	if creating {
		s.db.QueryRow(`SELECT COUNT(*) FROM trade_proposals WHERE status='pending'`).Scan(&n)
		if n >= s.opts.Limits.MaxPending {
			return fmt.Errorf("%d proposals are already waiting for the owner; the cap is %d", n, s.opts.Limits.MaxPending)
		}
	}
	if loss := s.dailyPnl(); loss <= -s.opts.Limits.DailyLossUSDT {
		return fmt.Errorf("daily loss stop: AI positions lost %.2f USDT today (cap %g); no new trades until tomorrow (UTC+8)", -loss, s.opts.Limits.DailyLossUSDT)
	}
	if a := s.account(); a != nil {
		snap, err := a.Snapshot(ctx, inst)
		if err != nil {
			return fmt.Errorf("cannot read the OKX account: %v", err)
		}
		if len(snap.Positions) > 0 {
			return fmt.Errorf("the owner already holds a %s position in %s; proposals only open new positions", snap.Positions[0].Side, inst)
		}
	}
	return nil
}

// dailyPnl sums the P&L of AI positions closed today (UTC+8) in the current mode.
func (s *Service) dailyPnl() float64 {
	t := s.now().In(tz8)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz8).UnixMilli()
	var v float64
	s.db.QueryRow(`SELECT COALESCE(SUM(pnl),0) FROM trade_proposals WHERE mode=? AND closed_at>=?`, string(s.Mode()), day).Scan(&v)
	return v
}

// ---------- approval ----------

// Approve resolves a pending proposal as approved (first answer wins; an expired one cannot be
// approved), then re-validates and executes it. It returns the final state; ErrClosed (with the
// current state) when someone else answered first or it expired.
func (s *Service) Approve(ctx context.Context, id int64, by, via string) (Proposal, error) {
	now := s.now().UnixMilli()
	ok, err := s.transition(id, []string{StatusPending}, StatusApproved, map[string]any{"resolved_by": by, "via": via}, "expires_at > ?", now)
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

// execute re-validates an approved proposal against the live price and the caps, then places the order
// (dry run: builds and records it). Any check failing invalidates it; nothing is sent.
func (s *Service) execute(ctx context.Context, id int64) (Proposal, error) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	ok, err := s.transition(id, []string{StatusApproved}, StatusExecuting, nil, "")
	if err != nil || !ok {
		p, _ := s.get(id)
		if err == nil {
			err = ErrClosed
		}
		return p, err
	}
	p, err := s.get(id)
	if err != nil {
		return p, err
	}
	invalid := func(msg string) (Proposal, error) {
		if ok, _ := s.transition(id, []string{StatusExecuting}, StatusInvalidated, map[string]any{"error": msg}, ""); ok {
			s.audit(id, StatusInvalidated, msg, "system")
			s.emit(StatusInvalidated, id)
		}
		return s.get(id)
	}
	px, err := s.freshQuote(p.InstID)
	if err != nil {
		return invalid(err.Error())
	}
	if drift := math.Abs(px-p.RefPx) / p.RefPx * 100; drift > s.opts.Limits.MaxDriftPct {
		return invalid(fmt.Sprintf("price moved %.2f%% since the proposal (%s → %s); the cap is %g%%", drift,
			okxorder.FormatPx(p.RefPx), okxorder.FormatPx(px), s.opts.Limits.MaxDriftPct))
	}
	spec, ok := s.opts.Spec(p.InstID)
	if !ok {
		return invalid("instrument metadata unknown")
	}
	if err := s.policy(ctx, p.InstID, id, false); err != nil {
		return invalid(err.Error())
	}
	mode, err := s.positionMode(ctx)
	if err != nil {
		return invalid(err.Error())
	}
	o, err := okxorder.Build(s.intent(p.InstID, p.Side, p.MarginUSDT, p.Leverage, p.SL, p.TP, "ctgp"+strconv.FormatInt(id, 10)), spec, px, mode, s.opts.Limits.Limits)
	if err != nil {
		return invalid(err.Error())
	}
	ectx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	res, perr := s.orders.Place(ectx, o)
	reqJSON, _ := json.Marshal(res.Requests)
	respJSON := []byte{}
	if len(res.Responses) > 0 {
		respJSON, _ = json.Marshal(res.Responses)
	}
	z := o.Sizing()
	set := map[string]any{"request": string(reqJSON), "response": string(respJSON), "exec_px": px, "contracts": z.Contracts, "notional": z.NotionalUSDT}
	if perr != nil {
		set["error"] = perr.Error()
		if ok, _ := s.transition(id, []string{StatusExecuting}, StatusFailed, set, ""); ok {
			s.audit(id, StatusFailed, perr.Error(), "okx")
			s.emit(StatusFailed, id)
		}
		p, _ = s.get(id)
		return p, nil
	}
	set["mode"], set["ord_id"] = string(res.Mode), res.OrdID
	if ok, _ := s.transition(id, []string{StatusExecuting}, StatusExecuted, set, ""); ok {
		text := fmt.Sprintf("%s 市价 %s %s 张 @≈%s", modeText(string(res.Mode)), sideText(p.Side), fmtNum(z.Contracts), okxorder.FormatPx(px))
		if res.OrdID != "" {
			text += " · ordId " + res.OrdID
		}
		s.audit(id, StatusExecuted, text, "okx")
		s.emit(StatusExecuted, id)
	}
	return s.get(id)
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

// ---------- following executed positions ----------

// follow detects executed positions that closed: a dry run when the last price crosses SL or TP
// (P&L estimated before fees), live when the position is gone from the account (P&L from OKX's
// positions history).
func (s *Service) follow(ctx context.Context) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM trade_proposals WHERE status='executed' AND closed_at=0`)
	if err != nil {
		s.logf("realtrade: follow: %v", err)
		return
	}
	var ps []Proposal
	for rows.Next() {
		if p, err := scan(rows); err == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	for _, p := range ps {
		if p.Mode == string(okxorder.DryRun) {
			s.followDry(p)
		} else {
			s.followLive(ctx, p)
		}
	}
}

func (s *Service) followDry(p Proposal) {
	px, _, ok := s.opts.Quote(p.InstID)
	if !ok || px <= 0 {
		return
	}
	long := p.Side == "long"
	kind, at := "", 0.0
	switch {
	case (long && px <= p.SL) || (!long && px >= p.SL):
		kind, at = "sl", p.SL
	case (long && px >= p.TP) || (!long && px <= p.TP):
		kind, at = "tp", p.TP
	default:
		return
	}
	spec, _ := s.opts.Spec(p.InstID)
	dir := 1.0
	if !long {
		dir = -1
	}
	s.closed(p, kind, at, (at-p.ExecPx)*dir*p.Contracts*spec.CtVal, "估算，未含手续费")
}

func (s *Service) followLive(ctx context.Context, p Proposal) {
	a := s.account()
	if a == nil || s.now().Sub(time.UnixMilli(p.StatusAt)) < closeSettle {
		return
	}
	snap, err := a.Snapshot(ctx, p.InstID)
	if err != nil {
		return
	}
	for _, x := range snap.Positions {
		if x.Side == p.Side {
			delete(s.missing, p.ID)
			return
		}
	}
	if _, ok := s.missing[p.ID]; !ok {
		s.missing[p.ID] = s.now()
	}
	v, err := a.Read(ctx, "/api/v5/account/positions-history", url.Values{"instType": {"SWAP"}, "instId": {p.InstID}, "limit": {"20"}})
	rows, _ := v.([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		str := func(k string) string { x, _ := m[k].(string); return x }
		ut, _ := strconv.ParseInt(str("uTime"), 10, 64)
		dir := str("direction")
		if ut < p.StatusAt || (dir != "" && dir != p.Side) {
			continue
		}
		pnl, _ := strconv.ParseFloat(str("realizedPnl"), 64)
		cpx, _ := strconv.ParseFloat(str("closeAvgPx"), 64)
		kind := "other"
		if t := str("type"); t != "3" && t != "4" && t != "5" && cpx > 0 {
			kind = "tp"
			if math.Abs(cpx-p.SL) < math.Abs(cpx-p.TP) {
				kind = "sl"
			}
		}
		delete(s.missing, p.ID)
		s.closed(p, kind, cpx, pnl, "OKX 已实现盈亏")
		return
	}
	if err == nil && s.now().Sub(s.missing[p.ID]) > closeWaitHist { // gone, but no history row: close without P&L
		delete(s.missing, p.ID)
		s.closed(p, "other", 0, 0, "仓位已不在，未查到平仓记录")
	}
}

func (s *Service) closed(p Proposal, kind string, px, pnl float64, note string) {
	res, err := s.db.Exec(`UPDATE trade_proposals SET closed_at=?, close_kind=?, close_px=?, pnl=?, updated=? WHERE id=? AND closed_at=0`,
		s.now().UnixMilli(), kind, px, pnl, s.now().UnixMilli(), p.ID)
	if err != nil {
		s.logf("realtrade: close %d: %v", p.ID, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 1 {
		s.audit(p.ID, "closed", fmt.Sprintf("%s %s %+.2f USDT（%s）", closeText(kind), okxorder.FormatPx(px), pnl, note), "system")
		s.emit("closed", p.ID)
	}
}

// Run expires pending proposals, follows executed ones and runs the triggers until ctx ends.
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
		case <-fol.C:
			s.follow(ctx)
		}
	}
}
