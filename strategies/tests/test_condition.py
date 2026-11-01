import io
import json
import math
import os
import sys
import textwrap
import time
import unittest

import numpy as np

from ctg import Condition, Fire
from ctg.condition import (MAX_CODE, ScriptError, _clean, _guarded_import, check_source, load, replay, run_check,
                           static_problems)
from ctg.condworker import Worker
from ctg.context import Bars

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)
import runner  # noqa: E402

AAVE = "AAVE-USDT-SWAP"
STEP = 900_000

RSI = textwrap.dedent('''\
    from ctg import Condition, Fire, ta

    class AaveRsi(Condition):
        """AAVE 15m RSI 跌破 30"""
        instruments = ["AAVE-USDT-SWAP"]
        timeframe = "15m"
        lookback = 50

        def check(self, ctx, bars):
            r = ta.rsi(bars["AAVE-USDT-SWAP"].c, 14)[-1]
            return Fire(r < 30, values={"rsi": r}, note=f"RSI {r:.1f}")
    ''')


def script(body: str, head: str = "", **decl) -> str:
    d = {"instruments": [AAVE], "timeframe": "15m", "lookback": 20}
    d.update(decl)
    lines = [f"    {k} = {v!r}" for k, v in d.items()]
    return (f"from ctg import Condition, Fire\n{head}\nclass S(Condition):\n    \"\"\"test\"\"\"\n" + "\n".join(lines) +
            "\n    def check(self, ctx, bars):\n" + textwrap.indent(textwrap.dedent(body), "        "))


def rows(closes, start=0, step=STEP):
    return [[start + i * step, c, c + 1, c - 1, c, 1.0, 100.0] for i, c in enumerate(closes)]


def bars_at(closes, n_end, lookback=20):
    """The bars a check sees at the close of bar n_end-1."""
    a = np.array(rows(closes), dtype=float)
    return {AAVE: Bars.from_array(a[max(0, n_end - lookback):n_end])}


class ConditionClassTest(unittest.TestCase):
    def test_meta_and_check(self):
        cls = load(RSI)
        self.assertTrue(issubclass(cls, Condition))
        self.assertEqual(cls.meta(), {"class": "AaveRsi", "name": "AaveRsi", "description": "AAVE 15m RSI 跌破 30",
                                      "instIds": [AAVE], "timeframe": "15m", "lookback": 50})
        res = check_source(RSI)
        self.assertTrue(res["ok"])
        self.assertEqual(len(res["meta"]["hash"]), 16)

    def test_fire_values_are_cleaned(self):
        self.assertEqual(_clean(np.bool_(True)), {"fire": True, "values": {}, "note": ""})
        self.assertEqual(_clean(None)["fire"], None)
        out = _clean(Fire(np.float64(1) > 0, values={"a": np.float64(2.5), "nan": math.nan, "s": "x", "inf": math.inf},
                          note="n" * 500))
        self.assertEqual(out["values"], {"a": 2.5})
        self.assertEqual(len(out["note"]), 200)
        self.assertIs(out["fire"], True)
        self.assertIsNone(Fire(None).hit)
        with self.assertRaises(TypeError):
            _clean(1.0)

    def test_declarations(self):
        bad = {
            "instruments": ([], "instruments 需为"),
            "timeframe": ("1D", "timeframe 需为"),
            "lookback": (5, "lookback 需为"),
        }
        for k, (v, want) in bad.items():
            with self.assertRaises(ScriptError) as e:
                load(script("return True\n", **{k: v}))
            self.assertIn(want, str(e.exception), k)
        with self.assertRaises(ScriptError) as e:
            load(script("return True\n", instruments=[f"C{i}-USDT-SWAP" for i in range(11)]))
        self.assertIn("最多 10", str(e.exception))
        with self.assertRaises(ScriptError) as e:
            load(script("return True\n", instruments=["aave"]))
        self.assertIn("不是 AAVE-USDT-SWAP", str(e.exception))
        with self.assertRaises(ScriptError) as e:
            load("from ctg import Condition\nclass S(Condition):\n    '''x'''\n    instruments=['A-USDT-SWAP']\n")
        self.assertIn("没有实现 check", str(e.exception))
        with self.assertRaises(ScriptError) as e:
            load(script("return True\n") + "\nclass T(S):\n    pass\n")
        self.assertIn("恰好一个", str(e.exception))
        with self.assertRaises(ScriptError) as e:
            load(script("return True\n").replace('"""test"""', "pass"))
        self.assertIn("写一行说明", str(e.exception))


class StaticCheckTest(unittest.TestCase):
    def msgs(self, code):
        return " | ".join(d["message"] for d in static_problems(code))

    def test_rejects(self):
        cases = {
            "import os": "不能导入 os",
            "from subprocess import run": "不能导入 subprocess",
            "import importlib": "不能导入 importlib",
            "from . import x": "相对导入",
            "open('f')": "不能用 open",
            "eval('1')": "不能用 eval",
            "__import__('os')": "不能用 __import__",
            "getattr(x, 'y')": "不能用 getattr",
            "x = ().__class__": "不能访问 __class__",
            "import numpy as np\nnp.load('a')": "np.load",
            "import numpy\nnumpy.fromfile('a')": "numpy.fromfile",
            "try:\n    pass\nexcept:\n    pass": "裸 except",
            "try:\n    pass\nexcept BaseException:\n    pass": "裸 except",
            "def f():\n    global x": "global",
            "def f(:": "语法错误",
        }
        for code, want in cases.items():
            self.assertIn(want, self.msgs(code), code)
        self.assertIn("脚本太长", self.msgs("x = 1\n" * (MAX_CODE // 5)))

    def test_allows(self):
        code = ("import math, statistics, numpy as np\nfrom collections import deque\nfrom ctg import ta, Condition\n"
                "from ctg.ta import rsi\nx = np.mean([1])\ntry:\n    pass\nexcept Exception:\n    pass\n")
        self.assertEqual(static_problems(code), [])

    def test_runtime_import_guard(self):
        with self.assertRaises(ImportError):
            _guarded_import("os")
        self.assertIs(_guarded_import("math"), math)
        # undefined names fail before running (pyflakes)
        self.assertFalse(check_source(script("return nope\n"))["ok"])


class RunCheckTest(unittest.TestCase):
    def test_values_and_freshness(self):
        obj = load(RSI)()
        closes = list(np.linspace(120, 90, 60))
        r = run_check(obj, bars_at(closes, 60, 50), {}, 60 * STEP, 0.2)
        self.assertIs(r["fire"], True)
        self.assertLess(r["values"]["rsi"], 30)
        self.assertTrue(r["note"].startswith("RSI "))
        # the newest bar is not the one closing at `now`: unknown, the script does not run
        r = run_check(obj, bars_at(closes, 59, 50), {}, 60 * STEP, 0.2)
        self.assertEqual((r["fire"], r["missing"]), (None, [AAVE]))
        r = run_check(obj, {}, {}, 60 * STEP, 0.2)
        self.assertEqual(r["missing"], [AAVE])

    def test_errors_carry_the_line(self):
        obj = load(script("x = 1\nreturn 1 / (x - 1)\n"))()
        r = run_check(obj, bars_at([1] * 30, 30), {}, 30 * STEP, 0.2)
        self.assertIsNone(r["fire"])
        self.assertIn("ZeroDivisionError", r["error"])
        self.assertIn("第 10 行", r["error"])
        r = run_check(load(script("return 1.5\n"))(), bars_at([1] * 30, 30), {}, 30 * STEP, 0.2)
        self.assertIn("请返回 True / False", r["error"])

    def test_cpu_budget(self):
        loop = script("while True:\n    try:\n        sum(range(1000))\n    except Exception:\n        pass\n")
        t0 = time.monotonic()
        r = run_check(load(loop)(), bars_at([1] * 30, 30), {}, 30 * STEP, 0.05)
        self.assertIn("预算", r["error"])
        self.assertLess(time.monotonic() - t0, 2)

    def test_context(self):
        code = script('''\
            assert ctx.instruments == ["AAVE-USDT-SWAP"] and ctx.timeframe == "15m"
            assert len(ctx.bars("AAVE-USDT-SWAP", 5)) == 5
            f = ctx.factors("AAVE-USDT-SWAP")
            return Fire(f["funding_hourly"] > 0, values={"n": len(bars["AAVE-USDT-SWAP"])})
            ''')
        r = run_check(load(code)(), bars_at([1] * 30, 30), {AAVE: {"funding_hourly": 0.0001}}, 30 * STEP, 0.2)
        self.assertEqual((r["fire"], r["values"], r["factors"]), (True, {"n": 20.0}, True))


class ReplayTest(unittest.TestCase):
    def test_edges_skip_unknown_and_start(self):
        # holds when the close is above 10; None while it is exactly 10 (unknown never makes a change)
        code = script("c = bars['AAVE-USDT-SWAP'].c[-1]\nreturn None if c == 10 else Fire(c > 10, note=f'{c:g}')\n",
                      lookback=10)
        closes = [12] * 12 + [5, 5, 12, 12, 5, 10, 12, 5, 12]
        out = replay(load(code), {AAVE: rows(closes)}, 12 * STEP, len(closes) * STEP, 0.2)
        # the close at k sees bars up to k-1: closes 12..21 read bars 11..20. No change at the first close (nothing
        # known before it); 0 → unknown → 1 is a change (unknown keeps the last known value)
        self.assertEqual(out["states"], "100110-101")
        self.assertEqual([e["t"] for e in out["edges"]], [15 * STEP, 19 * STEP, 21 * STEP])
        self.assertEqual(out["edges"][0]["note"], "12")
        self.assertEqual((out["edgeCount"], out["unknown"], out["errors"]), (3, 1, 0))
        self.assertEqual(out["last"]["fire"], True)

    def test_errors_and_gaps(self):
        code = script("c = bars['AAVE-USDT-SWAP'].c[-1]\nif c < 0:\n    raise ValueError('neg')\nreturn c > 1\n",
                      lookback=10)
        rs = rows([2] * 15 + [-1, 2])
        del rs[13]  # a gap: the close of bar 13 has no bar
        out = replay(load(code), {AAVE: rs}, 12 * STEP, 17 * STEP, 0.2)
        self.assertEqual(out["states"], "11-1-1")  # 14: bar 13 is missing; 16: the script raised
        self.assertEqual(out["errors"], 1)
        self.assertEqual(out["firstError"]["t"], 16 * STEP)
        self.assertIn("ValueError: neg", out["firstError"]["error"])


class WorkerTest(unittest.TestCase):
    def run_worker(self, *reqs):
        out = io.StringIO()
        w = Worker(out)
        w.serve(io.StringIO("".join(json.dumps(r) + "\n" for r in reqs) + "not json\n"))
        return w, [json.loads(x) for x in out.getvalue().splitlines()]

    def test_ops(self):
        bad = script("raise RuntimeError('boom')\n")
        closes = [100] * 40
        item = {"key": 1, "code": RSI, "now": 40 * STEP, "bars": {AAVE: rows(closes)}, "factors": {}}
        w, lines = self.run_worker(
            {"op": "check", "code": RSI},
            {"op": "eval", "budgetMs": 200, "items": [item, dict(item, key=2, code=bad), dict(item, key=3, code="x = (")]},
            {"op": "eval", "items": [item]},
            {"op": "replay", "code": RSI, "bars": {AAVE: rows(closes)}, "start": 30 * STEP, "end": 40 * STEP},
            {"op": "nope"},
        )
        self.assertEqual(lines[0], {"ready": True})
        self.assertTrue(lines[1]["ok"] and lines[1]["end"])
        ev = lines[2:6]
        self.assertEqual([x.get("key") for x in ev[:3]], [1, 2, 3])
        self.assertIs(ev[0]["fire"], False)  # flat closes: RSI is NaN, NaN < 30 is False
        self.assertIn("RuntimeError: boom", ev[1]["error"])
        self.assertIn("语法错误", ev[2]["error"])
        self.assertEqual(ev[3], {"end": True})
        self.assertEqual(len(w.cache), 3)  # the RSI script was loaded once for both batches and the replay
        self.assertEqual(lines[8]["states"], "0" * 11)
        self.assertIn("unknown op", lines[9]["error"])
        self.assertIn("bad request", lines[10]["error"])


FAKE = textwrap.dedent('''\
    import json, os, signal, sys, time
    if "--die" in sys.argv:
        sys.exit(2)
    print(json.dumps({"ready": True}), flush=True)
    for line in sys.stdin:
        req = json.loads(line)
        for i, it in enumerate(req.get("items") or []):
            if it["key"] == "boom":
                print("segfault-ish", file=sys.stderr, flush=True)
                sys.exit(3)
            if it["key"] == "hang":
                time.sleep(60)
            if it["key"] == "xcpu" and not os.path.exists(sys.argv[-1]):
                open(sys.argv[-1], "w").close()  # only the first process hits the limit
                os.kill(os.getpid(), signal.SIGXCPU)
            print(json.dumps({"i": i, "key": it["key"], "fire": True, "values": {}, "note": "", "ms": 1}), flush=True)
        print(json.dumps({"end": True, "op": req.get("op")}), flush=True)
    ''')


class CondWorkerTest(unittest.TestCase):
    def worker(self, *extra):
        w = runner.CondWorker([sys.executable, "-c", FAKE, *extra])
        self.addCleanup(w.stop)
        return w

    def test_crash_blames_the_running_item_and_reruns_the_rest(self):
        w = self.worker()
        out = w.request({"op": "eval", "budgetMs": 100, "items": [{"key": "a"}, {"key": "boom"}, {"key": "b"}]})
        items = out["items"]
        self.assertEqual([x["key"] for x in items], ["a", "boom", "b"])
        self.assertEqual([x["i"] for x in items], [0, 1, 2])
        self.assertTrue(items[1]["crash"])
        self.assertIn("异常退出（代码 3）", items[1]["error"])
        self.assertIn("segfault-ish", items[1]["error"])
        self.assertIs(items[2]["fire"], True)
        self.assertEqual(w.request({"op": "check"}), {"op": "check"})  # a fresh process serves the next request

    def test_hang_is_killed(self):
        w = self.worker()
        t0 = time.monotonic()
        out = w.request({"op": "eval", "budgetMs": 10, "items": [{"key": "hang"}, {"key": "c"}]})
        self.assertLess(time.monotonic() - t0, 10)
        self.assertIn("超时", out["items"][0]["error"])
        self.assertIs(out["items"][1]["fire"], True)

    def test_cpu_limit_is_not_the_scripts_fault(self):
        import tempfile

        mark = os.path.join(tempfile.mkdtemp(), "hit")
        w = self.worker(mark)
        out = w.request({"op": "eval", "budgetMs": 100, "items": [{"key": "a"}, {"key": "xcpu"}, {"key": "b"}]})
        self.assertEqual([(x["key"], x["fire"], "crash" in x) for x in out["items"]], [("a", True, False), ("xcpu", True, False), ("b", True, False)])

    def test_start_failure_is_infra(self):
        out = self.worker("--die").request({"op": "eval", "items": [{"key": "a"}]})
        self.assertTrue(out["infra"])
        self.assertIn("启动失败", out["error"])

    def test_real_worker(self):
        w = runner.CondWorker()
        self.addCleanup(w.stop)
        self.assertTrue(w.request({"op": "check", "code": RSI})["ok"])
        loop = script("while True:\n    pass\n")
        out = w.request({"op": "eval", "budgetMs": 50, "items": [{"key": 9, "code": loop, "now": 30 * STEP,
                                                                   "bars": {AAVE: rows([1] * 30)}}]})
        self.assertIn("预算", out["items"][0]["error"])


if __name__ == "__main__":
    unittest.main()
