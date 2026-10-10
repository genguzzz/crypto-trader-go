"""Checks strategy source for the editor: syntax, pyflakes, import, class declarations.

Reads the code on stdin and writes {"diagnostics": [...], "strategies": [meta...]} to stdout.
Runs in a throwaway process with the same limits as an instance (runner.py `check`)."""

from __future__ import annotations

import ast
import json
import re
import sys
import traceback

FILE = "strategy.py"
NAME_RE = re.compile(r"^[a-z0-9][a-z0-9-]{1,39}$")
INST_RE = re.compile(r"^[A-Z0-9]{1,20}-USDT-SWAP$")


def diag(line: int, col: int, msg: str, severity: str = "error", end_line: int | None = None,
         end_col: int | None = None) -> dict:
    d = {"line": max(1, int(line or 1)), "col": max(0, int(col or 0)), "severity": severity, "message": msg}
    if end_line:
        d["endLine"], d["endCol"] = int(end_line), int(end_col or 0)
    return d


def _pyflakes(code: str) -> list[dict]:
    try:
        from pyflakes import api, messages
        from pyflakes.reporter import Reporter
    except ImportError:
        return []
    out: list[dict] = []

    class R(Reporter):
        def __init__(self):
            pass

        def unexpectedError(self, filename, msg):
            out.append(diag(1, 0, str(msg)))

        def syntaxError(self, filename, msg, lineno, offset, text):
            pass  # reported by compile()

        def flake(self, m):
            sev = "error" if isinstance(m, (messages.UndefinedName, messages.UndefinedLocal,
                                            messages.UndefinedExport)) else "warning"
            out.append(diag(m.lineno, getattr(m, "col", 0), m.message % m.message_args, sev))

    api.check(code, FILE, R())
    return out


def _line_of(tb, code_file: str) -> int:
    line = 1
    for fr in traceback.extract_tb(tb):
        if fr.filename == code_file:
            line = fr.lineno or 1
    return line


def _class_line(code: str, name: str) -> int:
    try:
        for node in ast.walk(ast.parse(code)):
            if isinstance(node, ast.ClassDef) and node.name == name:
                return node.lineno
    except SyntaxError:
        pass
    return 1


def _attr_line(code: str, cls: str, attr: str) -> int:
    try:
        for node in ast.walk(ast.parse(code)):
            if isinstance(node, ast.ClassDef) and node.name == cls:
                for st in node.body:
                    targets = st.targets if isinstance(st, ast.Assign) else [st.target] if isinstance(st, ast.AnnAssign) else []
                    if any(isinstance(t, ast.Name) and t.id == attr for t in targets):
                        return st.lineno
                return node.lineno
    except SyntaxError:
        pass
    return 1


def check_class(cls, code: str) -> list[dict]:
    from .strategy import INTERVALS, TIMEFRAMES, Param, Screener, Strategy, Timeframe, vars_lookup

    out: list[dict] = []

    def bad(attr: str, msg: str, severity: str = "error"):
        out.append(diag(_attr_line(code, cls.__name__, attr), 0, f"{cls.__name__}.{attr}: {msg}", severity))

    name = cls.strategy_name()
    if not NAME_RE.match(name):
        bad("name", f"name {name!r} 需为 2–40 个小写字母、数字或 -")
    tf = vars_lookup(cls, "timeframe")
    if isinstance(tf, Param) and not isinstance(tf, Timeframe):
        bad("timeframe", "周期参数请用 Timeframe(...)")
    elif cls.default_timeframe() not in TIMEFRAMES:
        bad("timeframe", f"周期 {cls.default_timeframe()!r} 不在 {TIMEFRAMES} 里")
    u = cls.universe
    if isinstance(u, Screener):
        if u.side not in ("long", "short", "both"):
            bad("universe", "Screener 的 side 需为 long、short 或 both")
        if u.scope not in Screener.SCOPES:
            bad("universe", "Screener 的 scope 需为 pool 或 market")
        elif not 1 <= u.top <= u.max_top():
            bad("universe", f"Screener 的 top 需在 1–{u.max_top()}" + ("（全市场）" if u.scope == "market" else ""))
        if u.scope == "market" and u.rule:
            bad("universe", "全市场 Screener 按成交额排序，不能指定 rule（评分只覆盖活跃池）")
        if u.scope == "market" and u.min_score is not None:
            bad("universe", "全市场 Screener 没有评分，不能设 min_score")
        if u.refresh not in ("5m", "15m", "1H", "4H", "1D"):
            bad("universe", "Screener 的 refresh 需为 5m / 15m / 1H / 4H / 1D")
        if u.min_score is not None and not 0 <= u.min_score <= 100:
            bad("universe", "Screener 的 min_score 需在 0–100")
    elif isinstance(u, (list, tuple)):
        if not u:
            bad("universe", "universe 为空：列出合约，或用 Screener", "warning")
        for x in u:
            if not isinstance(x, str) or not INST_RE.match(x):
                bad("universe", f"{x!r} 不是 BTC-USDT-SWAP 这样的合约 ID")
        if len(u) > 50:
            bad("universe", "最多 50 个合约")
    else:
        bad("universe", "universe 需为合约 ID 列表或 Screener")
    for k, v in (cls.schedule or {}).items():
        if v not in INTERVALS:
            bad("schedule", f"定时器 {k!r} 的间隔 {v!r} 不在 {INTERVALS} 里")
    for pname, p in cls.param_specs().items():
        for msg in p.problems():
            bad(pname, msg)
    if all(getattr(cls, h) is getattr(Strategy, h) for h in ("on_bar", "on_bars", "on_timer")):
        out.append(diag(_class_line(code, cls.__name__), 0,
                        f"{cls.__name__} 没有重写 on_bar / on_bars / on_timer：它永远不会做任何事", "warning"))
    return out


def check(code: str) -> dict:
    from .runtime import load_classes

    diags: list[dict] = []
    try:
        compile(code, FILE, "exec")
    except SyntaxError as e:
        diags.append(diag(e.lineno or 1, (e.offset or 1) - 1, f"语法错误：{e.msg}", "error",
                          getattr(e, "end_lineno", None), (getattr(e, "end_offset", None) or 1) - 1))
        return {"diagnostics": diags, "strategies": []}
    diags += _pyflakes(code)
    try:
        classes = load_classes(code, FILE)
    except Exception as e:
        diags.append(diag(_line_of(e.__traceback__, FILE), 0, f"{type(e).__name__}: {e}"))
        return {"diagnostics": diags, "strategies": []}
    if not classes:
        diags.append(diag(1, 0, "没有找到 Strategy 子类（class MyStrategy(Strategy): ...）"))
    if len(classes) > 1:
        diags.append(diag(_class_line(code, classes[1].__name__), 0,
                          "一个文件只能定义一个策略，找到了：" + ", ".join(c.__name__ for c in classes)))
    metas = []
    for c in classes:
        diags += check_class(c, code)
        try:
            metas.append(c.meta())
        except Exception as e:
            diags.append(diag(_class_line(code, c.__name__), 0, f"声明有误：{type(e).__name__}: {e}"))
    diags.sort(key=lambda d: (d["line"], d["col"]))
    return {"diagnostics": diags, "strategies": metas}


def main() -> None:
    from .limits import apply

    apply()
    code = sys.stdin.read()
    sys.stdout.write(json.dumps(check(code), allow_nan=False))


if __name__ == "__main__":
    main()
