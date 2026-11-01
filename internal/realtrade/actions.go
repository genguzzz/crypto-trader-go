package realtrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
)

// ---------- creation ----------

// ProposeInput is what the agent submits (propose_trade).
type ProposeInput struct {
	ChatID     int64
	Kind       string // open (default) | add | reduce | close | adjust
	InstID     string
	Side       string // the position's side
	MarginMode string // open: isolated (default) | cross; other kinds take the position's
	OrdType    string // market (default) | limit
	Px         float64
	MarginUSDT float64
	Leverage   int // add: 0 = the position's
	SL, TP     float64
	Fraction   float64 // reduce
	Rationale  string
	// Invalidation is required for open and add (what proves the idea wrong before the stop).
	Invalidation string
	ExpiresMin   int // minutes to approve
	OrderMin     int // limit: minutes the order rests before the server cancels it
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

var kinds5 = map[string]bool{KindOpen: true, KindAdd: true, KindReduce: true, KindClose: true, KindAdjust: true}

// Propose validates the action against the current price, the position it acts on and every cap and
// stores it as pending. The source is the chat's trigger (signal / schedule) when the chat was started
// by one, else chat.
func (s *Service) Propose(ctx context.Context, in ProposeInput) (Proposal, error) {
	in.InstID = NormalizeInst(in.InstID)
	in.Side = strings.ToLower(strings.TrimSpace(in.Side))
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.MarginMode = strings.ToLower(strings.TrimSpace(in.MarginMode))
	in.OrdType = strings.ToLower(strings.TrimSpace(in.OrdType))
	if in.Kind == "" {
		in.Kind = KindOpen
	}
	if in.OrdType == "" || in.Kind == KindAdjust {
		in.OrdType = okxorder.Market
	}
	in.Rationale, in.Invalidation = strings.TrimSpace(in.Rationale), strings.TrimSpace(in.Invalidation)
	risky := in.Kind == KindOpen || in.Kind == KindAdd
	switch {
	case !kinds5[in.Kind]:
		return Proposal{}, errors.New("kind must be open, add, reduce, close or adjust")
	case utf8.RuneCountInString(in.Rationale) < 10:
		return Proposal{}, errors.New("rationale is required (why this action, why now)")
	case utf8.RuneCountInString(in.Rationale) > 2000:
		return Proposal{}, errors.New("rationale is too long (≤ 2000 characters)")
	case risky && utf8.RuneCountInString(in.Invalidation) < 4:
		return Proposal{}, errors.New("invalidation is required (what would prove the idea wrong)")
	case utf8.RuneCountInString(in.Invalidation) > 500:
		return Proposal{}, errors.New("invalidation is too long (≤ 500 characters)")
	case in.OrdType != okxorder.Market && in.OrdType != okxorder.Limit:
		return Proposal{}, errors.New("orderType must be market or limit")
	}
	if in.OrdType == okxorder.Limit {
		if in.OrderMin == 0 {
			in.OrderMin = defOrderMin
		}
		if in.OrderMin < minOrderMin || in.OrderMin > s.opts.Limits.LimitMaxMin {
			return Proposal{}, fmt.Errorf("orderExpiryMin must be %d–%d minutes", minOrderMin, s.opts.Limits.LimitMaxMin)
		}
	} else {
		in.OrderMin, in.Px = 0, 0
	}
	px, err := s.freshQuote(in.InstID)
	if err != nil {
		return Proposal{}, err
	}
	if _, ok := s.opts.Spec(in.InstID); !ok {
		return Proposal{}, fmt.Errorf("unknown instrument %s", in.InstID)
	}
	if in.OrdType == okxorder.Limit {
		if err := okxorder.Resting(in.Side, in.Kind == KindReduce || in.Kind == KindClose, in.Px, px); err != nil {
			return Proposal{}, err
		}
	}
	a := action{Kind: in.Kind, InstID: in.InstID, Side: in.Side, MarginMode: in.MarginMode, OrdType: in.OrdType, Px: in.Px,
		MarginUSDT: in.MarginUSDT, Leverage: in.Leverage, SL: in.SL, TP: in.TP, Fraction: in.Fraction}
	b, err := s.build(ctx, a, 0, px, okxorder.LongShortMode)
	if err != nil {
		return Proposal{}, err
	}
	if err := s.policy(ctx, a, b.pos, b.posID, 0, true); err != nil {
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
	z := b.order.Sizing()
	res, err := s.db.Exec(`INSERT INTO trade_proposals(created, updated, source, trigger_label, chat_id, inst_id, side, margin, leverage,
		contracts, notional, ref_px, sl, tp, rationale, invalidation, expires_at, status, status_at, mode, kind, ord_type, px, mgn_mode, fraction,
		pos_id, pos_sz, pos_avg, new_avg, liq_px, tpsl, note, order_min) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now.UnixMilli(), now.UnixMilli(), source, label, in.ChatID, in.InstID, in.Side, b.margin, b.lever, z.Contracts, z.NotionalUSDT,
		px, b.sl, b.tp, in.Rationale, in.Invalidation, now.Add(exp).UnixMilli(), StatusPending, now.UnixMilli(), string(s.Mode()),
		in.Kind, in.OrdType, b.px, b.mgnMode, in.Fraction, b.posID, b.pos.Contracts, b.pos.AvgPx, z.NewAvgPx, z.LiqPx, z.TPSL, z.Note, in.OrderMin)
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

// action is what a proposal asks for, as stored (the inputs of build).
type action struct {
	Kind, InstID, Side, MarginMode, OrdType string
	Px, MarginUSDT                          float64
	Leverage                                int
	SL, TP, Fraction                        float64
}

func actionOf(p Proposal) action {
	return action{Kind: p.Kind, InstID: p.InstID, Side: p.Side, MarginMode: p.MarginMode, OrdType: p.OrdType, Px: p.Px, MarginUSDT: p.MarginUSDT,
		Leverage: p.Leverage, SL: p.SL, TP: p.TP, Fraction: p.Fraction}
}

// built is a validated order and what it was built against.
type built struct {
	order   okxorder.Order
	pos     okxacct.Position // add / reduce / close / adjust: the position it acts on
	posID   int64            // its AI position, 0 = the owner's own
	margin  float64          // as stored: open / add margin
	lever   int
	mgnMode string
	sl, tp  float64
	px      float64 // the limit price (rounded)
}

// build validates the action against the price and, for every kind but open, the position it acts on.
// id 0 = at creation (client ids are placeholders; the order is only validated).
func (s *Service) build(ctx context.Context, a action, id int64, px float64, posMode string) (built, error) {
	spec, ok := s.opts.Spec(a.InstID)
	if !ok {
		return built{}, errors.New("instrument metadata unknown")
	}
	ids := strconv.FormatInt(id, 10)
	in := okxorder.Intent{InstID: a.InstID, Side: a.Side, MarginMode: a.MarginMode, OrdType: a.OrdType, Px: a.Px, MarginUSDT: a.MarginUSDT,
		Leverage: a.Leverage, SL: a.SL, TP: a.TP, Fraction: a.Fraction, ClientID: "ctgp" + ids}
	lim := s.opts.Limits.Limits
	b := built{margin: a.MarginUSDT, lever: a.Leverage, sl: a.SL, tp: a.TP}
	var err error
	if a.Kind == KindOpen {
		in.AlgoID = "ctga" + ids
		if in.MarginMode == okxorder.Cross {
			in.Equity = s.equity(ctx)
		}
		b.order, err = okxorder.Build(in, spec, px, posMode, lim)
	} else {
		in.AlgoID = "ctgt" + ids
		if b.pos, b.posID, err = s.position(ctx, a.InstID, a.Side); err != nil {
			return built{}, err
		}
		if in.MarginMode == "" {
			in.MarginMode = b.pos.MgnMode
		}
		if in.Leverage == 0 {
			in.Leverage = int(math.Round(b.pos.Lever))
		}
		if in.MarginMode == okxorder.Cross {
			in.Equity = s.equity(ctx)
		}
		b.lever = in.Leverage
		switch a.Kind {
		case KindAdd:
			b.order, err = okxorder.BuildAdd(in, b.pos, spec, px, posMode, lim)
		case KindReduce, KindClose:
			b.order, err = okxorder.BuildReduce(in, a.Kind == KindClose, b.pos, spec, px, posMode, lim)
			b.margin, b.sl, b.tp = 0, 0, 0
		case KindAdjust:
			b.order, err = okxorder.BuildAdjust(in, b.pos, spec, px, posMode, lim)
			b.margin = 0
		default:
			err = fmt.Errorf("unknown kind %q", a.Kind)
		}
	}
	if err != nil {
		return built{}, err
	}
	b.mgnMode = in.MarginMode
	if b.mgnMode == "" {
		b.mgnMode = okxorder.Isolated
	}
	if a.OrdType == okxorder.Limit {
		b.px = math.Round(a.Px/spec.TickSz) * spec.TickSz
		if z := b.order.Sizing(); z.EntryPx > 0 {
			b.px = z.EntryPx
		}
	}
	return b, nil
}

// equity is the account's available equity (USDT) for the cross-margin liquidation estimate; 0 = unknown.
func (s *Service) equity(ctx context.Context) float64 {
	a := s.account()
	if a == nil {
		return 0
	}
	snap, err := a.Snapshot(ctx, "")
	if err != nil || snap.Equity == nil {
		return 0
	}
	if snap.Equity.AvailEq > 0 {
		return snap.Equity.AvailEq
	}
	return snap.Equity.TotalEq
}

// root is the AI position (an executed open, not closed, filled at least partly) in an instrument and
// side, in the current mode.
func (s *Service) root(inst, side string) (Proposal, bool) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM trade_proposals WHERE kind='open' AND status='executed' AND closed_at=0 AND mode=?
		AND inst_id=? AND side=? ORDER BY id DESC`, string(s.Mode()), inst, side)
	if err != nil {
		return Proposal{}, false
	}
	defer rows.Close()
	for rows.Next() {
		if p, err := scan(rows); err == nil && p.CurContracts > 0 {
			return p, true
		}
	}
	return Proposal{}, false
}

// position is the position an action acts on: in a dry run the AI position ctg simulates, else the
// one on OKX (an AI one is linked by instrument and side).
func (s *Service) position(ctx context.Context, inst, side string) (okxacct.Position, int64, error) {
	r, isRoot := s.root(inst, side)
	if isRoot && s.Mode() == okxorder.DryRun {
		return simulated(r), r.ID, nil
	}
	a := s.account()
	if a == nil {
		return okxacct.Position{}, 0, fmt.Errorf("no %s position in %s (OKX account not configured)", side, inst)
	}
	snap, err := a.Snapshot(ctx, inst)
	if err != nil {
		return okxacct.Position{}, 0, fmt.Errorf("cannot read the OKX account: %v", err)
	}
	for _, x := range snap.Positions {
		if x.Side == side {
			if !isRoot {
				r.ID = 0
			}
			return x, r.ID, nil
		}
	}
	return okxacct.Position{}, 0, fmt.Errorf("no %s position in %s", side, inst)
}

// simulated is a dry-run AI position as okxacct would show it: its size, average and ctg's TP/SL
// (attached at the open, whole-position after any later action).
func simulated(r Proposal) okxacct.Position {
	full := strings.HasPrefix(r.curAlgo, "ctgt")
	t := func(px float64) []okxacct.Trigger {
		if px <= 0 {
			return []okxacct.Trigger{}
		}
		return []okxacct.Trigger{{AlgoID: "dry-" + r.curAlgo, ClientID: r.curAlgo, Px: px, Kind: "oco", Full: full, Contracts: r.CurContracts}}
	}
	return okxacct.Position{InstID: r.InstID, PosSide: r.Side, Side: r.Side, MgnMode: r.MarginMode, Contracts: r.CurContracts,
		AvgPx: r.CurAvgPx, Lever: float64(r.Leverage), TP: t(r.CurTP), SL: t(r.CurSL)}
}

// policy checks the account-level caps (self excluded): one proposal in flight per instrument; an open
// only where neither an AI position, a resting order of ctg's nor a position of the owner's exists, at
// most MaxOpen AI positions, not after the daily loss stop; an add not after the daily loss stop and at
// most MaxAdds per position; add / reduce / adjust not while a ctg order rests in the instrument (a
// close may: it cancels them); at creation at most MaxPending waiting proposals.
func (s *Service) policy(ctx context.Context, a action, pos okxacct.Position, posID, self int64, creating bool) error {
	mode := string(s.Mode())
	var n int
	var other int64
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM trade_proposals WHERE inst_id=? AND id<>? AND status IN ('pending','approved','executing')`,
		a.InstID, self).Scan(&n, &other)
	if n > 0 {
		return fmt.Errorf("proposal #%d for %s is still open; one at a time per instrument", other, a.InstID)
	}
	var resting int64
	s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM trade_proposals WHERE inst_id=? AND id<>? AND status='executed' AND mode=?
		AND ord_state IN ('live','partially_filled')`, a.InstID, self, mode).Scan(&resting)
	switch a.Kind {
	case KindOpen:
		s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM trade_proposals WHERE inst_id=? AND id<>? AND kind='open' AND status='executed'
			AND closed_at=0 AND mode=?`, a.InstID, self, mode).Scan(&n, &other)
		if n > 0 {
			return fmt.Errorf("AI position #%d in %s is open; act on it with add / reduce / close / adjust", other, a.InstID)
		}
		if resting > 0 {
			return fmt.Errorf("ctg's limit order #%d in %s is still resting", resting, a.InstID)
		}
		s.db.QueryRow(`SELECT COUNT(*) FROM trade_proposals WHERE id<>? AND mode=? AND kind='open' AND
			(status IN ('approved','executing') OR (status='executed' AND closed_at=0))`, self, mode).Scan(&n)
		if n >= s.opts.Limits.MaxOpen {
			return fmt.Errorf("%d AI positions are open; the cap is %d", n, s.opts.Limits.MaxOpen)
		}
	case KindAdd:
		if resting > 0 {
			return fmt.Errorf("ctg's limit order #%d in %s is still resting; wait for it or let it expire", resting, a.InstID)
		}
		since := pos.CTime
		s.db.QueryRow(`SELECT COUNT(*) FROM trade_proposals WHERE id<>? AND mode=? AND kind='add' AND inst_id=? AND side=? AND pos_id=?
			AND status IN ('approved','executing','executed') AND created>=?`, self, mode, a.InstID, a.Side, posID, since).Scan(&n)
		if n >= s.opts.Limits.MaxAdds {
			return fmt.Errorf("this position has had %d adds; the cap is %d", n, s.opts.Limits.MaxAdds)
		}
	case KindReduce, KindAdjust:
		if resting > 0 {
			return fmt.Errorf("ctg's limit order #%d in %s is still resting; wait for it, or close the position", resting, a.InstID)
		}
	}
	if creating {
		s.db.QueryRow(`SELECT COUNT(*) FROM trade_proposals WHERE status='pending'`).Scan(&n)
		if n >= s.opts.Limits.MaxPending {
			return fmt.Errorf("%d proposals are already waiting for the owner; the cap is %d", n, s.opts.Limits.MaxPending)
		}
	}
	if a.Kind == KindOpen || a.Kind == KindAdd { // reducing risk is always allowed
		if loss := s.dailyPnl(); loss <= -s.opts.Limits.DailyLossUSDT {
			return fmt.Errorf("daily loss stop: AI positions lost %.2f USDT today (cap %g); no new risk until tomorrow (UTC+8)", -loss, s.opts.Limits.DailyLossUSDT)
		}
	}
	if a.Kind == KindOpen {
		if acct := s.account(); acct != nil {
			snap, err := acct.Snapshot(ctx, a.InstID)
			if err != nil {
				return fmt.Errorf("cannot read the OKX account: %v", err)
			}
			if len(snap.Positions) > 0 {
				return fmt.Errorf("the owner already holds a %s position in %s; propose add / reduce / close / adjust on it instead of a new open", snap.Positions[0].Side, a.InstID)
			}
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

// ---------- execution ----------

// execute re-validates an approved proposal against the live price, the position and the caps, then
// sends it (dry run: builds and records it). Any check failing invalidates it; nothing is sent. The
// chat that proposed it is told what happened (steer).
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
			s.queueSteer(id, StatusInvalidated)
		}
		return s.get(id)
	}
	px, err := s.freshQuote(p.InstID)
	if err != nil {
		return invalid(err.Error())
	}
	risky := p.Kind == KindOpen || p.Kind == KindAdd
	if drift := math.Abs(px-p.RefPx) / p.RefPx * 100; risky && p.OrdType == okxorder.Market && drift > s.opts.Limits.MaxDriftPct {
		return invalid(fmt.Sprintf("price moved %.2f%% since the proposal (%s → %s); the cap is %g%%", drift,
			okxorder.FormatPx(p.RefPx), okxorder.FormatPx(px), s.opts.Limits.MaxDriftPct))
	}
	mode, err := s.positionMode(ctx)
	if err != nil {
		return invalid(err.Error())
	}
	b, err := s.build(ctx, actionOf(p), id, px, mode)
	if err != nil {
		return invalid(err.Error())
	}
	if p.Kind != KindOpen {
		if b.posID != p.PosID {
			return invalid("the position changed since the proposal (it is not the same AI position any more)")
		}
		if p.Kind == KindAdd && (b.mgnMode != p.MarginMode || b.lever != p.Leverage) {
			return invalid(fmt.Sprintf("the position is now %s %d×, not %s %d× as proposed", b.mgnMode, b.lever, p.MarginMode, p.Leverage))
		}
	}
	if err := s.policy(ctx, actionOf(p), b.pos, b.posID, id, false); err != nil {
		return invalid(err.Error())
	}
	ectx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	if p.Kind == KindClose { // a close first cancels ctg's resting orders in the instrument (an add must not fill after it)
		for _, r := range s.restingIn(p.InstID) {
			s.cancelResting(ectx, r, fmt.Sprintf("平仓提议 #%d 执行前撤销", id))
		}
	}
	res, perr := s.orders.Place(ectx, b.order)
	reqJSON, _ := json.Marshal(res.Requests)
	respJSON := []byte{}
	if len(res.Responses) > 0 {
		respJSON, _ = json.Marshal(res.Responses)
	}
	z := b.order.Sizing()
	set := map[string]any{"request": string(reqJSON), "response": string(respJSON), "exec_px": px, "contracts": z.Contracts,
		"notional": z.NotionalUSDT, "tpsl": z.TPSL, "pos_sz": z.PosContracts, "new_avg": z.NewAvgPx, "liq_px": z.LiqPx, "note": z.Note}
	if p.Kind == KindOpen {
		set["pos_id"] = id
	}
	if perr != nil {
		set["error"] = perr.Error()
		if ok, _ := s.transition(id, []string{StatusExecuting}, StatusFailed, set, ""); ok {
			s.audit(id, StatusFailed, perr.Error(), "okx")
			s.emit(StatusFailed, id)
			s.queueSteer(id, StatusFailed)
		}
		p, _ = s.get(id)
		return p, nil
	}
	set["mode"], set["ord_id"], set["algo_ids"] = string(res.Mode), res.OrdID, strings.Join(z.AlgoIDs, ",")
	if len(res.Warnings) > 0 {
		set["error"] = "警告：" + strings.Join(res.Warnings, "；")
	}
	limit := p.OrdType == okxorder.Limit
	if limit {
		set["ord_state"], set["order_until"] = OrdLive, s.now().Add(time.Duration(p.OrderMin)*time.Minute).UnixMilli()
	}
	if ok, _ := s.transition(id, []string{StatusExecuting}, StatusExecuted, set, ""); ok {
		text := fmt.Sprintf("%s %s %s", modeText(string(res.Mode)), kindText(p.Kind), sideText(p.Side))
		switch {
		case p.Kind == KindAdjust:
			text += " · " + z.TPSL
		case limit:
			text += fmt.Sprintf(" 限价 %s 张 @%s 已挂出，%d 分钟内未成交自动撤单", fmtNum(z.Contracts), okxorder.FormatPx(p.Px), p.OrderMin)
		default:
			text += fmt.Sprintf(" 市价 %s 张 @≈%s", fmtNum(z.Contracts), okxorder.FormatPx(px))
		}
		if res.OrdID != "" {
			text += " · ordId " + res.OrdID
		}
		s.audit(id, StatusExecuted, text, "okx")
		for _, w := range res.Warnings {
			s.audit(id, "warning", w, "okx")
		}
		s.emit(StatusExecuted, id)
		s.queueSteer(id, StatusExecuted) // composed when delivered, so it shows the position after applyFill
		if !limit {
			q, _ := s.get(id)
			s.applyFill(ectx, q, z.Contracts, px)
		}
	}
	return s.get(id)
}

// restingIn is ctg's limit orders resting in an instrument.
func (s *Service) restingIn(inst string) []Proposal {
	rows, err := s.db.Query(`SELECT `+cols+` FROM trade_proposals WHERE inst_id=? AND status='executed' AND mode=? AND ord_state IN ('live','partially_filled')`,
		inst, string(s.Mode()))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		if p, err := scan(rows); err == nil {
			out = append(out, p)
		}
	}
	return out
}
