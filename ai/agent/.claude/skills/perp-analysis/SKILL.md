---
name: perp-analysis
description: Full analysis of one OKX USDT perpetual and a trade plan — market context, structure, derivatives positioning (funding, OI, long/short ratio, liquidation clusters), order flow, news, both sides argued, then a decisive call with entry, stop, two take-profits and leverage. Use for 一键分析, 分析一下 X, X 能不能多/空, 给个计划, 逼空/插针风险, 庄/控盘 about one coin. Not for a single number (just fetch it), not for how the app works (ctg-platform), not for greetings or concepts.
---

# Perpetual analysis

The research flow follows TradingAgents (TauricResearch, Apache-2.0) — analysts → bull/bear debate → a decisive manager — done by you in one pass: you argue both sides yourself, then decide. ctg places no orders; the owner executes.

## 1. Collect (one round of parallel calls; summaries, not raw data)

| What | Call |
|---|---|
| Market context | `ca market` (skip if this conversation already has it from the last hour) |
| The coin, overall | `ca analyze <SYM>` — long/short danger, control score, max leverage, supports/resistances, events. Covers most of the picture. |
| Positioning | `ca funding <SYM>`, `ca oi <SYM>`, `ca liq <SYM>` (estimated liquidation clusters) |
| Flow and trend | `ca flow <SYM> -b 1H`, `ca indicators <SYM> -b 4H` |
| News | `ca events <SYM>`; WebSearch only to verify something material (hack, unlock, listing, ETF flows) |

Add a computation only when a level or ratio the plan needs is missing (ATR, swing high/low, relative strength vs BTC): fetch the raw series to a file and print just the result. Typical total: 6–10 calls. Interpret danger/control scores with [references/risk-scores.md](references/risk-scores.md).

## 2. Argue both sides

Write the strongest bull case and the strongest bear case, 2–4 points each. Every point carries a number and a time window and says what it usually means on perps. Score each side 0–10 for how well the evidence supports it.

## 3. Decide

Take the side with the stronger evidence. Do not stand aside just because both sides have points; stand aside only for a concrete reason (event within 24 h, no structural edge, reward/risk < 1.5), and give the plan that applies once the trigger hits.

Plan rules:
- Stop beyond the nearest swing point or liquidation cluster, roughly 1.5 × 4H ATR; state its distance.
- TP1 at the next support/resistance or liquidation band with reward/risk ≥ 1.5; TP2 at the next one.
- Leverage = the owner's preference from his notes. If liquidation would sit closer than 2 × the stop distance, or danger ≥ 50 / control ≥ 60, add one sentence after the plan (smaller size, wick risk); the plan itself keeps his leverage.
- Risk per trade ≤ 1% of equity. A red event (hack, delisting, large unlock, FOMC/CPI within 24 h) goes first and usually means stand aside.
- Danger scores are heuristics; rule scores count only with a significant out-of-sample Rank IC.

## 4. Answer (components defined in the system prompt)

1. `verdict` — direction, confidence, instId, one-line title, the reason in one sentence (include the market context if it matters).
2. `kpi` — 4–6 numbers that carry the call: price and 24h, funding with pool percentile, OI change, long/short ratio, danger score, nearest estimated liquidation cluster.
3. 2–4 short bullets on why: market, structure, positioning, news (direction, size, already priced in?).
4. `bullbear` — section 2.
5. `plan` — entry, stop, TP1/TP2, his leverage, risk %, hold, invalidation.
6. One line on the main risk as a price or an event.
