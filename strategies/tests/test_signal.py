import math
import os
import unittest

from ctg import (Bool, Choice, Float, Int, Param, Screener, Signal, SignalError, Strategy, Timeframe, Watchlist, pick)
from ctg.check import check
from ctg.context import Record


class SignalTest(unittest.TestCase):
    def test_atr_levels_long(self):
        w = Signal.open_long("BTC-USDT-SWAP", stop_loss="2atr", take_profit="3.5 ATR", reason="x").to_wire(100, 4)
        self.assertEqual(w["stopLoss"], 92)
        self.assertEqual(w["takeProfit"], 114)
        self.assertEqual(w["action"], "open_long")
        self.assertEqual(w["strength"], 1.0)

    def test_atr_levels_short(self):
        w = Signal.open_short("X", stop_loss="2atr", take_profit="1atr").to_wire(100, 4)
        self.assertEqual((w["stopLoss"], w["takeProfit"]), (108, 96))

    def test_prices_and_options(self):
        s = Signal.open_long("X", stop_loss=95.5, take_profit=None, strength=0.5, leverage=3, ttl_ms=60000,
                             max_slippage_bp=8)
        self.assertFalse(s.needs_atr())
        w = s.to_wire()
        self.assertEqual(w["stopLoss"], 95.5)
        self.assertNotIn("takeProfit", w)
        self.assertEqual((w["strength"], w["leverage"], w["ttlMs"], w["maxSlippageBp"]), (0.5, 3.0, 60000, 8.0))

    def test_close_and_reduce(self):
        self.assertEqual(Signal.close("X", reason="r").to_wire(), {"instId": "X", "action": "close",
                                                                    "strength": 1.0, "reason": "r"})
        self.assertEqual(Signal.reduce("X", 0.5).to_wire()["reduceFrac"], 0.5)
        with self.assertRaises(SignalError):
            Signal.reduce("X", 1.5)

    def test_errors(self):
        with self.assertRaises(SignalError):
            Signal.open_long("X", stop_loss="2 sigma").to_wire(100, 1)
        with self.assertRaises(SignalError):
            Signal.open_long("X", stop_loss="2atr").to_wire(100, math.nan)
        with self.assertRaises(SignalError):
            Signal.open_long("X", stop_loss="2atr").to_wire(None, None)
        with self.assertRaises(SignalError):
            Signal.open_long("X", stop_loss="30atr").to_wire(100, 4)  # negative price
        with self.assertRaises(SignalError):
            Signal.open_long("X", strength=0).to_wire()


class Demo(Strategy):
    universe = Screener(rule="r", top=5, min_score=60, filter={"spread_bps": (0, 10)})
    timeframe = "4H"
    n = Param(20, min=5, max=50, ai_tunable=True)
    k = Param(1.5)


class StrategyTest(unittest.TestCase):
    def test_params(self):
        s = Demo({"n": 7.6, "k": 2, "unknown": 1})
        self.assertEqual((s.n, s.k), (8, 2.0))
        self.assertIsInstance(s.n, int)
        self.assertEqual(Demo({"n": 500}).n, 50)
        self.assertEqual(Demo().n, 20)

    def test_meta(self):
        m = Demo.meta()
        self.assertEqual(m["name"], "demo")
        self.assertEqual(m["params"][0], {"name": "n", "type": "int", "default": 20.0, "min": 5.0, "max": 50.0,
                                          "aiTunable": True})
        self.assertEqual(m["meta"], {"canLong": True, "canShort": True})
        self.assertEqual(m["universe"], {"screener": {"rule": "r", "side": "long", "top": 5, "refresh": "1H",
                                                      "minScore": 60.0, "filter": {"spread_bps": [0.0, 10.0]}}})


class ScreenerScopeTest(unittest.TestCase):
    def test_wire(self):
        self.assertNotIn("scope", Screener(rule="r").to_wire())
        self.assertNotIn("scope", Screener(rule="r", scope="pool").to_wire())
        w = Screener(top=300, scope="market", filter={"spread_bps": (0, 10)}).to_wire()
        self.assertEqual((w["scope"], w["top"], w["rule"]), ("market", 300, ""))
        from ctg.context import Context
        with self.assertRaises(ValueError):  # a typo must not silently fall back to the pool
            Context(None).screener(top=200, scope="Market")

    def _msgs(self, screener: str) -> list[str]:
        code = ("from ctg import Strategy, Screener\n"
                "class S(Strategy):\n"
                f"    universe = {screener}\n")
        return [d["message"] for d in check(code)["diagnostics"] if ".universe" in d["message"]]

    def test_check(self):
        self.assertEqual(self._msgs('Screener(top=300, scope="market")'), [])
        self.assertEqual(self._msgs('Screener(rule="r", top=50)'), [])
        self.assertTrue(any("1–50" in m for m in self._msgs('Screener(rule="r", top=51)')))
        self.assertTrue(any("1–300" in m for m in self._msgs('Screener(top=301, scope="market")')))
        self.assertTrue(any("rule" in m for m in self._msgs('Screener(rule="r", top=10, scope="market")')))
        self.assertTrue(any("min_score" in m for m in self._msgs('Screener(top=10, min_score=50, scope="market")')))
        self.assertTrue(any("scope" in m for m in self._msgs('Screener(top=10, scope="all")')))


class NewSignalTest(unittest.TestCase):
    def test_percent_trailing_time_limit(self):
        w = Signal.open("X", "short", stop_loss="2%", take_profit="1%", trailing_stop="1.5atr", time_limit="6h").to_wire(100, 2)
        self.assertEqual((w["action"], w["stopLoss"], w["takeProfit"], w["trailDist"], w["timeLimitMs"]),
                         ("open_short", 102, 99, 3, 6 * 3600_000))
        self.assertNotIn("reverse", w)

    def test_reversed(self):
        w = Signal.open_long("X", stop_loss=90).reversed().to_wire(100, 1)
        self.assertTrue(w["reverse"])
        with self.assertRaises(SignalError):
            Signal.close("X").reversed()

    def test_bad_inputs(self):
        with self.assertRaises(SignalError):
            Signal.open("X", "up")
        with self.assertRaises(SignalError):
            Signal.open_long("X", time_limit="6 weeks").to_wire(100, 1)
        with self.assertRaises(SignalError):
            Signal.open_long("X", trailing_stop="1atr").to_wire(100, None)


class Typed(Strategy):
    timeframe = Timeframe("4H", options=["1H", "4H"])
    n = Int(10, min=1, max=20)
    k = Float(1.5, min=0, max=3, step=0.5)
    on = Bool(False)
    mode = Choice("b", ["a", "b", "c"], labels=["A", "B", "C"])
    direction = Choice("both", ["both", "long", "short"])
    can_short = False
    schedule = {"report": "1H"}

    def on_bars(self, ctx, bars):
        return None


class TypedParamTest(unittest.TestCase):
    def test_wire_values(self):
        s = Typed({"timeframe": 0, "n": 3.4, "on": 1, "mode": 2, "direction": 1})
        self.assertEqual((s.timeframe, s.n, s.k, s.on, s.mode, s.direction), ("1H", 3, 1.5, True, "c", "long"))
        d = Typed()
        self.assertEqual((d.timeframe, d.on, d.mode), ("4H", False, "b"))
        self.assertEqual(Typed({"mode": 9}).mode, "c")  # clamped into the options

    def test_meta(self):
        m = Typed.meta()
        self.assertEqual(m["timeframe"], "4H")
        specs = {p["name"]: p for p in m["params"]}
        self.assertEqual(specs["timeframe"], {"name": "timeframe", "type": "timeframe", "default": 1.0,
                                              "options": ["1H", "4H"], "label": "K 线周期"})
        self.assertEqual(specs["mode"]["labels"], ["A", "B", "C"])
        self.assertEqual(specs["on"]["default"], 0.0)
        self.assertEqual(m["meta"], {"canLong": True, "canShort": False, "schedule": {"report": "1H"}, "batch": True})

    def test_allows(self):
        self.assertTrue(Typed().allows("long"))
        self.assertFalse(Typed().allows("short"))  # can_short = False
        self.assertFalse(Typed({"direction": 1}).allows("short"))
        self.assertTrue(Demo().allows("short"))


class CheckTest(unittest.TestCase):
    def test_syntax_error(self):
        r = check("from ctg import Strategy\nclass X(Strategy:\n    pass\n")
        self.assertEqual(r["diagnostics"][0]["line"], 2)
        self.assertIn("语法错误", r["diagnostics"][0]["message"])

    def test_declarations(self):
        code = ("from ctg import Strategy, Int\n"
                "class MyThing(Strategy):\n"
                "    universe = ['btc']\n"
                "    n = Int(5, min=10)\n"
                "    def on_bar(self, ctx, bar):\n"
                "        return undefined_name\n")
        r = check(code)
        msgs = {d["line"]: d["message"] for d in r["diagnostics"]}
        self.assertIn("合约 ID", msgs[3])
        self.assertIn("超出范围", msgs[4])
        self.assertEqual(r["strategies"][0]["name"], "my-thing")

    def test_runtime_error_at_import(self):
        r = check("from ctg import Strategy\nx = 1 / 0\n")
        self.assertEqual(r["diagnostics"][-1]["line"], 2)
        self.assertIn("ZeroDivisionError", r["diagnostics"][-1]["message"])

    def test_library_is_clean(self):
        lib = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "library")
        for fn in sorted(os.listdir(lib)):
            if fn.endswith(".py"):
                with open(os.path.join(lib, fn), encoding="utf-8") as f:
                    r = check(f.read())
                self.assertEqual([d for d in r["diagnostics"]], [], fn)


class _Ctx:
    def __init__(self):
        self.state, self.now = {}, 0


class PortfolioTest(unittest.TestCase):
    def test_watchlist(self):
        ctx, w = _Ctx(), Watchlist()
        c = lambda i, side, score: Record(inst_id=i, side=side, score=score)  # noqa: E731
        w.sync(ctx, [c("A", "long", 90), c("B", "short", 85)], ttl_ms=100)
        ctx.now = 50
        self.assertEqual(w.sync(ctx, [c("A", "long", 92), c("C", "long", 81)], ttl_ms=100), ["B"])
        self.assertEqual([(x.inst_id, x.since) for x in w.items(ctx)], [("A", 0), ("C", 50)])
        ctx.now = 120
        self.assertEqual(w.sync(ctx, [c("A", "long", 92), c("C", "short", 81)], ttl_ms=100), ["A"])
        self.assertEqual(w.items(ctx)[0].since, 120)  # side change restarts the clock

    def test_pick(self):
        c = lambda i, side, score: Record(inst_id=i, side=side, score=score)  # noqa: E731
        pos = [Record(inst_id="H", pos_side="long")]
        got = pick([c("A", "long", 80), c("B", "long", 95), c("H", "long", 99), c("S", "short", 70)], pos, max_total=3,
                   max_side=2)
        self.assertEqual([x.inst_id for x in got], ["B", "S"])


if __name__ == "__main__":
    unittest.main()
