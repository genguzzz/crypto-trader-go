"""Script conditions for signal subscriptions (docs/trading-design.md §8, "Script signals").

A condition is a small class the server runs on every close of its timeframe; the subscription fires
when check turns from False to True:

    from ctg import Condition, Fire, ta

    class AaveRsiDip(Condition):
        \"\"\"AAVE 15m RSI 跌破 30\"\"\"
        instruments = ["AAVE-USDT-SWAP"]
        timeframe = "15m"
        lookback = 100

        def check(self, ctx, bars):
            r = ta.rsi(bars["AAVE-USDT-SWAP"].c, 14)[-1]
            return Fire(r < 30, values={"rsi": r}, note=f"RSI {r:.1f}")

check returns True / False, a Fire (the same plus values and a note), or None when it cannot tell
(unknown: never fires). It must be a pure function of the bars it is given — nothing survives between
calls — so the replay over recent history that validates a script sees exactly what the live checks
will. Scripts run in the strategy container (no network, read-only filesystem) under a CPU budget per
call; the static checks below are a guard rail on top of that, not the sandbox.
"""

from __future__ import annotations

import ast
import builtins
import contextlib
import hashlib
import math
import re
import signal
import time
from typing import Any

import numpy as np

from .context import Bars

FILE = "signal.py"
TIMEFRAMES = {"1m": 60_000, "5m": 300_000, "15m": 900_000, "1H": 3_600_000, "4H": 14_400_000}
MAX_CODE = 8000  # bytes of source
MAX_INSTRUMENTS = 10
MIN_LOOKBACK, MAX_LOOKBACK, DEFAULT_LOOKBACK = 10, 500, 200
MAX_VALUES, MAX_NOTE = 12, 200
LOAD_BUDGET_S = 2.0  # module-level code (imports, class bodies)

ALLOWED_IMPORTS = ("ctg", "numpy", "math", "statistics", "collections", "itertools", "functools", "typing",
                   "__future__")
BLOCKED_NAMES = {"open", "exec", "eval", "compile", "__import__", "globals", "locals", "vars", "input", "breakpoint",
                 "getattr", "setattr", "delattr", "exit", "quit", "help", "memoryview", "__builtins__", "__loader__",
                 "__spec__"}
# numpy entry points that read or write files, or reach C.
NUMPY_BLOCKED = {"load", "save", "savez", "savez_compressed", "loadtxt", "savetxt", "genfromtxt", "fromfile", "tofile",
                 "fromregex", "memmap", "DataSource", "ctypeslib", "lib", "f2py", "distutils", "testing", "ctypes"}
INST_RE = re.compile(r"^[A-Z0-9]{1,20}-USDT-SWAP$")


class ScriptError(Exception):
    """The script cannot be loaded (static check, import or declaration error)."""


class ScriptTimeout(BaseException):
    """The CPU budget of one call ran out (a BaseException, so `except Exception` cannot swallow it)."""


class Fire:
    """What check returns when it has more to say than True / False.

    hit: True fires (on the change from False), False does not, None = cannot tell (unknown).
    values: up to 12 numbers shown with the signal ({"rsi": 28.1}); note: one line (≤ 200 chars)."""

    __slots__ = ("hit", "values", "note")

    def __init__(self, hit: Any, values: dict | None = None, note: str = ""):
        self.hit = None if hit is None else bool(hit)
        self.values = dict(values or {})
        self.note = str(note or "")

    def __repr__(self) -> str:
        return f"Fire({self.hit}, values={self.values!r}, note={self.note!r})"


class Condition:
    """Subclass it, declare the data and write check. Class attributes:

      instruments  1–10 instIds ("AAVE-USDT-SWAP"); bars has one entry per instrument
      timeframe    "1m" "5m" "15m" "1H" "4H": check runs on every confirmed close of this bar
      lookback     bars of history per instrument (10–500, default 200)
      description  one line shown with the signal (default: the docstring)"""

    name: str = ""
    description: str = ""
    instruments: list[str] = []
    timeframe: str = "15m"
    lookback: int = DEFAULT_LOOKBACK

    def check(self, ctx: "ConditionContext", bars: dict[str, Bars]) -> Any:
        """Called on every close of the timeframe with {instId: Bars} (oldest first, the newest bar just
        closed). Return True / False, Fire(hit, values, note) or None (unknown)."""
        raise NotImplementedError

    @classmethod
    def meta(cls) -> dict:
        doc = (cls.__doc__ or "").strip().splitlines()
        desc = (cls.description or (doc[0] if doc else "")).strip()
        return {"class": cls.__name__, "name": cls.name or cls.__name__, "description": desc[:120],
                "instIds": [str(x) for x in cls.instruments], "timeframe": str(cls.timeframe),
                "lookback": int(cls.lookback)}


class ConditionContext:
    """What a check may read besides `bars`: ctx.now (ms, close of the bar just confirmed), ctx.timeframe,
    ctx.instruments, ctx.bars(inst, n=None) and ctx.factors(inst) (the fact-panel values of the last
    confirmed minute; NaN in replays, which have none)."""

    def __init__(self, bars: dict[str, Bars], factors: dict[str, dict], now: int, timeframe: str):
        self._bars, self._factors = bars, factors
        self.now, self.timeframe = int(now), timeframe
        self.instruments = list(bars)
        self.read_factors = False

    def bars(self, inst: str, n: int | None = None) -> Bars:
        """The declared bars of inst (the last n when given)."""
        b = self._bars.get(inst)
        if b is None:
            raise KeyError(f"{inst} is not in instruments")
        return b if n is None else b.tail(n)

    def factors(self, inst: str) -> dict[str, float]:
        """Fact-panel values (funding_hourly, oi_chg_1h, ret_4h …; missing → absent). Empty in replays."""
        self.read_factors = True
        return {k: (math.nan if v is None else float(v)) for k, v in (self._factors.get(inst) or {}).items()}


# ---------- static checks ----------

def diag(line: int, col: int, msg: str, severity: str = "error") -> dict:
    return {"line": max(1, int(line or 1)), "col": max(0, int(col or 0)), "severity": severity, "message": msg}


def static_problems(code: str) -> list[dict]:
    """Size, syntax, imports, forbidden names and attributes. A guard rail: the container is the sandbox."""
    if len(code.encode()) > MAX_CODE:
        return [diag(1, 0, f"脚本太长：{len(code.encode())} 字节，上限 {MAX_CODE}")]
    try:
        tree = ast.parse(code, FILE)
    except SyntaxError as e:
        return [diag(e.lineno or 1, (e.offset or 1) - 1, f"语法错误：{e.msg}")]
    out: list[dict] = []
    np_names = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            if isinstance(node, ast.ImportFrom) and node.level:
                out.append(diag(node.lineno, node.col_offset, "不能用相对导入"))
                continue
            mods = [a.name for a in node.names] if isinstance(node, ast.Import) else [node.module or ""]
            for m in mods:
                if m.split(".")[0] not in ALLOWED_IMPORTS:
                    out.append(diag(node.lineno, node.col_offset,
                                    f"不能导入 {m}：信号脚本只能用 {', '.join(ALLOWED_IMPORTS[:-1])}"))
            if isinstance(node, ast.Import):
                np_names.update(a.asname or a.name for a in node.names if a.name == "numpy")
        elif isinstance(node, ast.Name) and node.id in BLOCKED_NAMES:
            out.append(diag(node.lineno, node.col_offset, f"信号脚本不能用 {node.id}"))
        elif isinstance(node, ast.Attribute):
            if node.attr.startswith("__") and node.attr.endswith("__"):
                out.append(diag(node.lineno, node.col_offset, f"信号脚本不能访问 {node.attr}"))
            elif node.attr in NUMPY_BLOCKED and isinstance(node.value, ast.Name) and node.value.id in np_names:
                out.append(diag(node.lineno, node.col_offset, f"信号脚本不能用 {node.value.id}.{node.attr}（文件读写）"))
        elif isinstance(node, ast.ExceptHandler):
            if node.type is None or (isinstance(node.type, ast.Name) and node.type.id == "BaseException"):
                out.append(diag(node.lineno, node.col_offset, "不要用裸 except / except BaseException：写 except Exception"))
        elif isinstance(node, (ast.Global, ast.Nonlocal)):
            out.append(diag(node.lineno, node.col_offset, "信号脚本不能用 global / nonlocal：check 每次都从头计算"))
    out.sort(key=lambda d: (d["line"], d["col"]))
    return out


def _pyflakes(code: str) -> list[dict]:
    from .check import _pyflakes as flakes

    return [d for d in flakes(code) if d["severity"] == "error"]  # undefined names fail at run time


def _guarded_import(name, globals=None, locals=None, fromlist=(), level=0):
    if level or name.split(".")[0] not in ALLOWED_IMPORTS:
        raise ImportError(f"import {name} is not allowed in a signal script")
    return builtins.__import__(name, globals, locals, fromlist, level)


SAFE_BUILTINS = {k: v for k, v in vars(builtins).items() if k not in BLOCKED_NAMES}
SAFE_BUILTINS["__import__"] = _guarded_import


@contextlib.contextmanager
def cpu_budget(seconds: float):
    """Raise ScriptTimeout in the main thread once the process used `seconds` of CPU (SIGPROF);
    re-armed every 50 ms so a loop that catches it is interrupted again. No-op off the main thread."""
    if seconds <= 0 or not hasattr(signal, "setitimer"):
        yield
        return

    def fire(signum, frame):
        raise ScriptTimeout(f"超出 CPU 时间预算 {seconds * 1000:.0f} ms")

    try:
        old = signal.signal(signal.SIGPROF, fire)
    except ValueError:  # not the main thread
        yield
        return
    signal.setitimer(signal.ITIMER_PROF, seconds, 0.05)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_PROF, 0)
        signal.signal(signal.SIGPROF, old)


def code_hash(code: str) -> str:
    return hashlib.sha256(code.encode()).hexdigest()[:16]


def load(code: str) -> type[Condition]:
    """Statically check, execute and return the script's Condition subclass (ScriptError otherwise)."""
    probs = static_problems(code) or _pyflakes(code)
    if probs:
        raise ScriptError(f"第 {probs[0]['line']} 行：{probs[0]['message']}")
    ns: dict[str, Any] = {"__name__": "signal_script", "__builtins__": SAFE_BUILTINS}
    try:
        with cpu_budget(LOAD_BUDGET_S):
            exec(compile(code, FILE, "exec"), ns)
    except ScriptTimeout as e:
        raise ScriptError(f"加载脚本{e}") from None
    except Exception as e:
        raise ScriptError(f"加载失败：{type(e).__name__}: {e}") from None
    classes = [v for v in ns.values()
               if isinstance(v, type) and issubclass(v, Condition) and v is not Condition and v.__module__ == "signal_script"]
    if len(classes) != 1:
        raise ScriptError("脚本需要恰好一个 Condition 子类（class MySignal(Condition): ...），找到 "
                          f"{len(classes)} 个")
    cls = classes[0]
    probs = declaration_problems(cls)
    if probs:
        raise ScriptError(probs[0])
    return cls


def declaration_problems(cls: type[Condition]) -> list[str]:
    out = []
    insts = cls.instruments
    if not isinstance(insts, (list, tuple)) or not insts:
        out.append("instruments 需为 1–10 个合约 ID 的列表，如 [\"AAVE-USDT-SWAP\"]")
    else:
        if len(insts) > MAX_INSTRUMENTS:
            out.append(f"instruments 最多 {MAX_INSTRUMENTS} 个（现在 {len(insts)} 个）")
        for x in insts:
            if not isinstance(x, str) or not INST_RE.match(x):
                out.append(f"{x!r} 不是 AAVE-USDT-SWAP 这样的合约 ID")
        if len(set(insts)) != len(insts):
            out.append("instruments 有重复")
    if cls.timeframe not in TIMEFRAMES:
        out.append(f"timeframe 需为 {' / '.join(TIMEFRAMES)}（不能逐笔：脚本在 K 线收盘时运行）")
    lb = cls.lookback
    if not isinstance(lb, int) or isinstance(lb, bool) or not MIN_LOOKBACK <= lb <= MAX_LOOKBACK:
        out.append(f"lookback 需为 {MIN_LOOKBACK}–{MAX_LOOKBACK} 的整数")
    if cls.check is Condition.check:
        out.append("没有实现 check(self, ctx, bars)")
    if len(cls.meta()["description"]) == 0:
        out.append("写一行说明：类的 docstring 或 description，如 \"AAVE 15m RSI 跌破 30\"")
    return out


def check_source(code: str) -> dict:
    """Editor / validation check: {ok, diagnostics, meta}."""
    diags = static_problems(code)
    if not diags:
        diags = _pyflakes(code)
    if diags:
        return {"ok": False, "diagnostics": diags, "meta": None}
    try:
        cls = load(code)
    except ScriptError as e:
        return {"ok": False, "diagnostics": [diag(1, 0, str(e))], "meta": None}
    meta = cls.meta()
    meta["hash"] = code_hash(code)
    return {"ok": True, "diagnostics": [], "meta": meta}


# ---------- running ----------

def _clean(out: Any) -> dict:
    """check's return value → {fire, values, note} (fire None = unknown)."""
    if out is None:
        return {"fire": None, "values": {}, "note": ""}
    if isinstance(out, (bool, np.bool_)):
        return {"fire": bool(out), "values": {}, "note": ""}
    if isinstance(out, Fire):
        values = {}
        for k, v in list(out.values.items())[:MAX_VALUES]:
            try:
                f = float(v)
            except (TypeError, ValueError):
                continue
            if math.isfinite(f):
                values[str(k)[:32]] = f
        return {"fire": out.hit, "values": values, "note": out.note[:MAX_NOTE]}
    raise TypeError(f"check 返回了 {type(out).__name__}；请返回 True / False、Fire(...) 或 None")


def run_check(obj: Condition, bars: dict[str, Bars], factors: dict, now: int, budget_s: float) -> dict:
    """One check at the close `now`: {fire, values, note, ms, error?, missing?, factors?}.

    An instrument whose newest bar is not the one closing at `now` (no data, a gap) makes the result
    unknown without running the script — the same rule live and in replays."""
    cls = type(obj)
    step = TIMEFRAMES[cls.timeframe]
    missing = [i for i in cls.instruments if i not in bars or len(bars[i]) == 0 or int(bars[i].ts[-1]) + step != now]
    if missing:
        return {"fire": None, "values": {}, "note": "", "ms": 0.0, "missing": missing}
    ctx = ConditionContext(bars, factors, now, cls.timeframe)
    t0 = time.perf_counter()
    try:
        with cpu_budget(budget_s):
            out = obj.check(ctx, bars)
        res = _clean(out)
    except ScriptTimeout as e:
        res = {"fire": None, "values": {}, "note": "", "error": str(e)}
    except Exception as e:
        res = {"fire": None, "values": {}, "note": "", "error": _err_line(e)}
    res["ms"] = round((time.perf_counter() - t0) * 1000, 3)
    if ctx.read_factors:
        res["factors"] = True
    return res


def _err_line(e: BaseException) -> str:
    """"ZeroDivisionError: division by zero（第 12 行）" — the script's own line when it has one."""
    import traceback

    line = 0
    for fr in traceback.extract_tb(e.__traceback__):
        if fr.filename == FILE:
            line = fr.lineno or 0
    text = f"{type(e).__name__}: {e}"[:300]
    return f"{text}（第 {line} 行）" if line else text


def replay(cls: type[Condition], data: dict[str, list], start: int, end: int, budget_s: float,
           max_total_s: float = 60.0) -> dict:
    """Run check at every close from start to end (ms, inclusive) over the given history, each call
    seeing only the bars closed by then (the last `lookback`). Returns the value per close
    ("1" true, "0" false, "-" unknown) and the details of each false→true change."""
    step = TIMEFRAMES[cls.timeframe]
    arrays = {i: np.array(data.get(i) or [], dtype=float).reshape(-1, 7) for i in cls.instruments}
    obj = cls()
    states, edges, first_error = [], [], None
    errors = slow = unknown = 0
    ms_sum = ms_max = 0.0
    calls = 0
    read_factors = False
    last = None
    prev = None  # last known value: unknown never makes a change
    deadline = time.monotonic() + max_total_s
    t = start
    while t <= end:
        if time.monotonic() > deadline:
            return {"error": f"回放超过 {max_total_s:.0f} 秒，已停止（{len(states)} 根 K 线）", "states": "".join(states)}
        bars = {}
        for inst, a in arrays.items():
            k = int(np.searchsorted(a[:, 0], t - step, side="right")) if len(a) else 0
            bars[inst] = Bars.from_array(a[max(0, k - cls.lookback):k])
        r = run_check(obj, bars, {}, t, budget_s)
        if "missing" not in r:
            calls += 1
            ms_sum += r["ms"]
            ms_max = max(ms_max, r["ms"])
        read_factors = read_factors or bool(r.get("factors"))
        if r.get("error"):
            errors += 1
            if "预算" in r["error"]:
                slow += 1
            if first_error is None:
                first_error = {"t": t, "error": r["error"]}
        v = r["fire"]
        states.append("-" if v is None else "1" if v else "0")
        if v is None:
            unknown += 1
        elif v and prev is False:
            edges.append({"t": t, "values": r["values"], "note": r["note"]})
        if v is not None:
            prev = v
        last = {"t": t, **{k: r[k] for k in ("fire", "values", "note") if k in r}}
        if r.get("missing"):
            last["missing"] = r["missing"]
        if r.get("error"):
            last["error"] = r["error"]
        t += step
    return {"states": "".join(states), "edges": edges[:200], "edgeCount": len(edges), "errors": errors,
            "timeouts": slow, "firstError": first_error, "unknown": unknown, "calls": calls,
            "avgMs": round(ms_sum / calls, 3) if calls else 0.0, "maxMs": round(ms_max, 3), "factors": read_factors,
            "last": last}
