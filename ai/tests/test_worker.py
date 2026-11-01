import asyncio
import json
import os
import shutil
import sys
import tempfile
import time
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import llm  # noqa: E402
import worker  # noqa: E402

PROXY = "http://127.0.0.1:17080"
KEY = "sk-ant-api03-SECRETSECRETSECRET"
CONFIG = {"type": "config", "credential": {"kind": "api_key", "value": KEY}, "model": "claude-sonnet-5"}


class FakeCalls:
    """Replaces llm: records calls, can block, fail or count concurrency."""

    def __init__(self):
        self.delay = 0.0
        self.fail: Exception | None = None
        self.active = 0
        self.max_active = 0
        self.seen = []
        self.cancelled = 0

    async def _enter(self, what, *args):
        self.seen.append((what, args))
        self.active += 1
        self.max_active = max(self.max_active, self.active)
        try:
            if self.delay:
                await asyncio.sleep(self.delay)
            if self.fail:
                raise self.fail
        except asyncio.CancelledError:
            self.cancelled += 1
            raise
        finally:
            self.active -= 1

    async def review(self, request, config, proxy):
        await self._enter("review", request, config, proxy)
        return ({"action": "skip", "sizeScale": 0, "confidence": 0.6, "rationale": "r", "model": config["model"], "costUsd": 0.01,
                 "latencyMs": 5}, {"promptVersion": "review_v1"})

    async def reflect(self, request, config, proxy):
        await self._enter("reflect", request, config, proxy)
        return {"memory": "m", "promptVersion": "reflect_v1"}

    async def test(self, config, proxy):
        await self._enter("test", config, proxy)
        return {"reply": "OK"}


def gate(ok=True, proxy=PROXY):
    def check(p, timeout):
        if not ok:
            raise RuntimeError("proxy check: HTTP 502")
    return worker.ProxyGate(proxy, check=check, ok_interval=0.05, fail_interval=0.05)


class Harness:
    """A fake Go side: a Unix socket server the worker connects to."""

    def __init__(self, g: worker.ProxyGate, calls: FakeCalls):
        self.dir = tempfile.mkdtemp(prefix="ctgai")
        self.path = os.path.join(self.dir, "ai.sock")
        self.conns: asyncio.Queue = asyncio.Queue()
        self.g, self.calls = g, calls
        self.worker = worker.Worker(self.path, g, calls=calls)

    async def __aenter__(self):
        self.server = await asyncio.start_unix_server(lambda r, w: self.conns.put_nowait((r, w)), self.path)
        self.task = asyncio.ensure_future(self.worker.run_forever())
        self.r, self.w = await asyncio.wait_for(self.conns.get(), 5)
        return self

    async def __aexit__(self, *exc):
        self.task.cancel()
        self.w.close()
        self.server.close()
        shutil.rmtree(self.dir, ignore_errors=True)

    def send(self, msg):
        self.w.write((json.dumps(msg) + "\n").encode())

    async def recv(self, timeout=5.0):
        line = await asyncio.wait_for(self.r.readline(), timeout)
        if not line:
            raise EOFError
        return json.loads(line)

    async def recv_type(self, kind, timeout=5.0):
        while True:
            m = await self.recv(timeout)
            if m.get("type") == kind:
                return m


def arun(coro):
    return asyncio.run(asyncio.wait_for(coro, 20))


class WorkerProtocol(unittest.TestCase):
    def test_review_round_trip(self):
        async def t():
            calls = FakeCalls()
            g = gate()
            await g.check_now()
            async with Harness(g, calls) as h:
                hello = await h.recv()
                self.assertEqual(hello["type"], "hello")
                self.assertTrue(hello["ready"])
                self.assertTrue(hello["proxyOk"])
                h.send(CONFIG)
                h.send({"type": "ping"})
                self.assertEqual((await h.recv())["type"], "pong")
                h.send({"type": "review", "id": "r1", "request": {"signal": {"instId": "BTC"}}, "deadlineMs": int(time.time() * 1000) + 10_000})
                m = await h.recv()
                self.assertEqual(m["type"], "decision")
                self.assertEqual(m["id"], "r1")
                self.assertEqual(m["decision"]["action"], "skip")
                self.assertEqual(m["meta"]["promptVersion"], "review_v1")
                what, (req, cfg, proxy) = calls.seen[0]
                self.assertEqual(proxy, PROXY)
                self.assertEqual(cfg["credential"]["value"], KEY)
                h.send({"type": "test", "id": "t1"})
                self.assertEqual(await h.recv(), {"id": "t1", "type": "result", "result": {"reply": "OK"}})
                h.send({"type": "reflect", "id": "f1", "request": {"strategy": "s"}})
                self.assertEqual((await h.recv())["result"]["memory"], "m")
        arun(t())

    def test_missing_proxy_refuses(self):
        async def t():
            calls = FakeCalls()
            g = gate(proxy="")
            await g.check_now()
            async with Harness(g, calls) as h:
                hello = await h.recv()
                self.assertFalse(hello["ready"])
                self.assertFalse(hello["proxyOk"])
                self.assertIn("CTG_AI_PROXY", hello["proxyError"])
                h.send(CONFIG)
                for kind in ("review", "test", "reflect"):
                    h.send({"type": kind, "id": kind})
                    m = await h.recv()
                    self.assertEqual(m, {"type": "error", "id": kind, "error": "no proxy configured"})
                self.assertEqual(calls.seen, [])
        arun(t())

    def test_failing_health_check_not_ready_then_recovers(self):
        async def t():
            calls = FakeCalls()
            state = {"ok": False}

            def check(p, timeout):
                if not state["ok"]:
                    raise RuntimeError("proxy check: HTTP 502")
            g = worker.ProxyGate(PROXY, check=check, ok_interval=0.05, fail_interval=0.05)
            async with Harness(g, calls) as h:
                hello = await h.recv()
                self.assertFalse(hello["ready"])
                runner = asyncio.ensure_future(g.run())
                st = await h.recv_type("status")
                self.assertEqual(st, {"type": "status", "ready": False, "proxyOk": False, "proxyError": "proxy check: HTTP 502"})
                h.send(CONFIG)
                h.send({"type": "review", "id": "r1"})
                m = await h.recv_type("error")
                self.assertIn("proxy not healthy", m["error"])
                state["ok"] = True
                st = await h.recv_type("status")
                self.assertEqual(st, {"type": "status", "ready": True, "proxyOk": True, "proxyError": ""})
                h.send({"type": "review", "id": "r2"})
                self.assertEqual((await h.recv_type("decision"))["id"], "r2")
                runner.cancel()
        arun(t())

    def test_no_credential_refuses(self):
        async def t():
            g = gate()
            await g.check_now()
            async with Harness(g, FakeCalls()) as h:
                await h.recv()
                h.send({"type": "config", "credential": None, "model": "m"})
                h.send({"type": "review", "id": "r"})
                self.assertEqual((await h.recv())["error"], "no credential configured")
        arun(t())

    def test_concurrency_cancel_timeout(self):
        async def t():
            calls = FakeCalls()
            calls.delay = 0.3
            g = gate()
            await g.check_now()
            async with Harness(g, calls) as h:
                await h.recv()
                h.send(CONFIG)
                for i in range(6):
                    h.send({"type": "review", "id": f"r{i}"})
                ids = {(await h.recv())["id"] for _ in range(6)}
                self.assertEqual(ids, {f"r{i}" for i in range(6)})
                self.assertEqual(calls.max_active, worker.MAX_INFLIGHT)

                # cancel: no answer, the call is cancelled
                calls.delay = 5
                h.send({"type": "review", "id": "c1"})
                await asyncio.sleep(0.1)
                h.send({"type": "cancel", "id": "c1"})
                await asyncio.sleep(0.1)
                self.assertEqual(calls.cancelled, 1)
                # deadline in the past → at least 1 s, then timeout
                h.send({"type": "review", "id": "d1", "deadlineMs": int(time.time() * 1000) - 5000})
                m = await h.recv(timeout=5)
                self.assertEqual(m, {"type": "error", "id": "d1", "error": "timeout"})
        arun(t())

    def test_errors_are_scrubbed_and_network_errors_recheck_proxy(self):
        async def t():
            calls = FakeCalls()
            calls.fail = llm.CallError("401 invalid key " + KEY + " via proxy", network=True)
            checks = []
            g = worker.ProxyGate(PROXY, check=lambda p, t: checks.append(p), ok_interval=60, fail_interval=60)
            async with Harness(g, calls) as h:
                await h.recv()
                runner = asyncio.ensure_future(g.run())
                await h.recv_type("status")  # became ready
                h.send(CONFIG)
                h.send({"type": "review", "id": "e1"})
                m = await h.recv_type("error")
                self.assertNotIn(KEY, m["error"])
                self.assertIn("***", m["error"])
                await asyncio.sleep(0.2)
                self.assertGreaterEqual(len(checks), 2, "a network error must trigger an immediate proxy re-check")
                runner.cancel()
        arun(t())

    def test_reconnects(self):
        async def t():
            g = gate()
            await g.check_now()
            async with Harness(g, FakeCalls()) as h:
                await h.recv()
                h.w.close()
                r2, w2 = await asyncio.wait_for(h.conns.get(), 5)
                line = await asyncio.wait_for(r2.readline(), 5)
                self.assertEqual(json.loads(line)["type"], "hello")
                w2.close()
        arun(t())


class ProxyCheck(unittest.TestCase):
    def test_hello_uses_proxy_handler(self):
        import urllib.request
        seen = {}

        class Resp:
            status = 200

            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

        class Opener:
            def open(self, url, timeout):
                seen["url"], seen["timeout"] = url, timeout
                return Resp()

        orig = urllib.request.build_opener

        def build(*handlers):
            seen["proxies"] = handlers[0].proxies
            return Opener()
        urllib.request.build_opener = build
        try:
            worker.hello_through_proxy(PROXY)
        finally:
            urllib.request.build_opener = orig
        self.assertEqual(seen["url"], "https://api.anthropic.com/api/hello")
        self.assertEqual(seen["proxies"], {"http": PROXY, "https": PROXY})
        self.assertEqual(seen["timeout"], 10.0)

    def test_non_200_fails(self):
        import urllib.request

        class Resp:
            status = 204

            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

        class Opener:
            def open(self, url, timeout):
                return Resp()
        orig = urllib.request.build_opener
        urllib.request.build_opener = lambda *h: Opener()
        try:
            with self.assertRaisesRegex(RuntimeError, "HTTP 204"):
                worker.hello_through_proxy(PROXY)
        finally:
            urllib.request.build_opener = orig


if __name__ == "__main__":
    unittest.main()


class SignalToolLabels(unittest.TestCase):
    def test_signal_tools_allowed_and_labelled(self):
        import agent

        for t in ("subscribe_signal", "list_signals", "unsubscribe_signal"):
            self.assertIn(t, agent.CTG_DIRECT)
        self.assertEqual(agent.label_for("mcp__ctg__subscribe_signal"), "订阅信号")
        self.assertEqual(agent.label_for("mcp__ctg__unsubscribe_signal"), "取消订阅")
        self.assertEqual(agent.label_for("mcp__ctg__list_signals"), "查看信号")
        spec = {"type": "any", "of": [{"type": "zone", "instId": "AAVE-USDT-SWAP", "lo": 139.5, "hi": 140.2},
                                      {"type": "price", "instId": "AAVE-USDT-SWAP", "op": "above", "level": 140.2, "bar": "1H"}]}
        self.assertEqual(agent.brief_input("mcp__ctg__subscribe_signal", {"spec": spec}), "AAVE 139.5–140.2 或 AAVE ≥ 140.2 (1H 收盘)")
        self.assertEqual(agent.brief_input("mcp__ctg__subscribe_signal", {"spec": json.dumps(spec["of"][0])}), "AAVE 139.5–140.2")
        self.assertEqual(agent.brief_input("mcp__ctg__unsubscribe_signal", {"id": 12, "reason": "x"}), "#12")

    def test_script_signal_labels(self):
        import agent

        self.assertIn("test_signal_script", agent.CTG_DIRECT)
        self.assertEqual(agent.label_for("mcp__ctg__test_signal_script"), "测试信号脚本")
        code = 'from ctg import Condition, Fire, ta\n\nclass S(Condition):\n    """AAVE 15m RSI 收回 30"""\n    timeframe = "15m"\n'
        self.assertEqual(agent.brief_input("mcp__ctg__test_signal_script", {"code": code}), "AAVE 15m RSI 收回 30")
        self.assertEqual(agent.brief_input("mcp__ctg__test_signal_script", {"code": "x = 1"}), "脚本")
        self.assertEqual(agent.brief_input("mcp__ctg__subscribe_signal", {"spec": {"type": "script"}, "code": code}), "脚本：AAVE 15m RSI 收回 30")
        self.assertEqual(agent.brief_input("mcp__ctg__subscribe_signal", {"code": code}), "脚本：AAVE 15m RSI 收回 30")
        spec = {"type": "all", "of": [{"type": "script"}, {"type": "zone", "instId": "AAVE-USDT-SWAP", "lo": 1, "hi": 2}]}
        self.assertEqual(agent.brief_input("mcp__ctg__subscribe_signal", {"spec": spec, "code": code}), "脚本：AAVE 15m RSI 收回 30 且 AAVE 1–2")
