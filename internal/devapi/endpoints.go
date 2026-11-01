package devapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/alerts"
	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/rules"
)

// Param is one input of an endpoint: a query or path parameter in REST, a property in MCP.
type Param struct {
	Name     string   `json:"name"`
	In       string   `json:"in"`   // query | path
	Type     string   `json:"type"` // string | integer | number | boolean
	Desc     string   `json:"desc"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Default  any      `json:"default,omitempty"`
	Min      int      `json:"min,omitempty"`
	Max      int      `json:"max,omitempty"`
}

// Args are validated raw values (defaults applied).
type Args map[string]string

func (a Args) Str(k string) string { return a[k] }
func (a Args) Int(k string) int    { v, _ := strconv.Atoi(a[k]); return v }
func (a Args) Bool(k string) bool  { return a[k] == "true" || a[k] == "1" }

// Endpoint is one read-only query, served as GET /api/open/v1/<Path> and as MCP tool <Name>.
type Endpoint struct {
	Name    string  `json:"name"`
	Path    string  `json:"path"`
	Summary string  `json:"summary"`
	Desc    string  `json:"desc"`
	Params  []Param `json:"params"`
	CostTxt string  `json:"cost"` // human description of the rate-limit cost
	cost    func(Args) int
	run     func(context.Context, Args) (any, error)
	owner   bool // the owner's private data: MCP only, listed and served to owner keys only (OwnerKey)
	write   bool // changes state (propose_trade): not marked read-only to MCP clients
}

func (e *Endpoint) Cost(a Args) int {
	if e.cost == nil {
		return 1
	}
	return e.cost(a)
}

// bind validates raw inputs against the params, applying defaults.
func (e *Endpoint) bind(get func(string) (string, bool)) (Args, error) {
	a := Args{}
	for _, p := range e.Params {
		v, ok := get(p.Name)
		v = strings.TrimSpace(v)
		if !ok || v == "" {
			if p.Required {
				return nil, fmt.Errorf("missing required parameter %q", p.Name)
			}
			if p.Default != nil {
				a[p.Name] = fmt.Sprint(p.Default)
			}
			continue
		}
		switch p.Type {
		case "number":
			if f, err := strconv.ParseFloat(v, 64); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("parameter %q must be a number", p.Name)
			}
		case "integer":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("parameter %q must be an integer", p.Name)
			}
			if (p.Min != 0 || p.Max != 0) && (n < p.Min || n > p.Max) {
				return nil, fmt.Errorf("parameter %q must be between %d and %d", p.Name, p.Min, p.Max)
			}
		case "object":
			if !strings.HasPrefix(v, "{") || !json.Valid([]byte(v)) {
				return nil, fmt.Errorf("parameter %q must be a JSON object", p.Name)
			}
		case "boolean":
			switch strings.ToLower(v) {
			case "true", "1":
				v = "true"
			case "false", "0":
				v = "false"
			default:
				return nil, fmt.Errorf("parameter %q must be true or false", p.Name)
			}
		}
		if len(p.Enum) > 0 && !slices.Contains(p.Enum, v) {
			return nil, fmt.Errorf("parameter %q must be one of %s", p.Name, strings.Join(p.Enum, ", "))
		}
		a[p.Name] = v
	}
	return a, nil
}

type ctxKey struct{}

func withCaller(ctx context.Context, c *Caller) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}
func callerFrom(ctx context.Context) *Caller { c, _ := ctx.Value(ctxKey{}).(*Caller); return c }

var bars = []string{"1m", "5m", "15m", "1H", "4H"}

func instParam(in string) Param {
	return Param{Name: "instId", In: in, Type: "string", Required: true, Desc: "OKX USDT-margined perpetual id, e.g. BTC-USDT-SWAP (see search_instruments)"}
}
func barParam(def string) Param {
	return Param{Name: "bar", In: "query", Type: "string", Enum: bars, Default: def, Desc: "bar size; candles are aligned in UTC"}
}
func limitParam(def, hi int, what string) Param {
	return Param{Name: "limit", In: "query", Type: "integer", Default: def, Min: 1, Max: hi, Desc: what}
}

// Endpoints builds the registry. al may be nil (alert history is then omitted).
func (s *Service) buildEndpoints(e *market.Engine, al *alerts.Service) []*Endpoint {
	eps := []*Endpoint{
		{
			Name: "get_system_status", Path: "status", Summary: "Data freshness and recorder health",
			Desc: "Recorder phase (live / backfill), last confirmed minute, pool coverage, OKX stream connectivity and background job progress. " +
				"Use it to judge whether other results are fresh. Detailed internals (memory, database, configuration) are not exposed.",
			run: func(ctx context.Context, a Args) (any, error) { return publicStatus(e), nil },
		},
		{
			Name: "get_usage", Path: "me", Summary: "Your API key and remaining rate-limit allowance",
			Desc:    "The calling key's name, per-minute rate, burst capacity, remaining tokens and totals. Call it to pace heavy analysis.",
			CostTxt: "0 (free)", cost: func(Args) int { return 0 },
			run: func(ctx context.Context, a Args) (any, error) { return s.Usage(callerFrom(ctx)), nil },
		},
		{
			Name: "list_fields", Path: "fields", Summary: "Fact-panel field definitions",
			Desc: "Every fact-panel field (key, label, unit, formula) used in universe rows, percentiles and rule weights, plus the category codes.",
			run: func(ctx context.Context, a Args) (any, error) {
				return map[string]any{"fields": features.Fields, "categories": map[string]string{"1": "crypto", "3": "stock/ETF", "4": "commodity"},
					"units": map[string]string{
						"log":    "natural log (of a price, OI or turnover ratio). Simple change = exp(v) − 1: 0.01 → +1.01%, 0.10 → +10.5%, −0.10 → −9.5%; v × 100 is only an approximation for small values. vol_surprise_4h: multiple of the 7-day median = exp(v) (0.69 → ×2.0)",
						"z":      "z-score (standard deviations)",
						"bps":    "basis points (1 bp = 0.01%)",
						"rate/h": "funding rate per hour, normalized from 4h/8h settlements (0.0001 = 0.01%/h)",
						"x":      "ratio or multiple as stated in the field description"}}, nil
			},
		},
		{
			Name: "get_universe", Path: "universe", Summary: "Monitoring pool snapshot: fact panel, percentiles and rule scores",
			Desc: "The latest per-minute snapshot. Each row has 12h USDT turnover and rank, the fact panel (values), percentiles within the active pool (pct, 0-100) " +
				"and user-rule scores (long/short 0-100, unvalidated; missing inputs = unscored, never 0). Also lists contracts recently removed from the pool with the reason. " +
				"Pool = 12h turnover over 720 confirmed 1m candles strictly above the threshold, top N. Rows are ordered by 12h turnover unless sort is given, so the default is a liquidity list, not a candidate list. " +
				"log-unit values are natural logs (see list_fields). validation summarises the rule's track record; scores alone are not trade recommendations.",
			Params: []Param{
				{Name: "scope", In: "query", Type: "string", Enum: []string{"active", "watch", "all"}, Default: "active", Desc: "active = pool + watchlist, watch = watchlist only, all = whole recording universe"},
				{Name: "rule", In: "query", Type: "string", Desc: "rule id whose scores to include (default: the server's default rule; '*' = all rules; see list_rules)"},
				{Name: "category", In: "query", Type: "string", Enum: []string{"1", "3", "4"}, Desc: "filter by category: 1 crypto, 3 stock/ETF, 4 commodity"},
				{Name: "sort", In: "query", Type: "string", Default: "turnover12h", Desc: "turnover12h, long, short (score of the selected rule) or any fact-panel field key"},
				{Name: "order", In: "query", Type: "string", Enum: []string{"desc", "asc"}, Default: "desc", Desc: "sort order; rows without the sort value go last"},
				limitParam(200, 1000, "max rows"),
				{Name: "fields", In: "query", Type: "string", Desc: "compact output: comma-separated fact-panel keys to keep (e.g. ret_24h,funding_hourly,oi_chg_4h). Each row becomes " +
					"{instId, rank, last, turnover12h, oiUsd, v:{key: value}, p:{key: percentile}, long, short} with the selected rule's scores as plain numbers; " +
					"removed contracts and score details are left out. Use it whenever you need a table: the full rows are large"},
			},
			CostTxt: "1; scope=all costs 2",
			cost: func(a Args) int {
				if a.Str("scope") == "all" {
					return 2
				}
				return 1
			},
			run: func(ctx context.Context, a Args) (any, error) { return universe(e, a) },
		},
		{
			Name: "search_instruments", Path: "instruments", Summary: "Search all live OKX USDT perpetuals",
			Desc: "Catalog of every live OKX USDT-margined linear perpetual (crypto, stocks/ETFs, commodities, pre-market) with last price, 24h open and whether it is recorded / in the pool / watched.",
			Params: []Param{
				{Name: "q", In: "query", Type: "string", Desc: "case-insensitive substring of the instrument id, e.g. SOL"},
				{Name: "category", In: "query", Type: "string", Enum: []string{"1", "3", "4"}, Desc: "1 crypto, 3 stock/ETF, 4 commodity"},
				limitParam(50, 1000, "max rows"),
			},
			run: func(ctx context.Context, a Args) (any, error) {
				q, cat := strings.ToUpper(a.Str("q")), a.Str("category")
				out := []market.CatalogItem{}
				for _, it := range e.Catalog() {
					if (q == "" || strings.Contains(it.InstID, q)) && (cat == "" || it.Category == cat) {
						out = append(out, it)
						if len(out) >= a.Int("limit") {
							break
						}
					}
				}
				return map[string]any{"items": out}, nil
			},
		},
		{
			Name: "get_instrument", Path: "instruments/{instId}", Summary: "Instrument detail: prices, funding, open interest, scores, market cap",
			Desc: "Last/bid/ask, mark and index price, spot last, funding rate (and interval), open interest (coins and USD), 24h turnover, the fact-panel row with scores for every rule, " +
				"and CoinGecko market cap / supply for crypto. Opening an instrument outside the recording universe starts live streaming and a history download (syncing=true).",
			Params: []Param{instParam("path"),
				{Name: "detail", In: "query", Type: "string", Enum: []string{"compact", "full"}, Default: "compact",
					Desc: "compact: per-rule long/short scores only and rounded numbers (about a quarter of the size); full: every rule's per-field contributions"}},
			CostTxt: "2", cost: func(Args) int { return 2 },
			run: func(ctx context.Context, a Args) (any, error) {
				info, ok := e.Instrument(a.Str("instId"))
				if !ok {
					return nil, fmt.Errorf("unknown instrument %q", a.Str("instId"))
				}
				if a.Str("detail") == "full" {
					return info, nil
				}
				return compactInstrument(info)
			},
		},
		{
			Name: "get_candles", Path: "candles", Summary: "OHLC candles (swap or spot)",
			Desc: "Candles oldest first: t open time (ms), o/h/l/c, q quote turnover in USDT, x confirmed (false = forming bar). Swap candles come from recorded data; " +
				"market=spot fetches the matching OKX USDT spot pair.",
			Params: []Param{instParam("query"), barParam("15m"), limitParam(200, 3000, "number of bars (spot max 1000)"),
				{Name: "market", In: "query", Type: "string", Enum: []string{"swap", "spot"}, Default: "swap", Desc: "swap (recorded) or spot (fetched from OKX)"}},
			CostTxt: "1; market=spot costs 2",
			cost: func(a Args) int {
				if a.Str("market") == "spot" {
					return 2
				}
				return 1
			},
			run: func(ctx context.Context, a Args) (any, error) {
				if a.Str("market") == "spot" {
					cs, err := e.SpotCandles(ctx, a.Str("instId"), a.Str("bar"), min(a.Int("limit"), 1000))
					return tail(cs, a.Int("limit")), err
				}
				cs, err := e.Candles(a.Str("instId"), a.Str("bar"), a.Int("limit"))
				return tail(cs, a.Int("limit")), err
			},
		},
		{
			Name: "compare_with_majors", Path: "compare", Summary: "Relative performance vs BTC and ETH",
			Desc: "Closes of the instrument, BTC-USDT-SWAP and ETH-USDT-SWAP normalized to 100 at baseTs, the first returned bar (the last bar may be forming). " +
				"Values are simple price ratios: 101.5 = +1.5% since baseTs.",
			Params: []Param{instParam("query"), barParam("15m"), limitParam(200, 3000, "number of bars")},
			run: func(ctx context.Context, a Args) (any, error) {
				pts, ids, err := e.Compare(a.Str("instId"), a.Str("bar"), a.Int("limit"))
				if err != nil {
					return nil, err
				}
				pts = rebase(tail(pts, a.Int("limit")))
				var base int64
				if len(pts) > 0 {
					base = pts[0].T
				}
				return map[string]any{"ids": ids, "baseTs": base, "points": pts}, nil
			},
		},
		{
			Name: "get_series", Path: "series", Summary: "Recorded open interest or settled funding history",
			Desc: "field=oi: recorded open-interest samples (v = coins, u = USD). field=funding: settled funding rates at each funding time (v = rate per settlement interval, not per hour; intervals are 4h or 8h). " +
				"Both return only points with t within the last `hours` hours.",
			Params: []Param{instParam("query"),
				{Name: "field", In: "query", Type: "string", Enum: []string{"oi", "funding"}, Required: true, Desc: "oi or funding"},
				{Name: "hours", In: "query", Type: "integer", Default: 24, Min: 1, Max: 720, Desc: "look-back in hours"}},
			run: func(ctx context.Context, a Args) (any, error) {
				pts, err := e.Series(a.Str("instId"), a.Str("field"), a.Int("hours"))
				if err != nil {
					return nil, err
				}
				// the engine pads funding by 7 days for the UI chart; the API honours the requested window
				from := time.Now().UnixMilli() - int64(a.Int("hours"))*3_600_000
				out := make([]market.SeriesPoint, 0, len(pts))
				for _, p := range pts {
					if p.T >= from {
						out = append(out, p)
					}
				}
				return out, nil
			},
		},
		{
			Name: "get_orderbook", Path: "orderbook", Summary: "Order book snapshot with USDT depth",
			Desc:    "Best bid/ask, mid, spread (bps) and levels per side as [price, USDT notional, contracts], best first. The first 400 levels are exact; deeper ones are merged into price buckets.",
			Params:  []Param{instParam("query"), {Name: "depth", In: "query", Type: "integer", Default: 50, Min: 1, Max: 2000, Desc: "levels per side to return"}},
			CostTxt: "3", cost: func(Args) int { return 3 },
			run: func(ctx context.Context, a Args) (any, error) {
				ob, err := e.OrderBook(ctx, a.Str("instId"))
				if err != nil {
					return nil, err
				}
				d := a.Int("depth")
				ob.Bids, ob.Asks = ob.Bids[:min(d, len(ob.Bids))], ob.Asks[:min(d, len(ob.Asks))]
				return ob, nil
			},
		},
		{
			Name: "get_orderflow", Path: "orderflow", Summary: "Taker buy/sell volume per bar (delta), optional footprint",
			Desc: "Per bar: buy / sell = taker buy and sell USDT, n = trade count, src = trades (recorded here from the public trade stream) or rubik (OKX taker-volume history for 5m+ bars, no footprint). " +
				"Bars without full coverage are omitted: a missing bar is unknown, not zero. since = earliest locally recorded trade minute in range (older bars can only come from rubik). " +
				"The newest bar may be the forming one and can change. levels=true adds the footprint [price, buy, sell] with a shared price step.",
			Params: []Param{instParam("query"), barParam("15m"), limitParam(200, 1000, "number of bars"),
				{Name: "levels", In: "query", Type: "boolean", Default: false, Desc: "include per-price footprint levels (large)"}},
			CostTxt: "1; levels=true costs 3",
			cost: func(a Args) int {
				if a.Bool("levels") {
					return 3
				}
				return 1
			},
			run: func(ctx context.Context, a Args) (any, error) {
				out, err := e.OrderFlow(ctx, a.Str("instId"), a.Str("bar"), a.Int("limit"), a.Bool("levels"))
				out.Bars = tail(out.Bars, a.Int("limit"))
				return out, err
			},
		},
		{
			Name: "get_exhaustion", Path: "exhaustion", Summary: "Top / bottom exhaustion scores (0–100) with the checklist and a fade plan",
			Desc: "How close the latest move is to topping out or bottoming: extension from EMA20 in ATR, size of the run, volume climax, " +
				"rejection wick or pullback, RSI and divergence, liquidation spike on the squeezed side, funding percentile, taker flow; " +
				"gated by where the extreme sits in the last 30 bars. Same checklist as the agent's rev tool. Heuristic, not validated.",
			Params:  []Param{instParam("query"), {Name: "bar", In: "query", Type: "string", Enum: []string{"15m", "1H", "4H"}, Default: "1H", Desc: "bar size"}},
			CostTxt: "2", cost: func(Args) int { return 2 },
			run: func(ctx context.Context, a Args) (any, error) {
				return e.Exhaustion(ctx, a.Str("instId"), a.Str("bar"))
			},
		},
		{
			Name: "get_liquidations", Path: "liquidations", Summary: "Liquidations per bar and recent liquidation orders",
			Desc: "Per bar: long / short = USDT of long / short positions liquidated, nl / ns = counts. Only bars with liquidations are listed: a bar missing at or after since means zero, " +
				"before since means unknown (not recorded). err is set when the latest sync with OKX failed and stored data was served. recent = newest liquidation orders.",
			Params: []Param{instParam("query"), barParam("1H"), limitParam(200, 1000, "number of bars"),
				{Name: "recent", In: "query", Type: "integer", Default: 20, Min: 0, Max: 500, Desc: "number of most recent liquidation orders"}},
			run: func(ctx context.Context, a Args) (any, error) {
				out, err := e.Liquidations(ctx, a.Str("instId"), a.Str("bar"), a.Int("limit"), a.Int("recent"))
				out.Bars = tail(out.Bars, a.Int("limit"))
				return out, err
			},
		},
		{
			Name: "get_liquidation_map", Path: "liqmap", Summary: "Estimated liquidation price map",
			Desc: "ESTIMATE, not exchange data: open-interest increments over the look-back (5m steps with per-minute OI from `fine` on, hourly before) spread across 5-100x leverage tiers " +
				"(levers, assumed shares in weights) at approximate liquidation prices, shrunk as OI falls and removed once price trades through. " +
				"bins = [{p: bin low price, long, short, longBy, shortBy}] in USDT; longBy / shortBy split the amount by tier in the order of levers.",
			Params:  []Param{instParam("query"), {Name: "days", In: "query", Type: "integer", Default: 7, Min: 1, Max: 7, Desc: "look-back days"}},
			CostTxt: "2", cost: func(Args) int { return 2 },
			run: func(ctx context.Context, a Args) (any, error) {
				m, err := e.LiqMap(ctx, a.Str("instId"), a.Int("days"))
				if err != nil {
					return nil, err
				}
				return struct {
					Estimate bool   `json:"estimate"`
					Basis    string `json:"basis"`
					market.LiqMapResp
				}{true, "model estimate from open-interest increments and assumed leverage tiers (levers, weights); not observed positions or exchange liquidation levels", m}, nil
			},
		},
		{
			Name: "list_rules", Path: "rules", Summary: "Scoring rules",
			Desc: "User-defined scoring rules: long/short field weights over pool percentiles and the spread gate. Score = 50 + 50 × Σ(w·x)/Σ|w|, x = percentile mapped to ±1. All rules are unvalidated until the track record supports them.",
			run: func(ctx context.Context, a Args) (any, error) {
				return map[string]any{"rules": e.Rules(), "defaultRule": e.Settings().DefaultRule}, nil
			},
		},
		{
			Name: "get_rule_track_record", Path: "rules/{ruleId}/track", Summary: "Out-of-sample track record of a rule",
			Desc: "Per horizon, for live snapshots (out-of-sample) and the 14-day replay (recomputed over recent history, which may overlap the period the rule was designed on): " +
				"Spearman Rank IC between score and forward log return within the pool, its t-stat and hit rate over non-overlapping cross-sections; " +
				"topExcessBps = top-quintile mean forward return minus the pool mean (gross, relative skill); " +
				"topNetBps = top-quintile absolute forward return minus 10 bps round-trip taker fees and the spread (replay assumes 2 bps), so it includes the pool's overall drift. " +
				"Short side uses −return. Metrics are null when not measured. A verdict needs ≥ 30 cross-sections; no rule is validated by default.",
			Params: []Param{{Name: "ruleId", In: "path", Type: "string", Required: true, Desc: "rule id from list_rules"}},
			run: func(ctx context.Context, a Args) (any, error) {
				rep, ok := e.Track(a.Str("ruleId"))
				if !ok {
					return nil, fmt.Errorf("unknown rule %q", a.Str("ruleId"))
				}
				return rep, nil
			},
		},
	}
	if al != nil {
		eps = append(eps, &Endpoint{
			Name: "get_alert_events", Path: "alerts/events", Summary: "Recent monitoring alerts that fired",
			Desc:   "Monitoring-rule events (price move, score, funding) newest first: time, instrument, rule name, value and message. Delivery details are omitted.",
			Params: []Param{limitParam(50, 500, "number of events")},
			run: func(ctx context.Context, a Args) (any, error) {
				evs, err := al.History(a.Int("limit"))
				if err != nil {
					return nil, err
				}
				for i := range evs {
					evs[i].Delivery = nil
				}
				return evs, nil
			},
		})
	}
	if s.positions != nil {
		eps = append(eps, &Endpoint{
			Name: "get_positions", Path: "positions", Summary: "The owner's open OKX swap positions with TP/SL (read-only)",
			Desc: "The owner's live OKX account, read-only (this server cannot place, change or cancel orders): per position side, contracts / coins / " +
				"notionalUsd, avgPx (entry), markPx, liqPx, lever, upl (USDT), uplRatio (upl / margin), mgnRatio, and the resting tp / sl triggers " +
				"(px, full = closes the whole position, else contracts), nearest first; equity = account totals in USD. enabled=false means no " +
				"account is configured. Missing numbers are unknown. Cached ~3 s.",
			Params: []Param{{Name: "instId", In: "query", Type: "string", Desc: "only this instrument, e.g. BTC-USDT-SWAP (default: all)"}},
			owner:  true,
			run: func(ctx context.Context, a Args) (any, error) {
				v, err := s.positions(ctx, strings.ToUpper(a.Str("instId")))
				if err != nil {
					return nil, err
				}
				b, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				var m map[string]any
				if err := json.Unmarshal(b, &m); err != nil {
					return nil, err
				}
				return roundFloats(m), nil
			},
		})
	}
	if s.okxHistory != nil {
		eps = append(eps, &Endpoint{
			Name: "okx_history", Path: "okx-history", Summary: "Totals of the owner's OKX history over a period, paged on the server (read-only)",
			Desc: "Pages through a whole period of one history and returns totals and groups — use this, not okx_api, for anything summed " +
				"over time (this year's P&L, deposits vs withdrawals, funding fees, C2C volume, fees). kind: positions = closed swap " +
				"positions (realizedPnl = pnl + fee + fundingFee + liqPenalty; wins / losses; goes back as far as OKX keeps, over a year) · " +
				"orders = swap orders (back to the start of the year or more) · fills / bills = about 3 months, one row per fill / fee / funding " +
				"payment, so long periods come back partial — use positions for P&L and funding fees · deposits / withdrawals = credited / " +
				"completed only, all history · " +
				"funding = funding-account bills, about 1 year (C2C: 117 completed buys; 116 sells escrowed at placement, 118 refunds of cancelled sells, completed sells = 116 + 118; transfers 130/131, Earn 75/76). " +
				"from / to = the oldest and newest row counted; complete=false means it stopped at the cap (15k rows or 45 s) before begin — " +
				"say so. group: inst, ccy, month, day, type, side, state (default per kind). Sums are in each row's own ccy " +
				"(group by ccy when currencies mix).",
			Params: []Param{
				{Name: "kind", In: "query", Type: "string", Required: true, Enum: []string{"positions", "fills", "orders", "bills", "deposits", "withdrawals", "funding"}},
				{Name: "begin", In: "query", Type: "string", Desc: "period start, Beijing date like 2026-01-01 (default: as far back as OKX keeps)"},
				{Name: "end", In: "query", Type: "string", Desc: "period end, e.g. 2026-11-01 (default: now)"},
				{Name: "instId", In: "query", Type: "string", Desc: "only this instrument (positions, fills, orders, bills)"},
				{Name: "ccy", In: "query", Type: "string", Desc: "only this currency (bills, deposits, withdrawals, funding)"},
				{Name: "group", In: "query", Type: "string", Enum: []string{"inst", "ccy", "month", "day", "type", "side", "state"}},
				{Name: "sample", In: "query", Type: "integer", Default: 0, Min: 0, Max: 30, Desc: "also return this many raw rows (newest first)"},
			},
			owner: true,
			run: func(ctx context.Context, a Args) (any, error) {
				return s.okxHistory(ctx, a)
			},
		})
	}
	if s.propose != nil {
		eps = append(eps, &Endpoint{
			Name: "propose_trade", Path: "propose-trade", Summary: "Propose one real OKX trade for the owner to approve (never executes)",
			Desc: "Creates a pending trade proposal on the owner's OKX account and returns its proposalId. Only one shape exists: a market " +
				"open of a USDT perpetual in isolated margin with BOTH a stop-loss and a take-profit attached (triggered on the last price, " +
				"closed at market). The owner approves or rejects it (Telegram, web, app); it expires unapproved after expiresMin (default 15) " +
				"and is re-checked against the live price before it is placed. You cannot execute, change or cancel it, and nothing is traded " +
				"until the owner approves — never say a trade was placed. Size: marginUsdt is the isolated margin in USDT, contracts = " +
				"marginUsdt × leverage / (price × contract value), rounded down. The server rejects: sl / tp on the wrong side of the current " +
				"price, stop closer than 0.3% or farther than 10%, take-profit farther than 40%, reward:risk below the minimum, leverage or " +
				"margin above the caps, a stop beyond the liquidation distance, an open proposal or position in the same instrument, too many " +
				"open AI positions, the daily loss stop. The error says which; fix the plan or don't trade. mode=dryrun means an approved " +
				"trade is only recorded, not sent.",
			Params: []Param{
				instParam("query"),
				{Name: "side", In: "query", Type: "string", Required: true, Enum: []string{"long", "short"}},
				{Name: "marginUsdt", In: "query", Type: "number", Required: true, Desc: "isolated margin in USDT (cap in the error / get_trade_proposals limits)"},
				{Name: "leverage", In: "query", Type: "integer", Required: true, Min: 1, Max: 125, Desc: "isolated leverage (capped by the server)"},
				{Name: "sl", In: "query", Type: "number", Required: true, Desc: "stop-loss trigger price"},
				{Name: "tp", In: "query", Type: "number", Required: true, Desc: "take-profit trigger price"},
				{Name: "rationale", In: "query", Type: "string", Required: true, Desc: "why this trade and why now, in Chinese, 2–6 sentences: the evidence for and against"},
				{Name: "invalidation", In: "query", Type: "string", Required: true, Desc: "what would prove the idea wrong before the stop, in Chinese, one sentence"},
				{Name: "expiresMin", In: "query", Type: "integer", Default: 15, Min: 3, Max: 240, Desc: "minutes the owner has to approve: as long as the setup stays valid at roughly this price — short for a fast breakout, longer for a level that holds or when a signal woke you and he may not be looking (approval re-checks price drift anyway)"},
			},
			owner: true,
			write: true,
			run: func(ctx context.Context, a Args) (any, error) {
				var chat int64
				if c := callerFrom(ctx); c != nil {
					chat = c.Chat
				}
				return s.propose(ctx, chat, a)
			},
		})
	}
	if s.proposals != nil {
		eps = append(eps, &Endpoint{
			Name: "get_trade_proposals", Path: "trade-proposals", Summary: "The trade proposals and their status, plus the mode and caps",
			Desc: "Recent proposals made with propose_trade, newest first: status pending (waiting for the owner) → executed (dryrun = recorded, " +
				"not sent; live = placed on OKX) / failed / invalidated (re-check failed, nothing sent) / rejected / expired; closeKind tp / sl and " +
				"pnlUsdt once a position closed. Also mode and limits (maxLeverage, maxMarginUsdt, minRR, maxOpen, dailyLossUsdt, maxDriftPct).",
			Params: []Param{
				{Name: "status", In: "query", Type: "string", Enum: []string{"all", "open", "pending", "executed"}, Default: "all"},
				limitParam(10, 50, "number of proposals"),
			},
			owner: true,
			run: func(ctx context.Context, a Args) (any, error) {
				return s.proposals(ctx, a)
			},
		})
	}
	if s.signals != nil {
		chatOf := func(ctx context.Context) int64 {
			if c := callerFrom(ctx); c != nil {
				return c.Chat
			}
			return 0
		}
		eps = append(eps, &Endpoint{
			Name: "subscribe_signal", Path: "subscribe-signal", Summary: "Wait for a market condition: wake this conversation when it turns true",
			Desc: "Subscribes this conversation to a server-side condition. When it turns from false to true, the server appends a ⚡ 信号 message " +
				"(the values that matched + your intent) to this conversation and runs you again with its full history, so you can re-check and " +
				"propose_trade (still needs the owner's approval), re-subscribe or explain. It never trades by itself. Use it when a setup is worth " +
				"waiting for but its entry conditions are not met yet; one subscription per setup. A condition that already holds is rejected. " +
				"spec (JSON object), one of: " +
				`{"type":"price","instId":"AAVE-USDT-SWAP","op":"above"|"below","level":140.2,"bar":"1H"} (bar omitted = last trade price; ` +
				`1m/5m/15m/1H/4H = that bar's confirmed close) · {"type":"zone","instId":…,"lo":139.5,"hi":140.2} (price enters the zone) · ` +
				`{"type":"field","instId":…,"field":"funding_hourly","op":">","value":0} (fact-panel fields as in get_universe: ret_15m ret_1h ` +
				"ret_4h ret_24h (log) oi_chg_1h oi_chg_4h (log) vol_surprise_4h (log of the multiple) funding_hourly funding_pred_hourly (rate per " +
				"hour) basis trend_z_4h excess_btc_4h resid_1h resid_4h trend_ema eff_ratio_24h beta_btc_48h spread_bps; checked each minute) · " +
				`{"type":"moverisk","instId":…,"side":"top"|"bottom","metric":"pTail"|"pBig3","op":"<","value":0.4} or metric "extended" with ` +
				"value 0 (leaves the extended state) / 1 (enters it) (rev's continuation risk on confirmed 1H bars) · " +
				`{"type":"alert","ruleId":12,"instId":…?} (an alert rule fires) · {"type":"time","afterMin":120} or {"type":"time","at":"2026-11-02 10:00"} ` +
				`(UTC+8; a re-check) · {"type":"plan","planId":34,"state":"active"|"tp1"|"done"|"stopped"|"invalid"|"missed"|"expired"|"settled"} ` +
				`(an AI plan from a plan block reaches that state; active = entry zone touched) · {"type":"script"} with the Python in code (a ctg ` +
				"Condition class run on every close of its timeframe — for indicator, multi-condition or relative logic the built-ins cannot " +
				"express; test it with test_signal_script first; subscribing runs the same compile + replay check and rejects scripts that error, " +
				"are slow, would fire more than the daily cap or hold right now; at most one script per subscription, every ≠ tick) · " +
				`{"type":"all"|"any","of":[…]} (≤ 6 conditions). ` +
				"Put the invalidation in intent or subscribe it separately. Caps: 5 active per conversation, expiry ≤ 7 days, wake-ups per day are capped.",
			Params: []Param{
				{Name: "spec", In: "query", Type: "object", Required: true, Desc: "the condition, see the description"},
				{Name: "code", In: "query", Type: "string", Desc: "the Python of a script condition ({\"type\":\"script\"} in spec), exactly as tested with test_signal_script"},
				{Name: "intent", In: "query", Type: "string", Required: true, Desc: "in Chinese: what you will do when it fires — the plan (side, entry, stop, targets) and its invalidation; you read it back on wake-up"},
				{Name: "expiresHours", In: "query", Type: "number", Default: 24, Desc: "hours until it lapses unfired (0.25–168)"},
				{Name: "planId", In: "query", Type: "integer", Desc: "the AI plan (```plan block) this setup belongs to, if any"},
				{Name: "maxFires", In: "query", Type: "integer", Default: 1, Min: 1, Max: 5, Desc: "fires before it ends"},
				{Name: "cooldownMin", In: "query", Type: "integer", Default: 60, Min: 5, Max: 1440, Desc: "minutes between two fires (maxFires > 1)"},
				{Name: "every", In: "query", Type: "string", Enum: []string{"tick", "1m", "5m", "15m", "1H"}, Desc: "how often the server checks it: " +
					"tick = each ~2 s price update (only for a fast level / zone you must catch at once), 1m, 5m, 15m, 1H (on that confirmed " +
					"minute). Pick the slowest that still serves the setup — most waits (a 1H close, funding flip, risk easing, a level hours " +
					"away) need 5m–1H. Default: tick for last-price levels / zones / times, else 1m. A brief spike between checks can be missed."},
			},
			owner: true,
			write: true,
			run:   func(ctx context.Context, a Args) (any, error) { return s.signals.Subscribe(ctx, chatOf(ctx), a) },
		}, &Endpoint{
			Name: "list_signals", Path: "signals", Summary: "Signal subscriptions: this conversation's or everyone's, with distance to firing",
			Desc: "Subscriptions made with subscribe_signal (and the owner's own): signalId, condition, subscriber, intent, expiry, fires, and " +
				"for active ones how far the price is from firing. Also the caps and today's wake-ups. Check it before subscribing to avoid duplicates.",
			Params: []Param{
				{Name: "scope", In: "query", Type: "string", Enum: []string{"mine", "all"}, Default: "mine"},
				{Name: "status", In: "query", Type: "string", Enum: []string{"active", "fired", "ended", "all"}, Default: "active"},
				{Name: "instId", In: "query", Type: "string", Desc: "only this instrument"},
			},
			owner: true,
			run:   func(ctx context.Context, a Args) (any, error) { return s.signals.List(ctx, chatOf(ctx), a) },
		}, &Endpoint{
			Name: "unsubscribe_signal", Path: "unsubscribe-signal", Summary: "Cancel a signal subscription",
			Desc: "Cancels an active subscription of this conversation (when the setup is invalidated or replaced). Another subscriber's only " +
				"when the owner explicitly asked you to (ownerAsked=true).",
			Params: []Param{
				{Name: "id", In: "query", Type: "integer", Required: true, Desc: "signalId"},
				{Name: "reason", In: "query", Type: "string", Required: true, Desc: "why, in Chinese, one sentence"},
				{Name: "ownerAsked", In: "query", Type: "boolean", Default: false, Desc: "the owner asked to cancel it (needed for subscriptions of other conversations)"},
			},
			owner: true,
			write: true,
			run:   func(ctx context.Context, a Args) (any, error) { return s.signals.Unsubscribe(ctx, chatOf(ctx), a) },
		})
		if s.signals.TestScript != nil {
			eps = append(eps, &Endpoint{
				Name: "test_signal_script", Path: "test-signal-script", Summary: "Compile a script signal and replay it over recent history (stores nothing)",
				Desc: "Checks a script condition before subscribe_signal: compiles it (imports only ctg, numpy, math, statistics, collections, " +
					"itertools, functools, typing; no files or network), reads what it declares and replays check on every close of its timeframe " +
					"over the last 7 days (≤ 1000 bars incl. lookback). Returns ok, or problem (compile | error | slow | often | unknown | holds | nodata) " +
					"with the reason; the replay: closes, fires (false→true changes) and fires per day, the last fires with their values, " +
					"unknown closes, errors, average / max ms, holdsShare, and now (the newest close). Format:\n" +
					"from ctg import Condition, Fire, ta\n" +
					"class AaveRsiReclaim(Condition):\n" +
					"    \"\"\"AAVE 15m RSI 从 30 以下收回\"\"\"          # one line shown with the signal\n" +
					"    instruments = [\"AAVE-USDT-SWAP\"]           # 1–10\n" +
					"    timeframe = \"15m\"                          # 1m 5m 15m 1H 4H: check runs on each confirmed close\n" +
					"    lookback = 100                               # bars per instrument, 10–500\n" +
					"    def check(self, ctx, bars):                  # bars = {instId: Bars}, numpy ts o h l c vol volq, newest last\n" +
					"        r = ta.rsi(bars[\"AAVE-USDT-SWAP\"].c, 14)\n" +
					"        return Fire(r[-2] < 30 <= r[-1], values={\"rsi\": r[-1]}, note=f\"RSI {r[-1]:.1f}\")\n" +
					"check returns True / False, Fire(hit, values={…≤12 numbers}, note=\"one line\") or None (unknown: never fires). ctx.now (ms, the close), " +
					"ctx.bars(inst, n), ctx.factors(inst) (fact-panel fields live; NaN in replays). It must be a pure function of the bars: " +
					"nothing persists between calls. ta: sma ema rma rsi atr tr macd bbands crossover crossunder cross highest lowest stdev change.",
				Params: []Param{
					{Name: "code", In: "query", Type: "string", Required: true, Desc: "the Python source (≤ 8000 bytes)"},
				},
				owner: true,
				run:   func(ctx context.Context, a Args) (any, error) { return s.signals.TestScript(ctx, chatOf(ctx), a) },
			})
		}
	}
	if s.okxRead != nil {
		eps = append(eps, &Endpoint{
			Name: "okx_api", Path: "okx", Summary: "Read any private OKX API endpoint of the owner's account (GET only, read-only key)",
			Desc: "One signed GET to OKX's v5 REST API for the owner's own account, for questions the other tools don't cover. Read-only: " +
				"GET under /api/v5/account/, asset/, trade/, finance/, tradingBot/, copytrading/, users/; nothing can be placed, moved or withdrawn. " +
				"Common paths (params; times are ms; pages via after/before = the last row's id or ts; limit ≤ 100): " +
				"deposits /api/v5/asset/deposit-history (ccy, state 2=credited, after, before, limit) · withdrawals /api/v5/asset/withdrawal-history " +
				"(ccy, state 2=done, after, before, limit) · funding-account bills /api/v5/asset/bills (1 month) and /api/v5/asset/bills-history (about 1 year) " +
				"(ccy, type, after/before = ts, limit; types: 1 deposit, 2 withdrawal, 130/131 from/to trading account, 75/76 Simple Earn in/out, " +
				"173 fee rebate; C2C (P2P): 117 = a buy, credited only when completed; 116 = a sell, USDT escrowed when placed, and 118 refunds " +
				"it if cancelled, so completed sells = 116 + 118 — balChg is the crypto that moved; the fiat price, amount " +
				"and counterparty are in no API, OKX's P2P API is merchant-only) · " +
				"trading-account bills /api/v5/account/bills (7 days) and /api/v5/account/bills-archive (3 months) (instType, ccy, type 2=trade 8=funding fee, " +
				"after/before = billId, begin/end ms) · fills /api/v5/trade/fills-history (instType=SWAP required, 3 months) · orders " +
				"/api/v5/trade/orders-history (7 days) and orders-history-archive (3 months) (instType=SWAP required) · closed positions " +
				"/api/v5/account/positions-history (instType, instId, limit) · funding balances /api/v5/asset/balances · account valuation " +
				"/api/v5/asset/asset-valuation (ccy) · fee tier /api/v5/account/trade-fee (instType=SWAP) · account config /api/v5/account/config. " +
				"Output: data rows with empty fields dropped and ms times (ts, cTime, uTime, fillTime …) also given in Beijing time; " +
				"use fields to keep only the columns you need. Sum or count from the rows yourself and say which period they cover.",
			Params: []Param{
				{Name: "path", In: "query", Type: "string", Required: true, Desc: "the endpoint path, e.g. /api/v5/asset/deposit-history"},
				{Name: "query", In: "query", Type: "string", Desc: "OKX query parameters as k=v&k=v, e.g. ccy=USDT&limit=100"},
				{Name: "fields", In: "query", Type: "string", Desc: "comma-separated columns to keep, e.g. ts,ccy,amt,state (default all)"},
				{Name: "limit", In: "query", Type: "integer", Default: 100, Min: 1, Max: 300, Desc: "rows returned at most"},
			},
			owner: true,
			run: func(ctx context.Context, a Args) (any, error) {
				path := a.Str("path")
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				v, err := s.okxRead(ctx, path, a.Str("query"))
				if err != nil {
					return nil, err
				}
				return okxRows(v, a.Str("fields"), a.Int("limit")), nil
			},
		})
	}
	return eps
}

var tz8 = time.FixedZone("UTC+8", 8*3600)

// okxRows trims an OKX data array for the model: chosen columns, no empty values, ms times also as
// Beijing time, at most limit rows.
func okxRows(v any, fields string, limit int) any {
	rows, ok := v.([]any)
	if !ok {
		return v
	}
	if limit <= 0 {
		limit = 100
	}
	keep := map[string]bool{}
	for _, f := range strings.Split(fields, ",") {
		if f = strings.TrimSpace(f); f != "" {
			keep[f] = true
		}
	}
	out := make([]any, 0, min(len(rows), limit))
	for _, r := range rows[:min(len(rows), limit)] {
		m, ok := r.(map[string]any)
		if !ok {
			out = append(out, r)
			continue
		}
		c := map[string]any{}
		for k, x := range m {
			if len(keep) > 0 && !keep[k] {
				continue
			}
			if str, ok := x.(string); ok {
				if str == "" {
					continue
				}
				if (k == "ts" || strings.HasSuffix(k, "Time")) && len(str) == 13 {
					if ms, err := strconv.ParseInt(str, 10, 64); err == nil {
						c[k+"_bj"] = time.UnixMilli(ms).In(tz8).Format("2006-01-02 15:04:05")
					}
				}
			}
			if arr, ok := x.([]any); ok && len(arr) == 0 {
				continue
			}
			c[k] = x
		}
		out = append(out, c)
	}
	res := map[string]any{"rows": len(rows), "data": out}
	if len(rows) > limit {
		res["truncated"] = fmt.Sprintf("showing %d of %d rows", limit, len(rows))
	}
	return res
}

// tail keeps the newest n items (the engine pads short requests to at least 10 bars).
func tail[T any](xs []T, n int) []T {
	if n > 0 && len(xs) > n {
		return xs[len(xs)-n:]
	}
	return xs
}

type wsView struct {
	Name         string `json:"name"`
	Connected    bool   `json:"connected"`
	LastMsgAgoMs int64  `json:"lastMsgAgoMs"`
}

func publicStatus(e *market.Engine) any {
	st := e.Status()
	snap := e.Snapshot()
	ws := make([]wsView, 0, len(st.WS))
	for _, w := range st.WS {
		ws = append(ws, wsView{w.Name, w.Connected, w.LastMsgAgoMs})
	}
	now := time.Now().UnixMilli()
	return map[string]any{
		"phase": st.Phase, "live": st.Phase == "live" && now-st.ComputedAt < 3*60_000, "uptime": st.Uptime,
		"asOfMinute": st.AsOfMinute, "computedAt": st.ComputedAt, "serverTime": now,
		"preview": snap.Preview, "previewAt": snap.PreviewAt, "provisional": snap.Provisional,
		"coverage": st.Coverage, "poolThreshold": snap.Threshold, "poolTopN": snap.TopN,
		"streams": ws, "backfill": map[string]int64{"done": st.BackfillDone, "total": st.BackfillTotal},
		"replay": st.Replay, "history": st.History,
	}
}

func universe(e *market.Engine, a Args) (any, error) {
	snap := e.Snapshot()
	ruleID := a.Str("rule")
	if ruleID == "" {
		ruleID = e.Settings().DefaultRule
	}
	if ruleID != "*" {
		if _, ok := e.Rule(ruleID); !ok {
			return nil, fmt.Errorf("unknown rule %q (see list_rules)", ruleID)
		}
	}
	scope, cat := a.Str("scope"), a.Str("category")
	rows := make([]market.Row, 0, len(snap.Rows))
	for _, r := range snap.Rows {
		switch {
		case scope == "watch" && !r.Watch, scope == "active" && !r.Active && !r.Watch, cat != "" && r.Category != cat:
			continue
		}
		if ruleID != "*" && r.Scores != nil {
			sc, ok := r.Scores[ruleID]
			r.Scores = map[string]rules.Score{}
			if ok {
				r.Scores[ruleID] = sc
			}
		}
		rows = append(rows, r)
	}
	key, asc := a.Str("sort"), a.Str("order") == "asc"
	val := func(r market.Row) (float64, bool) {
		switch key {
		case "turnover12h":
			return r.Turnover12h, true
		case "long", "short":
			sc, ok := r.Scores[ruleID]
			if !ok {
				return 0, false
			}
			side := sc.Long
			if key == "short" {
				side = sc.Short
			}
			if side.Score == nil {
				return 0, false
			}
			return *side.Score, true
		}
		v, ok := r.Values[key]
		return v, ok
	}
	if key != "turnover12h" && key != "long" && key != "short" && !slices.ContainsFunc(features.Fields, func(f features.Field) bool { return f.Key == key }) {
		return nil, fmt.Errorf("unknown sort key %q (turnover12h, long, short or a field from list_fields)", key)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		vi, oi := val(rows[i])
		vj, oj := val(rows[j])
		if oi != oj {
			return oi
		}
		if asc {
			return vi < vj
		}
		return vi > vj
	})
	total := len(rows)
	rows = rows[:min(len(rows), a.Int("limit"))]
	head := map[string]any{
		"asOfMinute": snap.AsOfMinute, "phase": snap.Phase, "threshold": snap.Threshold, "topN": snap.TopN, "rule": ruleID, "total": total,
	}
	if fs := a.Str("fields"); fs != "" {
		keys := strings.Split(fs, ",")
		for i, k := range keys {
			keys[i] = strings.TrimSpace(k)
			if !slices.ContainsFunc(features.Fields, func(f features.Field) bool { return f.Key == keys[i] }) {
				return nil, fmt.Errorf("unknown field %q (see list_fields)", keys[i])
			}
		}
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			v, p := map[string]float64{}, map[string]float64{}
			for _, k := range keys {
				if x, ok := r.Values[k]; ok {
					v[k] = x
				}
				if x, ok := r.Pct[k]; ok {
					p[k] = x
				}
			}
			c := map[string]any{"instId": r.InstID, "rank": r.Rank, "last": r.Last, "turnover12h": r.Turnover12h, "oiUsd": r.OiUsd, "v": v, "p": p}
			if sc, ok := r.Scores[ruleID]; ok {
				c["long"], c["short"] = sc.Long.Score, sc.Short.Score
			}
			out = append(out, c)
		}
		head["rows"] = out
		return head, nil
	}
	return map[string]any{
		"seq": snap.Seq, "asOfMinute": snap.AsOfMinute, "decisionTs": snap.DecisionTs, "computedAt": snap.ComputedAt,
		"preview": snap.Preview, "previewAt": snap.PreviewAt, "provisional": snap.Provisional, "phase": snap.Phase,
		"threshold": snap.Threshold, "topN": snap.TopN, "coverage": snap.Coverage, "rule": ruleID,
		"total": total, "rows": rows, "removed": snap.Removed, "validation": validation(e, ruleID),
	}, nil
}

// validation summarises the evidence behind the scores shown, so a score is never read without it.
func validation(e *market.Engine, ruleID string) any {
	if ruleID == "*" {
		return map[string]string{"status": "unvalidated", "note": "scores rank instruments within the pool; see get_rule_track_record per rule"}
	}
	rep, ok := e.Track(ruleID)
	if !ok {
		return nil
	}
	type hz struct {
		Kind      string `json:"kind"`
		HorizonH  int    `json:"horizonH"`
		CrossSect int    `json:"crossSections"`
		Verdict   string `json:"verdict"`
	}
	var hs []hz
	for _, x := range rep.Live {
		hs = append(hs, hz{"live", x.HorizonH, x.CrossSects, x.Verdict})
	}
	for _, x := range rep.Replay {
		hs = append(hs, hz{"replay", x.HorizonH, x.CrossSects, x.Verdict})
	}
	return map[string]any{"status": "unvalidated", "ruleId": rep.RuleID, "version": rep.Version, "horizons": hs,
		"note": "scores are percentile ranks within the pool (50 = neutral), not probabilities or trade signals; they do not check order flow, the book or liquidity beyond the spread gate. See get_rule_track_record."}
}

// rebase renormalizes compare points to 100 at the first returned bar.
func rebase(pts []market.ComparePoint) []market.ComparePoint {
	if len(pts) == 0 {
		return pts
	}
	b := pts[0].V
	out := make([]market.ComparePoint, len(pts))
	for i, p := range pts {
		v := make(map[string]float64, len(p.V))
		for k, x := range p.V {
			if b[k] > 0 {
				v[k] = x / b[k] * 100
			}
		}
		out[i] = market.ComparePoint{T: p.T, V: v}
	}
	return out
}

// compactInstrument drops the per-field score contributions (most of the payload) and rounds floats to
// 6 significant digits, so a quick lookup ("ETH 资金费多少") doesn't cost an agent thousands of tokens.
func compactInstrument(info any) (any, error) {
	b, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if row, ok := m["row"].(map[string]any); ok {
		if sc, ok := row["scores"].(map[string]any); ok {
			for rule, v := range sc {
				r, _ := v.(map[string]any)
				out := map[string]any{"gate": r["gate"]}
				for _, side := range []string{"long", "short"} {
					if x, ok := r[side].(map[string]any); ok {
						out[side] = map[string]any{"score": x["score"]} // same shape as full, minus "contrib"
					}
				}
				sc[rule] = out
			}
		}
	}
	return roundFloats(m), nil
}

func roundFloats(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, y := range x {
			x[k] = roundFloats(y)
		}
	case []any:
		for i, y := range x {
			x[i] = roundFloats(y)
		}
	case float64:
		if x == math.Trunc(x) || math.IsInf(x, 0) || math.IsNaN(x) {
			return x
		}
		f, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'g', 6, 64), 64)
		return f
	}
	return v
}
