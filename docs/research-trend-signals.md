# Research: trend / direction signals (2026-09-28)

> Motivation: the user reported that long/short scores "don't indicate the trend, following them loses money", and asked for something more useful and faster-reacting for altcoins relative to BTC/ETH.
> Bottom line first: **no public method reliably predicts hourly moves**. The industry practice is to turn literature-backed signals into testable factors and decide by out-of-sample IC and net-of-cost returns. This change adds the ones computable from existing data to the fact panel and as reference rules, to be judged on the Rules & track record page.

## 1. Diagnosis (local 14-day replay, 2026-09-13 → 09-27, ~80 coins per cross-section)

| Rule | 1h long IC (t) | 4h long IC (t) | Reading |
|---|---|---|---|
| Trend + OI confirmation (default rule) | −0.042 (−4.8) | −0.059 (−3.5) | **Wrong sign**: the higher the long score, the weaker the next 1–4h |
| Short-term reversal (vs BTC) | +0.043 (+4.0) | +0.068 (+3.2) | The same information reversed; the top quintile is thin after fees/spread |
| Funding tilt | +0.004 | +0.004 | No signal |

In this sample, **hourly altcoin perps mean-revert rather than trend**. The default "chase + rising OI" rule was therefore systematically on the wrong side, matching the user's experience.
Caveat: 14 days is one market regime; the conclusion can flip, hence the ongoing out-of-sample record.

## 2. Methods used in practice

| Family | Typical construction | Evidence and horizon | This project |
|---|---|---|---|
| Time-series momentum (TSMOM) | sign of past N-period return / volatility-scaled MA differences (MACD-like) | Moskowitz, Ooi & Pedersen 2012; Baz et al. 2015 (EMA crossovers used by CTAs); 1–4 week momentum is significant in crypto (Liu & Tsyvinski 2021) | added `trend_ema` and the rule "Trend following (EMA + efficiency ratio)" |
| Cross-sectional momentum / reversal | rank the pool by past return, long strong / short weak, or the reverse | weekly momentum in crypto (Liu, Tsyvinski & Wu 2022); intraday-to-daily reversal is common | existing "Short-term reversal" |
| Residual (β-neutral) momentum / reversal | regress out the market (BTC) β first, rank by residual return | Blitz, Huij & Martens 2011: residualized signals are more stable with smaller drawdowns. Most altcoin moves are driven by BTC; without removing it, high β is mistaken for strength | added `beta_btc_48h`, `resid_1h/4h/15m`, rules "Residual reversal (β-neutral)" and "Fast residual momentum" |
| Trend / range regime filter | Kaufman efficiency ratio, ADX, Hurst exponent | trend signals mostly lose by whipsawing in ranges; decide "is it trending" before using trend signals | added `eff_ratio_24h` |
| Order-flow imbalance | taker buy/sell delta, order-book OFI | Cont, Kukanov & Stoikov 2014: OFI is roughly linear in contemporaneous price change; predictive power lives at seconds-to-minutes and decays fast | delta/CVD already shown; as a scoring field it needs universe-wide trade recording (backlog) |
| Intraday session momentum | opening-session return predicts the close | Gao, Han, Li & Zhou 2018 (equities); weak in 24h crypto markets | not done |
| Machine learning (GBDT/LSTM, …) | non-linear multi-factor combinations | needs long samples and strict rolling out-of-sample validation or overfits easily (Bailey et al. 2014) | revisit after ≥ 30 days of out-of-sample data (see roadmap, scoring methods) |

## 3. Why "faster" doesn't mean profitable

- Shorter horizons mean weaker signals and higher turnover: a 15-minute edge is usually below 0.1%, while fees + spread + slippage for a round trip are already 0.1%–0.2%. That is what the "top quintile net of cost" column is for.
- Fast signals trigger on noise more easily; they need a regime filter (efficiency ratio) and stricter gates.
- So "Fast residual momentum (15m)" is only a reference; its fields don't exist in the hourly replay, so its record only accumulates from go-live.

## 4. Changes

- New fields (direction-free facts; existing field definitions unchanged): `beta_btc_48h`, `resid_1h`, `resid_4h`, `trend_ema`, `eff_ratio_24h` (replayable); `ret_15m`, `resid_15m` (live only).
- New reference rules: Residual reversal (β-neutral), Trend following (EMA + efficiency ratio), Fast residual momentum (15m, live only). All unvalidated, **not recommendations**.
- The default scoring rule is now "Residual reversal (β-neutral)" (changeable in Settings; the browser remembers only rules picked by hand).

Replay results (14 days, 09-14 → 09-28, ~80 coins per cross-section; long IC and t; "net" = average excess of the long top quintile after costs):

| Rule | 1h IC (t) | 4h IC (t) | 4h top-quintile net |
|---|---|---|---|
| Trend + OI confirmation (old default) | −0.043 (−5.0) | −0.063 (−3.7) | −16 bps |
| Trend following (EMA + efficiency ratio) | −0.020 (−2.3) | −0.045 (−2.3) | −7 bps |
| Short-term reversal (vs BTC) | +0.043 (+4.0) | +0.066 (+3.3) | +18 bps |
| Residual reversal (β-neutral) | **+0.048 (+4.7)** | +0.062 (+3.2) | +14 bps |

Classic trend following is also on the wrong side at the hourly horizon in this sample, further evidence of a reversal regime; β-neutralization makes the 1h reversal signal slightly cleaner. All positive results are thin after costs, and the short side is negative after costs.

## 5. How to use

1. Check each rule's replay and live IC on Rules & track record: |t| ≥ 2 and a positive net-of-cost return count as preliminary evidence.
2. A rule flagged "wrong sign (significantly negative)" should be read in reverse, not followed.
3. A score is only a rank within the pool, not a win rate and not an order suggestion.

## References

- Moskowitz, Ooi & Pedersen (2012). Time series momentum. *Journal of Financial Economics*.
- Baz, Granger, Harvey, Le Roux & Rattray (2015). Dissecting investment strategies in the cross section and time series. SSRN.
- Blitz, Huij & Martens (2011). Residual momentum. *Journal of Empirical Finance*.
- Liu & Tsyvinski (2021). Risks and returns of cryptocurrency. *Review of Financial Studies*.
- Liu, Tsyvinski & Wu (2022). Common risk factors in cryptocurrency. *Journal of Finance*.
- Cont, Kukanov & Stoikov (2014). The price impact of order book events. *Journal of Financial Econometrics*.
- Gao, Han, Li & Zhou (2018). Market intraday momentum. *Journal of Financial Economics*.
- Kaufman, P. *Trading Systems and Methods* (efficiency ratio / KAMA).
- Bailey, Borwein, López de Prado & Zhu (2014). Pseudo-mathematics and financial charlatanism. *Notices of the AMS*.
