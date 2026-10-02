"""Behaviour of the built-in strategies against a scripted ctx."""

import math
import os
import sys
import unittest

import numpy as np

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "library"))

from ctg.context import Bar, Bars, Record, Series  # noqa: E402
from majors_trend import MajorsTrend  # noqa: E402
from pool_reversal import PoolReversal  # noqa: E402


def rows(closes, step=4 * 3600_000):
    return [[i * step, c, c + 1, c - 1, c, 10, 10 * c] for i, c in enumerate(closes)]


class Ctx:
    def __init__(self, bars=None, positions=None, ranks=None, series=None, scores=None, universe=None):
        self.state, self.now, self.timeframe = {}, 10**12, "4H"
        self._bars = bars or {}
        self._pos = positions or {}
        self._ranks = ranks or []
        self._series = series or {}
        self._scores = scores or {}
        self.universe = universe or list(self._bars)
        self.notes, self.logs = [], []

    def bars(self, inst, tf=None, n=200):
        r = self._bars.get((inst, tf)) or self._bars.get(inst) or []
        return Bars(r[-n:])

    def series(self, inst, field, tf=None, n=100):
        return Series([{"ts": i, "v": v} for i, v in enumerate(self._series.get((inst, field), []))])

    def position(self, inst):
        return self._pos.get(inst)

    def positions(self):
        return list(self._pos.values())

    def rank(self, rule, side="long", among=None, min_score=None, top=None):
        return [Record(r) for r in self._ranks if min_score is None or r["score"] >= min_score]

    def score(self, rule, inst):
        return self._scores.get(inst, (math.nan, math.nan))

    def notify(self, text, level="info", **data):
        self.notes.append(text)

    def log(self, msg, level="info"):
        self.logs.append(msg)


def bar_of(inst, r):
    return Bar(inst, "4H", dict(zip(["ts", "o", "h", "l", "c", "vol", "volq"], r)))


UP = [100 + 0.3 * i for i in range(150)]  # steady uptrend: EMA20 above EMA50, close near the fast line


class MajorsTrendTest(unittest.TestCase):
    def test_state_entry_without_cross(self):
        ctx = Ctx(bars={"BTC-USDT-SWAP": rows(UP)})
        sig = MajorsTrend().on_bar(ctx, bar_of("BTC-USDT-SWAP", rows(UP)[-1]))
        self.assertEqual((sig.action, sig.stop_loss, sig.take_profit, sig.reverse), ("open_long", "2.0atr", "4.0atr", False))
        self.assertTrue(ctx.notes[0].startswith("BTC 开多"))
        self.assertIn("快线在上", MajorsTrend().status(ctx))

    def test_cross_mode_waits(self):
        ctx = Ctx(bars={"BTC-USDT-SWAP": rows(UP)})
        self.assertIsNone(MajorsTrend({"entry": 1}).on_bar(ctx, bar_of("BTC-USDT-SWAP", rows(UP)[-1])))

    def test_no_chasing(self):
        jump = UP[:-1] + [UP[-1] + 30]  # last close far above the fast EMA
        ctx = Ctx(bars={"BTC-USDT-SWAP": rows(jump)})
        self.assertIsNone(MajorsTrend().on_bar(ctx, bar_of("BTC-USDT-SWAP", rows(jump)[-1])))
        self.assertIn("离快线太远", MajorsTrend().status(ctx))

    def test_reverse_from_short(self):
        ctx = Ctx(bars={"BTC-USDT-SWAP": rows(UP)}, positions={"BTC-USDT-SWAP": Record(inst_id="BTC-USDT-SWAP", pos_side="short")})
        sig = MajorsTrend().on_bar(ctx, bar_of("BTC-USDT-SWAP", rows(UP)[-1]))
        self.assertEqual((sig.action, sig.reverse), ("open_long", True))

    def test_long_only_closes_on_down_trend(self):
        down = [200 - 0.3 * i for i in range(150)]
        ctx = Ctx(bars={"BTC-USDT-SWAP": rows(down)}, positions={"BTC-USDT-SWAP": Record(inst_id="BTC-USDT-SWAP", pos_side="long")})
        sig = MajorsTrend({"direction": 1}).on_bar(ctx, bar_of("BTC-USDT-SWAP", rows(down)[-1]))
        self.assertEqual(sig.action, "close")


def rebound(n=60):
    """15m closes: a slide, then two strong bars back up (EMA9 reclaim + RSI back above 30)."""
    c = [100 - 0.8 * i for i in range(n - 2)]
    return c + [c[-1] + 3, c[-1] + 6]


class PoolReversalTest(unittest.TestCase):
    def ctx(self, **kw):
        a = rows(rebound(), step=900_000)
        btc = rows([100.0] * 30, step=3600_000)
        base = dict(bars={"A-USDT-SWAP": a, ("BTC-USDT-SWAP", "1H"): btc},
                    ranks=[{"inst_id": "A-USDT-SWAP", "side": "long", "score": 92.0, "long": 92.0, "short": 8.0}])
        base.update(kw)
        c = Ctx(**base)
        c.timeframe = "15m"
        return c, {"A-USDT-SWAP": bar_of("A-USDT-SWAP", a[-1])}

    def test_enters_on_confirmation(self):
        ctx, bars = self.ctx()
        out = PoolReversal().on_bars(ctx, bars)
        self.assertEqual(len(out), 1)
        s = out[0]
        self.assertEqual((s.inst, s.action, s.time_limit, s.stop_loss), ("A-USDT-SWAP", "open_long", "6.0h", "2.5atr"))
        self.assertIn("收复EMA9", s.reason)
        self.assertIn("RSI拐头", s.reason)
        self.assertEqual(ctx.state["watch"], {})  # entered: off the watchlist

    def test_score_alone_is_not_enough(self):
        flat = rows([100.0] * 60, step=900_000)
        ctx, _ = self.ctx(bars={"A-USDT-SWAP": flat, ("BTC-USDT-SWAP", "1H"): rows([100.0] * 30, step=3600_000)})
        out = PoolReversal().on_bars(ctx, {"A-USDT-SWAP": bar_of("A-USDT-SWAP", flat[-1])})
        self.assertEqual(out, [])
        self.assertIn("A多92（0/2）", PoolReversal().status(ctx))

    def test_btc_veto(self):
        btc = rows([100.0] * 29 + [90.0], step=3600_000)  # BTC dropped 10 in the last hour
        ctx, bars = self.ctx()
        ctx._bars[("BTC-USDT-SWAP", "1H")] = btc
        self.assertEqual(PoolReversal().on_bars(ctx, bars), [])

    def test_exit_when_score_normalises(self):
        ctx, bars = self.ctx(positions={"B-USDT-SWAP": Record(inst_id="B-USDT-SWAP", pos_side="long")},
                             scores={"B-USDT-SWAP": (40.0, 60.0)}, ranks=[])
        out = PoolReversal().on_bars(ctx, bars)
        self.assertEqual([(s.inst, s.action) for s in out], [("B-USDT-SWAP", "close")])


if __name__ == "__main__":
    np.seterr(all="ignore")
    unittest.main()
