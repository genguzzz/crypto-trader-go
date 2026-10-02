"""Render a ReviewRequest (Go JSON, camelCase) into a compact Markdown context pack.

The pack is what the model reads: short tables of numbers, not a raw JSON dump. Missing values are
rendered as "n/a" (unknown, never zero). Pure functions only; no SDK import.
"""

from __future__ import annotations

import math
from datetime import datetime, timedelta, timezone
from typing import Any, Iterable

CST = timezone(timedelta(hours=8), "UTC+8")
MAX_ROWS = 30  # rows per table (the most recent ones)
MAX_SERIES = 24  # values per numeric series
MAX_TEXT = 300  # characters per free-text cell
MAX_PACK = 60_000  # characters for the whole pack
NA = "n/a"


# ---------- formatting ----------


def is_num(v: Any) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def num(v: Any, digits: int = 6) -> str:
    """Compact number: thousands separators for large values, significant digits otherwise."""
    if v is None or not is_num(v):
        return NA if v is None else str(v)
    if isinstance(v, float) and (math.isnan(v) or math.isinf(v)):
        return NA
    if isinstance(v, int) or float(v).is_integer() and abs(v) < 1e15:
        return f"{int(v):,}" if abs(v) >= 10_000 else str(int(v))
    if abs(v) >= 10_000:
        return f"{v:,.2f}".rstrip("0").rstrip(".")
    if abs(v) >= 100:
        return f"{v:.2f}".rstrip("0").rstrip(".")
    return f"{v:.{digits}g}"


def pct(v: Any, digits: int = 2) -> str:
    if v is None or not is_num(v) or math.isnan(v):
        return NA
    return f"{v * 100:+.{digits}f}%"


def plain_pct(v: Any, digits: int = 2) -> str:
    if v is None or not is_num(v) or math.isnan(v):
        return NA
    return f"{v * 100:.{digits}f}%"


def ts(ms: Any) -> str:
    if not is_num(ms) or ms <= 0:
        return NA
    return datetime.fromtimestamp(ms / 1000, CST).strftime("%m-%d %H:%M")


def ts_full(ms: Any) -> str:
    if not is_num(ms) or ms <= 0:
        return NA
    return datetime.fromtimestamp(ms / 1000, CST).strftime("%Y-%m-%d %H:%M UTC+8")


def text(v: Any, limit: int = MAX_TEXT) -> str:
    s = "" if v is None else str(v)
    s = " ".join(s.split())  # one line
    s = s.replace("|", "/")
    return s if len(s) <= limit else s[: limit - 1] + "…"


def yes(v: Any) -> str:
    return "yes" if v else "no"


def table(headers: list[str], rows: Iterable[list[str]]) -> str:
    rows = list(rows)
    if not rows:
        return "(none)"
    out = ["| " + " | ".join(headers) + " |", "|" + "---|" * len(headers)]
    out += ["| " + " | ".join(r) + " |" for r in rows]
    return "\n".join(out)


def looks_like_ts(key: str, v: Any) -> bool:
    k = key.lower()
    return is_num(v) and v > 1e12 and (k in ("ts", "time", "t") or k.endswith("ts") or k.endswith("at") or k.endswith("time"))


def cell(key: str, v: Any) -> str:
    if looks_like_ts(key, v):
        return ts(v)
    if is_num(v):
        return num(v)
    if isinstance(v, bool):
        return yes(v)
    if v is None:
        return NA
    if isinstance(v, (list, dict)):
        return text(v, 80)
    return text(v)


# ---------- sections ----------


def render_signal(sig: dict) -> str:
    price = sig.get("price")
    action = sig.get("action", NA)
    stop, target = sig.get("stopLoss") or 0, sig.get("takeProfit") or 0
    lines = [
        f"- instrument **{sig.get('instId', NA)}** · action **{action}** · strength {num(sig.get('strength'))}"
        f" · reference price {num(price)} at {ts_full(sig.get('ts'))}",
    ]
    risk = None
    if is_num(price) and price > 0 and stop:
        risk = abs(price - stop)
        lines.append(f"- stop {num(stop)} ({pct((stop - price) / price)} from price, 1R = {num(risk)})")
    else:
        lines.append("- stop: none")
    if is_num(price) and price > 0 and target:
        rr = f", {abs(target - price) / risk:.2f}R" if risk else ""
        lines.append(f"- target {num(target)} ({pct((target - price) / price)} from price{rr})")
    else:
        lines.append("- target: none")
    extra = []
    if sig.get("leverage"):
        extra.append(f"leverage {num(sig['leverage'])}x requested")
    if sig.get("maxSlippageBp"):
        extra.append(f"slippage tolerance {num(sig['maxSlippageBp'])} bp")
    if sig.get("reduceFrac"):
        extra.append(f"reduce fraction {num(sig['reduceFrac'])}")
    if sig.get("ttlMs"):
        extra.append(f"TTL {num(sig['ttlMs'] / 1000)} s")
    if extra:
        lines.append("- " + " · ".join(extra))
    lines.append(f"- strategy reason: {text(sig.get('reason')) or NA}")
    return "\n".join(lines)


def render_account(acct: dict, positions: list) -> str:
    lim = acct.get("limits") or {}
    lines = [
        f"- {text(acct.get('name'))} ({acct.get('mode', NA)}) · equity {num(acct.get('equity'))} USDT · cash {num(acct.get('cash'))} USDT"
        f" · today PnL {num(acct.get('dayPnl'))} USDT · breaker tripped: {yes(acct.get('halted'))}"
        f" · open positions {acct.get('openCount', len(positions))}/{lim.get('maxPositions', NA)}",
        f"- hard limits (enforced by the order layer): risk per trade {plain_pct(lim.get('riskPct'))} of equity at strength 1"
        f" · max leverage {num(lim.get('maxLeverage'))}x · max margin per instrument {plain_pct(lim.get('maxInstPct'), 0)}"
        f" · daily loss breaker {plain_pct(lim.get('dailyLossPct'), 1)} · cooldown {lim.get('cooldownMin', NA)} min"
        f" · stop required: {yes(lim.get('requireStop'))} · taker fee {num(lim.get('takerFeeBp'))} bp",
    ]
    equity, day = acct.get("equity"), acct.get("dayPnl")
    if is_num(equity) and equity > 0 and is_num(day) and is_num(lim.get("dailyLossPct")) and lim["dailyLossPct"] > 0:
        used = -day / (equity * lim["dailyLossPct"]) if day < 0 else 0
        lines.append(f"- daily loss budget used: {plain_pct(used, 0)}")
    return "\n".join(lines)


def render_positions(positions: list) -> str:
    rows = []
    for p in positions[-MAX_ROWS:]:
        notional = None
        if all(is_num(p.get(k)) for k in ("qty", "ctVal", "markPx")):
            notional = p["qty"] * p["ctVal"] * p["markPx"]
        rows.append([
            text(p.get("instId")), text(p.get("posSide")), num(p.get("qty")), num(notional), num(p.get("entryPx")),
            num(p.get("markPx")), num(p.get("unrealizedPnl")), num(p.get("leverage")), num(p.get("liqPx")),
            num(p.get("stopLoss") or None), num(p.get("takeProfit") or None), ts(p.get("openedAt")),
        ])
    return table(["inst", "side", "contracts", "notional USDT", "entry", "mark", "uPnL USDT", "lev", "liq", "stop", "target", "opened"], rows)


def render_strategy(strat: dict, inst: dict) -> str:
    params = inst.get("params") or {}
    lines = [
        f"- **{text(strat.get('name'))}** version {text(strat.get('id'))} · timeframe {strat.get('timeframe', NA)}"
        f" · instance #{inst.get('id', NA)} config v{inst.get('configVersion', NA)} · exec {inst.get('exec', NA)}",
    ]
    if strat.get("description"):
        lines.append(f"- {text(strat['description'], 600)}")
    rows = []
    for p in strat.get("params") or []:
        name = p.get("name", "")
        lo, hi = p.get("min"), p.get("max")
        rng = f"{num(lo) if lo is not None else '-∞'} … {num(hi) if hi is not None else '∞'}"
        rows.append([text(name), num(params.get(name, p.get("default"))), num(p.get("default")), rng,
                     yes(p.get("aiTunable")), text(p.get("desc"), 120)])
    lines.append("")
    lines.append(table(["param", "value", "default", "range", "AI-tunable", "meaning"], rows))
    return "\n".join(lines)


BAR_COLS = ["ts", "o", "h", "l", "c", "vol", "volq"]


def render_rows_of_lists(key: str, rows: list) -> str:
    n = len(rows)
    rows = rows[-MAX_ROWS:]
    width = max(len(r) for r in rows)
    if "bar" in key.lower() or "kline" in key.lower() or "candle" in key.lower():
        headers = BAR_COLS[:width] + [f"c{i}" for i in range(len(BAR_COLS), width)]
    else:
        headers = [f"c{i}" for i in range(width)]
    body = []
    for r in rows:
        body.append([cell(headers[i], v) if i < len(r) else NA for i, v in enumerate(list(r) + [None] * (width - len(r)))])
    title = f"**{key}**" + (f" (last {len(rows)} of {n})" if n > len(rows) else "")
    return title + "\n" + table(headers, body)


def render_rows_of_dicts(key: str, rows: list) -> str:
    n = len(rows)
    rows = rows[-MAX_ROWS:]
    headers: list[str] = []
    for r in rows:
        for k in r:
            if k not in headers and len(headers) < 12:
                headers.append(k)
    body = [[cell(h, r.get(h)) for h in headers] for r in rows]
    title = f"**{key}**" + (f" (last {len(rows)} of {n})" if n > len(rows) else "")
    return title + "\n" + table(headers, body)


def series(values: list) -> str:
    n = len(values)
    tail = values[-MAX_SERIES:]
    s = " ".join(num(v) for v in tail)
    return s + (f" (last {len(tail)} of {n}, oldest first)" if n > len(tail) else "")


def flatten(prefix: str, v: Any, scalars: list, blocks: list, depth: int = 0) -> None:
    if isinstance(v, dict):
        if depth >= 3:
            scalars.append((prefix, text(v, 120)))
            return
        for k in sorted(v):
            flatten(f"{prefix}.{k}" if prefix else str(k), v[k], scalars, blocks, depth + 1)
    elif isinstance(v, list):
        if not v:
            scalars.append((prefix, "(empty)"))
        elif all(is_num(x) or x is None for x in v):
            scalars.append((prefix, series(v)))
        elif all(isinstance(x, list) for x in v):
            blocks.append(render_rows_of_lists(prefix, v))
        elif all(isinstance(x, dict) for x in v):
            blocks.append(render_rows_of_dicts(prefix, v))
        else:
            scalars.append((prefix, text(v, 200)))
    else:
        scalars.append((prefix, cell(prefix.rsplit(".", 1)[-1], v)))


def render_market(market: dict) -> str:
    if not market:
        return "(no market context provided)"
    scalars: list = []
    blocks: list = []
    flatten("", market, scalars, blocks)
    parts = []
    if scalars:
        parts.append(table(["field", "value"], [[text(k, 80), v] for k, v in scalars]))
    parts += blocks
    return "\n\n".join(parts)


def render_recent(recent: list) -> str:
    rows = [[ts(r.get("ts")), text(r.get("instId")), text(r.get("action")), text(r.get("decision")), text(r.get("result"), 120) or NA]
            for r in (recent or [])[-MAX_ROWS:]]
    return table(["time", "inst", "signal", "decision", "result"], rows)


def render_review(req: dict) -> str:
    """The context pack for one review."""
    sig = req.get("signal") or {}
    acct = req.get("account") or {}
    positions = req.get("positions") or []
    sections = [
        "# Signal to review\n" + render_signal(sig),
        "# Account\n" + render_account(acct, positions),
        "# Open positions in this account\n" + render_positions(positions),
        "# Strategy\n" + render_strategy(req.get("strategy") or {}, req.get("instance") or {}),
        "# Recent decisions of this instance (oldest first)\n" + render_recent(req.get("recent") or []),
    ]
    memory = (req.get("memory") or "").strip()
    sections.append("# Strategy experience notes (distilled from past outcomes; priors, not rules)\n" + (memory or "(none yet)"))
    head = "\n\n".join(sections)
    market = "# Market context (as of the signal; n/a = unknown)\n" + render_market(req.get("market") or {})
    room = MAX_PACK - len(head) - 200
    if len(market) > room:
        market = market[: max(room, 0)] + "\n…(market context truncated)"
    # market goes second so the signal and its numbers are read together
    first, rest = sections[0], "\n\n".join(sections[1:])
    return f"{first}\n\n{market}\n\n{rest}\n"


def render_reflect(req: dict) -> str:
    """The input for distilling a new experience document."""
    ds = req.get("decisions") or []
    rows = []
    executed = skipped = 0
    for d in ds:
        if d.get("action") == "execute":
            executed += 1
        else:
            skipped += 1
        outcomes = "; ".join(text(o.get("result"), 80) for o in d.get("outcomes") or []) or NA
        rows.append([str(d.get("id", "")), ts(d.get("ts")), text(d.get("instId")), text(d.get("signalAction")), num(d.get("price")),
                     text(d.get("action")), num(d.get("sizeScale")), num(d.get("confidence")),
                     text(d.get("signalReason"), 100), text(d.get("rationale"), 200), outcomes])
    cur = (req.get("current") or "").strip()
    return "\n\n".join([
        f"# Strategy {text(req.get('strategy'))} (timeframe {req.get('timeframe') or NA})\n{text(req.get('description'), 600)}",
        f"# Current experience document (version {req.get('currentVersion', 0)})\n" + (cur or "(none yet)"),
        f"# Decisions since then and before (oldest first): {len(ds)} total, {executed} executed, {skipped} skipped\n"
        + table(["id", "time", "inst", "signal", "price", "AI", "size", "conf", "strategy reason", "AI rationale", "outcome"], rows),
    ]) + "\n"
