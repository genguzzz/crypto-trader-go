"""Dumps the SDK's public API (signatures + docstrings) as JSON for the web app: the API reference
page and the editor's autocompletion read web/src/docs/sdk.json. tests/test_sdkdoc.py fails when
the file is stale; regenerate with:

    cd strategies && python3 -m ctg.sdkdoc > ../web/src/docs/sdk.json
"""

from __future__ import annotations

import inspect
import json
import sys

from . import portfolio, ta
from .context import Bar, Bars, Context, Series
from .signal import Signal
from .strategy import Bool, Choice, Float, Int, Param, Screener, Strategy, Timeframe

HANDLERS = ["on_start", "on_bar", "on_bars", "on_timer", "on_universe_change", "on_fill", "on_position_closed",
            "on_signal_result", "on_funding", "on_stop", "status"]
CLASS_ATTRS = {
    "name": "stable id, default the kebab-case class name",
    "description": "one line shown on the strategy card (default: the docstring)",
    "universe": "list of instrument ids, or Screener(...)",
    "timeframe": '"1m" "5m" "15m" "1H" "4H" "1D", or Timeframe("4H") to let instances choose',
    "can_long": "the strategy may open longs (default True)",
    "can_short": "the strategy may open shorts (default True)",
    "warmup": "bars of history the strategy reads",
    "schedule": '{"name": "15m"}: on_timer(ctx, "name") every 15 minutes',
}
CTX_ATTRS = {
    "state": "dict persisted after every event (JSON, ≤ 64 KB), restored on restart",
    "now": "host clock of the current event, ms (replay time in backtests)",
    "mode": '"live" or "backtest"',
    "params": "parameter values by name",
    "universe": "current instruments of the instance",
    "timeframe": "the instance's bar timeframe",
    "instance_id": "id of the instance",
}


def _sig(fn, drop_self: bool = True) -> str:
    try:
        s = inspect.signature(fn)
    except (TypeError, ValueError):
        return "()"
    params = list(s.parameters.values())
    if drop_self and params and params[0].name in ("self", "cls"):
        params = params[1:]
    return str(s.replace(parameters=params, return_annotation=inspect.Signature.empty))


def _doc(obj) -> str:
    return inspect.cleandoc(obj.__doc__ or "")


def _entry(group: str, name: str, kind: str, sig: str = "", doc: str = "") -> dict:
    return {"group": group, "name": name, "kind": kind, "sig": sig, "doc": doc}


def dump() -> list[dict]:
    out: list[dict] = []
    for k, d in CLASS_ATTRS.items():
        out.append(_entry("Strategy", k, "attr", "", d))
    for h in HANDLERS:
        out.append(_entry("Strategy", h, "handler", _sig(getattr(Strategy, h)), _doc(getattr(Strategy, h))))
    out.append(_entry("Strategy", "allows", "method", _sig(Strategy.allows), _doc(Strategy.allows)))
    for cls in (Int, Float, Bool, Choice, Timeframe, Param, Screener):
        out.append(_entry("params", cls.__name__, "class", _sig(cls.__init__), _doc(cls)))
    for name in ("open", "open_long", "open_short", "close", "reduce"):
        out.append(_entry("Signal", name, "method", _sig(getattr(Signal, name)), _doc(getattr(Signal, name))))
    out.append(_entry("Signal", "reversed", "method", _sig(Signal.reversed), _doc(Signal.reversed)))
    for name, fn in inspect.getmembers(Context, inspect.isfunction):
        if not name.startswith("_"):
            out.append(_entry("ctx", name, "method", _sig(fn), _doc(fn)))
    for k, d in CTX_ATTRS.items():
        out.append(_entry("ctx", k, "attr", "", d))
    for cls, fields in ((Bar, "inst tf ts o h l c vol volq"), (Bars, "ts o h l c vol volq"), (Series, "ts v")):
        out.append(_entry("data", cls.__name__, "class", fields, _doc(cls)))
    for name in ta.__all__:
        fn = getattr(ta, name)
        out.append(_entry("ta", name, "function", _sig(fn, False), _doc(fn)))
    out.append(_entry("portfolio", "Watchlist", "class", _sig(portfolio.Watchlist.__init__), _doc(portfolio.Watchlist)))
    for name in ("sync", "update", "remove", "items", "ids"):
        fn = getattr(portfolio.Watchlist, name)
        out.append(_entry("portfolio", f"Watchlist.{name}", "method", _sig(fn), _doc(fn)))
    out.append(_entry("portfolio", "pick", "function", _sig(portfolio.pick, False), _doc(portfolio.pick)))
    return out


def main() -> None:
    sys.stdout.write(json.dumps(dump(), ensure_ascii=False, indent=1) + "\n")


if __name__ == "__main__":
    main()
