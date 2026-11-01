// Package okxorder builds and places the real OKX orders the owner approved (2026-11-01): actions on a
// USDT-margined perpetual position, each one an approved proposal (internal/realtrade):
//
//   - open: set the leverage, then a market or limit order with both a take-profit and a stop-loss
//     attached (triggered on the last price, closed at market), isolated or cross margin;
//   - add (补仓): a market or limit order on an existing position of the same side and margin mode;
//   - reduce (减仓) / close (平仓): a reduce-only order for part of the position, or all of it (a market
//     close uses the close-position endpoint);
//   - adjust: move the position's take-profit and stop-loss.
//
// Every action other than open leaves the position with ONE take-profit/stop-loss that closes the whole
// position whatever its size (an OCO with closeFraction=1): an existing whole-position one is amended,
// otherwise ctg places one and cancels the size-bound ones ctg attached earlier. Orders of anyone else
// are never cancelled; an amended whole-position one is named in the proposal. Resting limit orders are
// cancelled only by their ctg client id. Nothing else is expressible — no transfers or withdrawals —
// and the live client sends only the allow-listed paths with allow-listed body keys (live.go). An
// Order can only be made by the Build functions, which validate it, and the dry-run Client (the
// default, see ModeFromEnv) has no HTTP client at all. Requests are signed through okxacct.Config (one
// key, one proxy-bound transport); see docs/trading-design.md §7.
package okxorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"crypto-trader-go/internal/okxacct"
)

// Mode is dryrun (build, validate and record; never send) or live.
type Mode string

const (
	DryRun Mode = "dryrun"
	Live   Mode = "live"
)

// ModeFromEnv reads CTG_TRADE_MODE: exactly "live" is live, anything else (unset, typos) is a dry run.
// It is the only way to switch to live: no API, agent or UI path can.
func ModeFromEnv() Mode {
	if os.Getenv("CTG_TRADE_MODE") == string(Live) {
		return Live
	}
	return DryRun
}

// Position modes of the OKX account (GET /api/v5/account/config posMode).
const (
	LongShortMode = "long_short_mode"
	NetMode       = "net_mode"
)

// Proposal kinds, margin modes and order types.
const (
	KindOpen   = "open"
	KindAdd    = "add"
	KindReduce = "reduce"
	KindClose  = "close"
	KindAdjust = "adjust"

	Isolated = "isolated"
	Cross    = "cross"

	Market = "market"
	Limit  = "limit"
)

// Spec is the instrument metadata sizing needs (public /public/instruments).
type Spec struct {
	CtVal, LotSz, MinSz, TickSz, MaxLever float64
}

// Limits bound a single order. Zero fields take the conservative defaults (DefaultLimits).
type Limits struct {
	MinLeverage   int     `json:"minLeverage"` // the owner's floor (0 = 1×)
	MaxLeverage   int     `json:"maxLeverage"`
	MinMarginUSDT float64 `json:"minMarginUsdt"` // the owner's floor per trade (0 = none)
	MaxMarginUSDT float64 `json:"maxMarginUsdt"` // margin per open / add
	MinStopPct    float64 `json:"minStopPct"`    // stop distance from the entry, % (noise floor)
	MaxStopPct    float64 `json:"maxStopPct"`
	MaxTargetPct  float64 `json:"maxTargetPct"`
	MinRR         float64 `json:"minRR"`       // target distance / stop distance
	MaxLimitPct   float64 `json:"maxLimitPct"` // a limit price at most this far from the price, %
}

var DefaultLimits = Limits{MaxLeverage: 5, MaxMarginUSDT: 50, MinStopPct: 0.3, MaxStopPct: 10, MaxTargetPct: 40, MinRR: 1, MaxLimitPct: 5}

// WithDefaults fills zero fields from DefaultLimits.
func (l Limits) WithDefaults() Limits {
	d := DefaultLimits
	if l.MaxLeverage <= 0 {
		l.MaxLeverage = d.MaxLeverage
	}
	if l.MinLeverage <= 0 || l.MinLeverage > l.MaxLeverage {
		l.MinLeverage = 1
	}
	if l.MaxMarginUSDT <= 0 {
		l.MaxMarginUSDT = d.MaxMarginUSDT
	}
	if l.MinMarginUSDT < 0 || l.MinMarginUSDT > l.MaxMarginUSDT {
		l.MinMarginUSDT = 0
	}
	if l.MinStopPct <= 0 {
		l.MinStopPct = d.MinStopPct
	}
	if l.MaxStopPct <= 0 {
		l.MaxStopPct = d.MaxStopPct
	}
	if l.MaxTargetPct <= 0 {
		l.MaxTargetPct = d.MaxTargetPct
	}
	if l.MinRR <= 0 {
		l.MinRR = d.MinRR
	}
	if l.MaxLimitPct <= 0 {
		l.MaxLimitPct = d.MaxLimitPct
	}
	return l
}

// Intent is what a proposal asks for. Open and add use margin, leverage and both exits; reduce and
// close use Fraction (close: all); adjust uses the exits only.
type Intent struct {
	InstID     string
	Side       string  // long | short: the position's side
	MarginMode string  // isolated (default) | cross
	OrdType    string  // market (default) | limit
	Px         float64 // limit price
	MarginUSDT float64
	Leverage   int
	SL, TP     float64
	Fraction   float64 // reduce: the share of the position to close, 0–1
	ClientID   string  // clOrdId: 1–32 letters / digits, makes a retried order idempotent on OKX
	AlgoID     string  // algoClOrdId of the TP/SL this order attaches (open) or places (other kinds)
	// Equity is the account's available equity in USDT for the cross-margin liquidation estimate; 0 =
	// unknown, the check is skipped and Sizing.Note says so.
	Equity float64
}

func (in Intent) normalized() Intent {
	if in.MarginMode == "" {
		in.MarginMode = Isolated
	}
	if in.OrdType == "" {
		in.OrdType = Market
	}
	return in
}

// Sizing is what an order amounts to at a given price.
type Sizing struct {
	Contracts    float64 `json:"contracts"`
	NotionalUSDT float64 `json:"notionalUsdt"`
	MarginUSDT   float64 `json:"marginUsdt"` // notional / leverage (OKX adds fees on top)
	EntryPx      float64 `json:"entryPx,omitempty"`
	StopPct      float64 `json:"stopPct"` // distance from the entry, %
	TargetPct    float64 `json:"targetPct"`
	RR           float64 `json:"rr"`
	PosContracts float64 `json:"posContracts,omitempty"` // add / reduce / close / adjust: the position before
	NewAvgPx     float64 `json:"newAvgPx,omitempty"`     // add: the average entry once filled
	LiqPx        float64 `json:"liqPx,omitempty"`        // estimated liquidation price after the order (0 = unknown)
	TPSL         string  `json:"tpsl,omitempty"`         // what happens to the position's TP/SL, in Chinese
	Note         string  `json:"note,omitempty"`         // a check that could not be made, in Chinese
	// AlgoIDs are ctg's own TP/SL orders on the position (close by limit: cancelled once it filled).
	AlgoIDs []string `json:"algoIds,omitempty"`
}

// Order is a validated list of requests. Its fields are unexported: the Build functions are the only
// constructors, so an Order that skipped validation cannot exist.
type Order struct {
	kind, instID string
	reqs         []Request
	sizing       Sizing
}

func (o Order) InstID() string { return o.instID }
func (o Order) Kind() string   { return o.kind }
func (o Order) Sizing() Sizing { return o.sizing }

var (
	clientID = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)
	// ownID is a ctg client id: ctgp<id> orders, ctga<id> attached and ctgt<id> whole-position TP/SL.
	ownID = regexp.MustCompile(`^ctg[pat][0-9]{1,24}$`)
)

// Owned reports whether a client order / algo id is one ctg set.
func Owned(clientID string) bool { return ownID.MatchString(clientID) }

func basics(in Intent) error {
	switch {
	case !strings.HasSuffix(in.InstID, "-USDT-SWAP"):
		return errors.New("only USDT-margined perpetuals (…-USDT-SWAP)")
	case in.Side != "long" && in.Side != "short":
		return errors.New("side must be long or short")
	case in.MarginMode != Isolated && in.MarginMode != Cross:
		return errors.New("marginMode must be isolated or cross")
	case in.OrdType != Market && in.OrdType != Limit:
		return errors.New("orderType must be market or limit")
	case in.OrdType == Limit && !(in.Px > 0):
		return errors.New("a limit order needs its price")
	}
	return nil
}

// entry is the price the order fills at: the limit price, or the current price for a market order.
func entry(in Intent, mark float64, lim Limits) (float64, error) {
	if !(mark > 0) || math.IsInf(mark, 0) {
		return 0, errors.New("no current price")
	}
	if in.OrdType != Limit {
		return mark, nil
	}
	if d := math.Abs(in.Px-mark) / mark * 100; d > lim.MaxLimitPct {
		return 0, fmt.Errorf("limit price %s is %.2f%% from the price %s; at most %g%%", FormatPx(in.Px), d, FormatPx(mark), lim.MaxLimitPct)
	}
	return in.Px, nil
}

// Resting checks that a new limit order would rest rather than fill at once: a buy (opening a long,
// closing a short) at or below the price, a sell at or above it. A marketable limit is a market order
// in disguise; propose that instead. Checked when proposing only: by approval the price may have
// crossed the level, and the order then fills at once at a better price.
func Resting(side string, closing bool, px, mark float64) error {
	buy := (side == "long") != closing
	switch {
	case buy && px > mark*1.0005:
		return fmt.Errorf("a buy limit at %s is above the price %s and would fill at once; use a market order or a lower price", FormatPx(px), FormatPx(mark))
	case !buy && px < mark*0.9995:
		return fmt.Errorf("a sell limit at %s is below the price %s and would fill at once; use a market order or a higher price", FormatPx(px), FormatPx(mark))
	}
	return nil
}

// exits checks the stop and target: on the losing / winning side of both the entry and the current
// price (so neither triggers at once), distances from the entry within the limits and, when rr, reward
// ≥ MinRR × risk.
func exits(in Intent, ref, mark float64, lim Limits, rr bool) (Sizing, error) {
	var z Sizing
	if !(in.SL > 0) || !(in.TP > 0) {
		return z, errors.New("both a stop-loss and a take-profit price are required")
	}
	long := in.Side == "long"
	if long && !(in.SL < math.Min(ref, mark) && in.TP > math.Max(ref, mark)) {
		return z, fmt.Errorf("a long needs stop-loss below and take-profit above the entry %s and the current price %s", FormatPx(ref), FormatPx(mark))
	}
	if !long && !(in.SL > math.Max(ref, mark) && in.TP < math.Min(ref, mark)) {
		return z, fmt.Errorf("a short needs stop-loss above and take-profit below the entry %s and the current price %s", FormatPx(ref), FormatPx(mark))
	}
	z.EntryPx = ref
	z.StopPct = math.Abs(ref-in.SL) / ref * 100
	z.TargetPct = math.Abs(in.TP-ref) / ref * 100
	z.RR = z.TargetPct / z.StopPct
	switch {
	case z.StopPct < lim.MinStopPct || z.StopPct > lim.MaxStopPct:
		return z, fmt.Errorf("stop-loss is %.2f%% from the entry; allowed %g–%g%%", z.StopPct, lim.MinStopPct, lim.MaxStopPct)
	case z.TargetPct < lim.MinStopPct || z.TargetPct > lim.MaxTargetPct:
		return z, fmt.Errorf("take-profit is %.2f%% from the entry; allowed %g–%g%%", z.TargetPct, lim.MinStopPct, lim.MaxTargetPct)
	case rr && z.RR < lim.MinRR-1e-9:
		return z, fmt.Errorf("reward:risk is %.2f; at least %g required", z.RR, lim.MinRR)
	}
	return z, nil
}

func leverageMargin(in Intent, lim Limits) error {
	if in.Leverage < max(lim.MinLeverage, 1) || in.Leverage > lim.MaxLeverage {
		return fmt.Errorf("leverage must be %d–%d×", max(lim.MinLeverage, 1), lim.MaxLeverage)
	}
	if !(in.MarginUSDT > 0) || in.MarginUSDT < lim.MinMarginUSDT || in.MarginUSDT > lim.MaxMarginUSDT {
		if lim.MinMarginUSDT > 0 {
			return fmt.Errorf("margin must be %g–%g USDT", lim.MinMarginUSDT, lim.MaxMarginUSDT)
		}
		return fmt.Errorf("margin must be above 0 and at most %g USDT", lim.MaxMarginUSDT)
	}
	return nil
}

// liqDistance is the estimated distance from the entry to liquidation in %: isolated ≈ 1/leverage
// minus the maintenance margin; cross ≈ the available equity over the position's notional minus the
// maintenance margin (0 when the equity is unknown).
func liqDistance(mode string, lever int, equity, notional float64) float64 {
	if mode == Cross {
		if !(equity > 0) || !(notional > 0) {
			return 0
		}
		return (equity/notional - 0.005) * 100
	}
	return 100/float64(lever) - 0.5
}

const crossUnknown = "全仓强平价取决于账户可用权益，当前读不到，未校验止损与强平的距离"

// liqCheck keeps the stop well inside the liquidation distance (80%).
func liqCheck(z *Sizing, mode string, lever int, equity, notional, from float64, long bool) error {
	d := liqDistance(mode, lever, equity, notional)
	if d == 0 {
		z.Note = crossUnknown
		return nil
	}
	if d <= 0 {
		return errors.New("the account's available equity does not cover this cross position")
	}
	if z.StopPct > d*0.8 {
		if mode == Cross {
			return fmt.Errorf("in cross margin the stop (%.2f%%) is too close to the estimated liquidation (≈%.1f%% with %.0f USDT available); smaller size", z.StopPct, d, equity)
		}
		return fmt.Errorf("at %d× the stop (%.2f%%) is too close to liquidation (≈%.1f%%); lower the leverage", lever, z.StopPct, d)
	}
	if long {
		z.LiqPx = max(from*(1-d/100), 0) // 0: the equity covers a move to zero
	} else {
		z.LiqPx = from * (1 + d/100)
	}
	return nil
}

// Check validates an open without sizing: side, margin mode, order type, leverage and margin within the
// caps, the exits (exits) against the entry and the current price, and the stop well before the
// estimated liquidation price.
func Check(in Intent, mark float64, lim Limits) (Sizing, error) {
	lim = lim.WithDefaults()
	in = in.normalized()
	var z Sizing
	if err := basics(in); err != nil {
		return z, err
	}
	if err := leverageMargin(in, lim); err != nil {
		return z, err
	}
	ref, err := entry(in, mark, lim)
	if err != nil {
		return z, err
	}
	if z, err = exits(in, ref, mark, lim, true); err != nil {
		return z, err
	}
	notional := in.MarginUSDT * float64(in.Leverage)
	return z, liqCheck(&z, in.MarginMode, in.Leverage, in.Equity, notional, ref, in.Side == "long")
}

func prepare(in Intent, spec Spec, posMode string) (Intent, error) {
	in = in.normalized()
	if !(spec.CtVal > 0) || !(spec.LotSz > 0) || !(spec.TickSz > 0) {
		return in, errors.New("instrument metadata unknown")
	}
	if in.ClientID != "" && !clientID.MatchString(in.ClientID) {
		return in, errors.New("client order id must be 1–32 letters or digits")
	}
	if in.AlgoID != "" && !Owned(in.AlgoID) {
		return in, errors.New("TP/SL client id must be a ctg id")
	}
	if posMode != LongShortMode && posMode != NetMode {
		return in, fmt.Errorf("unknown account position mode %q", posMode)
	}
	in.SL, in.TP = roundTo(in.SL, spec.TickSz), roundTo(in.TP, spec.TickSz)
	if in.OrdType == Limit {
		in.Px = roundTo(in.Px, spec.TickSz)
	}
	return in, nil
}

func lotsFor(margin float64, lever int, px float64, spec Spec) float64 {
	return math.Floor(margin*float64(lever)/(px*spec.CtVal)/spec.LotSz+1e-9) * spec.LotSz
}

func (in Intent) orderSide(closing bool) string {
	if (in.Side == "long") != closing {
		return "buy"
	}
	return "sell"
}

// Build validates an open against the current price and the instrument and returns its requests:
// the leverage, then the order with TP and SL attached. Size = margin × leverage / (entry × ctVal)
// contracts, rounded down to lotSz, at least minSz.
func Build(in Intent, spec Spec, mark float64, posMode string, lim Limits) (Order, error) {
	lim = lim.WithDefaults()
	if spec.MaxLever > 0 && float64(in.Leverage) > spec.MaxLever {
		return Order{}, fmt.Errorf("%s allows at most %g× leverage", in.InstID, spec.MaxLever)
	}
	in, err := prepare(in, spec, posMode)
	if err != nil {
		return Order{}, err
	}
	z, err := Check(in, mark, lim)
	if err != nil {
		return Order{}, err
	}
	contracts := lotsFor(in.MarginUSDT, in.Leverage, z.EntryPx, spec)
	if contracts <= 0 || contracts < spec.MinSz-1e-12 {
		need := math.Max(spec.MinSz, spec.LotSz) * spec.CtVal * z.EntryPx / float64(in.Leverage)
		return Order{}, fmt.Errorf("margin %g USDT at %d× is below the minimum order size (needs ≈ %.2f USDT)", in.MarginUSDT, in.Leverage, need)
	}
	z.Contracts = contracts
	z.NotionalUSDT = contracts * spec.CtVal * z.EntryPx
	z.MarginUSDT = z.NotionalUSDT / float64(in.Leverage)
	if z.MarginUSDT > lim.MaxMarginUSDT*1.0001 {
		return Order{}, fmt.Errorf("margin %.2f USDT is above the cap %g", z.MarginUSDT, lim.MaxMarginUSDT)
	}
	ps := posSide(in.Side, posMode)
	levPosSide := ps
	if in.MarginMode == Cross {
		levPosSide = "" // cross leverage is per instrument, both sides
	}
	lev, _ := json.Marshal(leverageBody{InstID: in.InstID, Lever: strconv.Itoa(in.Leverage), MgnMode: in.MarginMode, PosSide: levPosSide})
	body := orderBody{InstID: in.InstID, TdMode: in.MarginMode, Side: in.orderSide(false), PosSide: ps, OrdType: in.OrdType,
		Sz: formatStep(contracts, spec.LotSz), ClOrdID: in.ClientID, AttachAlgoOrds: []attachedAlgo{{AttachAlgoClOrdID: in.AlgoID,
			TpTriggerPx: formatStep(in.TP, spec.TickSz), TpOrdPx: "-1", TpTriggerPxType: "last",
			SlTriggerPx: formatStep(in.SL, spec.TickSz), SlOrdPx: "-1", SlTriggerPxType: "last"}}}
	if in.OrdType == Limit {
		body.Px = formatStep(in.Px, spec.TickSz)
	}
	ord, _ := json.Marshal(body)
	z.TPSL = "止盈止损随开仓单挂出（按成交张数）"
	return Order{kind: KindOpen, instID: in.InstID, reqs: []Request{{Method: "POST", Path: pathLeverage, Body: lev}, {Method: "POST", Path: pathOrder, Body: ord}},
		sizing: z}, nil
}

// position checks that pos is the intent's open position.
func position(in Intent, pos okxacct.Position) error {
	switch {
	case pos.InstID != in.InstID || !(pos.Contracts > 0):
		return fmt.Errorf("no open position in %s", in.InstID)
	case pos.Side != in.Side:
		return fmt.Errorf("the position in %s is %s, not %s", in.InstID, pos.Side, in.Side)
	}
	return nil
}

// nearestSL is the position's effective stop (the one closest to the price), 0 when it has none.
func nearestSL(pos okxacct.Position) float64 {
	if len(pos.SL) == 0 {
		return 0
	}
	best := pos.SL[0].Px
	for _, t := range pos.SL {
		if (pos.Side == "long" && t.Px > best) || (pos.Side == "short" && t.Px < best) {
			best = t.Px
		}
	}
	return best
}

// noWider refuses a stop farther from the price than the position's current one: an add or an
// adjustment never moves the invalidation away (no averaging down past it, no hoping).
func noWider(in Intent, pos okxacct.Position, tick float64) error {
	cur := nearestSL(pos)
	if cur == 0 {
		return nil
	}
	if (in.Side == "long" && in.SL < cur-tick/2) || (in.Side == "short" && in.SL > cur+tick/2) {
		return fmt.Errorf("the new stop %s is farther away than the position's stop %s; a stop is only ever moved closer", FormatPx(in.SL), FormatPx(cur))
	}
	return nil
}

// BuildAdd validates an add to an existing position (same side, its margin mode and leverage) and
// returns its requests: the position's TP/SL moved to the new levels for the whole position (tpsl),
// then the order (no leverage change, nothing attached). The stop is checked against the new average
// entry and the estimated liquidation, and may not be wider than the position's current stop.
func BuildAdd(in Intent, pos okxacct.Position, spec Spec, mark float64, posMode string, lim Limits) (Order, error) {
	lim = lim.WithDefaults()
	in, err := prepare(in, spec, posMode)
	if err != nil {
		return Order{}, err
	}
	if err := position(in, pos); err != nil {
		return Order{}, err
	}
	if pos.MgnMode != "" && pos.MgnMode != in.MarginMode {
		return Order{}, fmt.Errorf("the position is in %s margin; an add uses the same", pos.MgnMode)
	}
	if lev := int(math.Round(pos.Lever)); lev > 0 && lev != in.Leverage {
		return Order{}, fmt.Errorf("the position is at %d×; an add uses the same leverage", lev)
	}
	z, err := Check(in, mark, lim)
	if err != nil {
		return Order{}, err
	}
	if err := noWider(in, pos, spec.TickSz); err != nil {
		return Order{}, err
	}
	contracts := lotsFor(in.MarginUSDT, in.Leverage, z.EntryPx, spec)
	if contracts <= 0 || contracts < spec.MinSz-1e-12 {
		return Order{}, fmt.Errorf("margin %g USDT at %d× is below the minimum order size", in.MarginUSDT, in.Leverage)
	}
	z.Contracts = contracts
	z.NotionalUSDT = contracts * spec.CtVal * z.EntryPx
	z.MarginUSDT = z.NotionalUSDT / float64(in.Leverage)
	z.PosContracts = pos.Contracts
	z.NewAvgPx = (pos.Contracts*pos.AvgPx + contracts*z.EntryPx) / (pos.Contracts + contracts)
	z.Note, z.LiqPx = "", 0
	// the stop against the whole position: its distance from the new average within the liquidation margin
	stop := math.Abs(z.NewAvgPx-in.SL) / z.NewAvgPx * 100
	saved := z.StopPct
	z.StopPct = stop
	total := (pos.Contracts + contracts) * spec.CtVal * z.NewAvgPx
	if err := liqCheck(&z, in.MarginMode, in.Leverage, in.Equity, total, z.NewAvgPx, in.Side == "long"); err != nil {
		return Order{}, err
	}
	z.StopPct = saved
	tp, text, err := tpslPlan(in, pos, spec, posMode)
	if err != nil {
		return Order{}, err
	}
	z.TPSL = text
	body := orderBody{InstID: in.InstID, TdMode: in.MarginMode, Side: in.orderSide(false), PosSide: posSide(in.Side, posMode), OrdType: in.OrdType,
		Sz: formatStep(contracts, spec.LotSz), ClOrdID: in.ClientID}
	if in.OrdType == Limit {
		body.Px = formatStep(in.Px, spec.TickSz)
	}
	ord, _ := json.Marshal(body)
	return Order{kind: KindAdd, instID: in.InstID, reqs: append(tp, Request{Method: "POST", Path: pathOrder, Body: ord}), sizing: z}, nil
}

// BuildReduce validates a partial (reduce: Fraction of the position, rounded down to lotSz, at least
// minSz, less than all) or full close (close) and returns its requests. A full market close is OKX's
// close-position (exactly the whole position), followed by cancelling ctg's TP/SL on it (best effort);
// everything else is a reduce-only order. Before a partial close, size-bound TP/SL that ctg attached
// are replaced by one for the whole position at the same levels, so the rest stays covered.
func BuildReduce(in Intent, full bool, pos okxacct.Position, spec Spec, mark float64, posMode string, lim Limits) (Order, error) {
	lim = lim.WithDefaults()
	in, err := prepare(in, spec, posMode)
	if err != nil {
		return Order{}, err
	}
	if err := basics(in); err != nil {
		return Order{}, err
	}
	if err := position(in, pos); err != nil {
		return Order{}, err
	}
	if pos.MgnMode == Isolated || pos.MgnMode == Cross {
		in.MarginMode = pos.MgnMode
	}
	ref, err := entry(in, mark, lim)
	if err != nil {
		return Order{}, err
	}
	kind, sz := KindClose, pos.Contracts
	if !full {
		kind = KindReduce
		if !(in.Fraction > 0 && in.Fraction < 1) {
			return Order{}, errors.New("a reduce closes a fraction of the position between 0 and 1 (use close for all of it)")
		}
		sz = math.Floor(pos.Contracts*in.Fraction/spec.LotSz+1e-9) * spec.LotSz
		if sz <= 0 || sz < spec.MinSz-1e-12 {
			return Order{}, fmt.Errorf("%.0f%% of %s contracts is below the minimum order size", in.Fraction*100, fmtNum(pos.Contracts))
		}
		if sz >= pos.Contracts-spec.LotSz/2 {
			return Order{}, errors.New("that is the whole position; propose a close instead")
		}
	}
	z := Sizing{Contracts: sz, NotionalUSDT: sz * spec.CtVal * ref, EntryPx: ref, PosContracts: pos.Contracts}
	if pos.Lever > 0 {
		z.MarginUSDT = z.NotionalUSDT / pos.Lever
	}
	ps := posSide(in.Side, posMode)
	var reqs []Request
	own := ownedAlgos(pos)
	if full {
		for _, a := range own {
			z.AlgoIDs = append(z.AlgoIDs, a.id)
		}
		z.TPSL = "平仓后 OKX 撤掉整仓止盈止损；ctg 挂的止盈止损随后一并撤销"
	} else {
		reqs, z.TPSL = keepCovered(in, pos, spec, posMode)
	}
	if full && in.OrdType == Market {
		body, _ := json.Marshal(closeBody{InstID: in.InstID, MgnMode: in.MarginMode, PosSide: ps, AutoCxl: false, ClOrdID: in.ClientID})
		reqs = append(reqs, Request{Method: "POST", Path: pathClosePos, Body: body})
		if len(own) > 0 {
			reqs = append(reqs, cancelAlgosReq(in.InstID, z.AlgoIDs))
		}
		return Order{kind: kind, instID: in.InstID, reqs: reqs, sizing: z}, nil
	}
	body := orderBody{InstID: in.InstID, TdMode: in.MarginMode, Side: in.orderSide(true), PosSide: ps, OrdType: in.OrdType,
		Sz: formatStep(sz, spec.LotSz), ClOrdID: in.ClientID}
	if posMode == NetMode {
		body.ReduceOnly = true
	}
	if in.OrdType == Limit {
		body.Px = formatStep(in.Px, spec.TickSz)
	}
	ord, _ := json.Marshal(body)
	return Order{kind: kind, instID: in.InstID, reqs: append(reqs, Request{Method: "POST", Path: pathOrder, Body: ord}), sizing: z}, nil
}

// BuildAdjust validates new TP/SL levels for a position (sides against the current price, distances
// within the limits, no reward:risk floor, the stop never wider than now) and returns the requests
// that put them on the whole position (tpsl).
func BuildAdjust(in Intent, pos okxacct.Position, spec Spec, mark float64, posMode string, lim Limits) (Order, error) {
	lim = lim.WithDefaults()
	in, err := prepare(in, spec, posMode)
	if err != nil {
		return Order{}, err
	}
	in.OrdType = Market
	if err := basics(in); err != nil {
		return Order{}, err
	}
	if err := position(in, pos); err != nil {
		return Order{}, err
	}
	if pos.MgnMode == Isolated || pos.MgnMode == Cross {
		in.MarginMode = pos.MgnMode
	}
	if !(mark > 0) {
		return Order{}, errors.New("no current price")
	}
	z, err := exits(in, mark, mark, lim, false)
	if err != nil {
		return Order{}, err
	}
	if err := noWider(in, pos, spec.TickSz); err != nil {
		return Order{}, err
	}
	z.PosContracts, z.Contracts = pos.Contracts, pos.Contracts
	reqs, text, err := tpslPlan(in, pos, spec, posMode)
	if err != nil {
		return Order{}, err
	}
	z.TPSL = text
	return Order{kind: KindAdjust, instID: in.InstID, reqs: reqs, sizing: z}, nil
}

// CancelOrder cancels one of ctg's resting orders (an expired limit) by its client id.
func CancelOrder(instID, clOrdID string) (Order, error) {
	if !strings.HasPrefix(clOrdID, "ctgp") || !Owned(clOrdID) || !strings.HasSuffix(instID, "-USDT-SWAP") {
		return Order{}, errors.New("only ctg's own orders can be cancelled")
	}
	body, _ := json.Marshal(cancelOrderBody{InstID: instID, ClOrdID: clOrdID})
	return Order{kind: "cancel", instID: instID, reqs: []Request{{Method: "POST", Path: pathCancelOrder, Body: body}}}, nil
}

// CancelAlgos cancels ctg's own TP/SL orders by algo id (after a full close by limit filled). The ids
// come from Sizing.AlgoIDs, which only lists algos with a ctg client id.
func CancelAlgos(instID string, ids []string) (Order, error) {
	if len(ids) == 0 || !strings.HasSuffix(instID, "-USDT-SWAP") {
		return Order{}, errors.New("nothing to cancel")
	}
	r := cancelAlgosReq(instID, ids)
	r.Optional = false
	return Order{kind: "cancel", instID: instID, reqs: []Request{r}}, nil
}

// ---- the position's TP/SL ----

// algo is one TP/SL order on a position (OKX lists its TP and SL leg under the same algoId).
type algo struct {
	id, client string
	tp, sl     float64
	full       bool
}

func algosOf(pos okxacct.Position) []algo {
	var out []algo
	idx := map[string]int{}
	for leg, list := range [][]okxacct.Trigger{pos.TP, pos.SL} {
		for _, t := range list {
			i, ok := idx[t.AlgoID]
			if !ok {
				i = len(out)
				idx[t.AlgoID] = i
				out = append(out, algo{id: t.AlgoID, client: t.ClientID, full: t.Full})
			}
			if leg == 0 {
				out[i].tp = t.Px
			} else {
				out[i].sl = t.Px
			}
		}
	}
	return out
}

func ownedAlgos(pos okxacct.Position) []algo {
	var out []algo
	for _, a := range algosOf(pos) {
		if Owned(a.client) {
			out = append(out, a)
		}
	}
	return out
}

// tpslPlan puts in.TP / in.SL on the whole position: OKX allows one whole-position TP/SL per position,
// so an existing one (ctg's, or the owner's — named in the text, which the owner approves) is amended;
// otherwise ctg places one (OCO, closeFraction=1, algoClOrdId in.AlgoID). Then the size-bound ones ctg
// attached earlier are cancelled (best effort: the new one already covers the position). The owner's
// own size-bound TP/SL are left alone and mentioned.
func tpslPlan(in Intent, pos okxacct.Position, spec Spec, posMode string) ([]Request, string, error) {
	if in.AlgoID == "" {
		return nil, "", errors.New("a TP/SL client id is required")
	}
	tp, sl := formatStep(in.TP, spec.TickSz), formatStep(in.SL, spec.TickSz)
	var reqs []Request
	var text string
	var fullA *algo
	var stale []string
	others := 0
	for _, a := range algosOf(pos) {
		switch {
		case a.full && fullA == nil:
			a := a
			fullA = &a
		case Owned(a.client):
			stale = append(stale, a.id)
		default:
			others++
		}
	}
	if fullA != nil {
		body, _ := json.Marshal(amendAlgoBody{InstID: in.InstID, AlgoID: fullA.id, NewTpTriggerPx: tp, NewTpOrdPx: "-1", NewTpTriggerPxType: "last",
			NewSlTriggerPx: sl, NewSlOrdPx: "-1", NewSlTriggerPxType: "last"})
		reqs = append(reqs, Request{Method: "POST", Path: pathAmendAlgos, Body: body})
		text = fmt.Sprintf("把整仓止盈止损改为 止损 %s / 止盈 %s", FormatPx(in.SL), FormatPx(in.TP))
		if !Owned(fullA.client) {
			text = fmt.Sprintf("修改你在 OKX 设置的整仓止盈止损（algoId %s）为 止损 %s / 止盈 %s", fullA.id, FormatPx(in.SL), FormatPx(in.TP))
		}
	} else {
		reqs = append(reqs, fullTPSL(in, tp, sl, posMode))
		text = fmt.Sprintf("挂整仓止盈止损（覆盖全部张数）止损 %s / 止盈 %s", FormatPx(in.SL), FormatPx(in.TP))
	}
	if len(stale) > 0 {
		reqs = append(reqs, cancelAlgosReq(in.InstID, stale))
		text += fmt.Sprintf("，撤掉 ctg 原先按张数挂的 %d 个", len(stale))
	}
	if others > 0 {
		text += fmt.Sprintf("；你自己另挂的 %d 个部分止盈止损保留不动", others)
	}
	return reqs, text, nil
}

// keepCovered prepares a partial close: TP/SL that cover the whole position stay as they are; size-bound
// ones ctg attached are replaced by a whole-position one at the same levels so the rest stays covered.
func keepCovered(in Intent, pos okxacct.Position, spec Spec, posMode string) ([]Request, string) {
	var own []algo
	for _, a := range algosOf(pos) {
		if a.full {
			return nil, "整仓止盈止损继续覆盖剩余仓位"
		}
		if Owned(a.client) {
			own = append(own, a)
		}
	}
	if len(own) == 0 {
		if len(pos.TP)+len(pos.SL) == 0 {
			return nil, "这个仓位没有止盈止损"
		}
		return nil, "止盈止损是你自己挂的，保持不变（请确认剩余仓位仍被覆盖）"
	}
	if own[0].tp == 0 || own[0].sl == 0 || in.AlgoID == "" {
		return nil, "ctg 的止盈止损不完整，保持不变"
	}
	keep := in
	keep.TP, keep.SL = own[0].tp, own[0].sl
	ids := make([]string, len(own))
	for i, a := range own {
		ids[i] = a.id
	}
	return []Request{fullTPSL(keep, formatStep(keep.TP, spec.TickSz), formatStep(keep.SL, spec.TickSz), posMode), cancelAlgosReq(in.InstID, ids)},
		fmt.Sprintf("先把止盈止损换成整仓（止损 %s / 止盈 %s 不变），剩余仓位继续被覆盖", FormatPx(keep.SL), FormatPx(keep.TP))
}

func fullTPSL(in Intent, tp, sl, posMode string) Request {
	b := algoBody{InstID: in.InstID, TdMode: in.MarginMode, Side: in.orderSide(true), PosSide: posSide(in.Side, posMode), OrdType: "oco",
		CloseFraction: "1", TpTriggerPx: tp, TpOrdPx: "-1", TpTriggerPxType: "last", SlTriggerPx: sl, SlOrdPx: "-1", SlTriggerPxType: "last",
		AlgoClOrdID: in.AlgoID}
	if posMode == NetMode {
		b.ReduceOnly = true
	}
	body, _ := json.Marshal(b)
	return Request{Method: "POST", Path: pathOrderAlgo, Body: body}
}

func cancelAlgosReq(instID string, ids []string) Request {
	rows := make([]cancelAlgoRow, len(ids))
	for i, id := range ids {
		rows[i] = cancelAlgoRow{InstID: instID, AlgoID: id}
	}
	body, _ := json.Marshal(rows)
	return Request{Method: "POST", Path: pathCancelAlgos, Body: body, Optional: true}
}

func posSide(side, posMode string) string {
	if posMode == LongShortMode {
		return side
	}
	return ""
}

// ---- the exact requests ----

const (
	pathLeverage    = "/api/v5/account/set-leverage"
	pathOrder       = "/api/v5/trade/order"
	pathClosePos    = "/api/v5/trade/close-position"
	pathCancelOrder = "/api/v5/trade/cancel-order"
	pathOrderAlgo   = "/api/v5/trade/order-algo"
	pathAmendAlgos  = "/api/v5/trade/amend-algos"
	pathCancelAlgos = "/api/v5/trade/cancel-algos"
)

// Request is one signed POST as it is (or, in a dry run, would be) sent. Optional ones (cancelling
// superseded TP/SL) may fail without failing the action: the error is kept as a warning.
type Request struct {
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Body     json.RawMessage `json:"body"`
	Optional bool            `json:"optional,omitempty"`
}

type leverageBody struct {
	InstID  string `json:"instId"`
	Lever   string `json:"lever"`
	MgnMode string `json:"mgnMode"`
	PosSide string `json:"posSide,omitempty"`
}

type attachedAlgo struct {
	AttachAlgoClOrdID string `json:"attachAlgoClOrdId,omitempty"`
	TpTriggerPx       string `json:"tpTriggerPx"`
	TpOrdPx           string `json:"tpOrdPx"`
	TpTriggerPxType   string `json:"tpTriggerPxType"`
	SlTriggerPx       string `json:"slTriggerPx"`
	SlOrdPx           string `json:"slOrdPx"`
	SlTriggerPxType   string `json:"slTriggerPxType"`
}

type orderBody struct {
	InstID         string         `json:"instId"`
	TdMode         string         `json:"tdMode"`
	Side           string         `json:"side"`
	PosSide        string         `json:"posSide,omitempty"`
	OrdType        string         `json:"ordType"`
	Sz             string         `json:"sz"`
	Px             string         `json:"px,omitempty"`
	ReduceOnly     bool           `json:"reduceOnly,omitempty"`
	ClOrdID        string         `json:"clOrdId,omitempty"`
	AttachAlgoOrds []attachedAlgo `json:"attachAlgoOrds,omitempty"`
}

type closeBody struct {
	InstID  string `json:"instId"`
	MgnMode string `json:"mgnMode"`
	PosSide string `json:"posSide,omitempty"`
	AutoCxl bool   `json:"autoCxl"`
	ClOrdID string `json:"clOrdId,omitempty"`
}

type algoBody struct {
	InstID          string `json:"instId"`
	TdMode          string `json:"tdMode"`
	Side            string `json:"side"`
	PosSide         string `json:"posSide,omitempty"`
	OrdType         string `json:"ordType"`
	CloseFraction   string `json:"closeFraction"`
	ReduceOnly      bool   `json:"reduceOnly,omitempty"`
	TpTriggerPx     string `json:"tpTriggerPx"`
	TpOrdPx         string `json:"tpOrdPx"`
	TpTriggerPxType string `json:"tpTriggerPxType"`
	SlTriggerPx     string `json:"slTriggerPx"`
	SlOrdPx         string `json:"slOrdPx"`
	SlTriggerPxType string `json:"slTriggerPxType"`
	AlgoClOrdID     string `json:"algoClOrdId"`
}

type amendAlgoBody struct {
	InstID             string `json:"instId"`
	AlgoID             string `json:"algoId"`
	NewTpTriggerPx     string `json:"newTpTriggerPx"`
	NewTpOrdPx         string `json:"newTpOrdPx"`
	NewTpTriggerPxType string `json:"newTpTriggerPxType"`
	NewSlTriggerPx     string `json:"newSlTriggerPx"`
	NewSlOrdPx         string `json:"newSlOrdPx"`
	NewSlTriggerPxType string `json:"newSlTriggerPxType"`
}

type cancelAlgoRow struct {
	InstID string `json:"instId"`
	AlgoID string `json:"algoId"`
}

type cancelOrderBody struct {
	InstID  string `json:"instId"`
	ClOrdID string `json:"clOrdId"`
}

// Requests are the POSTs in the order they are sent; the first failing one that is not Optional stops the rest.
func (o Order) Requests() []Request { return append([]Request(nil), o.reqs...) }

// Result is what Place did: the requests and, live, OKX's answers.
type Result struct {
	Mode      Mode              `json:"mode"`
	Requests  []Request         `json:"requests"`
	Responses []json.RawMessage `json:"responses,omitempty"`
	OrdID     string            `json:"ordId,omitempty"`  // the order's (an order or close-position)
	AlgoID    string            `json:"algoId,omitempty"` // a TP/SL ctg placed
	Warnings  []string          `json:"warnings,omitempty"`
}

// Client places validated orders.
type Client interface {
	Mode() Mode
	// Place sends the order's requests in turn. A dry run returns them unsent.
	Place(ctx context.Context, o Order) (Result, error)
}

// dryRun has no fields and no HTTP client: it cannot send anything.
type dryRun struct{}

func NewDryRun() Client { return dryRun{} }

func (dryRun) Mode() Mode { return DryRun }

func (dryRun) Place(_ context.Context, o Order) (Result, error) {
	if o.instID == "" || len(o.reqs) == 0 {
		return Result{}, errors.New("empty order")
	}
	return Result{Mode: DryRun, Requests: o.Requests()}, nil
}

// ---- helpers ----

func decimals(step float64) int {
	s := strconv.FormatFloat(step, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

func roundTo(v, step float64) float64 {
	if step <= 0 {
		return v
	}
	r := math.Round(v/step) * step
	p := math.Pow10(decimals(step))
	return math.Round(r*p) / p
}

func formatStep(v, step float64) string {
	return strconv.FormatFloat(roundTo(v, step), 'f', decimals(step), 64)
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// FormatPx prints a price with up to 6 significant digits.
func FormatPx(v float64) string {
	if v == 0 {
		return "0"
	}
	d := max(0, 5-int(math.Floor(math.Log10(math.Abs(v)))))
	return strconv.FormatFloat(math.Round(v*math.Pow10(d))/math.Pow10(d), 'f', -1, 64)
}
