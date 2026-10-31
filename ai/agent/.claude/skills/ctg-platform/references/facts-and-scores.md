# Facts panel, rule scores and track record

Live definitions come from MCP: `list_fields` (field key, name, unit, formula), `list_rules` (rule weights and gate), `get_rule_track_record` (track record), `get_universe` / `get_instrument` (current values, percentiles, scores).

## Facts panel (factors)

Directionless, objective measurements; live and history replay use the same formulas. "Live only" = absent from history replay (not replayable).

| key | Name | Unit | Definition |
|---|---|---|---|
| `ret_15m` / `ret_1h` / `ret_4h` / `ret_24h` | Change (涨跌) | log | Perp close-to-close log return (15m live only) |
| `excess_btc_4h` | Relative to BTC 4h (相对 BTC 4h) | log | ret_4h − BTC-USDT-SWAP return over the same period |
| `trend_z_4h` | 4h trend strength (4h 趋势强度) | z | ret_4h ÷ (std of the last 48 1H returns × √4) |
| `trend_ema` | EMA trend (EMA 趋势) | z | (EMA8 − EMA24) (1H closes) ÷ price ÷ 1H return std |
| `eff_ratio_24h` | Trend efficiency 24h (趋势效率 24h) | 0–1 | \|24h net change\| ÷ sum of absolute changes over 24 1H bars; 1 = one-way, 0 = chop |
| `beta_btc_48h` | BTC β 48h | × | Regression slope of 48 1H log returns on BTC |
| `resid_15m` / `resid_1h` / `resid_4h` | Residual return (残差收益) | log | Return − β × BTC return over the same period (removes market drag; 15m live only) |
| `oi_chg_1h` / `oi_chg_4h` | OI change (OI 变化) | log | Log change of coin-denominated open interest oiCcy (excludes USD OI changes caused by price) |
| `vol_surprise_4h` | Volume surprise 4h (放量 4h) | log | log(last-4h USDT turnover ÷ median of the 42 4h turnovers over the past 7 days); UI shows a multiple |
| `funding_hourly` | Funding/hour (资金费/小时) | rate/h | Latest settled funding ÷ settlement interval hours (4h/8h normalized) |
| `funding_pred_hourly` | Predicted funding/hour (预测资金费/小时) | rate/h | Current-period predicted funding ÷ interval hours (live only) |
| `basis` | Perp–spot basis (期现基差) | log | log(perp price / OKX same-name USDT spot price); no spot = unknown (live only) |
| `spread_bps` | Spread (点差) | bps | (ask − bid) / mid (live only, used by the gate) |
| `turnover12h` | 12h turnover (12h 成交额) | USDT | Sum of volCcyQuote over 720 confirmed 1m candles (not percentile-scored) |

**Unit conversions (important)**:
- `log` fields in API/MCP are natural logs. The UI shows simple percent = (e^v − 1) × 100%; nearly equal for small moves. Example: v = 0.0953 → +10%.
- `vol_surprise_4h` is shown as ×e^v (e.g. v = 0.693 → ×2.00).
- `funding_hourly` is a per-hour rate: 0.0001 = 0.01%/h (≈ 0.08%/8h). The UI shows ×100 with 4 decimals, e.g. `0.0100%/h`. MCP `get_series field=funding` returns the **per-settlement** rate, not per hour.
- Positive funding = longs pay shorts; negative the reverse. OKX settlement intervals vary by contract (4h/8h); compare coins using the hourly value.
- Rising OI only means more opening than closing; it does **not** tell you whether longs or shorts are adding.

## Percentiles

Each field is ranked within the **current active pool** to get percentile p ∈ [0, 100] (0 lowest, 100 highest, ties take the middle rank). API name: `pct`. It only compares "who is stronger in the pool right now"; robust to outliers but loses magnitude.

## Rule scores (long score / short score, 多头分 / 空头分)

1. Percentile → ±1: x = 2·p/100 − 1 (pool top +1, median 0, bottom −1).
2. **Score = 50 + 50 × Σ(wᵢ·xᵢ) ÷ Σ|wᵢ|**, range 0–100, 50 neutral.
3. Positive weight = higher field scores more; negative weight = lower scores more; only relative weight size matters.
4. **Long and short are independent**: each has its own weights, they need not sum to 100; a low long score doesn't mean short it.
5. **Missing ⇒ unscored**: if any weighted field is missing, that side shows "—", never filled with 0. Common for new coins/data gaps.
6. **Gate**: currently a spread cap (bps, 0 = no cap). Above it, the contract is marked "Gate" (门槛) (API `gate` = `BLOCKED:…`, pass = `PASS`); the score is still computed but flags excessive trading cost; no score overrides the gate.
7. Intraday preview every 10 s on the open minute (changes); each hour on the hour an immutable snapshot is saved per rule; only snapshots enter the track record.

Example: long weights = 4h trend 0.45 + OI change 0.30 + volume surprise 0.25, percentiles 99/99/98 → x ≈ 0.98/0.98/0.96 → long ≈ 98.7.

A score is only a candidate ranking, not a win rate or an order recommendation; all rules are marked "unvalidated" (未验证) by default.

## Rules & track record page [规则与战绩](/rules)

- Left: rule list (custom + built-in "baseline" (对照) rules); right: track record; below: "Edit weights" (编辑权重).
- Editing: enter long and short weights per field, set spread gate and description. **Save = publish a new version (v+1)**: the current pool is rescored immediately, history replay recomputes in the background, old versions' track records are kept. You can also "Save as new rule" (另存为新规则) or delete.
- Rules containing non-replayable fields can't be history-replayed; they only accumulate live track record.
- The default rule is chosen in [设置](/settings) → Scoring rules (评分规则); the Scanner can switch temporarily.

**Built-in baseline rules** (benchmarks, not recommendations): `resid-reversal` 残差反转 (β-neutral, default rule), `trend-oi-confirm` 趋势+OI 确认 (old direction, measured IC negative, kept as a baseline), `short-term-reversal` 短期反转 (relative to BTC), `funding-tilt` 资金费倾向, `ema-trend` 趋势跟随 (EMA + efficiency ratio), `fast-resid-momentum` 快速残差动量 (15m, live only). Link like [残差反转](/rules/resid-reversal).

## Reading the track record

Horizons 1h / 4h / 24h, two sample types:
- **Live** (上线后): real hourly snapshots, immutable = out-of-sample, most trustworthy.
- **History replay** (历史回放): recomputes the last 14 days from 1H data using the pool at that time; only coins still listed (survivorship bias), spread assumed 2 bps, may overlap data seen while designing the rule — reference only.

| Metric | Meaning |
|---|---|
| Rank IC | Spearman correlation, at the same moment within the pool, between score rank and rank of actual return over the next h hours, −1 to 1. 0.02–0.05 already counts as signal in factor research. Short IC uses −return |
| t-stat | Mean IC ÷ standard error; only non-overlapping cross-sections per horizon (1h hourly, 4h every 4 hours…). \|t\| > 2 ≈ 5% significance |
| Cross-sections | Sample count; under 30 the verdict is "insufficient sample" |
| Top-20% excess (gross) | Mean return of the top-scoring fifth − pool mean (relative skill) |
| Top-20% net | Absolute return of the top fifth − round-trip taker 5 bps×2 − spread; includes the whole pool's drift |
| Hit rate | API fields `longHit` / `shortHit` (hit ratio over non-overlapping cross-sections) |
| Cumulative IC curve | Long cumulative IC: steadily rising = consistently effective, falling = direction is reversed |

**Verdict labels**: `样本不足` (insufficient sample, < 30 cross-sections), `有初步证据（仍需更长样本）` (preliminary evidence, needs a longer sample: ≥ 30 cross-sections, t > 2 and positive after fees), `方向相反（显著为负）` (reversed, significantly negative), `无显著证据` (no significant evidence).

How to read: prefer live samples; a small IC matters only if t is significant; a rule negative after fees is hard to trade even with positive IC; trying many parameter sets produces false positives (overfitting).
