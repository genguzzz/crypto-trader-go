package devapi

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/market"
)

// Microstructure tools: the recorded trade tape (spot + swap), live books with order counts,
// recorded wall lifecycles, a one-call dump / absorption / spot-control check, and DEX swaps.

func (a Args) Float(k string) float64 { v, _ := strconv.ParseFloat(a[k], 64); return v }

func coinParam() Param {
	return Param{Name: "instId", In: "query", Type: "string", Required: true, Desc: "coin or OKX id: ENA, ENA-USDT (spot) or ENA-USDT-SWAP"}
}

var windowParams = []Param{
	{Name: "hours", In: "query", Type: "number", Default: 24, Desc: "window length in hours (0.25–48; trades are kept 48 h)"},
	{Name: "end", In: "query", Type: "string", Desc: "window end, Beijing time '2026-11-01 20:00' or unix ms (default now)"},
}

// window turns hours / end into [from, to) ms.
func window(a Args, maxH float64) (int64, int64, error) {
	h := a.Float("hours")
	if h < 0.25 || h > maxH {
		return 0, 0, fmt.Errorf("hours must be between 0.25 and %g", maxH)
	}
	to := time.Now().UnixMilli()
	if s := strings.TrimSpace(a.Str("end")); s != "" {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			to = ms
		} else {
			var t time.Time
			for _, f := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"} {
				if t, err = time.ParseInLocation(f, s, tz8); err == nil {
					break
				}
			}
			if err != nil {
				return 0, 0, fmt.Errorf("end: use '2026-11-01 20:00' (Beijing) or unix ms")
			}
			to = t.UnixMilli()
		}
	}
	return to - int64(h*3.6e6), to, nil
}

func floats(s string) ([]float64, error) {
	var out []float64
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x == "" {
			continue
		}
		v, err := strconv.ParseFloat(x, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("bad number %q", x)
		}
		if len(out) > 0 && v <= out[len(out)-1] {
			return nil, fmt.Errorf("values must increase: %s", s)
		}
		out = append(out, v)
	}
	return out, nil
}

var barMs = map[string]int64{"1m": 60_000, "5m": 300_000, "15m": 900_000, "1H": 3_600_000, "4H": 14_400_000}

// markets resolves the instruments a market=swap|spot|both call reads.
func markets(e *market.Engine, inst, m string) (swap, spot string, err error) {
	swap, spot, err = e.ResolvePair(inst)
	if err != nil {
		return
	}
	switch m {
	case "swap":
		spot = ""
		if swap == "" {
			err = fmt.Errorf("%s has no USDT perpetual on OKX", inst)
		}
	case "spot":
		swap = ""
		if spot == "" {
			err = fmt.Errorf("%s has no USDT spot pair on OKX", inst)
		}
	}
	if swap != "" {
		e.RequestTape(swap)
	} else if spot != "" {
		e.RequestTape(spot)
	}
	return
}

func (s *Service) microEndpoints(e *market.Engine) []*Endpoint {
	marketParam := Param{Name: "market", In: "query", Type: "string", Enum: []string{"both", "swap", "spot"}, Default: "both", Desc: "perpetual, USDT spot pair, or both side by side"}
	return []*Endpoint{
		{
			Name: "analyze_trades", Path: "trades/analysis", Summary: "Trade-tape statistics over a window: size distribution, large-order delta, repeated clip sizes, sweeps, profile, spot vs swap",
			Desc: "Every public trade of the perpetual and its USDT spot pair, grouped into taker orders (same ms + side = one aggressive order), over up to 48 h. " +
				"Recorded live for the pool's top 50 (kept 2 h after leaving it), the watchlist and coins viewed or asked about (asking starts recording for 6 h); missing minutes are backfilled " +
				"from OKX daily trade files and history-trades — coverage.pct says how complete the window is, coverage.backfill whether a download is still running. Sections (comma list):\n" +
				"summary — buy/sell/delta USDT, OHLC, VWAP, taker-order size percentiles, largeBuy/SellUsd and largeDeltaPct (orders ≥ largeUsd vs the rest: who drove the delta), " +
				"top1PctShare (volume share of the largest 1% of orders), lambdaBpsPer100k (1m price impact per 100k net flow, OLS) and wash (opposite-side identical non-round sizes within 2 s at the same price).\n" +
				"sizes — buy/sell/net per size bucket (default edges at the p50/p90/p99/p99.9 order size; or sizeEdges).\n" +
				"bars — per bar o/h/l/c, buy, sell, cvd, largeBuy/largeSell.\n" +
				"profile — volume by price bin (buy/sell and large), poc.\n" +
				"clips — runs of identical-size taker orders on one side (algorithmic slicing: 'selling in fixed chunks'): count, usdEach, span, medGapSec, gapCV (<0.5 = clockwork), " +
				"px0→px1, driftBps, withTrendPct (% of steps that printed worse for the clipper = pushing price), sideSharePct (its share of same-side volume meanwhile), " +
				"oppSame (same size on the other side = a bot on both sides), round (hand-typed size, weaker), pattern burst|clockwork|ladder|repeat.\n" +
				"sweeps — taker orders that ate ≥ 3 price levels and moved price ≥ sweepBps: counts, USDT per side, the largest with impact.\n" +
				"large — the largest taker orders, with the move 1 and 5 min later (+ = in the order's direction).\n" +
				"cross (market=both) — spot share of volume, both deltas, lead-lag of 5 s returns (leader spot|swap|none), basis, impact per market.",
			Params: append(append([]Param{coinParam(), marketParam}, windowParams...),
				Param{Name: "sections", In: "query", Type: "string", Default: "summary,sizes,clips,sweeps,cross", Desc: "comma list of summary, sizes, bars, profile, clips, sweeps, large, cross"},
				Param{Name: "sizeEdges", In: "query", Type: "string", Desc: "size bucket edges in USDT, increasing, e.g. 1000,10000,50000,200000"},
				Param{Name: "largeUsd", In: "query", Type: "number", Desc: "large-order threshold in USDT (default ≈ p99 order size)"},
				Param{Name: "bar", In: "query", Type: "string", Enum: []string{"auto", "1m", "5m", "15m", "1H", "4H"}, Default: "auto", Desc: "bars section: bar size (auto ≤ 48 bars)"},
				Param{Name: "profileBins", In: "query", Type: "integer", Default: 24, Min: 4, Max: 100, Desc: "profile section: price bins"},
				Param{Name: "clipMin", In: "query", Type: "integer", Default: 5, Min: 3, Max: 200, Desc: "clips: minimum repeats in a run"},
				Param{Name: "clipMinUsd", In: "query", Type: "number", Desc: "clips: ignore orders below this USDT (default ≈ p90 order size, ≥ 200)"},
				Param{Name: "clipTolPct", In: "query", Type: "number", Default: 0, Desc: "clips: sizes within ±this % count as the same (0 = identical)"},
				Param{Name: "clipGapMin", In: "query", Type: "number", Default: 30, Desc: "clips: a run ends after this many minutes without a repeat"},
				Param{Name: "sweepBps", In: "query", Type: "number", Default: 5, Desc: "sweeps: minimum first→last fill move in bps"},
				Param{Name: "side", In: "query", Type: "string", Enum: []string{"all", "buy", "sell"}, Default: "all", Desc: "clips / sweeps / large: only this taker side"},
				Param{Name: "minUsd", In: "query", Type: "number", Default: 0, Desc: "clips / sweeps / large: ignore orders below this USDT"},
				Param{Name: "top", In: "query", Type: "integer", Default: 8, Min: 1, Max: 100, Desc: "rows per list"},
				Param{Name: "fetch", In: "query", Type: "boolean", Default: true, Desc: "backfill missing minutes from OKX (waits up to 25 s)"}),
			CostTxt: "3", cost: func(Args) int { return 3 },
			run: func(ctx context.Context, a Args) (any, error) {
				swap, spot, err := markets(e, a.Str("instId"), a.Str("market"))
				if err != nil {
					return nil, err
				}
				from, to, err := window(a, 48)
				if err != nil {
					return nil, err
				}
				q := market.TapeQuery{From: from, To: to, Sections: map[string]bool{}, LargeUsd: a.Float("largeUsd"), BarMs: barMs[a.Str("bar")],
					ProfileBins: a.Int("profileBins"), ClipMin: a.Int("clipMin"), ClipMinUsd: a.Float("clipMinUsd"), ClipTolPct: a.Float("clipTolPct"),
					ClipGapMin: a.Float("clipGapMin"), Top: a.Int("top"), MinUsd: a.Float("minUsd"), SweepBps: a.Float("sweepBps")}
				for _, x := range strings.Split(a.Str("sections"), ",") {
					q.Sections[strings.TrimSpace(x)] = true
				}
				if q.SizeEdges, err = floats(a.Str("sizeEdges")); err != nil {
					return nil, fmt.Errorf("sizeEdges: %w", err)
				}
				switch a.Str("side") {
				case "buy":
					q.Side = 1
				case "sell":
					q.Side = -1
				}
				return e.AnalyzeTrades(ctx, swap, spot, q, a.Bool("fetch"))
			},
		},
		{
			Name: "get_trades", Path: "trades", Summary: "Raw taker orders from the trade tape (for your own statistics in Python)",
			Desc: "Taker orders (trades grouped by ms + side) of one market as rows [t ms, side 1 buy / -1 sell, vwap px, usd, qty (contracts for swap, coins for spot), price levels], " +
				"filtered by size and side, newest or oldest first, plus coverage. Use analyze_trades for ready-made statistics; this is for distributions it does not cover.",
			Params: append(append([]Param{coinParam(),
				{Name: "market", In: "query", Type: "string", Enum: []string{"swap", "spot"}, Default: "swap"}}, windowParams...),
				Param{Name: "minUsd", In: "query", Type: "number", Default: 0, Desc: "only orders ≥ this USDT"},
				Param{Name: "side", In: "query", Type: "string", Enum: []string{"all", "buy", "sell"}, Default: "all"},
				Param{Name: "limit", In: "query", Type: "integer", Default: 1000, Min: 1, Max: 20000},
				Param{Name: "order", In: "query", Type: "string", Enum: []string{"newest", "oldest"}, Default: "newest"},
				Param{Name: "fetch", In: "query", Type: "boolean", Default: true, Desc: "backfill missing minutes from OKX"}),
			CostTxt: "3", cost: func(Args) int { return 3 },
			run: func(ctx context.Context, a Args) (any, error) {
				swap, spot, err := markets(e, a.Str("instId"), a.Str("market"))
				if err != nil {
					return nil, err
				}
				from, to, err := window(a, 48)
				if err != nil {
					return nil, err
				}
				side := 0
				switch a.Str("side") {
				case "buy":
					side = 1
				case "sell":
					side = -1
				}
				return e.TapeOrders(ctx, firstSet(swap, spot), from, to, a.Bool("fetch"), a.Float("minUsd"), side, a.Int("limit"), a.Str("order") == "newest")
			},
		},
		{
			Name: "get_book_analysis", Path: "book/analysis", Summary: "Live order book of swap and spot: depth bands, distribution by distance, walls with order counts and age",
			Desc: "Up to 5000 levels per side now. bands: USDT within ±pct of mid per side, imbalance (bid−ask)/(bid+ask) and order counts. " +
				"buckets (buckets=true): USDT and orders per distance bucket from mid. walls: levels ≥ wallMult × the median level within bandPct (and ≥ minWallUsd) with " +
				"orders (1 order = one big player; many = a crowd), avgOrderUsd, xMedian, and for live-tracked books ageMin (how long it has stood) and filledUsd (traded against it). " +
				"medianLevelUsd, avgOrderUsd per side, top10SharePct (depth concentration), depth2PctOfTurnover ((bid+ask within ±2%) / 24h turnover: thin books are easy to push).",
			Params: []Param{coinParam(), marketParam,
				{Name: "bandPct", In: "query", Type: "number", Default: 3, Desc: "walls and buckets within ±this % of mid"},
				{Name: "bands", In: "query", Type: "string", Default: "0.25,0.5,1,2,5", Desc: "depth bands, % from mid, increasing"},
				{Name: "buckets", In: "query", Type: "boolean", Default: false, Desc: "include the distance histogram"},
				{Name: "bucketBps", In: "query", Type: "number", Desc: "histogram bucket width in bps (default ≈ 20 buckets)"},
				{Name: "wallMult", In: "query", Type: "number", Default: 6, Desc: "wall = level ≥ this × median level"},
				{Name: "minWallUsd", In: "query", Type: "number", Default: 0, Desc: "and at least this USDT"},
				{Name: "top", In: "query", Type: "integer", Default: 5, Min: 1, Max: 50, Desc: "walls per side"}},
			CostTxt: "3", cost: func(Args) int { return 3 },
			run: func(ctx context.Context, a Args) (any, error) {
				swap, spot, err := markets(e, a.Str("instId"), a.Str("market"))
				if err != nil {
					return nil, err
				}
				bands, err := floats(a.Str("bands"))
				if err != nil {
					return nil, fmt.Errorf("bands: %w", err)
				}
				bp := a.Float("bandPct")
				if bp <= 0 || bp > 50 {
					return nil, fmt.Errorf("bandPct must be in (0, 50]")
				}
				q := market.BookQuery{BandPct: bp, Bands: bands, BucketBps: a.Float("bucketBps"), WallMult: max(a.Float("wallMult"), 1), MinWall: a.Float("minWallUsd"), Top: a.Int("top")}
				out := map[string]any{}
				for _, id := range []string{swap, spot} {
					if id == "" {
						continue
					}
					b, err := e.BookShapeFor(ctx, id, q)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", id, err)
					}
					if !a.Bool("buckets") {
						b.Buckets = nil
					}
					out[b.Market] = b
				}
				return out, nil
			},
		},
		{
			Name: "get_wall_history", Path: "book/walls", Summary: "Recorded order-book walls: filled vs pulled, pulled as price approached, walls moving with price, book depth over time",
			Desc: "Walls (levels ≥ 8× the median level within ±3%, ≥ 3e-4 of 24h turnover) on live-tracked books (top of the pool, watchlist, viewed / asked-about coins; asking starts tracking for 6 h), kept 7 days. " +
				"Each wall: px, t0/t1, first/peak/end USDT, peakOrders, filledUsd (traded at its price while it stood), distance to mid at start / closest / end (bps) and outcome: " +
				"filled (trades ate ≥ 60% of the shrink: real interest), pulled (≤ 25%: cancelled), mixed, lost (stream gap), active (standing now). " +
				"Per side: counts and USDT per outcome, approachedN / approachedPulledN (walls price came within 30 bps of, and how many of those were cancelled untraded: the real-vs-fake test), pulledNear (cancelled once price was within 30 bps and at least halfway closer: spoof-like), medLifeMin, longLivedN (≥ 30 min), " +
				"movingChains (≥ 3 walls pulled and re-placed nearby within 10 s at similar size: a wall following price). book: per bar average depth within ±1 / ±2%, imbalance and USDT in walls.",
			Params: append(append([]Param{coinParam(), marketParam}, Param{Name: "hours", In: "query", Type: "number", Default: 24, Desc: "window in hours (0.25–168)"},
				windowParams[1]),
				Param{Name: "minUsd", In: "query", Type: "number", Default: 0, Desc: "only walls whose peak ≥ this USDT"},
				Param{Name: "minLifeSec", In: "query", Type: "integer", Default: 30, Min: 0, Max: 86400, Desc: "leave out walls that stood less than this (market makers re-quote large levels every few seconds)"},
				Param{Name: "events", In: "query", Type: "boolean", Default: true, Desc: "list the largest walls"},
				Param{Name: "top", In: "query", Type: "integer", Default: 8, Min: 1, Max: 100, Desc: "walls listed (events and active)"},
				Param{Name: "bar", In: "query", Type: "string", Enum: []string{"none", "5m", "15m", "1H", "4H"}, Default: "1H", Desc: "book-depth series bar (none = omit)"}),
			CostTxt: "2", cost: func(Args) int { return 2 },
			run: func(ctx context.Context, a Args) (any, error) {
				swap, spot, err := markets(e, a.Str("instId"), a.Str("market"))
				if err != nil {
					return nil, err
				}
				from, to, err := window(a, 168)
				if err != nil {
					return nil, err
				}
				out := map[string]any{}
				for _, id := range []string{swap, spot} {
					if id == "" {
						continue
					}
					w, err := e.WallHistory(id, from, to, barMs[a.Str("bar")], a.Float("minUsd"), a.Int("minLifeSec"), a.Int("top"), a.Bool("events"))
					if err != nil {
						return nil, err
					}
					out[w.Market] = w
				}
				return out, nil
			},
		},
		{
			Name: "assess_manipulation", Path: "assess", Summary: "One-call check of a coin: dumping, absorption, spot control, spoofing — verdicts with evidence",
			Desc: "Runs analyze_trades (both markets), get_book_analysis and get_wall_history over the window and applies transparent rules. verdict.dump (砸盘: large-order net selling, " +
				"same-size sell runs walking price down, sell sweeps, bid walls pulled as price came, ask walls moving down, heavy ask side), verdict.absorption (吸筹/托盘: taker selling " +
				"while price holds, quiet same-size buying, long-standing or filled bid walls, bid-heavy book), verdict.spotControl (现货控盘: wash-like pairs, clip-driven spot volume, " +
				"concentrated large orders, spot leading swap, spot premium, thin / easily moved spot book), verdict.spoofing (one side's walls cancelled as price approaches or following price while the other side's are not — symmetric pulling is market making and is listed under against). " +
				"Each: level 0–3 (无/弱/中/强), score, evidence and against, in Chinese with numbers; plus the compact numbers behind them. Heuristic: trades carry no account ids. " +
				"Check coverage.pct of each market before leaning on it.",
			Params:  append([]Param{coinParam()}, Param{Name: "hours", In: "query", Type: "number", Default: 24, Desc: "window in hours (1–48)"}, windowParams[1]),
			CostTxt: "5", cost: func(Args) int { return 5 },
			run: func(ctx context.Context, a Args) (any, error) {
				if h := a.Float("hours"); h < 1 {
					return nil, fmt.Errorf("hours must be between 1 and 48")
				}
				from, to, err := window(a, 48)
				if err != nil {
					return nil, err
				}
				return e.Assess(ctx, a.Str("instId"), from, to)
			},
		},
		{
			Name: "get_dex_activity", Path: "dex", Summary: "On-chain DEX pools of a coin and their recent large swaps (GeckoTerminal)",
			Desc: "The coin's largest DEX pools (symbol match, price within 10% of OKX, liquidity ≥ 10k) with 24h volume, price change and buy/sell / buyer/seller counts, " +
				"then swaps ≥ minUsd from their trade feeds (last 300 per pool, ≤ 24 h): totals per side, the largest swaps (wallet = tx sender, tx hash) and the wallets with the largest net. " +
				"For CEX-listed coins on-chain volume is usually a small fraction of OKX; useful for whale wallets moving in or out and for DEX-native tokens.",
			Params: []Param{coinParam(),
				{Name: "minUsd", In: "query", Type: "number", Default: 10000, Desc: "only swaps ≥ this USD"},
				{Name: "pools", In: "query", Type: "integer", Default: 3, Min: 1, Max: 5, Desc: "pools to read"},
				{Name: "top", In: "query", Type: "integer", Default: 15, Min: 1, Max: 50, Desc: "swaps listed"}},
			CostTxt: "2", cost: func(Args) int { return 2 },
			run: func(ctx context.Context, a Args) (any, error) {
				return e.DexActivity(ctx, a.Str("instId"), math.Max(a.Float("minUsd"), 0), a.Int("pools"), a.Int("top"))
			},
		},
	}
}

func firstSet(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
