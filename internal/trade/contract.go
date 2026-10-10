// Package trade is the order layer: accounts, strategy instances, signals, risk, sizing, orders,
// simulated fills, positions and the ledger. Strategies only emit Signals; this package is the only
// executor. See docs/trading-design.md for the full contract.
//
// This file is the shared contract between packages built in parallel (trade, strategyhost,
// interact, aigate, api, web). Change it only together with docs/trading-design.md.
package trade

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// ---------- enums ----------

type Mode string // account mode

const (
	ModeLive     Mode = "live"     // real-time paper trading
	ModeBacktest Mode = "backtest" // historical replay
	ModeReal     Mode = "real"     // real OKX orders (not implemented; rejected on create)
)

type ExecMode string // what happens to a strategy instance's signals

const (
	ExecAuto   ExecMode = "auto"   // risk + sizing, then order
	ExecAI     ExecMode = "ai"     // AI review first, then risk + sizing
	ExecRecord ExecMode = "record" // log only, never order
)

type ConfirmMode string // human confirmation through the interaction layer

const (
	ConfirmOff  ConfirmMode = "off"
	ConfirmOpen ConfirmMode = "open" // only signals that open or add
	ConfirmAll  ConfirmMode = "all"
)

type Action string // signal action

const (
	OpenLong  Action = "open_long"
	OpenShort Action = "open_short"
	Close     Action = "close"  // close the whole position of the instance in this instrument
	Reduce    Action = "reduce" // reduce by Signal.ReduceFrac
)

type InstanceState string

const (
	StCreated InstanceState = "created"
	StRunning InstanceState = "running"
	StPaused  InstanceState = "paused"
	StFaulted InstanceState = "faulted"
	StStopped InstanceState = "stopped"
	StDone    InstanceState = "done" // backtest finished
)

// SignalOutcome is where a signal ended up.
type SignalOutcome string

const (
	OutPending   SignalOutcome = "pending"    // waiting for AI or human
	OutOrdered   SignalOutcome = "ordered"    // turned into an order group
	OutRejected  SignalOutcome = "rejected"   // risk / validation (Reason says why)
	OutSkippedAI SignalOutcome = "skipped_ai" // AI said skip
	OutSkippedUs SignalOutcome = "skipped_user"
	OutExpired   SignalOutcome = "expired" // TTL passed while waiting
	OutRecorded  SignalOutcome = "recorded"
)

type OrderType string

const (
	OrdMarket OrderType = "market" // executed as IOC with a worst price from the slippage tolerance
	OrdLimit  OrderType = "limit"
	OrdStop   OrderType = "stop"   // stop-loss trigger → market, ignores slippage cap
	OrdTarget OrderType = "target" // take-profit trigger → market
)

type OrderState string

const (
	OsTrigger   OrderState = "waiting_trigger"
	OsLive      OrderState = "live"
	OsPartial   OrderState = "partially_filled"
	OsFilled    OrderState = "filled"
	OsCancelled OrderState = "cancelled"
	OsRejected  OrderState = "rejected"
)

// ---------- domain ----------

// Limits are account-level hard rules. Neither strategies nor AI can bypass them.
type Limits struct {
	MaxPositions    int         `json:"maxPositions"`    // concurrent open positions (instruments)
	MaxLeverage     float64     `json:"maxLeverage"`     // isolated leverage cap
	RiskPct         float64     `json:"riskPct"`         // equity fraction risked per trade at strength 1 (0.01 = 1%, up to 5 = 500%)
	MaxInstPct      float64     `json:"maxInstPct"`      // max margin in one instrument as equity fraction
	DailyLossPct    float64     `json:"dailyLossPct"`    // circuit breaker: realized+unrealized loss today (UTC+8 day)
	SlippageBp      float64     `json:"slippageBp"`      // default tolerance for market entries
	CooldownMin     int         `json:"cooldownMin"`     // minutes before re-entering an instrument after a close
	TakerFeeBp      float64     `json:"takerFeeBp"`      // default 5
	MakerFeeBp      float64     `json:"makerFeeBp"`      // default 2
	RequireStop     bool        `json:"requireStop"`     // opening signals must carry a stop (default true)
	Confirm         ConfirmMode `json:"confirm"`         // human confirmation
	ConfirmTimeoutS int         `json:"confirmTimeoutS"` // default 120
	ConfirmDefault  string      `json:"confirmDefault"`  // "skip" (default) | "execute"
}

func DefaultLimits() Limits {
	return Limits{MaxPositions: 5, MaxLeverage: 5, RiskPct: 0.01, MaxInstPct: 0.3, DailyLossPct: 0.05,
		SlippageBp: 10, CooldownMin: 30, TakerFeeBp: 5, MakerFeeBp: 2, RequireStop: true,
		Confirm: ConfirmOff, ConfirmTimeoutS: 120, ConfirmDefault: "skip"}
}

type Account struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Mode      Mode    `json:"mode"`
	Deposit   float64 `json:"deposit"` // total USDT deposited
	Limits    Limits  `json:"limits"`
	Archived  bool    `json:"archived"`
	CreatedAt int64   `json:"createdAt"` // ms
	// backtest only
	From int64 `json:"from,omitempty"` // ms, inclusive
	To   int64 `json:"to,omitempty"`   // ms, exclusive
	// Backtest is the state of the last backtest run (backtest accounts only).
	Backtest *BacktestStatus `json:"backtest,omitempty"`
	// derived (filled by reads)
	Equity    float64 `json:"equity"`
	Cash      float64 `json:"cash"` // available (not used as margin)
	DayPnl    float64 `json:"dayPnl"`
	Halted    bool    `json:"halted"` // circuit breaker tripped for today
	OpenCount int     `json:"openCount"`
	Instances int     `json:"instances"`
}

// BacktestStatus is the progress of a backtest run.
type BacktestStatus struct {
	State      string  `json:"state"`           // idle | running | done | failed
	Progress   float64 `json:"progress"`        // 0..1 of the replayed range
	Clock      int64   `json:"clock,omitempty"` // replay time reached, ms
	Error      string  `json:"error,omitempty"`
	StartedAt  int64   `json:"startedAt,omitempty"`
	FinishedAt int64   `json:"finishedAt,omitempty"`
}

// Universe is either a static list or a screener query evaluated every Refresh.
type Universe struct {
	Static   []string       `json:"static,omitempty"`
	Screener *ScreenerQuery `json:"screener,omitempty"`
}

// ScreenerQuery selects a dynamic universe. Scope "" / "pool" ranks the active pool (by a rule's
// score on Side, else by pool rank); scope "market" ranks every recorded crypto USDT swap by its
// 12h USDT turnover (no rule: scores exist only for pool rows).
type ScreenerQuery struct {
	Scope    string                `json:"scope,omitempty"` // "" | pool | market
	Rule     string                `json:"rule,omitempty"`  // rule id; sort by its score on Side
	Side     string                `json:"side,omitempty"`  // long | short | both (the stronger side)
	Top      int                   `json:"top"`
	MinScore float64               `json:"minScore,omitempty"`
	Filter   map[string][2]float64 `json:"filter,omitempty"`  // field → [min,max] on fact-panel values (NaN-free)
	Refresh  string                `json:"refresh,omitempty"` // "1H" default
}

const (
	ScopePool      = "pool"
	ScopeMarket    = "market"
	MaxPoolTop     = 50  // screener top, pool scope
	MaxMarketTop   = 300 // screener top, market scope
	CategoryCrypto = "1" // OKX instCategory of crypto swaps
)

// Market reports whether the query ranks the whole recorded crypto market.
func (q ScreenerQuery) Market() bool { return q.Scope == ScopeMarket }

// MarketQuery checks a market-scope query sent at runtime (ctx.screener does not pass
// validUniverse) and bounds Top, so live and replay answer it the same way.
func (q ScreenerQuery) MarketQuery() (ScreenerQuery, error) {
	if q.Rule != "" {
		return q, errors.New("全市场筛选按成交额排序，不能指定评分规则（评分只覆盖活跃池）")
	}
	if q.MinScore != 0 {
		return q, errors.New("全市场筛选没有评分，不能设最低分")
	}
	if q.Top <= 0 || q.Top > MaxMarketTop {
		q.Top = MaxMarketTop
	}
	return q, nil
}

// StrategyVersion is an immutable (code, param schema) pair. ID = first 12 hex of sha256(code).
type StrategyVersion struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"` // class-level name, e.g. "majors-trend"
	File            string       `json:"file"` // path under strategies/
	Description     string       `json:"description"`
	Timeframe       string       `json:"timeframe"`
	Params          []ParamSpec  `json:"params"`
	DefaultUniverse Universe     `json:"defaultUniverse"`
	Meta            StrategyMeta `json:"meta"`
	Code            string       `json:"code,omitempty"`
	CreatedAt       int64        `json:"createdAt"`
}

// StrategyMeta is what a strategy class declares besides its params (SDK ≥ 2).
type StrategyMeta struct {
	CanShort bool              `json:"canShort,omitempty"` // may emit open_short
	CanLong  bool              `json:"canLong,omitempty"`  // may emit open_long
	Warmup   int               `json:"warmup,omitempty"`   // bars of history the strategy reads
	Schedule map[string]string `json:"schedule,omitempty"` // timer name → interval ("15m", "1H"), on_timer
	Batch    bool              `json:"batch,omitempty"`    // overrides on_bars (one event per bar time)
	Builtin  bool              `json:"builtin,omitempty"`  // shipped in strategies/library (read-only in the editor)
	SourceID int64             `json:"sourceId,omitempty"` // user strategy this version was published from
	Draft    bool              `json:"draft,omitempty"`    // registered for a trial run only; hidden from lists
}

// allows reports whether the strategy declared it may take an opening action. Versions that declare
// neither direction (SDK 1, or strategies that only send messages) are not restricted.
func (m StrategyMeta) allows(a Action) bool {
	if !m.CanLong && !m.CanShort {
		return true
	}
	return (a != OpenLong || m.CanLong) && (a != OpenShort || m.CanShort)
}

// Param types. Every value travels as a number: bool 0/1, choice and timeframe the option index.
const (
	PTFloat     = "float"
	PTInt       = "int"
	PTBool      = "bool"
	PTChoice    = "choice"
	PTTimeframe = "timeframe"
)

type ParamSpec struct {
	Name      string   `json:"name"`
	Type      string   `json:"type,omitempty"` // "" = float (SDK 1)
	Default   float64  `json:"default"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Step      *float64 `json:"step,omitempty"`
	Options   []string `json:"options,omitempty"` // choice / timeframe
	Labels    []string `json:"labels,omitempty"`  // display names of Options (optional)
	Group     string   `json:"group,omitempty"`
	Label     string   `json:"label,omitempty"` // display name (default: Name)
	Desc      string   `json:"desc,omitempty"`
	AITunable bool     `json:"aiTunable,omitempty"`
}

// Instance = a strategy version attached to an account with its own params and universe.
// Editing params creates a new ConfigVersion (old signals keep theirs).
type Instance struct {
	ID            int64              `json:"id"`
	AccountID     int64              `json:"accountId"`
	VersionID     string             `json:"versionId"`
	Name          string             `json:"name"`
	Params        map[string]float64 `json:"params"`
	Universe      Universe           `json:"universe"`
	Exec          ExecMode           `json:"exec"`
	State         InstanceState      `json:"state"`
	StateReason   string             `json:"stateReason,omitempty"`
	Status        string             `json:"status,omitempty"` // the strategy's own status() line (running instances)
	ConfigVersion int                `json:"configVersion"`
	Heartbeat     int64              `json:"heartbeat"` // ms, last event acknowledged
	CreatedAt     int64              `json:"createdAt"`
}

type Signal struct {
	ID            int64         `json:"id"`
	InstanceID    int64         `json:"instanceId"`
	AccountID     int64         `json:"accountId"`
	InstID        string        `json:"instId"`
	Action        Action        `json:"action"`
	Strength      float64       `json:"strength"`             // 0..1, scales risk size; default 1
	StopLoss      float64       `json:"stopLoss,omitempty"`   // absolute price (SDK resolves "2atr")
	TakeProfit    float64       `json:"takeProfit,omitempty"` // absolute price
	MaxSlippageBp float64       `json:"maxSlippageBp,omitempty"`
	Leverage      float64       `json:"leverage,omitempty"`    // requested; capped by Limits.MaxLeverage
	ReduceFrac    float64       `json:"reduceFrac,omitempty"`  // for Reduce
	TTLms         int64         `json:"ttlMs,omitempty"`       // default 5 min
	Reverse       bool          `json:"reverse,omitempty"`     // open: first close this instance's opposite position (no cooldown)
	TrailDist     float64       `json:"trailDist,omitempty"`   // open: trailing stop distance (price units)
	TimeLimitMs   int64         `json:"timeLimitMs,omitempty"` // open: close the position after this long
	Reason        string        `json:"reason"`
	Ts            int64         `json:"ts"`     // decision time (bar close, exchange time), ms
	RecvTs        int64         `json:"recvTs"` // local receive time, ms
	Price         float64       `json:"price"`  // reference price at Ts
	Outcome       SignalOutcome `json:"outcome"`
	OutcomeReason string        `json:"outcomeReason,omitempty"`
	ConfigVersion int           `json:"configVersion"`
	AIDecisionID  int64         `json:"aiDecisionId,omitempty"`
}

type Order struct {
	ID         int64      `json:"id"`
	AccountID  int64      `json:"accountId"`
	InstanceID int64      `json:"instanceId"`
	SignalID   int64      `json:"signalId,omitempty"` // 0 = manual / system (liquidation)
	InstID     string     `json:"instId"`
	Side       string     `json:"side"`    // buy | sell
	PosSide    string     `json:"posSide"` // long | short
	Type       OrderType  `json:"type"`
	Qty        float64    `json:"qty"`             // contracts
	Price      float64    `json:"price,omitempty"` // limit price / worst price for market
	TriggerPx  float64    `json:"triggerPx,omitempty"`
	ReduceOnly bool       `json:"reduceOnly"`
	OCOGroup   string     `json:"ocoGroup,omitempty"`
	Leverage   float64    `json:"leverage"`
	State      OrderState `json:"state"`
	Filled     float64    `json:"filled"`
	AvgPx      float64    `json:"avgPx,omitempty"`
	Reason     string     `json:"reason,omitempty"` // reject / cancel reason
	CreatedAt  int64      `json:"createdAt"`
	UpdatedAt  int64      `json:"updatedAt"`
}

type Fill struct {
	ID          int64   `json:"id"`
	OrderID     int64   `json:"orderId"`
	AccountID   int64   `json:"accountId"`
	InstanceID  int64   `json:"instanceId"`
	InstID      string  `json:"instId"`
	Side        string  `json:"side"`
	PosSide     string  `json:"posSide"`
	Qty         float64 `json:"qty"`
	Px          float64 `json:"px"`
	Fee         float64 `json:"fee"` // USDT, positive = paid
	SlippageBp  float64 `json:"slippageBp"`
	RealizedPnl float64 `json:"realizedPnl"` // for closing fills, before fee
	Liquidation bool    `json:"liquidation,omitempty"`
	Ts          int64   `json:"ts"`
	Kind        string  `json:"kind,omitempty"`   // fill | stop | target | time | liquidation (events only, not stored)
	Closed      bool    `json:"closed,omitempty"` // this fill closed the position (events only, not stored)
}

type Position struct {
	AccountID     int64   `json:"accountId"`
	InstanceID    int64   `json:"instanceId"`
	InstID        string  `json:"instId"`
	PosSide       string  `json:"posSide"`
	Qty           float64 `json:"qty"` // contracts
	CtVal         float64 `json:"ctVal"`
	EntryPx       float64 `json:"entryPx"`
	Leverage      float64 `json:"leverage"`
	Margin        float64 `json:"margin"`
	LiqPx         float64 `json:"liqPx"`
	MarkPx        float64 `json:"markPx"`
	UnrealizedPnl float64 `json:"unrealizedPnl"`
	StopLoss      float64 `json:"stopLoss,omitempty"`
	TakeProfit    float64 `json:"takeProfit,omitempty"`
	OpenedAt      int64   `json:"openedAt"`
}

type LedgerEntry struct {
	ID        int64   `json:"id"`
	AccountID int64   `json:"accountId"`
	Kind      string  `json:"kind"`   // deposit | fee | funding | pnl | liquidation
	Amount    float64 `json:"amount"` // signed USDT
	InstID    string  `json:"instId,omitempty"`
	RefID     int64   `json:"refId,omitempty"`
	Ts        int64   `json:"ts"`
}

// ---------- dependencies provided to trade ----------

// Bar is one OHLCV candle (open time ms, USDT turnover in VolQ).
type Bar struct {
	Ts                    int64
	O, H, L, C, Vol, VolQ float64
}

// Spec is the contract specification used for sizing and validation.
type Spec struct {
	InstID   string
	CtVal    float64 // coins per contract
	TickSz   float64
	LotSz    float64
	MinSz    float64
	MaxLever float64
	Category string // OKX instCategory: "1" crypto, "3" stock/ETF, "4" commodity, …
}

// Quote is a top-of-book view for live fills. Asks/Bids may be empty (then Last ± half spread).
type Quote struct {
	Last, Bid, Ask, Mark float64
	Asks, Bids           [][2]float64 // [price, contracts], best first
	Ts                   int64
}

// MarketData is implemented by an adapter over market.Engine (live) and by the replayer (backtest).
// All reads are "as of" the caller's clock: the replayer never returns data after its current time.
type MarketData interface {
	Now() int64 // ms
	Spec(instID string) (Spec, bool)
	Quote(ctx context.Context, instID string) (Quote, error)
	Bars(instID, tf string, n int) ([]Bar, error)     // tf: 1m 5m 15m 1H 4H 1D; oldest first; confirmed only
	Factors(instID string) (map[string]float64, bool) // fact-panel values of the last confirmed minute
	Screener(q ScreenerQuery) ([]ScreenRow, error)
	Score(rule, instID string) (long, short float64, ok bool)    // an unscored side is NaN
	FundingRate(instID string, at int64) (rate float64, ok bool) // settled rate for the settlement at `at`
	Keep(instID string, on bool)                                 // keep the instrument streaming while a position/universe needs it
}

// KeepBatcher is optionally implemented by live MarketData: Keep for many instruments at once
// (one stream subscription update for a large universe).
type KeepBatcher interface {
	KeepMany(on, off []string)
}

type ScreenRow struct {
	InstID string             `json:"instId"`
	Rank   int                `json:"rank"`
	Last   float64            `json:"last"`
	Long   float64            `json:"long"`  // NaN = unscored (null in JSON)
	Short  float64            `json:"short"` // NaN = unscored (null in JSON)
	Values map[string]float64 `json:"values"`
}

// MarshalJSON writes an unscored side (NaN) as null: missing input is unscored, never 0.
func (r ScreenRow) MarshalJSON() ([]byte, error) {
	f := func(x float64) *float64 {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return &x
	}
	type row struct {
		InstID string             `json:"instId"`
		Rank   int                `json:"rank"`
		Last   *float64           `json:"last"`
		Long   *float64           `json:"long"`
		Short  *float64           `json:"short"`
		Values map[string]float64 `json:"values"`
	}
	return json.Marshal(row{r.InstID, r.Rank, f(r.Last), f(r.Long), f(r.Short), r.Values})
}

// Interactor is the human interaction layer (internal/interact). Notify is fire-and-forget
// (persisted to an outbox inside trade's own transaction is NOT required; interact keeps its own).
type Interactor interface {
	Notify(ev Event)
	// Ask blocks until a choice, ctx cancel, or deadline; returns the option key ("execute",
	// "half", "skip") and who answered ("telegram:<user>", "web", "timeout").
	Ask(ctx context.Context, p Prompt) (choice, by string, err error)
}

type Event struct {
	Kind      string         `json:"kind"` // signal | order | fill | cancel | stop | target | time | liquidation | reject | breaker | instance | ai | daily | strategy
	AccountID int64          `json:"accountId"`
	Account   string         `json:"account"`
	Mode      Mode           `json:"mode"`
	Title     string         `json:"title"` // one line, Chinese, e.g. "开多成交"
	Lines     []string       `json:"lines"`
	Ts        int64          `json:"ts"`
	Ref       string         `json:"ref,omitempty"`  // e.g. "ord-8f2c"
	Data      map[string]any `json:"data,omitempty"` // machine-readable payload (strategy messages, for webhooks)
}

type Prompt struct {
	ID       string         `json:"id"`
	Event    Event          `json:"event"`
	Options  []PromptOption `json:"options"`
	Deadline time.Time      `json:"deadline"`
	Default  string         `json:"default"`
}

type PromptOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Reviewer is the AI decision layer (internal/aigate). Review must respect ctx (deadline = signal TTL).
type Reviewer interface {
	Review(ctx context.Context, req ReviewRequest) (Decision, error)
	Available() bool
}

type ReviewRequest struct {
	Signal    Signal            `json:"signal"`
	Account   Account           `json:"account"`
	Instance  Instance          `json:"instance"`
	Strategy  StrategyVersion   `json:"strategy"` // without Code
	Positions []Position        `json:"positions"`
	Market    map[string]any    `json:"market"` // bars summary, factors, funding, OI, rank
	Recent    []DecisionSummary `json:"recent"` // last N decisions of this instance with outcomes
	Memory    string            `json:"memory"` // strategy experience document (latest version)
}

type DecisionSummary struct {
	Ts       int64  `json:"ts"`
	InstID   string `json:"instId"`
	Action   string `json:"action"`
	Decision string `json:"decision"`
	Result   string `json:"result"` // e.g. "+1.2% in 6h" or "stopped -0.8%"
}

type Decision struct {
	ID          int64              `json:"id"`
	Action      string             `json:"action"`    // execute | skip
	SizeScale   float64            `json:"sizeScale"` // 0..1
	StopLoss    float64            `json:"stopLoss,omitempty"`
	TakeProfit  float64            `json:"takeProfit,omitempty"`
	Confidence  float64            `json:"confidence"`
	Rationale   string             `json:"rationale"`
	ParamChange map[string]float64 `json:"paramChange,omitempty"` // proposals; applied only via approval
	Model       string             `json:"model"`
	CostUSD     float64            `json:"costUsd"`
	LatencyMs   int64              `json:"latencyMs"`
}

// InstBar is one instrument's bar in a batch delivered at one bar time.
type InstBar struct {
	InstID string
	Bar    Bar
}

// SeriesPoint is one bucket of a data series (ts = bucket open, ms).
type SeriesPoint struct {
	Ts int64   `json:"ts"`
	V  float64 `json:"v"`
}

// SignalSink is what the strategy host calls. Submit validates and persists the signal and returns
// its id immediately; processing (AI, confirm, risk, order) continues asynchronously in live mode and
// synchronously in backtest mode (the replayer waits for it before advancing).
type SignalSink interface {
	Submit(ctx context.Context, s Signal) (int64, error)
	Position(instanceID int64, instID string) (Position, bool)
	Positions(instanceID int64) []Position
	AccountView(instanceID int64) (Account, bool)
	// Series reads a data series of instID as of the instance's clock (oi, funding, buy, sell, delta).
	Series(instanceID int64, instID, field, tf string, n int) ([]SeriesPoint, error)
	// Notify delivers a strategy message (ctx.notify); Log records a strategy log line (ctx.log).
	Notify(instanceID int64, level, text string, data map[string]any)
	Log(instanceID int64, level, msg string)
}

// ---------- strategy runtime (implemented by internal/strategyhost) ----------

// Runner starts strategy processes and lists available strategy code.
type Runner interface {
	// Describe lists strategies found in strategies/library (one entry per class, code included).
	Describe(ctx context.Context) ([]StrategyVersion, error)
	// Start spawns a process for the instance and completes the init handshake (on_start).
	Start(ctx context.Context, inst Instance, ver StrategyVersion, mode Mode, state []byte, md MarketData, sink SignalSink) (Session, error)
	// Check compiles and inspects strategy source for the editor (nothing is stored or run).
	Check(ctx context.Context, code string) (CheckResult, error)
	Available() bool // supervisor connected
}

// CheckResult is what the editor shows for a piece of strategy source.
type CheckResult struct {
	Diagnostics []Diagnostic      `json:"diagnostics"`
	Strategies  []StrategyVersion `json:"strategies"` // declared classes (no ID / code)
}

// Diagnostic is one problem at a 1-based line and 0-based column.
type Diagnostic struct {
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"endLine,omitempty"`
	EndCol   int    `json:"endCol,omitempty"`
	Severity string `json:"severity"` // error | warning
	Message  string `json:"message"`
}

// Fatal reports whether any diagnostic is an error.
func (c CheckResult) Fatal() bool {
	for _, d := range c.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// Session is one running instance process. Every method blocks until the process acknowledged the
// event (lockstep) or ctx/5 s timeout; a timeout kills the process and returns ErrFaulted.
type Session interface {
	// Bars delivers the bars of every universe instrument that closed at one bar time.
	Bars(ctx context.Context, tf string, bars []InstBar) error
	Timer(ctx context.Context, name string) error
	Universe(ctx context.Context, added, removed, current []string) error
	Fill(ctx context.Context, f Fill) error
	SignalResult(ctx context.Context, signalID int64, outcome SignalOutcome, reason string) error
	Funding(ctx context.Context, instID string, rate, amount float64) error
	Stop(ctx context.Context) error
	State() []byte         // latest ctx.state (persist it)
	Status() string        // latest status() line of the strategy
	Done() <-chan struct{} // closed when the process exits
	Err() error            // why it exited (nil after Stop)
}

// ErrFaulted is returned by Session methods when the process timed out or died.
var ErrFaulted = errors.New("strategy process faulted")
