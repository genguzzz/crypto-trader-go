"""Building blocks for strategies that trade several instruments at once (see library/pool_reversal.py).

A cross-sectional strategy usually works in layers: rank the pool (ctx.rank), keep candidates on a
watchlist for a while, wait for a per-instrument trigger, then pick what to open within position
limits. Watchlist and pick cover the second and the last step."""

from __future__ import annotations

from typing import Any, Iterable

from .context import Record


class Watchlist:
    """Candidates being watched, stored in ctx.state[key] so they survive restarts.

    Each entry: inst_id, side, score, since (ms, when it entered), plus anything you set with
    update(ctx, inst, **fields)."""

    def __init__(self, key: str = "watch"):
        self.key = key

    def _d(self, ctx) -> dict[str, dict]:
        return ctx.state.setdefault(self.key, {})

    def sync(self, ctx, candidates: Iterable[Any], ttl_ms: int) -> list[str]:
        """Bring the list in line with this round's candidates (rows with inst_id, side, score):
        new ones enter with since = ctx.now, existing ones get the new score and side (a side change
        restarts the clock), entries no longer among the candidates or watched longer than ttl_ms leave.
        Returns the instruments that left."""
        d = self._d(ctx)
        cur = {}
        for c in candidates:
            old = d.get(c.inst_id)
            since = old["since"] if old and old.get("side") == c.side else ctx.now
            e = dict(old or {}) if old and old.get("side") == c.side else {}
            e.update(side=c.side, score=float(c.score), since=since)
            cur[c.inst_id] = e
        left = [k for k in d if k not in cur]
        for k, e in list(cur.items()):
            if ctx.now - e["since"] > ttl_ms:
                del cur[k]
                left.append(k)
        d.clear()
        d.update(cur)
        return sorted(left)

    def update(self, ctx, inst: str, **fields) -> None:
        if inst in self._d(ctx):
            self._d(ctx)[inst].update(fields)

    def remove(self, ctx, inst: str) -> None:
        self._d(ctx).pop(inst, None)

    def items(self, ctx) -> list[Record]:
        """Entries, best score first."""
        rows = [Record(inst_id=k, **v) for k, v in self._d(ctx).items()]
        rows.sort(key=lambda r: (-r.score, r.inst_id))
        return rows

    def ids(self, ctx) -> set[str]:
        return set(self._d(ctx))


def pick(candidates: Iterable[Any], positions: Iterable[Any], max_total: int, max_side: int | None = None) -> list[Any]:
    """Choose which candidates (rows with inst_id, side, score) to open, best score first, so that
    open positions plus new ones stay within max_total, and within max_side per side. Instruments
    already held are skipped."""
    held = {p.inst_id for p in positions}
    per_side = {"long": 0, "short": 0}
    for p in positions:
        per_side[p.pos_side] = per_side.get(p.pos_side, 0) + 1
    room = max_total - len(held)
    out = []
    for c in sorted(candidates, key=lambda c: (-c.score, c.inst_id)):
        if room <= 0:
            break
        if c.inst_id in held or (max_side is not None and per_side.get(c.side, 0) >= max_side):
            continue
        out.append(c)
        held.add(c.inst_id)
        per_side[c.side] = per_side.get(c.side, 0) + 1
        room -= 1
    return out
