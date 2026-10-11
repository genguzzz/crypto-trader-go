"""Offline multi-instrument backtest for ctg strategies over an hourly OKX panel.

Runs the strategy file itself through the real SDK runtime (`ctg.runtime.Runtime.handle`), with a
Python mirror of the Go order layer's backtest model (internal/trade: risk.go sizing, broker.go bar
model). It exists because the production database keeps ~50 days of 1H history, while strategy
research needs one to two years across hundreds of instruments.

Model (same as the Go backtest unless noted):
- base resolution 1H; strategy events at the close of each strategy-timeframe bar (UTC-aligned)
- a signal fills at the next base bar's open ± half spread (slip_bp) — market only, no book walk
- per base bar and position: open gap through the liquidation price → liquidation; stop / target
  on high/low (stop first; a gap through the level fills at the open); intrabar liquidation; trailing
  stop moves after the bar; time limits close at the first base bar open at or after the limit
- sizing: qty = equity × riskPct × strength / |entry − stop| (USDT notional per coin price), leverage
  = min(signal, maxLeverage, the bound keeping the liquidation 25 % of the stop distance beyond
  the stop), margin capped by cash and maxInstPct; lots and min size are not modelled
- funding: settled rates from the panel when known, charged at each settlement hour held through
- universe: Screener(scope="market") = the top N crypto swaps by 12h USDT turnover, refreshed on the
  screener's interval; static lists are used as given
Not modelled: order book depth, partial fills, AI review / confirmations, maintenance-margin tiers.
"""
from __future__ import annotations

import json
import math
import os
import sys
from dataclasses import dataclass, field

import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "strategies"))

from ctg.runtime import Runtime, load_classes, pick_class  # noqa: E402

H = 3600_000
TF_H = {"1H": 1, "4H": 4, "1D": 24}
REFRESH_H = {"5m": 1, "15m": 1, "1H": 1, "4H": 4, "1D": 24}
LIQ_BUFFER = 0.25


def default_limits():
    return dict(maxPositions=5, maxLeverage=8, riskPct=0.1, maxInstPct=0.3, dailyLossPct=0, slippageBp=10,
                cooldownMin=0, takerFeeBp=5, makerFeeBp=2, requireStop=True)


@dataclass
class Pos:
    j: int
    inst: str
    side: int              # +1 long / -1 short
    qty: float             # coins
    entry: float
    margin: float
    lever: float
    stop: float
    target: float
    trail: float           # trailing distance (price), 0 = off
    best: float
    t_open: int            # ms
    time_limit: int        # ms, 0 = none
    liq: float
    fee_in: float
    funding: float = 0.0
    reason: str = ""


@dataclass
class Order:
    j: int
    inst: str
    kind: str              # open | close
    side: int = 0
    qty: float = 0.0
    stop: float = 0.0
    target: float = 0.0
    lever: float = 0.0
    trail: float = 0.0
    time_limit: int = 0
    reason: str = ""


class FakeConn:
    """Stands in for the Unix-socket connection: queries and signals are answered by the host."""

    def __init__(self, host: "Host"):
        self.host = host
        self.replies: list[dict] = []

    def send(self, msg: dict) -> None:
        t = msg.get("type")
        if t == "query":
            try:
                data = self.host.query(msg["method"], msg.get("args") or {})
                self.replies.append({"type": "result", "qid": msg["qid"], "data": data})
            except Exception as e:  # mirrors the host's error reply
                self.replies.append({"type": "error", "qid": msg["qid"], "error": str(e)})
        elif t == "signal":
            sid, err = self.host.signal(msg)
            if err:
                self.replies.append({"type": "error", "qid": msg["qid"], "error": err})
            else:
                self.replies.append({"type": "signal_ack", "qid": msg["qid"], "id": sid})
        elif t == "log":
            self.host.logs.append((self.host.now, msg.get("level"), msg.get("msg")))
        elif t == "status":
            self.host.status = msg.get("msg")
        # state / notify: nothing to do offline

    def recv(self):
        return self.replies.pop(0) if self.replies else None


class Host:
    def __init__(self, panel: dict, strategy_file: str, params: dict | None = None, class_name: str = "",
                 deposit: float = 3000.0, limits: dict | None = None, start_ms: int = 0, end_ms: int = 0,
                 slip_bp: float = 3.0, mmr: float = 0.005, universe: dict | list | None = None, verbose: bool = False,
                 funding_default: float = 0.0001):
        self.P = panel
        self.ts = panel["ts"]
        self.insts = list(panel["insts"])
        self.idx = {s: j for j, s in enumerate(self.insts)}
        self.o, self.h, self.l, self.c, self.q = (panel[k] for k in "ohlcq")
        self.fund = panel.get("fund")
        # OKX serves only ~3 months of funding history: before an instrument's first known rate, charge
        # funding_default per 8h settlement (00/08/16 UTC; longs pay, shorts receive)
        self.funding_default = funding_default
        known = np.isfinite(self.fund) if self.fund is not None else np.zeros(self.c.shape, bool)
        self.fund_from = np.where(known.any(0), np.argmax(known, 0), len(self.ts))
        T, N = self.c.shape
        crypto = panel.get("crypto")
        self.crypto = np.ones(N, bool) if crypto is None else np.asarray(crypto, bool)
        code = open(strategy_file).read()
        self.cls = pick_class(load_classes(code, os.path.basename(strategy_file)), class_name)
        self.limits = {**default_limits(), **(limits or {})}
        self.deposit = float(deposit)
        self.slip = slip_bp / 1e4
        self.mmr = mmr
        self.verbose = verbose
        meta = self.cls.meta()
        self.tf = meta["timeframe"]
        if self.tf not in TF_H:
            raise ValueError(f"timeframe {self.tf} not supported offline (1H/4H/1D)")
        self.tfh = TF_H[self.tf]
        self.uspec = universe if universe is not None else meta["universe"]
        self.params = self._wire_params(params or {})
        self.i0 = int(np.searchsorted(self.ts, start_ms)) if start_ms else 0
        self.i1 = int(np.searchsorted(self.ts, end_ms)) if end_ms else T
        # rolling 12h turnover for the market screener; like the Go screener, an instrument missing any
        # of the 12 hours is left out (NaN)
        qz = np.nan_to_num(self.q, nan=0.0).astype(np.float64)
        cs = np.vstack([np.zeros((1, N)), np.cumsum(qz, 0)])
        cnt = np.vstack([np.zeros((1, N)), np.cumsum(np.isfinite(self.q), 0)])
        q12 = np.where(cnt[12:] - cnt[:-12] == 12, cs[12:] - cs[:-12], np.nan)   # hours i-11..i
        self.q12 = np.vstack([np.full((11, N), np.nan), q12])
        # state
        self.now = 0
        self.i = 0
        self.realized = 0.0
        self.pos: dict[int, Pos] = {}
        self.orders: list[Order] = []
        self.pending_inst: set[int] = set()
        self.last_close: dict[int, int] = {}
        self.marks = np.full(N, np.nan)
        self.trades: list[dict] = []
        self.equity: list[tuple[int, float]] = []
        self.logs: list = []
        self.signals = {"ordered": 0, "rejected": 0}
        self.reject_reasons: dict[str, int] = {}
        self.status = ""
        self.cur_universe: list[str] = []
        self.sig_id = 0
        self.day_key, self.day_start = None, self.deposit
        self.fills_out: list[dict] = []

    # ------------------------------------------------------------------ params
    def _wire_params(self, params: dict) -> dict:
        """Accept human values (choice strings, bools) or wire numbers."""
        specs = self.cls.param_specs()
        out = {}
        for k, v in params.items():
            if k not in specs:
                raise KeyError(f"unknown param {k!r}; have {sorted(specs)}")
            out[k] = specs[k].encode(v) if isinstance(v, (str, bool)) else float(v)
        return out

    # ------------------------------------------------------------------ account
    def unreal(self) -> float:
        u = 0.0
        for p in self.pos.values():
            m = self.marks[p.j]
            if math.isfinite(m):
                u += p.side * (m - p.entry) * p.qty
        return u

    def equity_now(self) -> float:
        return self.deposit + self.realized + self.unreal() - sum(p.funding for p in self.pos.values())

    def cash(self) -> float:
        return self.equity_now() - sum(p.margin for p in self.pos.values())

    def _limits_wire(self):
        return dict(self.limits)

    # ------------------------------------------------------------------ queries
    def query(self, method: str, a: dict):
        if method == "bars":
            return self._bars(a["instId"], a.get("tf") or self.tf, int(a.get("n") or 200))
        if method == "account":
            return {"id": 1, "name": "offline", "mode": "backtest", "deposit": self.deposit, "equity": self.equity_now(),
                    "cash": self.cash(), "limits": self._limits_wire(), "openCount": len(self.pos)}
        if method == "positions":
            return [self._pos_wire(p) for p in self.pos.values()]
        if method == "position":
            j = self.idx.get(a.get("instId"))
            p = self.pos.get(j) if j is not None else None
            return self._pos_wire(p) if p else None
        if method == "spec":
            return {"instId": a.get("instId"), "ctVal": 1, "lotSz": 0, "minSz": 0, "tickSz": 0, "maxLever": 100}
        if method == "screener":
            return [{"instId": s, "rank": k + 1, "long": None, "short": None} for k, s in enumerate(self.cur_universe)]
        if method in ("series", "factors", "score"):
            return [] if method == "series" else {}
        raise RuntimeError(f"query {method} not supported offline")

    def _pos_wire(self, p: Pos):
        return {"instId": p.inst, "posSide": "long" if p.side > 0 else "short", "qty": p.qty, "entryPx": p.entry,
                "margin": p.margin, "lever": p.lever, "liqPx": p.liq, "openedAt": p.t_open,
                "upl": p.side * (self.marks[p.j] - p.entry) * p.qty if math.isfinite(self.marks[p.j]) else 0.0}

    def _bars(self, inst: str, tf: str, n: int):
        j = self.idx.get(inst)
        if j is None:
            return []
        k = TF_H.get(tf)
        if k is None:
            raise RuntimeError(f"tf {tf} not supported offline")
        end = self.i + 1                          # hours < end are complete at the current clock
        start = max(0, end - k * (n + 1) - k)
        ts = self.ts[start:end]
        first = np.nonzero(ts % (k * H) == 0)[0]
        if not len(first):
            return []
        s0 = start + first[0]
        m = (end - s0) // k
        if m <= 0:
            return []
        sl = slice(s0, s0 + m * k)
        o = self.o[sl, j].reshape(m, k)
        h = self.h[sl, j].reshape(m, k)
        l = self.l[sl, j].reshape(m, k)
        c = self.c[sl, j].reshape(m, k)
        q = self.q[sl, j].reshape(m, k)
        ok = np.isfinite(c).all(1)
        rows = []
        bts = self.ts[s0:s0 + m * k:k]
        for r in np.nonzero(ok)[0][-n:]:
            rows.append([int(bts[r]), float(o[r, 0]), float(np.max(h[r])), float(np.min(l[r])), float(c[r, -1]),
                         0.0, float(np.sum(q[r]))])
        return rows

    # ------------------------------------------------------------------ signals
    def _reject(self, why: str):
        self.signals["rejected"] += 1
        key = why.split("：")[0][:40]
        self.reject_reasons[key] = self.reject_reasons.get(key, 0) + 1
        return None, why

    def signal(self, w: dict):
        self.sig_id += 1
        inst = w.get("instId")
        j = self.idx.get(inst)
        if j is None:
            return self._reject(f"未知合约 {inst}")
        act = w.get("action")
        if j in self.pending_inst:
            return self._reject("同一合约已有待处理信号")
        if act in ("close", "reduce"):
            if j not in self.pos:
                return self._reject("无持仓")
            self.orders.append(Order(j, inst, "close", reason=w.get("reason", "")))
            self.pending_inst.add(j)
            self.signals["ordered"] += 1
            return self.sig_id, None
        side = 1 if act == "open_long" else -1
        L = self.limits
        if L.get("dailyLossPct", 0) > 0 and self._day_pnl() <= -L["dailyLossPct"] * self.day_start:
            return self._reject("日内熔断")
        cd = L.get("cooldownMin", 0) * 60_000
        if cd and self.now - self.last_close.get(j, -10**15) < cd:
            return self._reject("冷却中")
        stop = float(w.get("stopLoss") or 0)
        if L.get("requireStop", True) and stop <= 0:
            return self._reject("账户要求开仓信号带止损")
        if j in self.pos:
            if self.pos[j].side != side:
                return self._reject("已有反向持仓")
            return self._reject("加仓未在离线回测中模拟")
        opens = len(self.pos) + sum(1 for o in self.orders if o.kind == "open")
        if opens >= L["maxPositions"]:
            return self._reject("已达最大持仓数")
        ref = self.c[self.i, j]
        if not (ref > 0):
            return self._reject("缺少参考价")
        if stop > 0 and ((side > 0 and stop >= ref) or (side < 0 and stop <= ref)):
            return self._reject("止损价位于错误一侧")
        eq = self.equity_now()
        strength = min(max(float(w.get("strength") or 1), 0), 1) or 1
        dist = abs(ref - stop)
        lev = float(w.get("leverage") or 0)
        if lev <= 0 or lev > L["maxLeverage"]:
            lev = L["maxLeverage"]
        bound = self._lev_for_stop(ref, stop, dist, side)
        if bound > 0:
            lev = min(lev, bound)
        lev = math.floor(lev * 100) / 100
        if lev < 1 and side > 0:
            lev = 1
        if lev < 1:
            return self._reject("止损距离过大")
        qty = eq * L["riskPct"] * strength / dist
        max_margin = min(self.cash() * 0.98, eq * L["maxInstPct"])
        if max_margin <= 0:
            return self._reject("可用保证金不足")
        qty = min(qty, max_margin * lev / ref)
        if qty <= 0:
            return self._reject("数量为 0")
        tp = float(w.get("takeProfit") or 0)
        self.orders.append(Order(j, inst, "open", side, qty, stop, tp, lev, float(w.get("trailDist") or 0),
                                 int(w.get("timeLimitMs") or 0), w.get("reason", "")))
        self.pending_inst.add(j)
        self.signals["ordered"] += 1
        return self.sig_id, None

    def _lev_for_stop(self, entry, stop, dist, side):
        if side > 0:
            s = stop - LIQ_BUFFER * dist
            d = 1 - s * (1 - self.mmr) / entry
        else:
            s = stop + LIQ_BUFFER * dist
            d = s * (1 + self.mmr) / entry - 1
        return 1 / d if d > 0 else 0

    def _day_pnl(self):
        return self.equity_now() - self.day_start

    # ------------------------------------------------------------------ fills
    def _fee(self, notional, maker=False):
        return notional * (self.limits["makerFeeBp"] if maker else self.limits["takerFeeBp"]) / 1e4

    def _close(self, p: Pos, px: float, kind: str):
        realized = p.side * (px - p.entry) * p.qty
        fee = self._fee(px * p.qty)
        if kind == "liquidation":
            realized = -p.margin
            fee = 0.0
        self.realized += realized - fee - p.funding
        pnl = realized - fee - p.fee_in - p.funding
        self.trades.append(dict(inst=p.inst, side="long" if p.side > 0 else "short", t_in=p.t_open, t_out=self.now,
                                entry=p.entry, exit=px, qty=p.qty, notional=p.qty * p.entry, margin=p.margin,
                                lever=p.lever, stop=p.stop, pnl=pnl, fees=fee + p.fee_in, funding=p.funding, kind=kind,
                                reason=p.reason))
        del self.pos[p.j]
        self.last_close[p.j] = self.now
        self.fills_out.append({"instId": p.inst, "posSide": "long" if p.side > 0 else "short",
                               "side": "sell" if p.side > 0 else "buy", "qty": p.qty, "px": px, "fee": fee,
                               "realizedPnl": realized, "kind": kind, "closed": True, "ts": self.now})

    def _process_hour(self, i: int):
        """Open of base bar i: pending orders, then stops / targets / liquidations inside the bar."""
        self.now = int(self.ts[i])
        o, h, l = self.o[i], self.h[i], self.l[i]
        orders, self.orders = self.orders, []
        for od in orders:
            j = od.j
            if not np.isfinite(o[j]):
                self.orders.append(od)    # no bar: try again next hour
                continue
            self.pending_inst.discard(j)
            if od.kind == "close":
                p = self.pos.get(j)
                if p:
                    self._close(p, o[j] * (1 - p.side * self.slip), "fill")
                continue
            px = o[j] * (1 + od.side * self.slip)
            if od.stop > 0 and ((od.side > 0 and od.stop >= px) or (od.side < 0 and od.stop <= px)):
                continue                  # opened through the stop: the order layer would reject the stop
            margin = od.qty * px / od.lever
            fee = self._fee(od.qty * px)
            per = margin / od.qty
            liq = (px + per) / (1 + self.mmr) if od.side < 0 else max(0.0, (px - per) / (1 - self.mmr))
            self.realized -= fee
            p = Pos(j, od.inst, od.side, od.qty, px, margin, od.lever, od.stop, od.target, od.trail, px, self.now,
                    od.time_limit, liq, fee, reason=od.reason)
            self.pos[j] = p
            self.fills_out.append({"instId": od.inst, "posSide": "long" if od.side > 0 else "short",
                                   "side": "buy" if od.side > 0 else "sell", "qty": od.qty, "px": px, "fee": fee,
                                   "realizedPnl": 0.0, "kind": "fill", "closed": False, "ts": self.now})
        for j in list(self.pos):
            p = self.pos[j]
            if not np.isfinite(o[j]):
                continue
            if p.time_limit and self.now >= p.t_open + p.time_limit:
                self._close(p, o[j] * (1 - p.side * self.slip), "time")
                continue
            if p.liq > 0 and ((p.side > 0 and o[j] <= p.liq) or (p.side < 0 and o[j] >= p.liq)):
                self._close(p, p.liq, "liquidation")
                continue
            hit = None
            if p.stop > 0 and ((p.side > 0 and l[j] <= p.stop) or (p.side < 0 and h[j] >= p.stop)):
                px = min(p.stop, o[j]) if p.side > 0 else max(p.stop, o[j])
                hit = (px * (1 - p.side * self.slip), "stop")
            elif p.target > 0 and ((p.side > 0 and h[j] >= p.target) or (p.side < 0 and l[j] <= p.target)):
                px = max(p.target, o[j]) if p.side > 0 else min(p.target, o[j])
                hit = (px * (1 - p.side * self.slip), "target")
            if hit:
                self._close(p, hit[0], hit[1])
                continue
            if p.liq > 0 and ((p.side > 0 and l[j] <= p.liq) or (p.side < 0 and h[j] >= p.liq)):
                self._close(p, p.liq, "liquidation")
                continue
            if p.trail > 0:
                p.best = max(p.best, h[j]) if p.side > 0 else min(p.best, l[j])
                ns = p.best - p.side * p.trail
                p.stop = max(p.stop, ns) if p.side > 0 else (min(p.stop, ns) if p.stop > 0 else ns)
        # funding at settlement hours (rate stored at the settlement hour)
        settle = self.now % (8 * H) == 0
        if self.fund is not None or self.funding_default:
            for p in self.pos.values():
                r = self.fund[i, p.j] if self.fund is not None else np.nan
                if not np.isfinite(r) and i < self.fund_from[p.j] and settle:
                    r = self.funding_default
                if p.t_open < self.now and np.isfinite(r) and r != 0:
                    mk = self.marks[p.j] if math.isfinite(self.marks[p.j]) else p.entry
                    p.funding += p.side * r * p.qty * mk
        self.marks = np.where(np.isfinite(self.c[i]), self.c[i], self.marks)

    # ------------------------------------------------------------------ universe
    def _universe_at(self, i: int) -> list[str]:
        u = self.uspec
        if isinstance(u, dict) and "screener" in u:
            sc = u["screener"]
            if sc.get("scope") != "market" or sc.get("rule"):
                raise RuntimeError("offline backtest supports Screener(scope='market') or static universes")
            top = int(sc.get("top") or 20)
            t = np.where(self.crypto & np.isfinite(self.q12[i]), self.q12[i], -1)
            order = np.argsort(-t)[:top]
            return [self.insts[j] for j in order if t[j] > 0]
        if isinstance(u, dict):
            return list(u.get("static") or [])
        return list(u)

    # ------------------------------------------------------------------ run
    def _event(self, rt: Runtime, msg: dict):
        msg["seq"] = 1
        msg["now"] = self.now
        err = rt.handle(msg)
        if err:
            self.logs.append((self.now, "error", err))
            if self.verbose:
                print(err, file=sys.stderr)

    def _deliver_fills(self, rt: Runtime):
        fills, self.fills_out = self.fills_out, []
        for f in fills:
            self._event(rt, {"type": "fill", "fill": f})

    def run(self, progress: bool = False):
        rt = Runtime(FakeConn(self), self.cls)
        rt.ctx.timeframe = self.tf
        self.i = max(self.i0 - 1, 0)
        self.now = int(self.ts[self.i]) + H
        self._event(rt, {"type": "init", "instance": {"id": 1, "params": self.params, "universe": {}}, "mode": "backtest",
                         "timeframe": self.tf})
        sc = self.uspec.get("screener") if isinstance(self.uspec, dict) else None
        refresh = REFRESH_H.get((sc or {}).get("refresh") or "1H", 1) if sc else 0
        first = True
        n_hours = self.i1 - self.i0
        for i in range(self.i0, self.i1):
            self.i = i
            self._process_hour(i)
            self._deliver_fills(rt)
            self.now = int(self.ts[i]) + H     # close of base bar i
            dk = (self.now + 8 * H) // (24 * H)
            if dk != self.day_key:
                self.day_key, self.day_start = dk, self.equity_now()
            # the screener refreshes on its own interval (like the Go service), the first time at start
            if first or (refresh and self.now % (refresh * H) == 0):
                cur = self._universe_at(i)
                added = [s for s in cur if s not in self.cur_universe]
                removed = [s for s in self.cur_universe if s not in cur]
                if first or added or removed:
                    self.cur_universe = cur
                    self._event(rt, {"type": "universe", "added": added, "removed": removed, "current": cur})
                first = False
            if self.now % (self.tfh * H) == 0:
                k = self.tfh
                bars = []
                for s in self.cur_universe:
                    j = self.idx[s]
                    seg = slice(i - k + 1, i + 1)
                    c = self.c[seg, j]
                    if i - k + 1 < 0 or not np.isfinite(c).all():
                        continue
                    bars.append({"instId": s, "bar": {"ts": int(self.ts[i - k + 1]), "o": float(self.o[i - k + 1, j]),
                                                      "h": float(np.max(self.h[seg, j])), "l": float(np.min(self.l[seg, j])),
                                                      "c": float(c[-1]), "vol": 0.0,
                                                      "volq": float(np.sum(self.q[seg, j]))}})
                if bars:
                    self._event(rt, {"type": "bars", "tf": self.tf, "bars": bars})
            self.equity.append((self.now, self.equity_now()))
            if progress and (i - self.i0) % 720 == 0:
                print(f"  {100 * (i - self.i0) / max(n_hours, 1):5.1f}%  equity {self.equity_now():10.1f}  "
                      f"open {len(self.pos)}  trades {len(self.trades)}", file=sys.stderr, flush=True)
        return self


def load_panel(path: str) -> dict:
    """Load a panel written by fetch.py. A sidecar <panel>.noncrypto.json (list of bases) can mark
    non-crypto swaps in panels that lack the `crypto` array."""
    z = np.load(path, allow_pickle=False)
    P = {k: z[k] for k in z.files}
    P["insts"] = [str(x) for x in P["insts"]]
    side = os.path.splitext(path)[0] + ".noncrypto.json"
    if "crypto" not in P and os.path.exists(side):
        non = set(json.load(open(side)))
        P["crypto"] = np.array([s.split("-")[0] not in non for s in P["insts"]])
    return P
