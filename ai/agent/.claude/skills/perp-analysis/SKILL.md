---
name: perp-analysis
description: Full analysis of one OKX USDT perpetual for the owner — market structure, derivatives positioning (funding, OI, long/short, liquidations), order flow, news and catalysts, then a bull vs bear debate and a risk-managed plan. Use for 一键分析, 分析一下 X, 能不能多/空, 给个交易计划, 逼空/插针风险, 庄/控盘 questions about one instrument. Not for quick single-number questions.
---

# Perpetual analysis workflow

Adapted from TradingAgents (TauricResearch, Apache-2.0): analyst team → bull/bear debate → a decisive research/risk manager. No order step: ctg places no orders, the owner executes.

## 0. Market first

Run `ca market -f json` (reuse it if this chat already has one from the last hour): BTC/ETH trend and funding, breadth, fear & greed, market-wide news. Note the regime in one line; it frames every later judgement.

## 1. Collect (in parallel where possible)

| Analyst | Sources |
|---|---|
| Structure | `get_instrument` (fact panel, percentiles in the pool, rule scores), `get_candles` 15m/1H/4H, `compare_with_majors` |
| Derivatives | `get_series` oi + funding (72h+), `get_liquidations`, `get_liquidation_map` (estimate), `get_orderbook` |
| Flow | `get_orderflow` 15m/1H: taker delta, CVD vs price divergence |
| Risk scores | sandbox: `ca analyze <SYM> -f json` (long/short danger 0–100, control score, max leverage, supports/resistances, events) |
| News & catalysts | sandbox `ca events <SYM> -f json` (coin news, exchange announcements, macro calendar); WebSearch / WebFetch to verify or read the source (unlocks, hacks, listings). Judge each item: direction, size, already priced in? |

Compute indicators in the sandbox when needed (ATR, swing highs/lows, realized vol, funding percentiles) instead of estimating. Interpret risk scores with [references/risk-scores.md](references/risk-scores.md).

## 2. Debate

For a full (一键) analysis, launch the `bull` and `bear` subagents in parallel with the same evidence summary (key numbers with time windows). Each argues its side as hard as the data allows and names what would prove it wrong. For a quick follow-up question, argue both sides yourself in two lines each.

## 3. Decision (you: head of research and risk)

Critically weigh the debate against the data and make a definitive call: long, short or stand aside. Do not default to 观望 just because both sides have valid points — commit to the side with the stronger, better-evidenced arguments. 观望 needs a concrete reason (event within 24 h, no structural edge, reward/risk < 1.5) plus the trigger that would turn it into a trade. Sizing rules:
- Stop beyond the nearest swing point or liquidation cluster, ~1.5 × 4H ATR; take-profits at the next resistance/support or liquidation band, reward/risk ≥ 1.5 for TP1. Use the owner's preferred leverage from the notes; liquidation must stay ≥ 2 × the stop distance, otherwise say to cut leverage or size; cut it further when danger ≥ 50 or control ≥ 60 (≤ 3x, warn about wicks).
- Risk per trade ≤ 1% of equity. A red event (hack, delisting, large unlock, FOMC/CPI within 24 h) goes first and usually means stand aside.
- Danger scores are heuristics, not validated; rule scores count only with a significant out-of-sample Rank IC.

## 4. Answer template (Chinese, phone-sized)

```
### 结论：做多 / 做空 / 观望（信心 低/中/高）— 一句话理由
⚠ 事件：…（没有写"无重大事件"）
**大盘** BTC …（结构/资金费）· 恐惧贪婪 … · 与大盘：顺势/逆势
**价格** $… · 24h ±% · 7d ±%（相对 BTC ±%）
**多方** 1… 2… 3…
**空方** 1… 2… 3…
**计划**：入场区 … · 止损 …（−x%，理由）· 止盈 1 …（+x%，盈亏比 …）/ 止盈 2 … · 杠杆 N x（按主人偏好）· 单笔风险 …% · 持有 … · 观点作废：…
**关键数据**：资金费 …/8h（分位）· OI 24h ±% · 多空比 … · 主动买占比 … · 上/下方估算清算密集区 …
```
No disclaimers ("仅供参考", "不构成投资建议" and the like): risk is expressed by the stop and the invalidation level.
