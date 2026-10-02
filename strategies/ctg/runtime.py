"""Per-instance protocol loop (docs/trading-design.md §2)."""

from __future__ import annotations

import json
import math
import sys
import traceback
import types
from typing import Any

from . import ta
from .context import Bar, Context, record
from .signal import Signal, SignalError
from .strategy import Strategy
from .wire import Conn, ProtocolError

SDK_VERSION = "1"
MAX_STATE = 64 << 10


def load_classes(code: str, file: str) -> list[type[Strategy]]:
    """Execute strategy source as a fresh module and return its Strategy subclasses."""
    modname = "strategy_" + "".join(c if c.isalnum() else "_" for c in file)
    mod = types.ModuleType(modname)
    mod.__file__ = file
    sys.modules[modname] = mod
    exec(compile(code, file, "exec"), mod.__dict__)
    return [v for v in vars(mod).values()
            if isinstance(v, type) and issubclass(v, Strategy) and v is not Strategy and v.__module__ == modname]


def pick_class(classes: list[type[Strategy]], class_name: str = "", name: str = "") -> type[Strategy]:
    for c in classes:
        if class_name and c.__name__ == class_name:
            return c
    for c in classes:
        if name and c.strategy_name() == name:
            return c
    if len(classes) == 1:
        return classes[0]
    raise LookupError(f"strategy class not found (class={class_name!r} name={name!r}, have "
                      f"{[c.__name__ for c in classes]})")


class Runtime:
    def __init__(self, conn: Conn, cls: type[Strategy]):
        self.conn = conn
        self.cls = cls
        self.strategy: Strategy | None = None
        self.ctx = Context(self)
        self.ctx.timeframe = cls.timeframe
        self.qid = 0
        self.bar: Bar | None = None
        self.sent_state = ""

    # ---- outgoing ----
    def log(self, level: str, msg: str) -> None:
        self.conn.send({"type": "log", "level": level, "msg": msg[:4000]})

    def _call(self, msg: dict) -> dict:
        self.qid += 1
        msg["qid"] = self.qid
        self.conn.send(msg)
        reply = self.conn.recv()
        if reply is None:
            raise SystemExit(0)
        if reply.get("qid") != self.qid:
            raise ProtocolError(f"expected reply to {self.qid}, got {reply}")
        return reply

    def query(self, method: str, args: dict) -> Any:
        r = self._call({"type": "query", "method": method, "args": args})
        if r.get("type") == "error":
            raise RuntimeError(f"query {method}: {r.get('error')}")
        return r.get("data")

    def submit(self, sig: Signal) -> int | None:
        try:
            wire = self._resolve(sig)
        except SignalError as e:
            self.log("warn", f"signal {sig.action} {sig.inst} dropped: {e}")
            return None
        r = self._call({"type": "signal", **wire})
        if r.get("type") != "signal_ack":
            self.log("warn", f"signal {sig.action} {sig.inst} refused: {r.get('error')}")
            return None
        return r.get("id")

    def _resolve(self, sig: Signal) -> dict:
        entry = atr = None
        if sig.needs_atr():
            b = self.ctx.bars(sig.inst, self.cls.timeframe, 100)
            if len(b):
                entry = float(b.c[-1])
                atr = float(ta.atr(b.h, b.l, b.c, 14)[-1])
            if self.bar is not None and self.bar.inst == sig.inst and math.isfinite(self.bar.c):
                entry = self.bar.c
        return sig.to_wire(entry, atr)

    def _emit(self, out: Any) -> None:
        if out is None:
            return
        sigs = out if isinstance(out, (list, tuple)) else [out]
        for s in sigs:
            if isinstance(s, Signal):
                self.submit(s)
            elif s is not None:
                self.log("warn", f"handler returned {type(s).__name__}, expected Signal")

    def _flush_state(self) -> None:
        try:
            data = json.dumps(self.ctx.state, sort_keys=True, separators=(",", ":"), allow_nan=False)
        except (TypeError, ValueError) as e:
            self.log("error", f"ctx.state not saved: {e}")
            return
        if data == self.sent_state:
            return
        if len(data) > MAX_STATE:
            self.log("error", f"ctx.state not saved: {len(data)} bytes > {MAX_STATE}")
            return
        self.conn.send({"type": "state", "data": json.loads(data)})
        self.sent_state = data

    # ---- incoming ----
    def _dispatch(self, msg: dict) -> Any:
        t = msg.get("type")
        ctx, st = self.ctx, self.strategy
        if t == "init":
            inst = msg.get("instance") or {}
            st = self.strategy = self.cls(inst.get("params") or {})
            ctx.instance_id = int(inst.get("id") or 0)
            ctx.mode = msg.get("mode") or "live"
            ctx.params = {k: getattr(st, k) for k in self.cls.param_specs()}
            ctx.universe = list((inst.get("universe") or {}).get("static") or [])
            state = msg.get("state")
            ctx.state = state if isinstance(state, dict) else {}
            self.sent_state = json.dumps(ctx.state, sort_keys=True, separators=(",", ":"))
            return st.on_start(ctx)
        if st is None:
            raise ProtocolError(f"{t} before init")
        if t == "bar":
            self.bar = Bar(msg.get("instId", ""), msg.get("tf", ""), msg.get("bar") or {})
            return st.on_bar(ctx, self.bar)
        if t == "universe":
            ctx.universe = list(msg.get("current") or [])
            return st.on_universe_change(ctx, list(msg.get("added") or []), list(msg.get("removed") or []))
        if t == "fill":
            return st.on_fill(ctx, record(msg.get("fill") or {}))
        if t == "signal_result":
            return st.on_signal_result(ctx, record({k: msg.get(k) for k in ("signalId", "outcome", "reason")}))
        if t == "funding":
            return st.on_funding(ctx, msg.get("instId", ""), _num(msg.get("rate")), _num(msg.get("amount")))
        if t == "stop":
            return st.on_stop(ctx)
        self.log("warn", f"unknown event {t!r}")
        return None

    def handle(self, msg: dict) -> str | None:
        """Run one event; returns the handler error (already logged) or None."""
        self.ctx.now = int(msg.get("now") or 0)
        self.ctx._cache.clear()
        self.bar = None
        err = None
        try:
            self._emit(self._dispatch(msg))
        except (ProtocolError, SystemExit, OSError):
            raise
        except Exception:
            err = traceback.format_exc(limit=8)[-4000:]
            self.log("error", f"{msg.get('type')} handler raised:\n{err}")
        self._flush_state()
        return err

    def run(self) -> int:
        while True:
            msg = self.conn.recv()
            if msg is None:
                return 0
            if msg.get("type") == "error":
                print(f"host refused: {msg.get('error')}", file=sys.stderr)
                return 3
            seq = msg.get("seq")
            if seq is None:
                raise ProtocolError(f"unexpected message {msg.get('type')!r}")
            err = self.handle(msg)
            done = {"type": "done", "seq": seq}
            if err:
                done["error"] = err.strip().splitlines()[-1][:500]
            self.conn.send(done)
            if msg.get("type") == "stop":
                return 0


def _num(v: Any) -> float:
    return math.nan if v is None else float(v)


def run_instance(conn: Conn, cls: type[Strategy], instance: int, token: str) -> int:
    conn.send({"type": "hello", "instance": instance, "token": token, "sdk": SDK_VERSION})
    try:
        return Runtime(conn, cls).run()
    finally:
        conn.close()
