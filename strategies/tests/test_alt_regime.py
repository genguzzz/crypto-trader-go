"""alt-regime-trend against scripted markets: breakout longs, weak-alt shorts, exits and sizing."""

import os
import sys
import unittest

import numpy as np

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "library"))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from alt_regime import BTC, AltRegimeTrend, _resid72, _smooth_last  # noqa: E402
from ctg import ta  # noqa: E402
from ctg.context import Bars, Record  # noqa: E402
from test_library import Ctx, bar_of  # noqa: E402

STEP = 4 * 3600_000
N = 240


def series(closes, vol=1e6, last_vol=None):
    rows = []
    for i, c in enumerate(closes):
        q = vol if last_vol is None or i < len(closes) - 6 else last_vol
        rows.append([i * STEP, c, c * 1.005, c * 0.995, c, 0, q])
    return rows


class AcctCtx(Ctx):
    def __init__(self, *a, equity=3000.0, risk_pct=0.1, **kw):
        super().__init__(*a, **kw)
        self.now = N * STEP
        self._acct = Record(equity=equity, cash=equity, limits=Record(riskPct=risk_pct))

    def account(self):
        return self._acct


def market(trend, n_alts=40, breakout=None, btc_trend=None):
    """n_alts alts with a common trend; `breakout` gets a final 4H close well above its range on heavy volume."""
    bars = {}
    for k in range(n_alts):
        inst = f"A{k}-USDT-SWAP"
        base = 1 + 0.001 * k
        closes = [base * (1 + trend) ** i * (1 + 0.01 * np.sin(i / 3 + k)) for i in range(N)]
        bars[inst] = series(closes)
    if breakout:
        closes = [1.0 + 0.002 * np.sin(i / 2) for i in range(N - 1)] + [1.2]
        bars[breakout] = series(closes, vol=1e6, last_vol=5e6)
    bt = btc_trend if btc_trend is not None else trend
    bars[BTC] = series([100 * (1 + bt) ** i for i in range(N)])
    return bars


def run(strategy, bars, **kw):
    ctx = AcctCtx(bars=bars, **kw)
    strategy.on_start(ctx)
    strategy.on_universe_change(ctx, [i for i in bars if i != BTC], [])
    last = {i: bar_of(i, r[-1]) for i, r in bars.items() if i != BTC}
    return ctx, strategy.on_bars(ctx, last) or []


class AltRegimeTrendTest(unittest.TestCase):
    def test_vectorised_indicators_match_ta(self):
        rng = np.random.default_rng(3)
        C = np.exp(np.cumsum(rng.normal(0, 0.02, (4, 180)), 1))
        H, L = C * 1.01, C * 0.99
        self.assertTrue(np.allclose(_smooth_last(C, 42, 2 / 43), [ta.ema(c, 42)[-1] for c in C]))
        prev = np.concatenate([np.full((4, 1), np.nan), C[:, :-1]], 1)
        TR = np.fmax(H - L, np.fmax(abs(H - prev), abs(L - prev)))
        TR[:, 0] = H[:, 0] - L[:, 0]
        self.assertTrue(np.allclose(_smooth_last(TR, 14, 1 / 14), [ta.atr(h, l, c, 14)[-1] for h, l, c in zip(H, L, C)]))

    def test_resid_removes_btc_beta(self):
        rng = np.random.default_rng(5)
        rb = rng.normal(0, 0.01, 60)
        btc = Bars([[i * STEP, 0, 0, 0, 100 * np.exp(np.sum(rb[:i])), 0, 0] for i in range(61)])
        C = np.array([np.exp(np.cumsum(np.r_[0, 2 * rb])), np.exp(np.cumsum(np.r_[0, 2 * rb - 0.002]))])
        TS = np.tile(np.arange(61) * STEP, (2, 1))
        r = _resid72(C, TS, btc)
        self.assertAlmostEqual(r[0], 0, places=6)          # pure 2×BTC: no residual
        self.assertAlmostEqual(r[1], -0.036, places=3)      # −0.2 % per bar over 18 bars

    def test_breakout_long_in_bull_market(self):
        s = AltRegimeTrend({"margin_usdt": 125, "top_n": 200})
        ctx, out = run(s, market(0.002, breakout="X-USDT-SWAP"))
        opens = [x for x in out if x.action == "open_long"]
        self.assertEqual([x.inst for x in opens], ["X-USDT-SWAP"])
        sig = opens[0]
        d = (1.2 - sig.stop_loss) / 1.2
        self.assertLessEqual(d, 2.0 / 8 + 1e-9)             # stop within the loss cap
        self.assertAlmostEqual(sig.strength * 3000 * 0.1 / d, 1000, delta=1)   # notional = 125 × 8
        self.assertEqual(sig.leverage, 8)
        self.assertEqual(ctx.state["legs"]["X-USDT-SWAP"], 1)

    def test_no_breakout_long_in_bear_market(self):
        s = AltRegimeTrend({"margin_usdt": 125})
        _, out = run(s, market(-0.002, breakout="X-USDT-SWAP"))
        self.assertFalse([x for x in out if x.action == "open_long"])

    def test_weak_alt_shorts_when_breadth_is_low(self):
        s = AltRegimeTrend({"margin_usdt": 125, "use_trend": False})
        ctx, out = run(s, market(-0.003))
        shorts = [x for x in out if x.action == "open_short"]
        self.assertTrue(shorts)
        self.assertLessEqual(len(shorts), 5)
        self.assertEqual(shorts[0].time_limit, "72h")
        self.assertTrue(all(ctx.state["legs"][x.inst] == 2 for x in shorts))

    def test_position_limit(self):
        held = {f"A{k}-USDT-SWAP": Record(inst_id=f"A{k}-USDT-SWAP", pos_side="short") for k in range(5)}
        s = AltRegimeTrend({"margin_usdt": 125})
        _, out = run(s, market(0.002, breakout="X-USDT-SWAP"), positions=held)
        self.assertFalse([x for x in out if x.action.startswith("open")])

    def test_trend_exit_below_channel_mid(self):
        bars = market(0.002)
        closes = [1.0 + 0.002 * i for i in range(N - 1)] + [0.9]
        bars["Y-USDT-SWAP"] = series(closes)
        s = AltRegimeTrend({"margin_usdt": 125})
        _, out = run(s, bars, positions={"Y-USDT-SWAP": Record(inst_id="Y-USDT-SWAP", pos_side="long")})
        self.assertEqual([x.inst for x in out if x.action == "close"], ["Y-USDT-SWAP"])

    def test_exit_after_leaving_the_universe(self):
        bars = market(0.002)
        closes = [1.0 + 0.002 * i for i in range(N - 1)] + [0.9]
        bars["Y-USDT-SWAP"] = series(closes)
        s = AltRegimeTrend({"margin_usdt": 125})
        ctx = AcctCtx(bars=bars, positions={"Y-USDT-SWAP": Record(inst_id="Y-USDT-SWAP", pos_side="long")})
        s.on_start(ctx)
        s.on_universe_change(ctx, [i for i in bars if i != BTC], [])
        s._buf["Y-USDT-SWAP"] = {k: v[:-1] for k, v in s._buf["Y-USDT-SWAP"].items()}   # last bar not seen yet
        s.on_universe_change(ctx, [], ["Y-USDT-SWAP"])
        last = {i: bar_of(i, r[-1]) for i, r in bars.items() if i not in (BTC, "Y-USDT-SWAP")}
        out = s.on_bars(ctx, last) or []
        self.assertEqual([x.inst for x in out if x.action == "close"], ["Y-USDT-SWAP"])
        self.assertNotIn("Y-USDT-SWAP", [x.inst for x in out if x.action.startswith("open")])

    def test_exit_after_restart_outside_the_universe(self):
        bars = market(0.002)
        closes = [1.0 + 0.002 * i for i in range(N - 1)] + [0.9]
        bars["Y-USDT-SWAP"] = series(closes)
        s = AltRegimeTrend({"margin_usdt": 125})
        ctx = AcctCtx(bars=bars, positions={"Y-USDT-SWAP": Record(inst_id="Y-USDT-SWAP", pos_side="long")},
                      universe=[i for i in bars if i not in (BTC, "Y-USDT-SWAP")])
        s.on_start(ctx)                                    # fresh process: empty buffers
        s.on_universe_change(ctx, ctx.universe, [])        # Y is no longer in the universe
        last = {i: bar_of(i, r[-1]) for i, r in bars.items() if i not in (BTC, "Y-USDT-SWAP")}
        out = s.on_bars(ctx, last) or []
        self.assertEqual([x.inst for x in out if x.action == "close"], ["Y-USDT-SWAP"])

    def test_stop_capped_at_low_leverage(self):
        s = AltRegimeTrend({"margin_usdt": 125, "leverage": 1, "stop_atr": 6})
        bars = market(0.002)
        closes = [1.0 + 0.3 * np.sin(i) for i in range(N - 1)] + [3.0]   # very wide ATR, breakout
        bars["W-USDT-SWAP"] = series(closes, vol=1e6, last_vol=5e6)
        _, out = run(s, bars)
        sig = [x for x in out if x.inst == "W-USDT-SWAP"][0]
        self.assertGreater(sig.stop_loss, 0)
        self.assertAlmostEqual((3.0 - sig.stop_loss) / 3.0, 0.5)

    def test_suggested_margin(self):
        s = AltRegimeTrend({"aggr": 1.0, "style": 1})        # aggressive: dd95 = 50 margins
        self.assertAlmostEqual(s.suggested_margin(3000), 3000 * 0.5 / 50)
        s = AltRegimeTrend({"aggr": 0.0})                    # conservative: 10 % budget / 60 margins
        self.assertAlmostEqual(s.suggested_margin(3000), 3000 * 0.1 / 60)
        self.assertEqual(s.suggested_margin(0), 0)

    def test_auto_margin_when_zero(self):
        s = AltRegimeTrend({"aggr": 1.0, "style": 1})
        _, out = run(s, market(0.002, breakout="X-USDT-SWAP"), equity=10000)
        sig = [x for x in out if x.action == "open_long"][0]
        d = (1.2 - sig.stop_loss) / 1.2
        self.assertAlmostEqual(sig.strength * 10000 * 0.1 / d, 10000 * 0.5 / 50 * 8, delta=1)


if __name__ == "__main__":
    np.seterr(all="ignore")
    unittest.main()
