import math
import unittest

from ctg import Param, Screener, Signal, SignalError, Strategy


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
        self.assertEqual(m["params"][0], {"name": "n", "default": 20.0, "min": 5.0, "max": 50.0, "aiTunable": True})
        self.assertEqual(m["universe"], {"screener": {"rule": "r", "side": "long", "top": 5, "refresh": "1H",
                                                      "minScore": 60.0, "filter": {"spread_bps": [0.0, 10.0]}}})


if __name__ == "__main__":
    unittest.main()
