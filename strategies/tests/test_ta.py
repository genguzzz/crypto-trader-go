import math
import unittest
import warnings

import numpy as np

from ctg import ta

nan = math.nan


class TaTest(unittest.TestCase):
    def assertSeries(self, got, want, places=9):
        self.assertEqual(len(got), len(want))
        for i, (g, w) in enumerate(zip(got, want)):
            if math.isnan(w):
                self.assertTrue(math.isnan(g), f"[{i}] = {g}, want NaN")
            else:
                self.assertAlmostEqual(g, w, places=places, msg=f"[{i}]")

    def setUp(self):
        warnings.simplefilter("error")
        self.addCleanup(warnings.resetwarnings)

    def test_sma_ema_rma(self):
        x = [1, 2, 3, 10, 5]
        self.assertSeries(ta.sma(x, 3), [nan, nan, 2, 5, 6])
        # alpha 0.5, seeded with SMA(1,2,3) = 2
        self.assertSeries(ta.ema(x, 3), [nan, nan, 2, 6, 5.5])
        # alpha 1/3
        self.assertSeries(ta.rma(x, 3), [nan, nan, 2, 14 / 3, 43 / 9])

    def test_gaps_do_not_poison(self):
        self.assertSeries(ta.ema([1, 2, 3, nan, 5], 3), [nan, nan, 2, nan, 3.5])
        self.assertSeries(ta.ema([nan, 1, 2, 3], 3), [nan, nan, nan, 2])
        self.assertSeries(ta.sma([1, nan, 3, 4, 5], 2), [nan, nan, nan, 3.5, 4.5])

    def test_rsi(self):
        # changes 1 1 -1 1 1; Wilder n=2 → up .. 1 .5 .75 .875, down .. 0 .5 .25 .125
        self.assertSeries(ta.rsi([1, 2, 3, 2, 3, 4], 2), [nan, nan, 100, 50, 75, 87.5])
        self.assertSeries(ta.rsi([5, 4, 3], 2), [nan, nan, 0])

    def test_atr(self):
        h, l, c = [10, 11, 12], [8, 9, 9], [9, 10, 11]
        self.assertSeries(ta.tr(h, l, c), [2, 2, 3])
        self.assertSeries(ta.atr(h, l, c, 2), [nan, 2, 2.5])

    def test_macd(self):
        x = np.linspace(1, 50, 60) + np.sin(np.arange(60))
        line, sig, hist = ta.macd(x, 3, 6, 4)
        self.assertSeries(line, ta.ema(x, 3) - ta.ema(x, 6))
        self.assertSeries(sig, ta.ema(line, 4))
        self.assertSeries(hist, line - sig)
        self.assertTrue(math.isnan(sig[7]) and not math.isnan(sig[8]))

    def test_bbands_stdev(self):
        mid, up, lo = ta.bbands([1, 2, 3], 3, 2)
        d = 2 * math.sqrt(2 / 3)
        self.assertSeries(mid, [nan, nan, 2])
        self.assertSeries(up, [nan, nan, 2 + d])
        self.assertSeries(lo, [nan, nan, 2 - d])
        self.assertAlmostEqual(ta.stdev([2, 4, 4, 4, 5, 5, 7, 9], 8)[-1], 2.0)

    def test_highest_lowest_change(self):
        self.assertSeries(ta.highest([1, 3, 2, 5], 2), [nan, 3, 3, 5])
        self.assertSeries(ta.lowest([1, 3, 2, 5], 2), [nan, 1, 2, 2])
        self.assertSeries(ta.change([1, 4, 9]), [nan, 3, 5])
        self.assertSeries(ta.change([1, 4, 9], 2), [nan, nan, 8])

    def test_cross(self):
        self.assertEqual(list(ta.crossover([1, 2, 3], [2, 2, 2])), [False, False, True])
        self.assertEqual(list(ta.crossunder([3, 2, 1], 2)), [False, False, True])
        self.assertEqual(list(ta.crossover([1, nan, 3], 2)), [False, False, False])

    def test_degenerate_inputs(self):
        for f in (lambda x: ta.sma(x, 3), lambda x: ta.ema(x, 3), lambda x: ta.rsi(x, 3),
                  lambda x: ta.stdev(x, 3), lambda x: ta.highest(x, 3), lambda x: ta.change(x, 3),
                  lambda x: ta.atr(x, x, x, 3), lambda x: ta.bbands(x)[0], lambda x: ta.crossover(x, x)):
            for x in ([], [1.0], [nan] * 5, [nan, 1, nan, 2, nan]):
                out = f(x)
                self.assertEqual(len(out), len(x))


if __name__ == "__main__":
    unittest.main()
