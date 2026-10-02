"""The `ctx` object handed to every handler, plus the small data types it returns."""

from __future__ import annotations

import math
import re
from typing import TYPE_CHECKING, Any

import numpy as np

if TYPE_CHECKING:
    from .runtime import Runtime
    from .signal import Signal

_CAMEL = re.compile(r"_([a-z])")


class Record(dict):
    """A dict with attribute access; snake_case names also find the camelCase wire keys (pos.pos_side)."""

    def __getattr__(self, k: str) -> Any:
        if k in self:
            return self[k]
        camel = _CAMEL.sub(lambda m: m.group(1).upper(), k)
        if camel in self:
            return self[camel]
        raise AttributeError(k)


def record(v: Any) -> Any:
    if isinstance(v, dict):
        return Record({k: record(x) for k, x in v.items()})
    if isinstance(v, list):
        return [record(x) for x in v]
    return v


def _f(v: Any) -> float:
    return math.nan if v is None else float(v)


class Bar:
    """One confirmed candle of `inst` on `tf`; `ts` is the open time in ms."""

    __slots__ = ("inst", "tf", "ts", "o", "h", "l", "c", "vol", "volq")

    def __init__(self, inst: str, tf: str, b: dict):
        self.inst, self.tf = inst, tf
        self.ts = int(b.get("ts") or 0)
        self.o, self.h, self.l, self.c = _f(b.get("o")), _f(b.get("h")), _f(b.get("l")), _f(b.get("c"))
        self.vol, self.volq = _f(b.get("vol")), _f(b.get("volq"))

    def __repr__(self) -> str:
        return f"Bar({self.inst} {self.tf} ts={self.ts} c={self.c})"


class Bars:
    """Columnar candles, oldest first: numpy arrays ts o h l c vol volq."""

    __slots__ = ("ts", "o", "h", "l", "c", "vol", "volq")

    def __init__(self, rows: list):
        a = np.array(rows, dtype=float).reshape(-1, 7) if rows else np.empty((0, 7))
        self.ts = a[:, 0].astype(np.int64)
        self.o, self.h, self.l, self.c, self.vol, self.volq = (a[:, i] for i in range(1, 7))

    def __len__(self) -> int:
        return len(self.c)


class Context:
    """Read access to market data and the account, plus `state` (a dict persisted after every event)."""

    def __init__(self, rt: "Runtime"):
        self._rt = rt
        self._cache: dict = {}
        self.state: dict = {}
        self.params: dict = {}
        self.universe: list[str] = []
        self.instance_id = 0
        self.mode = "live"
        self.now = 0  # ms, the host clock at the current event
        self.timeframe = "1H"

    def bars(self, inst: str, tf: str | None = None, n: int = 200) -> Bars:
        """Last n confirmed bars (cached within one event)."""
        key = (inst, tf or self.timeframe, int(n))
        if key not in self._cache:
            self._cache[key] = Bars(self._rt.query("bars", {"instId": inst, "tf": key[1], "n": key[2]}) or [])
        return self._cache[key]

    def factors(self, inst: str) -> dict[str, float]:
        """Fact-panel values of the last confirmed minute (missing → NaN); {} if unknown."""
        d = self._rt.query("factors", {"instId": inst}) or {}
        return {k: _f(v) for k, v in d.items()}

    def screener(self, rule: str = "", side: str = "long", top: int = 20, min_score: float | None = None,
                 filter: dict | None = None) -> list[Record]:
        q: dict[str, Any] = {"rule": rule, "side": side, "top": int(top)}
        if min_score is not None:
            q["minScore"] = float(min_score)
        if filter:
            q["filter"] = {k: [float(v[0]), float(v[1])] for k, v in filter.items()}
        return record(self._rt.query("screener", q) or [])

    def score(self, rule: str, inst: str) -> tuple[float, float]:
        """(long, short) score of a rule, NaN when unscored."""
        d = self._rt.query("score", {"rule": rule, "instId": inst})
        if not d:
            return math.nan, math.nan
        return _f(d.get("long")), _f(d.get("short"))

    def position(self, inst: str) -> Record | None:
        """This instance's open position in inst, or None."""
        return record(self._rt.query("position", {"instId": inst}))

    def positions(self) -> list[Record]:
        return record(self._rt.query("positions", {}) or [])

    def account(self) -> Record | None:
        return record(self._rt.query("account", {}))

    def spec(self, inst: str) -> Record | None:
        return record(self._rt.query("spec", {"instId": inst}))

    def submit(self, signal: "Signal") -> int | None:
        """Send a signal now (e.g. from on_fill); returns its id, or None if it was refused."""
        return self._rt.submit(signal)

    def log(self, msg: str, level: str = "info") -> None:
        self._rt.log(level, str(msg))
