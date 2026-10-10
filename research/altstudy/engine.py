"""Multi-instrument hourly backtester for altcoin perp strategies (research mirror of internal/trade).

Conventions (match the Go order layer's backtest model):
- a decision at the close of bar i executes at the open of bar i+1 (market) or rests as a limit order
- stops / targets trigger on bar high/low; the stop wins when both are touched in one bar; a gap
  through the stop fills at the open; the bar an order fills in is checked against its stop
- taker fee on market/stop fills, maker fee on limit fills, plus slippage on market/stop fills
- funding: settled rate × notional at each settlement hour held (longs pay positive rates)
- at most K open positions (pending entries count), one per instrument, re-entry cooldown
"""
from dataclasses import dataclass, field
import numpy as np

H = 3600_000
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "altbt"))
import harness  # noqa: E402  (shared panel loader)

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
PANEL = os.environ.get("ALTSTUDY_PANEL", os.path.join(ROOT, "data", "altbt", "panel.npz"))
STABLES = {"PAXG", "XAUT", "USDC", "USDE", "FDUSD", "TUSD", "DAI"}


def load_panel(path=PANEL):
    """The research/altbt panel (fetch.py); P["crypto"] marks OKX instCategory 1."""
    P = harness.load_panel(path)
    if "crypto" not in P:
        P["crypto"] = np.ones(len(P["insts"]), bool)
    return P


def universe(P, top=200, look_h=720, min_age_h=720, exclude=("BTC", "ETH")):
    """Top-N by trailing USDT turnover, refreshed daily at 00:00 UTC from data before that hour.
    Instruments need min_age_h hours of history; BTC/ETH and non-crypto are excluded."""
    q, c = P["q"], P["c"]
    T, N = c.shape
    base = np.array([i.split("-")[0] for i in P["insts"]])
    ok_inst = ~np.isin(base, list(exclude) + list(STABLES)) & np.asarray(P["crypto"], bool)
    qz = np.nan_to_num(q, nan=0.0).astype(np.float64)
    cs = np.vstack([np.zeros((1, N)), np.cumsum(qz, 0)])
    have = np.vstack([np.zeros((1, N)), np.cumsum(np.isfinite(c), 0)])
    U = np.zeros((T, N), bool)
    day_starts = np.where(P["ts"] % (24 * H) == 0)[0]
    for k, s in enumerate(day_starts):
        e = day_starts[k + 1] if k + 1 < len(day_starts) else T
        if s < look_h:
            continue
        turn = cs[s] - cs[s - look_h]
        age = have[s] - have[max(0, s - min_age_h)]
        elig = ok_inst & (age >= min_age_h * 0.95) & np.isfinite(c[s - 1])
        turn = np.where(elig, turn, -1)
        idx = np.argsort(-turn)[:top]
        idx = idx[turn[idx] > 0]
        U[s:e, idx] = True
    return U


@dataclass
class Cfg:
    K: int = 5                    # max concurrent positions
    margin: float = 125.0         # USDT margin per trade
    lev: float = 8.0              # notional = margin × lev
    loss_cap: float = 2.0         # max loss at the stop, in margins (2 = 200 %)
    min_stop: float = 0.01        # stops tighter than this (fraction) are widened to it
    size_mode: str = "notional"   # "notional": fixed margin×lev; "risk": notional = risk / stop dist
    risk: float = 0.0             # USDT lost at the stop in "risk" mode (0 → loss_cap × margin)
    taker_bp: float = 5.0
    maker_bp: float = 2.0
    slip_bp: float = 5.0
    max_hold: int = 72            # hours; 0 = none
    cooldown: int = 4             # hours after a close before re-entering the instrument
    limit_wait: int = 4           # hours a limit entry rests
    trail_k: float = 0.0          # chandelier trail in ATRs (0 = off)
    trail_after_R: float = 0.0    # start trailing once the trade is this many R in profit
    be_after_R: float = 0.0       # move the stop to entry once this many R in profit (0 = off)
    tp_R: float = 0.0             # take profit at this many R (0 = off)
    stall_h: int = 0              # exit at the open after this many hours if the trade never reached stall_R
    stall_R: float = 0.5
    leg_hold: dict = field(default_factory=dict)   # leg -> max hold hours (overrides max_hold)
    exit_legs: tuple = (1,)       # legs the exit signal applies to
    compound: bool = False        # margin scales with equity / start equity
    equity0: float = 3000.0


@dataclass
class Pos:
    j: int
    side: int
    entry: float
    stop: float
    stop0: float
    tp: float
    notional: float
    t_in: int
    fee_in: float
    ext: float           # best price since entry (high for long, low for short)
    funding: float = 0.0
    reason: str = ""
    leg: int = 1


def simulate(P, sig, cfg: Cfg, t0=0, t1=None, U=None):
    """sig: enter[T,N] int8 (+1/-1 at bar close), score[T,N] (higher first), stop[T,N] (fraction of
    entry), optional exit[T,N] bool (close at next open), limit[T,N] (limit price; NaN = market),
    atr[T,N] (price units, for trailing). Returns (trades list, equity array over [t0,t1))."""
    o, h, l, c, fund = P["o"], P["h"], P["l"], P["c"], P["fund"]
    T, N = c.shape
    t1 = T if t1 is None else t1
    enter, score, stopf = sig["enter"], sig["score"], sig["stop"]
    exit_ = sig.get("exit")
    limit = sig.get("limit")
    atr = sig.get("atr")
    legs = sig.get("leg")
    tf, mf, sl = cfg.taker_bp / 1e4, cfg.maker_bp / 1e4, cfg.slip_bp / 1e4
    dcap = cfg.loss_cap / cfg.lev
    pos: dict[int, Pos] = {}
    pend: dict[int, tuple] = {}      # j -> (side, stopfrac, limit_px or nan, expires_at, score, reason)
    pend_exit: set[int] = set()
    last_close = np.full(N, -10**9)
    trades = []
    eq = np.zeros(t1 - t0)
    realized = 0.0

    def margin_now():
        if not cfg.compound:
            return cfg.margin
        return max(0.0, cfg.margin * (cfg.equity0 + realized) / cfg.equity0)

    def close(p: Pos, px, i, kind, fee_rate):
        nonlocal realized
        gross = p.side * (px / p.entry - 1) * p.notional
        fee = fee_rate * p.notional * px / p.entry
        pnl = gross - p.fee_in - fee - p.funding
        realized += pnl
        R = abs(p.entry - p.stop0) / p.entry * p.notional
        trades.append(dict(leg=p.leg, j=p.j, inst=P["insts"][p.j], side=p.side, t_in=p.t_in, t_out=i, entry=p.entry, exit=px,
                           notional=p.notional, gross=gross, fees=p.fee_in + fee, funding=p.funding, pnl=pnl,
                           R=pnl / R if R > 0 else np.nan, kind=kind, stopd=abs(p.entry - p.stop0) / p.entry,
                           reason=p.reason))
        last_close[p.j] = i
        del pos[p.j]

    for i in range(t0, t1):
        # 1. pending exits at the open
        for j in list(pend_exit):
            if j in pos and np.isfinite(o[i, j]):
                p = pos[j]
                close(p, o[i, j] * (1 - p.side * sl), i, "signal", tf)
            pend_exit.discard(j)
        # 2. time limits at the open
        if cfg.max_hold or cfg.leg_hold:
            for j in list(pos):
                p = pos[j]
                mh = cfg.leg_hold.get(p.leg, cfg.max_hold)
                if mh and i - p.t_in >= mh and np.isfinite(o[i, j]):
                    close(p, o[i, j] * (1 - p.side * sl), i, "time", tf)
        if cfg.stall_h:
            for j in list(pos):
                p = pos[j]
                if i - p.t_in == cfg.stall_h and np.isfinite(o[i, j]):
                    d0 = abs(p.entry - p.stop0)
                    if d0 > 0 and p.side * (p.ext - p.entry) / d0 < cfg.stall_R:
                        close(p, o[i, j] * (1 - p.side * sl), i, "stall", tf)
        # 3. pending entries
        for j in list(pend):
            side, sf, lpx, exp, sc, rsn, leg = pend[j]
            if not np.isfinite(o[i, j]):
                if i >= exp:
                    del pend[j]
                continue
            px = None
            if not np.isfinite(lpx):
                px, fee_rate = o[i, j] * (1 + side * sl), tf
            elif side > 0 and l[i, j] <= lpx:
                px, fee_rate = min(lpx, o[i, j]), mf
            elif side < 0 and h[i, j] >= lpx:
                px, fee_rate = max(lpx, o[i, j]), mf
            if px is None:
                if i >= exp:
                    del pend[j]
                continue
            del pend[j]
            m = margin_now()
            if m <= 0:
                continue
            notional = m * cfg.lev
            if cfg.size_mode == "risk":
                risk = cfg.risk or cfg.loss_cap * m
                notional = min(notional * 4, risk / sf)   # gross cap 4× the fixed notional
            st = px * (1 - side * sf)
            tp = px * (1 + side * cfg.tp_R * sf) if cfg.tp_R > 0 else np.nan
            pos[j] = Pos(j, side, px, st, st, tp, notional, i, fee_rate * notional, px, reason=rsn, leg=leg)
        # 4. funding at settlement hours (rate stored at the settlement hour)
        for j, p in pos.items():
            r = fund[i, j]
            if np.isfinite(r) and r != 0:
                p.funding += p.side * r * p.notional * (c[i - 1, j] / p.entry if np.isfinite(c[i - 1, j]) else 1)
        # 5. stops / targets inside bar i
        for j in list(pos):
            p = pos[j]
            if not np.isfinite(h[i, j]):
                continue
            if p.side > 0:
                if l[i, j] <= p.stop:
                    close(p, min(p.stop, o[i, j]) * (1 - sl), i, "stop", tf)
                    continue
                if np.isfinite(p.tp) and h[i, j] >= p.tp:
                    close(p, max(p.tp, o[i, j]), i, "target", mf)
                    continue
                p.ext = max(p.ext, h[i, j])
            else:
                if h[i, j] >= p.stop:
                    close(p, max(p.stop, o[i, j]) * (1 + sl), i, "stop", tf)
                    continue
                if np.isfinite(p.tp) and l[i, j] <= p.tp:
                    close(p, min(p.tp, o[i, j]), i, "target", mf)
                    continue
                p.ext = min(p.ext, l[i, j])
            # 6. stop management at the close
            d0 = abs(p.entry - p.stop0)
            prof = p.side * (p.ext - p.entry) / d0 if d0 > 0 else 0
            if cfg.be_after_R > 0 and prof >= cfg.be_after_R:
                be = p.entry * (1 + p.side * 2 * tf)
                p.stop = max(p.stop, be) if p.side > 0 else min(p.stop, be)
            if cfg.trail_k > 0 and atr is not None and prof >= cfg.trail_after_R and np.isfinite(atr[i, j]):
                ts_ = p.ext - p.side * cfg.trail_k * atr[i, j]
                p.stop = max(p.stop, ts_) if p.side > 0 else min(p.stop, ts_)
        # 7. signal exits at the close
        if exit_ is not None:
            for j, p in pos.items():
                if p.leg not in cfg.exit_legs:
                    continue
                e = exit_[i, j]
                if e == p.side * -1 or e == 2:
                    pend_exit.add(j)
        # 8. new entries at the close
        free = cfg.K - len(pos) - len(pend)
        if free > 0:
            row = enter[i]
            cand = np.nonzero(row)[0]
            if U is not None and len(cand):
                cand = cand[U[i, cand]]
            if len(cand):
                cand = cand[np.argsort(-score[i, cand])]
                for j in cand:
                    if free <= 0:
                        break
                    if j in pos or j in pend or i - last_close[j] < cfg.cooldown:
                        continue
                    sf = stopf[i, j]
                    if not np.isfinite(sf) or sf <= 0:
                        continue
                    sf = min(max(sf, cfg.min_stop), dcap)
                    lpx = limit[i, j] if limit is not None else np.nan
                    side = int(row[j])
                    pend[j] = (side, sf, lpx, i + (cfg.limit_wait if np.isfinite(lpx) else 1), score[i, j],
                               sig.get("reason", ""), int(legs[i, j]) if legs is not None else 1)
                    free -= 1
        # 9. mark to market
        unreal = 0.0
        for j, p in pos.items():
            px = c[i, j]
            if np.isfinite(px):
                unreal += p.side * (px / p.entry - 1) * p.notional - p.fee_in - p.funding
        eq[i - t0] = realized + unreal
    return trades, eq


def stats(trades, eq, ts, cfg: Cfg, equity0=None):
    """Summary in USDT and in margins (scale-free, comparable across margin sizes)."""
    e0 = equity0 or cfg.equity0
    n = len(trades)
    pnl = np.array([t["pnl"] for t in trades]) if n else np.zeros(0)
    curve = e0 + eq
    peak = np.maximum.accumulate(np.maximum(curve, e0))
    dd = (curve - peak)
    days = (ts[-1] - ts[0]) / (24 * H) if len(ts) > 1 else 1
    daily = curve[::24]
    dret = np.diff(daily) / np.maximum(daily[:-1], 1e-9)
    sharpe = dret.mean() / dret.std() * np.sqrt(365) if len(dret) > 5 and dret.std() > 0 else np.nan
    wins = pnl[pnl > 0]
    loss = pnl[pnl <= 0]
    return dict(
        trades=n, net=float(eq[-1]) if len(eq) else 0.0, net_margins=float(eq[-1] / cfg.margin) if len(eq) else 0,
        maxdd=float(dd.min()), maxdd_margins=float(dd.min() / cfg.margin), maxdd_pct=float((dd / peak).min()),
        win=float((pnl > 0).mean()) if n else np.nan,
        pf=float(wins.sum() / -loss.sum()) if len(loss) and loss.sum() < 0 else np.nan,
        avgR=float(np.nanmean([t["R"] for t in trades])) if n else np.nan,
        sharpe=float(sharpe), per_day=n / days,
        fees=float(sum(t["fees"] for t in trades)), funding=float(sum(t["funding"] for t in trades)),
        long_pnl=float(sum(t["pnl"] for t in trades if t["side"] > 0)),
        short_pnl=float(sum(t["pnl"] for t in trades if t["side"] < 0)),
        min_equity=float(curve.min()),
        net_x5=float(eq[-1] - np.sort(pnl)[-5:].sum()) if n >= 5 else np.nan,
        pos_months=float(np.mean(np.diff(curve[::720]) > 0)) if len(curve) > 1440 else np.nan,
    )
