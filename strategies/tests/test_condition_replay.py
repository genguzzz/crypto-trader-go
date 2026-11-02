"""Script-signal replays on real OKX history (testdata/okx-1m-20261030.csv.gz, shared with the Go tests).

The replay must give each close exactly the bars closed by then — the last `lookback`, never a later
one — and turn the answers into false→true edges. These tests compute the same conditions straight
from the full arrays (vectorised, independently of the replay loop) and require the same state on every
close, for common conditions, several timeframes and multiple instruments; plus the warm-up rule (fewer
than `lookback` bars: unknown, the script is not run)."""

import csv
import gzip
import os
import textwrap
import unittest

import numpy as np

from ctg.condition import load, replay, run_check
from ctg.context import Bars

FIXTURE = os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "okx-1m-20261030.csv.gz")
STEPS = {"1m": 60_000, "5m": 300_000, "15m": 900_000, "1H": 3_600_000}
ENA, PEPE, BTC = "ENA-USDT-SWAP", "PEPE-USDT-SWAP", "BTC-USDT-SWAP"


def load_fixture():
    out = {}
    with gzip.open(FIXTURE, "rt") as f:
        for r in csv.DictReader(f):
            out.setdefault(r["instId"], []).append([int(r["ts"])] + [float(r[k]) for k in ("o", "h", "l", "c", "vol", "volq")])
    return {k: np.array(sorted(v), dtype=float) for k, v in out.items()}


def aggregate(a: np.ndarray, step: int) -> np.ndarray:
    """1m rows → UTC-aligned buckets of `step` ms (the host's BarsTF)."""
    if step == 60_000:
        return a
    b = (a[:, 0] // step * step).astype(np.int64)
    out = []
    for k in np.unique(b):
        x = a[b == k]
        out.append([k, x[0, 1], x[:, 2].max(), x[:, 3].min(), x[-1, 4], x[:, 5].sum(), x[:, 6].sum()])
    return np.array(out)


DATA = load_fixture()


def code(body: str, insts, tf: str, lookback: int) -> str:
    return (f"from ctg import Condition, Fire, ta\n\nclass C(Condition):\n    \"\"\"replay test\"\"\"\n"
            f"    instruments = {list(insts)!r}\n    timeframe = {tf!r}\n    lookback = {lookback}\n\n"
            "    def check(self, ctx, bars):\n" + textwrap.indent(textwrap.dedent(body), "        "))


def sma(x, n):
    out = np.full(len(x), np.nan)
    c = np.cumsum(np.insert(x, 0, 0.0))
    out[n - 1:] = (c[n:] - c[:-n]) / n
    return out


class ReplayEqualsDirectComputation(unittest.TestCase):
    def check_case(self, body, insts, tf, lookback, direct):
        """direct(arrays, i) → the expected answer at bar index i of the first instrument (None = unknown)."""
        step = STEPS[tf]
        arrays = {i: aggregate(DATA[i], step) for i in insts}
        first = arrays[insts[0]]
        start, end = int(first[0, 0]) + step, int(first[-1, 0]) + step
        out = replay(load(code(body, insts, tf, lookback)), {i: a.tolist() for i, a in arrays.items()}, start, end, 1.0, 120)
        self.assertNotIn("error", out)
        self.assertEqual(out["errors"], 0, out.get("firstError"))
        states = out["states"]
        self.assertEqual(len(states), len(first))
        want, prev, edges = [], None, []
        for i in range(len(first)):
            v = None if i + 1 < lookback else direct(arrays, i)  # warm-up: fewer than lookback bars
            want.append("-" if v is None else "1" if v else "0")
            if v is not None:
                if v and prev is False:
                    edges.append(int(first[i, 0]) + step)
                prev = v
        self.assertEqual(states, "".join(want))
        self.assertEqual([e["t"] for e in out["edges"]], edges[:200])
        self.assertEqual(out["edgeCount"], len(edges))
        self.assertGreater(len(edges), 0, "the case should fire on this history")
        self.assertGreater(states.count("0"), 0)
        return out

    def test_sma_cross_5m(self):
        def direct(a, i):
            c = a[ENA][:, 4]
            m = sma(c, 20)
            return bool(c[i - 1] <= m[i - 1] and c[i] > m[i])
        self.check_case("""
            c = bars["ENA-USDT-SWAP"].c
            m = ta.sma(c, 20)
            return bool(c[-2] <= m[-2] and c[-1] > m[-1])
        """, [ENA], "5m", 30, direct)

    def test_breakout_15m(self):
        def direct(a, i):
            b = a[PEPE]
            return bool(b[i, 4] > b[i - 12:i, 2].max())
        self.check_case("""
            b = bars["PEPE-USDT-SWAP"]
            return bool(b.c[-1] > b.h[-13:-1].max())
        """, [PEPE], "15m", 13, direct)

    def test_volume_spike_1m(self):
        def direct(a, i):
            b = a[ENA]
            return bool(b[i, 6] > 3 * b[i - 20:i, 6].mean())
        self.check_case("""
            q = bars["ENA-USDT-SWAP"].volq
            return bool(q[-1] > 3 * q[-21:-1].mean())
        """, [ENA], "1m", 21, direct)

    def test_relative_strength_two_instruments_15m(self):
        def direct(a, i):
            e, b = a[ENA][:, 4], a[BTC][:, 4]
            return bool((e[i] / e[i - 4] - 1) - (b[i] / b[i - 4] - 1) > 0.01)
        self.check_case("""
            e, b = bars["ENA-USDT-SWAP"].c, bars["BTC-USDT-SWAP"].c
            return bool((e[-1] / e[-5] - 1) - (b[-1] / b[-5] - 1) > 0.01)
        """, [ENA, BTC], "15m", 10, direct)

    def test_hourly_range(self):
        def direct(a, i):
            b = a[PEPE]
            return bool(b[i, 2] / b[i, 3] - 1 > 0.025)
        self.check_case("""
            b = bars["PEPE-USDT-SWAP"]
            return bool(b.h[-1] / b.l[-1] - 1 > 0.025)
        """, [PEPE], "1H", 10, direct)

    def test_no_lookahead(self):
        """A script that would fire only if it saw the next bar never fires."""
        step = STEPS["5m"]
        a = aggregate(DATA[ENA], step)
        body = """
            b = bars["ENA-USDT-SWAP"]
            assert int(b.ts[-1]) + 300000 == ctx.now, "newest bar is the one closing now"
            return len(b) <= 10
        """
        out = replay(load(code(body, [ENA], "5m", 10)), {ENA: a.tolist()}, int(a[0, 0]) + step, int(a[-1, 0]) + step, 1.0)
        self.assertEqual(out["errors"], 0, out.get("firstError"))
        self.assertEqual(set(out["states"][9:]), {"1"})  # exactly lookback bars every time, never more


class WarmupRule(unittest.TestCase):
    def test_fewer_than_lookback_is_unknown_and_not_run(self):
        a = DATA[ENA][:30]
        cls = load(code("raise RuntimeError('must not run')\n", [ENA], "1m", 50))
        r = run_check(cls(), {ENA: Bars.from_array(a)}, {}, int(a[-1, 0]) + 60_000, 0.2)
        self.assertIsNone(r["fire"])
        self.assertEqual(r["warmup"], [ENA])
        self.assertNotIn("error", r)
        # a gap at the close still wins (missing), and enough bars run the script
        r = run_check(cls(), {ENA: Bars.from_array(a)}, {}, int(a[-1, 0]) + 120_000, 0.2)
        self.assertEqual(r["missing"], [ENA])
        cls2 = load(code("return True\n", [ENA], "1m", 30))
        self.assertIs(run_check(cls2(), {ENA: Bars.from_array(a)}, {}, int(a[-1, 0]) + 60_000, 0.2)["fire"], True)

    def test_replay_starts_after_warmup(self):
        a = DATA[PEPE][:100]
        out = replay(load(code("c = bars['PEPE-USDT-SWAP'].c\nreturn bool(c[-1] > c[-2])\n", [PEPE], "1m", 20)),
                     {PEPE: a.tolist()}, int(a[0, 0]) + 60_000, int(a[-1, 0]) + 60_000, 0.5)
        self.assertEqual(out["errors"], 0)  # c[-2] never sees a 1-bar window
        self.assertEqual(out["states"][:19], "-" * 19)
        self.assertNotIn("-", out["states"][19:])


if __name__ == "__main__":
    unittest.main()
