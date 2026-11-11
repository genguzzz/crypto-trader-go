"""ctg AI worker: serves review / reflect / test calls for internal/aigate over a Unix socket.

Protocol: NDJSON over $CTG_RUN_DIR/ai.sock (docs/trading-design.md §5 plus the additive messages
documented in internal/aigate/worker.go). The worker connects (reconnecting with backoff), says
hello, receives the credential and model in ``config`` messages and handles up to MAX_INFLIGHT
calls concurrently. Every Anthropic request goes through the proxy in CTG_AI_PROXY; without a
working proxy the worker reports itself not ready and refuses all calls (never direct).
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
import sys
import time
import urllib.error
import urllib.request
from typing import Any, Awaitable, Callable

import llm

PROTOCOL = "1"
MAX_INFLIGHT = 3
MAX_CHATS = 2  # chat turns run long; they get their own slots so reviews are never queued behind them
MAX_LINE = 16 << 20
HELLO_URL = "https://api.anthropic.com/api/hello"
PROXY_OK_INTERVAL = 60.0
PROXY_FAIL_INTERVAL = 10.0
IDLE_TIMEOUT = 60.0  # Go pings every 15 s; silence for this long means a dead connection

log = logging.getLogger("ctg-ai")


def sdk_versions() -> tuple[str, str]:
    try:
        import claude_agent_sdk
        from claude_agent_sdk._cli_version import __cli_version__
        return getattr(claude_agent_sdk, "__version__", ""), __cli_version__
    except Exception:
        return "", ""


def scrub(msg: str, *secrets: str) -> str:
    for s in secrets:
        if s and len(s) >= 8:
            msg = msg.replace(s, "***")
    return msg[:1000]


# ---------- proxy health ----------


def hello_through_proxy(proxy: str, timeout: float = 10.0) -> None:
    """GET https://api.anthropic.com/api/hello through the proxy; raises unless HTTP 200."""
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))
    try:
        with opener.open(HELLO_URL, timeout=timeout) as resp:
            if resp.status != 200:
                raise RuntimeError(f"proxy check: HTTP {resp.status}")
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"proxy check: HTTP {e.code}") from e
    except urllib.error.URLError as e:
        raise RuntimeError(f"proxy check: {e.reason}") from e


class ProxyGate:
    """Tracks whether the configured proxy reaches the Anthropic API.

    Checked before declaring ready, every 60 s while healthy, every 10 s while failing, and right
    after any call that failed with a connection-like error.
    """

    def __init__(self, proxy: str, check: Callable[[str, float], None] = hello_through_proxy,
                 ok_interval: float = PROXY_OK_INTERVAL, fail_interval: float = PROXY_FAIL_INTERVAL, timeout: float = 10.0):
        self.proxy = (proxy or "").strip()
        self.check = check
        self.ok_interval, self.fail_interval, self.timeout = ok_interval, fail_interval, timeout
        self.ok = False
        self.error = "" if self.proxy else "no proxy configured (set CTG_AI_PROXY)"
        self.on_change: Callable[[], None] | None = None
        self._wake = asyncio.Event()

    @property
    def configured(self) -> bool:
        return bool(self.proxy)

    @property
    def ready(self) -> bool:
        return self.configured and self.ok

    async def check_now(self) -> bool:
        if not self.configured:
            return False
        try:
            await asyncio.get_running_loop().run_in_executor(None, self.check, self.proxy, self.timeout)
            ok, err = True, ""
        except Exception as e:  # noqa: BLE001 - any failure means not ready
            ok, err = False, str(e) or type(e).__name__
        changed = ok != self.ok or err != self.error
        self.ok, self.error = ok, err
        if changed:
            if ok:
                log.info("proxy check ok")
            else:
                log.error("proxy check failed: %s", err)
            if self.on_change:
                self.on_change()
        return ok

    def recheck(self) -> None:
        self._wake.set()

    async def run(self) -> None:
        if not self.configured:
            log.error("CTG_AI_PROXY is not set: refusing all calls (no direct connections)")
            return
        while True:
            await self.check_now()
            self._wake.clear()
            try:
                await asyncio.wait_for(self._wake.wait(), self.ok_interval if self.ok else self.fail_interval)
            except asyncio.TimeoutError:
                pass


# ---------- worker ----------


class Worker:
    def __init__(self, socket_path: str, gate: ProxyGate, calls: Any = llm, max_inflight: int = MAX_INFLIGHT):
        self.socket_path = socket_path
        self.gate = gate
        self.calls = calls  # module-like: review, reflect, test, chat coroutines
        self.config: dict[str, Any] = {}
        self.sem = asyncio.Semaphore(max_inflight)
        self.chat_sem = asyncio.Semaphore(MAX_CHATS)
        self.tasks: dict[str, asyncio.Task] = {}
        self.inboxes: dict[str, Any] = {}  # running chat calls: their steer inbox (agent.Inbox)
        self.writer: asyncio.StreamWriter | None = None
        self.wlock = asyncio.Lock()
        self.sdk, self.cli = sdk_versions()
        gate.on_change = self._status_changed

    # -- io --

    async def send(self, msg: dict) -> None:
        w = self.writer
        if w is None:
            return
        data = (json.dumps(msg, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
        async with self.wlock:
            try:
                w.write(data)
                await w.drain()
            except (ConnectionError, RuntimeError):
                pass

    def status_fields(self) -> dict[str, Any]:
        return {"ready": self.gate.ready, "proxyOk": self.gate.ok, "proxyError": self.gate.error}

    def _status_changed(self) -> None:
        if self.writer is not None:
            asyncio.ensure_future(self.send({"type": "status", **self.status_fields()}))

    async def run_forever(self) -> None:
        backoff = 0.5
        while True:
            try:
                reader, writer = await asyncio.open_unix_connection(self.socket_path, limit=MAX_LINE)
            except (FileNotFoundError, ConnectionRefusedError, OSError) as e:
                log.info("connect %s: %s (retry in %.1fs)", self.socket_path, e, backoff)
                await asyncio.sleep(backoff)
                backoff = min(backoff * 2, 10.0)
                continue
            backoff = 0.5
            log.info("connected to %s", self.socket_path)
            try:
                await self.session(reader, writer)
            except Exception as e:  # noqa: BLE001 - reconnect on any session failure
                log.warning("session ended: %s", e)
            finally:
                self.writer = None
                for t in list(self.tasks.values()):
                    t.cancel()
                self.tasks.clear()
                writer.close()
            await asyncio.sleep(backoff)

    async def session(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        self.writer = writer
        await self.send({"type": "hello", "worker": PROTOCOL, "sdk": self.sdk, "cli": self.cli, **self.status_fields()})
        while True:
            line = await asyncio.wait_for(reader.readline(), IDLE_TIMEOUT)
            if not line:
                return
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            await self.dispatch(msg)

    # -- messages --

    async def dispatch(self, msg: dict) -> None:
        kind = msg.get("type")
        if kind == "ping":
            await self.send({"type": "pong"})
        elif kind == "config":
            self.config = {"credential": msg.get("credential"), "model": msg.get("model") or llm.DEFAULT_MODEL,
                           "effort": msg.get("effort"), "agent": msg.get("agent") or {}}
            cred = self.config["credential"] or {}
            log.info("config: model %s, credential %s", self.config["model"], cred.get("kind") or "none")
        elif kind == "cancel":
            t = self.tasks.pop(str(msg.get("id")), None)
            if t:
                t.cancel()
        elif kind == "steer":
            # a further message for a running chat call: answered in it, or refused so Go retries it later
            cid = str(msg.get("id") or "")
            inbox = self.inboxes.get(str(msg.get("callId") or ""))
            if cid and inbox is not None and inbox.put(str(msg.get("message") or "")):
                await self.send({"type": "result", "id": cid, "result": {"ok": True}})
            elif cid:
                await self.send({"type": "error", "id": cid, "error": "the turn is not running or is ending"})
        elif kind in ("review", "reflect", "test", "chat"):
            cid = str(msg.get("id") or "")
            if not cid:
                return
            refusal = self.refusal()
            if refusal:
                await self.send({"type": "error", "id": cid, "error": refusal})
                return
            task = asyncio.ensure_future(self.handle(kind, cid, msg))
            self.tasks[cid] = task
            task.add_done_callback(lambda _t, cid=cid: self.tasks.pop(cid, None))

    def refusal(self) -> str:
        if not self.gate.configured:
            return "no proxy configured"
        if not self.gate.ok:
            return "proxy not healthy: " + (self.gate.error or "not checked yet")
        cred = self.config.get("credential") or {}
        if not cred.get("value"):
            return "no credential configured"
        return ""

    def timeout_for(self, msg: dict, default: float) -> float:
        dl = msg.get("deadlineMs")
        if isinstance(dl, (int, float)) and dl > 0:
            return max(1.0, dl / 1000 - time.time() - 0.5)
        return default

    async def handle(self, kind: str, cid: str, msg: dict) -> None:
        config = dict(self.config)  # snapshot: a config change does not affect a running call
        secret = (config.get("credential") or {}).get("value", "")
        timeout = self.timeout_for(msg, {"reflect": 300.0, "chat": 600.0}.get(kind, 90.0))
        try:
            out = await asyncio.wait_for(self._run(kind, msg, config), timeout)
        except asyncio.CancelledError:
            return  # Go cancelled (deadline passed) or the connection dropped; nobody waits for an answer
        except asyncio.TimeoutError:
            await self.send({"type": "error", "id": cid, "error": "timeout"})
            return
        except Exception as e:  # noqa: BLE001 - report every failure to Go
            if getattr(e, "network", False):
                self.gate.recheck()
            await self.send({"type": "error", "id": cid, "error": scrub(str(e) or type(e).__name__, secret)})
            return
        await self.send({"id": cid, **out})

    async def _run(self, kind: str, msg: dict, config: dict) -> dict:
        if kind == "chat":
            cid = str(msg.get("id"))

            async def emit(ev: dict) -> None:
                await self.send({"type": "event", "id": cid, "event": ev})

            import agent  # lazy: agent imports llm

            inbox = agent.Inbox()
            self.inboxes[cid] = inbox
            try:
                async with self.chat_sem:
                    return {"type": "result", "result": await self.calls.chat(msg, config, self.gate.proxy, emit, inbox)}
            finally:
                inbox.close()
                self.inboxes.pop(cid, None)
        async with self.sem:
            proxy = self.gate.proxy
            if kind == "review":
                decision, meta = await self.calls.review(msg.get("request") or {}, config, proxy)
                return {"type": "decision", "decision": decision, "meta": meta}
            if kind == "reflect":
                return {"type": "result", "result": await self.calls.reflect(msg.get("request") or {}, config, proxy)}
            return {"type": "result", "result": await self.calls.test(config, proxy)}


def prepare_environment(proxy: str) -> None:
    """Process-wide: drop inherited credentials, route everything through the proxy, writable HOME."""
    for k in llm.STRIPPED_ENV:
        if os.environ.pop(k, None) is not None:
            log.warning("ignoring %s from the environment (credentials come from ctg only)", k)
    if proxy:
        os.environ.update(llm.proxy_env(proxy))
    else:
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY", "all_proxy"):
            os.environ.pop(k, None)
    home = os.environ.get("HOME", "")
    if home:
        try:
            os.makedirs(home, exist_ok=True)
        except OSError:
            pass
    if not home or not os.access(home, os.W_OK):
        os.environ["HOME"] = "/tmp"


async def amain() -> None:
    run_dir = os.environ.get("CTG_RUN_DIR") or os.path.join("data", "run")
    sock = os.environ.get("CTG_AI_SOCKET") or os.path.join(run_dir, "ai.sock")
    proxy = os.environ.get("CTG_AI_PROXY", "").strip()
    prepare_environment(proxy)
    gate = ProxyGate(proxy)
    worker = Worker(sock, gate)
    log.info("ctg-ai worker %s (sdk %s, cli %s), socket %s, proxy %s", PROTOCOL, worker.sdk or "?", worker.cli or "?", sock,
             "configured" if proxy else "MISSING")
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for s in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(s, stop.set)
        except NotImplementedError:
            pass
    tasks = [asyncio.ensure_future(gate.run()), asyncio.ensure_future(worker.run_forever())]
    await stop.wait()
    for t in tasks:
        t.cancel()


def main() -> None:
    logging.basicConfig(level=os.environ.get("CTG_AI_LOG", "INFO"), stream=sys.stderr,
                        format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    asyncio.run(amain())


if __name__ == "__main__":
    main()
