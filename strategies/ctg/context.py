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


class Series:
    """A data series, oldest first: numpy arrays ts (bucket open, ms) and v."""

    __slots__ = ("ts", "v")

    def __init__(self, rows: list):
        self.ts = np.array([int(r.get("ts") or 0) for r in rows], dtype=np.int64)
        self.v = np.array([_f(r.get("v")) for r in rows], dtype=float)

    def __len__(self) -> int:
        return len(self.v)


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

    def series(self, inst: str, field: str, tf: str | None = None, n: int = 100) -> Series:
        """Last n complete buckets of a data series (cached within one event):
        oi (open interest USDT at the bucket close), funding (settled rates, one per settlement),
        buy / sell / delta (taker buy, sell, buy − sell notional USDT)."""
        key = ("series", inst, field, tf or self.timeframe, int(n))
        if key not in self._cache:
            rows = self._rt.query("series", {"instId": inst, "field": field, "tf": key[3], "n": key[4]}) or []
            self._cache[key] = Series(rows)
        return self._cache[key]

    def rank(self, rule: str, side: str = "long", among: list[str] | None = None, min_score: float | None = None,
             top: int | None = None) -> list[Record]:
        """Instruments of the active pool ranked by a rule's score, best first. side: long | short | both
        (each row then takes its stronger side). Rows: inst_id, side, score, long, short, values.
        among limits the ranking to those instruments."""
        rows = self.screener(rule, side, 200, None)
        want = set(among) if among is not None else None
        out = []
        for r in rows:
            if want is not None and r.inst_id not in want:
                continue
            lg, sh = _f(r.get("long")), _f(r.get("short"))
            if side != "both":
                s = side
            elif math.isnan(lg) or (not math.isnan(sh) and sh > lg):   # an unscored side never wins
                s = "short"
            else:
                s = "long"
            score = sh if s == "short" else lg
            if math.isnan(score) or (min_score is not None and score < min_score):
                continue
            out.append(Record(inst_id=r.inst_id, side=s, score=score, long=lg, short=sh, values=r.get("values") or {}))
        out.sort(key=lambda x: (-x.score, x.inst_id))
        return out[:top] if top else out

    def screener(self, rule: str = "", side: str = "long", top: int = 20, min_score: float | None = None,
                 filter: dict | None = None, scope: str = "pool") -> list[Record]:
        """Like Screener(...): scope="pool" ranks the active pool (by `rule` when given), scope="market"
        every recorded crypto swap by 12h USDT turnover (no rule). Rows: inst_id, rank, last, long,
        short, values; an unscored side is NaN (always for market rows)."""
        from .strategy import Screener

        if scope not in Screener.SCOPES:
            raise ValueError(f"scope must be one of {Screener.SCOPES}, not {scope!r}")
        if scope == "market" and (rule or min_score):
            raise ValueError("a market screener ranks by turnover: no rule or min_score")
        q: dict[str, Any] = {"rule": rule, "side": side, "top": int(top)}
        if scope == "market":
            q["scope"] = "market"
        if min_score is not None:
            q["minScore"] = float(min_score)
        if filter:
            q["filter"] = {k: [float(v[0]), float(v[1])] for k, v in filter.items()}
        rows = record(self._rt.query("screener", q) or [])
        for r in rows:  # unscored / unknown come as null: NaN, like ctx.score (comparisons are False, not errors)
            for k in ("long", "short", "last"):
                r[k] = _f(r.get(k))
        return rows

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
        """A line in the instance's run log (shown on its card and in trial runs)."""
        self._rt.log(level, str(msg))

    def notify(self, text: str, level: str = "info", **data) -> None:
        """Send a message to the owner's channels (Telegram, webhook) — live instances only; backtests
        and trial runs keep it in the run log. At most 30 per hour; identical text once per 10 minutes.
        Extra keyword arguments travel as data (webhook JSON, extra lines in Telegram)."""
        self._rt.notify(level, str(text), data)
