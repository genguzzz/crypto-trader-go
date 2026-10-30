---
name: perp-analysis
description: Full analysis of one OKX USDT perpetual for the owner — market structure, derivatives positioning (funding, OI, long/short, liquidations), order flow, news and catalysts, then a bull vs bear debate and a risk-managed plan. Use for 一键分析, 分析一下 X, 能不能多/空, 给个交易计划, 逼空/插针风险, 庄/控盘 questions about one instrument. Not for quick single-number questions.
---

# Perpetual analysis workflow

Adapted from TradingAgents (TauricResearch, Apache-2.0): analyst team → bull/bear debate → risk manager. No trader/portfolio step: ctg places no orders.

## 1. Collect (in parallel where possible)

| Analyst | Sources |
|---|---|
| Structure | `get_instrument` (fact panel, percentiles in the pool, rule scores), `get_candles` 15m/1H/4H, `compare_with_majors` |
| Derivatives | `get_series` oi + funding (72h+), `get_liquidations`, `get_liquidation_map` (estimate), `get_orderbook` |
| Flow | `get_orderflow` 15m/1H: taker delta, CVD vs price divergence |
| Risk scores | sandbox: `ca analyze <SYM> -f json` (long/short danger 0–100, control score, max leverage, supports/resistances, events) |
| News & catalysts | WebSearch: "<coin> news", listing/delisting, unlocks, hacks; macro events in the next 24 h |

Compute indicators in the sandbox when needed (ATR, swing highs/lows, realized vol, funding percentiles) instead of estimating. Interpret risk scores with [references/risk-scores.md](references/risk-scores.md).

## 2. Debate

For a full (一键) analysis, launch the `bull` and `bear` subagents in parallel with the same evidence summary (key numbers with time windows). Each returns its 3 strongest arguments and what would invalidate them. For a quick follow-up question, argue both sides yourself in two lines each.

## 3. Risk manager (you)

Weigh the debate against the data, then decide: long / short / no trade (观望 is a valid answer). Rules:
- Stop beyond the nearest swing point or liquidation cluster, ~1.5 × 4H ATR; leverage such that liquidation is ≥ 2.5 × the stop distance; cut it further when danger ≥ 50 or control ≥ 60 (≤ 3x, warn about wicks).
- Risk per trade ≤ 1% of equity. A red event (hack, delisting, large unlock, FOMC/CPI within 24 h) goes first and usually means stand aside.
- Danger scores are heuristics, not validated; rule scores count only with a significant out-of-sample Rank IC.

## 4. Answer template (Chinese, phone-sized)

```
### 结论：做多 / 做空 / 观望（信心 低/中/高）
⚠ 事件：…（没有写"无重大事件"）
**价格** $… · 24h ±% · 7d ±%（相对 BTC ±%）
**多方** 1… 2… 3…
**空方** 1… 2… 3…
**计划**：入场区 … · 止损 …（理由）· 目标 … / … · 杠杆 ≤ N x · 观点作废：…
**关键数据**：资金费 …/8h（分位）· OI 24h ±% · 多空比 … · 主动买占比 … · 上/下方估算清算密集区 …
```
End with one line: 研究参考，非投资建议。
