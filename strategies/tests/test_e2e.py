"""End-to-end: real instance.py / runner.py processes against a tiny fake Go host."""

import json
import os
import socket
import subprocess
import sys
import tempfile
import unittest

import numpy as np

from ctg import ta
from ctg.describe import describe
from ctg.wire import Conn

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOKEN = "t0k3n"
H = 4 * 3600_000


def make_bars(fast=5, slow=20):
    """4H bars ending exactly on the bar where EMA(fast) crosses above EMA(slow)."""
    closes = [100 - 0.5 * i for i in range(30)] + [85 + 2.0 * j for j in range(30)]
    c = np.array(closes)
    k = int(np.argmax(ta.crossover(ta.ema(c, fast), ta.ema(c, slow))))
    assert k > 0
    return [[i * H, x, x + 1, x - 1, x, 10, 10 * x] for i, x in enumerate(closes[:k + 1])]


class FakeHost:
    """Serves one Unix socket like internal/strategyhost, one connection at a time."""

    def __init__(self, bars):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "s.sock")
        self.srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.srv.bind(self.path)
        self.srv.listen(4)
        self.srv.settimeout(20)
        self.bars = bars
        self.signals = []
        self.logs = []
        self.states = []
        self.position = None
        self.conns = []

    def accept(self):
        c, _ = self.srv.accept()
        c.settimeout(20)
        self.conns.append(Conn(c))
        return self.conns[-1]

    def event(self, conn, msg):
        """Send one event, answer queries until done; returns the done message."""
        conn.send(msg)
        while True:
            m = conn.recv()
            assert m is not None, "connection closed"
            t = m["type"]
            if t == "done":
                assert m["seq"] == msg["seq"]
                return m
            if t == "log":
                self.logs.append(m)
            elif t == "state":
                self.states.append(m["data"])
            elif t == "signal":
                self.signals.append(m)
                conn.send({"type": "signal_ack", "qid": m["qid"], "id": len(self.signals)})
            elif t == "query":
                a = m.get("args") or {}
                if m["method"] == "bars":
                    data = self.bars[-a["n"]:]
                elif m["method"] == "position":
                    data = self.position
                else:
                    conn.send({"type": "error", "qid": m["qid"], "error": "unsupported"})
                    continue
                conn.send({"type": "result", "qid": m["qid"], "data": data})
            else:
                raise AssertionError(f"unexpected {m}")

    def close(self):
        for c in self.conns:
            c.close()
        self.srv.close()


def env(path, **extra):
    e = dict(os.environ, CTG_SOCKET=path, CTG_INSTANCE="12", CTG_TOKEN=TOKEN, PYTHONPATH=ROOT,
             OPENBLAS_NUM_THREADS="1")
    e.update(extra)
    return e


def init_msg(params, state=None):
    return {"type": "init", "seq": 0, "now": 0, "mode": "backtest", "state": state or {},
            "instance": {"id": 12, "accountId": 1, "params": params,
                         "universe": {"static": ["BTC-USDT-SWAP"]}},
            "strategy": {"name": "x", "timeframe": "4H"}}


class InstanceTest(unittest.TestCase):
    def start(self, host, **extra):
        p = subprocess.Popen([sys.executable, os.path.join(ROOT, "instance.py")], env=env(host.path, **extra),
                             stdin=subprocess.PIPE, stderr=subprocess.PIPE, cwd=ROOT)
        self.addCleanup(lambda: (p.kill(), p.wait(), p.stderr.close(), p.stdin.close()))
        return p

    def test_majors_trend_round_trip(self):
        bars = make_bars()
        host = FakeHost(bars)
        self.addCleanup(host.close)
        p = self.start(host, CTG_FILE="library/majors_trend.py", CTG_CLASS="MajorsTrend")
        conn = host.accept()
        hello = conn.recv()
        self.assertEqual(hello, {"type": "hello", "instance": 12, "token": TOKEN, "sdk": "1"})

        done = host.event(conn, init_msg({"fast": 5, "slow": 20}))
        self.assertNotIn("error", done)
        self.assertIn("started fast=5 slow=20", host.logs[0]["msg"])
        self.assertEqual(host.states[-1], {"reenter": {}})

        last = bars[-1]
        bar = {"ts": last[0], "o": last[1], "h": last[2], "l": last[3], "c": last[4], "vol": 10, "volq": 1}
        done = host.event(conn, {"type": "bar", "seq": 1, "now": last[0] + H, "instId": "BTC-USDT-SWAP",
                                 "tf": "4H", "bar": bar})
        self.assertNotIn("error", done)
        self.assertEqual(len(host.signals), 1)
        s = host.signals[0]
        a = np.array(bars, dtype=float)
        atr = ta.atr(a[:, 2], a[:, 3], a[:, 4], 14)[-1]
        self.assertEqual(s["action"], "open_long")
        self.assertAlmostEqual(s["stopLoss"], last[4] - 2 * atr)
        self.assertAlmostEqual(s["takeProfit"], last[4] + 4 * atr)

        host.event(conn, {"type": "stop", "seq": 2, "now": 0})
        self.assertEqual(p.wait(timeout=10), 0)

    def test_handler_exception_is_acknowledged(self):
        code = (b"from ctg import Strategy\n"
                b"class Boom(Strategy):\n"
                b"    def on_bar(self, ctx, bar):\n"
                b"        if bar.c > 1: raise ZeroDivisionError('boom')\n"
                b"        ctx.state['ok'] = bar.ts\n")
        host = FakeHost([])
        self.addCleanup(host.close)
        p = self.start(host, CTG_FILE="library/boom.py", CTG_CODE_STDIN="1")
        p.stdin.write(code)
        p.stdin.close()
        conn = host.accept()
        conn.recv()
        host.event(conn, init_msg({}))
        bar = {"ts": 5, "o": 1, "h": 1, "l": 1, "c": 2, "vol": None, "volq": None}
        done = host.event(conn, {"type": "bar", "seq": 1, "now": 1, "instId": "X", "tf": "1H", "bar": bar})
        self.assertIn("ZeroDivisionError: boom", done["error"])
        self.assertTrue(any("Traceback" in m["msg"] for m in host.logs))
        bar["c"] = 1
        done = host.event(conn, {"type": "bar", "seq": 2, "now": 1, "instId": "X", "tf": "1H", "bar": bar})
        self.assertNotIn("error", done)
        self.assertEqual(host.states[-1], {"ok": 5})
        conn.close()  # host gone → the process exits on EOF
        self.assertEqual(p.wait(timeout=10), 0)

    def test_rejected_hello_exits(self):
        host = FakeHost([])
        self.addCleanup(host.close)
        p = self.start(host, CTG_FILE="library/majors_trend.py")
        conn = host.accept()
        conn.recv()
        conn.send({"type": "error", "error": "unauthorized"})
        self.assertEqual(p.wait(timeout=10), 3)


class RunnerTest(unittest.TestCase):
    def test_describe_spawn_exit(self):
        host = FakeHost(make_bars())
        self.addCleanup(host.close)
        p = subprocess.Popen([sys.executable, os.path.join(ROOT, "runner.py")], env=env(host.path),
                             stderr=subprocess.DEVNULL, cwd=ROOT)
        self.addCleanup(lambda: (p.kill(), p.wait()))
        sup = host.accept()
        self.assertEqual(sup.recv(), {"type": "supervise"})

        sup.send({"type": "describe", "id": 1})
        r = sup.recv()
        self.assertEqual((r["type"], r["id"]), ("describe_result", 1))
        names = {s["name"]: s for s in r["strategies"]}
        self.assertEqual(names["majors-trend"]["class"], "MajorsTrend")
        self.assertEqual(names["pool-reversal"]["universe"]["screener"]["rule"], "resid-reversal")

        sup.send({"type": "spawn", "instance": 12, "token": TOKEN, "file": "library/majors_trend.py",
                  "class": "MajorsTrend", "code": names["majors-trend"]["code"]})
        inst = host.accept()
        self.assertEqual(inst.recv()["token"], TOKEN)
        host.event(inst, init_msg({"fast": 5, "slow": 20}))
        host.event(inst, {"type": "stop", "seq": 1, "now": 0})
        ex = sup.recv()
        self.assertEqual((ex["type"], ex["instance"], ex["token"], ex["code"]), ("exited", 12, TOKEN, 0))

        sup.send({"type": "spawn", "instance": 13, "token": "x", "file": "../runner.py"})
        ex = sup.recv()
        while ex["type"] == "log":
            ex = sup.recv()
        self.assertEqual((ex["type"], ex["instance"], ex["code"]), ("exited", 13, -1))
        self.assertIn("outside", ex["stderr"])


class DescribeTest(unittest.TestCase):
    def test_library(self):
        got = {d["name"]: d for d in describe(ROOT)}
        self.assertNotIn("error", json.dumps(list(got)))
        mt = got["majors-trend"]
        self.assertEqual(mt["universe"], {"static": ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]})
        self.assertEqual(mt["timeframe"], "4H")
        self.assertTrue(all(p["aiTunable"] for p in mt["params"]))
        self.assertEqual(got["pool-reversal"]["timeframe"], "1H")


if __name__ == "__main__":
    unittest.main()
