---
name: perp-analysis
description: Full analysis of one OKX USDT perpetual for the owner — market structure, derivatives positioning (funding, OI, long/short, liquidations), order flow, news and catalysts, then a bull vs bear debate and a risk-managed plan. Use for 一键分析, 分析一下 X, 能不能多/空, 给个交易计划, 逼空/插针风险, 庄/控盘 questions about one instrument. Not for quick single-number questions.
---

# Perpetual analysis workflow

Adapted from TradingAgents (TauricResearch, Apache-2.0): analyst team → bull/bear debate → a decisive research/risk manager. No order step: ctg places no orders, the owner executes.

## 0. Market first

Run `ca market -f json` (reuse it if this chat already has one from the last hour): BTC/ETH trend and funding, breadth, fear & greed, market-wide news. Note the regime in one line; it frames every later judgement.

## 1. Collect (in parallel where possible; summaries first, raw data only inside Python)

| Analyst | Sources |
|---|---|
| Risk scores & structure | `ca analyze <SYM>` (text): long/short danger, control, max leverage, supports/resistances, events — this covers most of the picture in one call |
| Derivatives | `ca funding <SYM>`, `ca oi <SYM>`, `ca liq <SYM>` (estimated liquidation clusters), data tool `get_orderbook` (depth ≤ 50) |
| Flow & trend | `ca flow <SYM> -b 1H`, `ca indicators <SYM> -b 4H`; relative strength vs BTC only through Python on `ca ctg /compare … -f json > f.json` |
| News & catalysts | `ca events <SYM>`; WebSearch / WebFetch to verify or read the source (unlocks, hacks, listings). Judge each item: direction, size, already priced in? |

Need something more specific (ATR, swing levels, realized vol, funding percentile)? Fetch the raw series to a file and print only the computed numbers. Interpret risk scores with [references/risk-scores.md](references/risk-scores.md).

## 2. Debate

For a full (一键) analysis, launch the `bull` and `bear` subagents in parallel with the same evidence summary (key numbers with time windows). Each argues its side as hard as the data allows and names what would prove it wrong. For a quick follow-up question, argue both sides yourself in two lines each.

## 3. Decision (you: head of research and risk)

Critically weigh the debate against the data and make a definitive call: long, short or stand aside. Do not default to 观望 just because both sides have valid points — commit to the side with the stronger, better-evidenced arguments. 观望 needs a concrete reason (event within 24 h, no structural edge, reward/risk < 1.5) plus the trigger that would turn it into a trade. Sizing rules:
- Stop beyond the nearest swing point or liquidation cluster, ~1.5 × 4H ATR; take-profits at the next resistance/support or liquidation band, reward/risk ≥ 1.5 for TP1. The plan always uses the owner's preferred leverage from the notes. If liquidation would sit closer than 2 × the stop distance, or danger ≥ 50 / control ≥ 60, add one line after the plan saying so (smaller size, or ≤ 3x, wick risk) — the plan itself stays at the preference.
- Risk per trade ≤ 1% of equity. A red event (hack, delisting, large unlock, FOMC/CPI within 24 h) goes first and usually means stand aside.
- Danger scores are heuristics, not validated; rule scores count only with a significant out-of-sample Rank IC.

## 4. Answer (components, see the chat prompt for their JSON)

1. `verdict` — direction, confidence, instId, one-line title, the reason in one sentence. If the market regime matters, say it in `reason`.
2. `kpi` — 4–6 numbers that carry the call: price/24h, funding (with pool percentile), OI change, long/short ratio, danger score, nearest estimated liquidation cluster.
3. 2–4 short bullets: why — market context, structure, positioning, news (direction, size, priced in?).
4. `bullbear` — the debate's strongest points with scores.
5. `plan` — entry, stop (beyond the swing point / cluster), TP1 and TP2, the owner's leverage, risk %, hold, invalidation. Standing aside: the plan that applies once the trigger hits.
6. One line on the main risk as a price or event. No disclaimers ("仅供参考", "不构成投资建议" and the like).
