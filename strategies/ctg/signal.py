"""Signals: the only thing a strategy can emit. The Go order layer decides whether and how much to trade."""

from __future__ import annotations

import math
import re
from dataclasses import dataclass
from typing import Union

Level = Union[float, str, None]  # absolute price, "<k>atr", or None

_ATR = re.compile(r"^\s*(\d+(?:\.\d+)?)\s*atr\s*$", re.IGNORECASE)

OPEN_LONG, OPEN_SHORT, CLOSE, REDUCE = "open_long", "open_short", "close", "reduce"


class SignalError(ValueError):
    pass


def atr_multiple(level: Level) -> float | None:
    """2.5 for "2.5atr", None for anything else."""
    if isinstance(level, str):
        m = _ATR.match(level)
        if not m:
            raise SignalError(f"bad level {level!r}: use a price or '<k>atr'")
        return float(m.group(1))
    return None


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

    @classmethod
    def open_long(cls, inst: str, stop_loss: Level = None, take_profit: Level = None, strength: float = 1.0,
                  leverage: float | None = None, max_slippage_bp: float | None = None, ttl_ms: int | None = None,
                  reason: str = "") -> "Signal":
        return cls(inst, OPEN_LONG, strength, stop_loss, take_profit, max_slippage_bp, leverage, None, ttl_ms, reason)

    @classmethod
    def open_short(cls, inst: str, stop_loss: Level = None, take_profit: Level = None, strength: float = 1.0,
                   leverage: float | None = None, max_slippage_bp: float | None = None, ttl_ms: int | None = None,
                   reason: str = "") -> "Signal":
        return cls(inst, OPEN_SHORT, strength, stop_loss, take_profit, max_slippage_bp, leverage, None, ttl_ms, reason)

    @classmethod
    def close(cls, inst: str, reason: str = "", ttl_ms: int | None = None) -> "Signal":
        return cls(inst, CLOSE, reason=reason, ttl_ms=ttl_ms)

    @classmethod
    def reduce(cls, inst: str, frac: float, reason: str = "", ttl_ms: int | None = None) -> "Signal":
        if not 0 < frac <= 1:
            raise SignalError("reduce frac must be in (0, 1]")
        return cls(inst, REDUCE, reduce_frac=float(frac), reason=reason, ttl_ms=ttl_ms)

    def needs_atr(self) -> bool:
        return self.action in (OPEN_LONG, OPEN_SHORT) and (
            isinstance(self.stop_loss, str) or isinstance(self.take_profit, str))

    def to_wire(self, entry: float | None = None, atr: float | None = None) -> dict:
        """Wire form with "<k>atr" levels resolved around `entry` (reference price) using `atr`."""
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
                k = atr_multiple(level)
                if k is not None:
                    if entry is None or atr is None or not math.isfinite(entry) or not math.isfinite(atr) or atr <= 0:
                        raise SignalError(f"cannot resolve {level!r}: ATR(14) unavailable")
                    price = entry + direction * k * atr
                else:
                    price = float(level)
                if not math.isfinite(price) or price <= 0:
                    raise SignalError(f"{key} resolved to {price}")
                d[key] = price
        for key, v in (("maxSlippageBp", self.max_slippage_bp), ("leverage", self.leverage),
                       ("reduceFrac", self.reduce_frac), ("ttlMs", self.ttl_ms)):
            if v is not None:
                d[key] = int(v) if key == "ttlMs" else float(v)
        return d
