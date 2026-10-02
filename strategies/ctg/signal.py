"""Signals: the only thing a strategy can emit. The Go order layer decides whether and how much to trade."""

from __future__ import annotations

import dataclasses
import math
import re
from dataclasses import dataclass
from typing import Union

Level = Union[float, str, None]  # absolute price, "<k>atr", "<k>%", or None

_ATR = re.compile(r"^\s*(\d+(?:\.\d+)?)\s*atr\s*$", re.IGNORECASE)
_PCT = re.compile(r"^\s*(\d+(?:\.\d+)?)\s*%\s*$")
_DUR = re.compile(r"^\s*(\d+(?:\.\d+)?)\s*(m|h|d)\s*$", re.IGNORECASE)
_DUR_MS = {"m": 60_000, "h": 3_600_000, "d": 86_400_000}

OPEN_LONG, OPEN_SHORT, CLOSE, REDUCE = "open_long", "open_short", "close", "reduce"


class SignalError(ValueError):
    pass


def atr_multiple(level: Level) -> float | None:
    """2.5 for "2.5atr", None for anything else that is valid."""
    if isinstance(level, str):
        m = _ATR.match(level)
        if m:
            return float(m.group(1))
        if not _PCT.match(level):
            raise SignalError(f"bad level {level!r}: use a price, '<k>atr' or '<k>%'")
    return None


def _distance(level: Level, entry: float | None, atr: float | None, what: str) -> float:
    """A distance from the entry: k × ATR for "<k>atr", entry × k% for "<k>%", else the number itself."""
    k = atr_multiple(level)
    if k is not None:
        if atr is None or not math.isfinite(atr) or atr <= 0:
            raise SignalError(f"cannot resolve {what} {level!r}: ATR(14) unavailable")
        return k * atr
    if isinstance(level, str):
        if entry is None or not math.isfinite(entry) or entry <= 0:
            raise SignalError(f"cannot resolve {what} {level!r}: no reference price")
        return entry * float(_PCT.match(level).group(1)) / 100
    return float(level)


def duration_ms(d: Union[str, int, float, None]) -> int | None:
    """"30m" / "6h" / "2d" / milliseconds → milliseconds."""
    if d is None:
        return None
    if isinstance(d, str):
        m = _DUR.match(d)
        if not m:
            raise SignalError(f"bad duration {d!r}: use e.g. '30m', '6h', '2d'")
        return int(float(m.group(1)) * _DUR_MS[m.group(2).lower()])
    return int(d)


@dataclass
class Signal:
    inst: str
    action: str
    strength: float = 1.0
    stop_loss: Level = None
    take_profit: Level = None
    max_slippage_bp: float | None = None
    leverage: float | None = None
    reduce_frac: float | None = None
    ttl_ms: int | None = None
    reason: str = ""
    trailing_stop: Level = None  # distance: price units, "<k>atr" or "<k>%"
    time_limit: Union[str, int, None] = None  # "6h" / ms: close the position after this long
    reverse: bool = False  # close this instance's opposite position first (see Signal.reversed)

    @classmethod
    def open(cls, inst: str, side: str, stop_loss: Level = None, take_profit: Level = None, strength: float = 1.0,
             leverage: float | None = None, max_slippage_bp: float | None = None, ttl_ms: int | None = None,
             trailing_stop: Level = None, time_limit: Union[str, int, None] = None, reason: str = "") -> "Signal":
        """Open (or add to) a position on `side` ("long" / "short").

        stop_loss / take_profit: a price, "<k>atr" (k × ATR(14) of the strategy timeframe from the
        reference price) or "<k>%". trailing_stop: a distance that follows the best price (the stop only
        tightens). time_limit: close after "30m" / "6h" / "2d"."""
        if side not in ("long", "short"):
            raise SignalError(f"side must be 'long' or 'short', not {side!r}")
        return cls(inst, OPEN_LONG if side == "long" else OPEN_SHORT, strength, stop_loss, take_profit, max_slippage_bp,
                   leverage, None, ttl_ms, reason, trailing_stop, time_limit)

    @classmethod
    def open_long(cls, inst: str, stop_loss: Level = None, take_profit: Level = None, strength: float = 1.0,
                  leverage: float | None = None, max_slippage_bp: float | None = None, ttl_ms: int | None = None,
                  reason: str = "", trailing_stop: Level = None, time_limit: Union[str, int, None] = None) -> "Signal":
        return cls.open(inst, "long", stop_loss, take_profit, strength, leverage, max_slippage_bp, ttl_ms,
                        trailing_stop, time_limit, reason)

    @classmethod
    def open_short(cls, inst: str, stop_loss: Level = None, take_profit: Level = None, strength: float = 1.0,
                   leverage: float | None = None, max_slippage_bp: float | None = None, ttl_ms: int | None = None,
                   reason: str = "", trailing_stop: Level = None, time_limit: Union[str, int, None] = None) -> "Signal":
        return cls.open(inst, "short", stop_loss, take_profit, strength, leverage, max_slippage_bp, ttl_ms,
                        trailing_stop, time_limit, reason)

    @classmethod
    def close(cls, inst: str, reason: str = "", ttl_ms: int | None = None) -> "Signal":
        return cls(inst, CLOSE, reason=reason, ttl_ms=ttl_ms)

    @classmethod
    def reduce(cls, inst: str, frac: float, reason: str = "", ttl_ms: int | None = None) -> "Signal":
        if not 0 < frac <= 1:
            raise SignalError("reduce frac must be in (0, 1]")
        return cls(inst, REDUCE, reduce_frac=float(frac), reason=reason, ttl_ms=ttl_ms)

    def reversed(self) -> "Signal":
        """This opening signal as a reverse: when the instance holds the opposite side, the order layer
        closes it and then opens this one (the re-entry cooldown does not apply). Without an opposite
        position it is a plain open."""
        if self.action not in (OPEN_LONG, OPEN_SHORT):
            raise SignalError("only opening signals can reverse")
        return dataclasses.replace(self, reverse=True)

    @property
    def side(self) -> str | None:
        return {OPEN_LONG: "long", OPEN_SHORT: "short"}.get(self.action)

    def needs_atr(self) -> bool:
        return self.action in (OPEN_LONG, OPEN_SHORT) and any(
            isinstance(x, str) and _ATR.match(x) for x in (self.stop_loss, self.take_profit, self.trailing_stop))

    def to_wire(self, entry: float | None = None, atr: float | None = None) -> dict:
        """Wire form with "<k>atr" / "<k>%" levels resolved around `entry` (reference price) using `atr`."""
        if self.action not in (OPEN_LONG, OPEN_SHORT, CLOSE, REDUCE):
            raise SignalError(f"unknown action {self.action!r}")
        if not 0 < self.strength <= 1:
            raise SignalError("strength must be in (0, 1]")
        d: dict = {"instId": self.inst, "action": self.action, "strength": float(self.strength),
                   "reason": str(self.reason)[:500]}
        if self.action in (OPEN_LONG, OPEN_SHORT):
            sign = 1.0 if self.action == OPEN_LONG else -1.0
            for key, level, direction in (("stopLoss", self.stop_loss, -sign), ("takeProfit", self.take_profit, sign)):
                if level is None:
                    continue
                if isinstance(level, str):
                    if entry is None or not math.isfinite(entry):
                        raise SignalError(f"cannot resolve {key} {level!r}: no reference price")
                    price = entry + direction * _distance(level, entry, atr, key)
                else:
                    price = float(level)
                if not math.isfinite(price) or price <= 0:
                    raise SignalError(f"{key} resolved to {price}")
                d[key] = price
            if self.trailing_stop is not None:
                dist = _distance(self.trailing_stop, entry, atr, "trailing_stop")
                if not math.isfinite(dist) or dist <= 0:
                    raise SignalError(f"trailing_stop resolved to {dist}")
                d["trailDist"] = dist
            limit = duration_ms(self.time_limit)
            if limit is not None:
                if limit <= 0:
                    raise SignalError("time_limit must be positive")
                d["timeLimitMs"] = limit
            if self.reverse:
                d["reverse"] = True
        for key, v in (("maxSlippageBp", self.max_slippage_bp), ("leverage", self.leverage),
                       ("reduceFrac", self.reduce_frac), ("ttlMs", self.ttl_ms)):
            if v is not None:
                d[key] = int(v) if key == "ttlMs" else float(v)
        return d
