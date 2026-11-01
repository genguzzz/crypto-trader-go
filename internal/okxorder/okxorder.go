// Package okxorder places the one kind of real OKX order the owner approved on 2026-11-01: a market
// open of a USDT-margined perpetual in isolated margin with both a take-profit and a stop-loss attached
// (triggered on the last price, closed at market), after setting that instrument's isolated leverage.
// Nothing else is expressible: no limit orders, no cancel / amend / close, no transfers or withdrawals.
// An Order can only be made by Build, which validates it, and the dry-run Client (the default, see
// ModeFromEnv) has no HTTP client at all. Requests are signed through okxacct.Config (one key, one
// proxy-bound transport); see docs/trading-design.md §7.
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

// Spec is the instrument metadata sizing needs (public /public/instruments).
type Spec struct {
	CtVal, LotSz, MinSz, TickSz, MaxLever float64
}

// Limits bound a single order. Zero fields take the conservative defaults (DefaultLimits).
type Limits struct {
	MinLeverage   int     `json:"minLeverage"` // the owner's floor (0 = 1×)
	MaxLeverage   int     `json:"maxLeverage"`
	MinMarginUSDT float64 `json:"minMarginUsdt"` // the owner's floor per trade (0 = none)
	MaxMarginUSDT float64 `json:"maxMarginUsdt"` // isolated margin per trade
	MinStopPct    float64 `json:"minStopPct"`    // stop distance from the mark, % (noise floor)
	MaxStopPct    float64 `json:"maxStopPct"`
	MaxTargetPct  float64 `json:"maxTargetPct"`
	MinRR         float64 `json:"minRR"` // target distance / stop distance
}

var DefaultLimits = Limits{MaxLeverage: 5, MaxMarginUSDT: 50, MinStopPct: 0.3, MaxStopPct: 10, MaxTargetPct: 40, MinRR: 1}

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
	return l
}

// Intent is what a trade proposal asks for: side, isolated margin, leverage and the two exits.
type Intent struct {
	InstID     string
	Side       string // long | short
	MarginUSDT float64
	Leverage   int
	SL, TP     float64
	ClientID   string // clOrdId: 1–32 letters / digits, makes a retried order idempotent on OKX
}

// Sizing is what an order amounts to at a given mark price.
type Sizing struct {
	Contracts    float64 `json:"contracts"`
	NotionalUSDT float64 `json:"notionalUsdt"`
	MarginUSDT   float64 `json:"marginUsdt"` // notional / leverage (OKX adds fees on top)
	StopPct      float64 `json:"stopPct"`    // distance from the mark, %
	TargetPct    float64 `json:"targetPct"`
	RR           float64 `json:"rr"`
}

// Order is a validated market open with TP and SL. Its fields are unexported: Build is the only
// constructor, so an Order that skipped validation cannot exist.
type Order struct {
	instID, side, posSide, sz, tp, sl, clOrdID string
	lever                                      int
	sizing                                     Sizing
}

func (o Order) InstID() string { return o.instID }
func (o Order) Sizing() Sizing { return o.sizing }

var clientID = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)

// Check validates the exits and leverage against the mark price without sizing: SL on the losing side
// of the mark, TP on the winning side, distances within the limits, reward ≥ MinRR × risk, and the stop
// well before the isolated liquidation price.
func Check(in Intent, mark float64, lim Limits) (Sizing, error) {
	lim = lim.WithDefaults()
	var z Sizing
	if !strings.HasSuffix(in.InstID, "-USDT-SWAP") {
		return z, errors.New("only USDT-margined perpetuals (…-USDT-SWAP)")
	}
	if in.Side != "long" && in.Side != "short" {
		return z, errors.New("side must be long or short")
	}
	if in.Leverage < max(lim.MinLeverage, 1) || in.Leverage > lim.MaxLeverage {
		return z, fmt.Errorf("leverage must be %d–%d×", max(lim.MinLeverage, 1), lim.MaxLeverage)
	}
	if !(in.MarginUSDT > 0) || in.MarginUSDT < lim.MinMarginUSDT || in.MarginUSDT > lim.MaxMarginUSDT {
		if lim.MinMarginUSDT > 0 {
			return z, fmt.Errorf("margin must be %g–%g USDT", lim.MinMarginUSDT, lim.MaxMarginUSDT)
		}
		return z, fmt.Errorf("margin must be above 0 and at most %g USDT", lim.MaxMarginUSDT)
	}
	if !(mark > 0) || math.IsInf(mark, 0) {
		return z, errors.New("no current price")
	}
	if !(in.SL > 0) || !(in.TP > 0) {
		return z, errors.New("both a stop-loss and a take-profit price are required")
	}
	long := in.Side == "long"
	if long && !(in.SL < mark && in.TP > mark) {
		return z, fmt.Errorf("a long needs stop-loss below and take-profit above the current price %s", FormatPx(mark))
	}
	if !long && !(in.SL > mark && in.TP < mark) {
		return z, fmt.Errorf("a short needs stop-loss above and take-profit below the current price %s", FormatPx(mark))
	}
	z.StopPct = math.Abs(mark-in.SL) / mark * 100
	z.TargetPct = math.Abs(in.TP-mark) / mark * 100
	z.RR = z.TargetPct / z.StopPct
	switch {
	case z.StopPct < lim.MinStopPct || z.StopPct > lim.MaxStopPct:
		return z, fmt.Errorf("stop-loss is %.2f%% from the price; allowed %g–%g%%", z.StopPct, lim.MinStopPct, lim.MaxStopPct)
	case z.TargetPct < lim.MinStopPct || z.TargetPct > lim.MaxTargetPct:
		return z, fmt.Errorf("take-profit is %.2f%% from the price; allowed %g–%g%%", z.TargetPct, lim.MinStopPct, lim.MaxTargetPct)
	case z.RR < lim.MinRR-1e-9:
		return z, fmt.Errorf("reward:risk is %.2f; at least %g required", z.RR, lim.MinRR)
	}
	// isolated liquidation ≈ 1/leverage minus maintenance margin away; keep the stop well inside it
	if liq := (100/float64(in.Leverage) - 0.5) * 0.8; z.StopPct > liq {
		return z, fmt.Errorf("at %d× the stop (%.2f%%) is too close to liquidation (≈%.1f%%); lower the leverage", in.Leverage, z.StopPct, liq/0.8)
	}
	return z, nil
}

// Build validates an intent against the current mark price and the instrument and returns the order.
// Size = margin × leverage / (mark × ctVal) contracts, rounded down to lotSz, at least minSz.
func Build(in Intent, spec Spec, mark float64, posMode string, lim Limits) (Order, error) {
	lim = lim.WithDefaults()
	if spec.MaxLever > 0 && float64(in.Leverage) > spec.MaxLever {
		return Order{}, fmt.Errorf("%s allows at most %g× leverage", in.InstID, spec.MaxLever)
	}
	if !(spec.CtVal > 0) || !(spec.LotSz > 0) || !(spec.TickSz > 0) {
		return Order{}, errors.New("instrument metadata unknown")
	}
	if in.ClientID != "" && !clientID.MatchString(in.ClientID) {
		return Order{}, errors.New("client order id must be 1–32 letters or digits")
	}
	if posMode != LongShortMode && posMode != NetMode {
		return Order{}, fmt.Errorf("unknown account position mode %q", posMode)
	}
	in.SL, in.TP = roundTo(in.SL, spec.TickSz), roundTo(in.TP, spec.TickSz)
	z, err := Check(in, mark, lim)
	if err != nil {
		return Order{}, err
	}
	lots := math.Floor(in.MarginUSDT*float64(in.Leverage)/(mark*spec.CtVal)/spec.LotSz + 1e-9)
	contracts := lots * spec.LotSz
	if contracts <= 0 || contracts < spec.MinSz-1e-12 {
		need := math.Max(spec.MinSz, spec.LotSz) * spec.CtVal * mark / float64(in.Leverage)
		return Order{}, fmt.Errorf("margin %g USDT at %d× is below the minimum order size (needs ≈ %.2f USDT)", in.MarginUSDT, in.Leverage, need)
	}
	z.Contracts = contracts
	z.NotionalUSDT = contracts * spec.CtVal * mark
	z.MarginUSDT = z.NotionalUSDT / float64(in.Leverage)
	if z.MarginUSDT > lim.MaxMarginUSDT*1.0001 {
		return Order{}, fmt.Errorf("margin %.2f USDT is above the cap %g", z.MarginUSDT, lim.MaxMarginUSDT)
	}
	o := Order{instID: in.InstID, side: "buy", sz: formatStep(contracts, spec.LotSz), tp: formatStep(in.TP, spec.TickSz),
		sl: formatStep(in.SL, spec.TickSz), clOrdID: in.ClientID, lever: in.Leverage, sizing: z}
	if in.Side == "short" {
		o.side = "sell"
	}
	if posMode == LongShortMode {
		o.posSide = in.Side
	}
	return o, nil
}

// ---- the exact requests ----

const (
	pathLeverage = "/api/v5/account/set-leverage"
	pathOrder    = "/api/v5/trade/order"
)

// Request is one signed POST as it is (or, in a dry run, would be) sent.
type Request struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

type leverageBody struct {
	InstID  string `json:"instId"`
	Lever   string `json:"lever"`
	MgnMode string `json:"mgnMode"`
	PosSide string `json:"posSide,omitempty"`
}

type attachedAlgo struct {
	TpTriggerPx     string `json:"tpTriggerPx"`
	TpOrdPx         string `json:"tpOrdPx"`
	TpTriggerPxType string `json:"tpTriggerPxType"`
	SlTriggerPx     string `json:"slTriggerPx"`
	SlOrdPx         string `json:"slOrdPx"`
	SlTriggerPxType string `json:"slTriggerPxType"`
}

type orderBody struct {
	InstID         string         `json:"instId"`
	TdMode         string         `json:"tdMode"`
	Side           string         `json:"side"`
	PosSide        string         `json:"posSide,omitempty"`
	OrdType        string         `json:"ordType"`
	Sz             string         `json:"sz"`
	ClOrdID        string         `json:"clOrdId,omitempty"`
	AttachAlgoOrds []attachedAlgo `json:"attachAlgoOrds"`
}

// Requests are the two POSTs: isolated leverage for the instrument (and side), then the market order
// with TP and SL attached (-1 = close at market when triggered; triggers on the last price).
func (o Order) Requests() []Request {
	lev, _ := json.Marshal(leverageBody{InstID: o.instID, Lever: strconv.Itoa(o.lever), MgnMode: "isolated", PosSide: o.posSide})
	ord, _ := json.Marshal(orderBody{InstID: o.instID, TdMode: "isolated", Side: o.side, PosSide: o.posSide, OrdType: "market", Sz: o.sz,
		ClOrdID: o.clOrdID, AttachAlgoOrds: []attachedAlgo{{TpTriggerPx: o.tp, TpOrdPx: "-1", TpTriggerPxType: "last",
			SlTriggerPx: o.sl, SlOrdPx: "-1", SlTriggerPxType: "last"}}})
	return []Request{{"POST", pathLeverage, lev}, {"POST", pathOrder, ord}}
}

// Result is what Place did: the requests and, live, OKX's answers.
type Result struct {
	Mode      Mode              `json:"mode"`
	Requests  []Request         `json:"requests"`
	Responses []json.RawMessage `json:"responses,omitempty"`
	OrdID     string            `json:"ordId,omitempty"`
}

// Client places validated orders.
type Client interface {
	Mode() Mode
	// Place sets the isolated leverage, then places the order. A dry run returns the requests unsent.
	Place(ctx context.Context, o Order) (Result, error)
}

// dryRun has no fields and no HTTP client: it cannot send anything.
type dryRun struct{}

func NewDryRun() Client { return dryRun{} }

func (dryRun) Mode() Mode { return DryRun }

func (dryRun) Place(_ context.Context, o Order) (Result, error) {
	if o.instID == "" {
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

// FormatPx prints a price with up to 6 significant digits.
func FormatPx(v float64) string {
	if v == 0 {
		return "0"
	}
	d := max(0, 5-int(math.Floor(math.Log10(math.Abs(v)))))
	return strconv.FormatFloat(math.Round(v*math.Pow10(d))/math.Pow10(d), 'f', -1, 64)
}
