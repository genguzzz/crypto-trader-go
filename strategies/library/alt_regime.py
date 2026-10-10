# 内置策略：山寨币状态趋势（只读示例，可在编辑器里「复制为新策略」后修改）。
#
# 研究过程和数据见 docs/research-alt-perps.md。一句话：山寨币合约里能被历史数据反复验证的优势只有两类——
#   1 趋势突破做多：牛市状态下，放量突破 N 根 4H 新高的币，少数会走出几倍的大行情。大部分突破会小亏出局，
#     利润集中在少数大行情上（正偏度），所以不能早早锁利，用“跌回通道中轨”离场，让赢家奔跑。
#   2 熊市做空弱势币：大部分山寨币跌破 7 日均线（市场宽度低）时，做空过去 72 小时相对 BTC 最弱的币，
#     持有 72 小时。它在熊市里赚“稳定的小钱”，正好和趋势腿互补（一个吃牛市，一个吃熊市）。
# 两条腿共用一组持仓名额（默认最多 5 笔），每 4 小时 K 线收盘时决策一次。
#
# 仓位按主人的习惯：每笔保证金 × 杠杆（默认 8 倍）= 名义仓位；止损按 K 线（ATR）动态计算，
# 但单笔最大亏损不超过保证金的 loss_cap 倍（默认 200%，8 倍下 = 价格反向 25%）。
# 每笔保证金可以手填；填 0 时按「激进度」从账户权益自动算出合理上限（见 _margin）。
#
# 注意（订单层是逐仓）：8 倍逐仓在价格反向约 12% 时就会强平，比 25% 的止损先到。订单层会自动把杠杆调低，
# 让强平价落在止损之外——名义仓位和止损时的亏损不变，只是占用的保证金更多。等价于全仓下的“单笔最多亏 2 倍保证金”。
#
# 本文件的注释是中文的，这是仓库“全部用英文”约定的例外：内置策略也是给主人看的教材。
import math

import numpy as np

from ctg import Bar, Bool, Choice, Float, Int, Screener, Signal, Strategy, ta

BTC = "BTC-USDT-SWAP"
SKIP = {"BTC-USDT-SWAP", "ETH-USDT-SWAP"}   # 大币不在本策略的交易范围内（BTC 只用来判断大盘状态）
KEEP = 240                                  # 每个币在内存里保留的 4H K 线根数（40 天）
DAY = 6                                     # 一天 = 6 根 4H K 线
BAR_MS = 4 * 3600_000
MAX_STOP = 0.5                              # 止损距离硬上限 50%（低杠杆时「亏损倍数 ÷ 杠杆」可能 ≥ 100%）
STYLES = {
    # 风格预设：信号快慢 + 是否做空突破。dd95 = 回测蒙特卡洛估计的一年 95% 分位最大回撤（单位：单笔保证金，
    # 5 笔持仓上限时），用来把「激进度」换算成每笔保证金，见 docs/research-alt-perps.md。
    "conservative": dict(n=55, vol_x=1.5, breakdown=False, dd95=60.0),
    "aggressive": dict(n=20, vol_x=1.2, breakdown=True, dd95=50.0),
}


class AltRegimeTrend(Strategy):
    """Altcoin perps (top ~200 by turnover): regime-gated Donchian breakouts plus weak-alt shorts in broad
    downtrends; owner sizing (margin × leverage, ATR stop capped at a multiple of the margin)."""

    name = "alt-regime-trend"
    description = ("山寨币状态趋势：牛市做多放量突破、熊市做空最弱的币；保证金 × 杠杆固定名义仓位，"
                   "ATR 动态止损且单笔亏损不超过保证金的 2 倍；按权益和激进度算建议保证金。")
    # 全市场按 12 小时成交额排名的前 300 个加密合约；策略内部再按 30 天成交额取前 top_n 个
    universe = Screener(scope="market", top=300, refresh="4H")
    timeframe = "4H"
    warmup = KEEP

    # ---- 1 风格与仓位 ----
    style = Choice("conservative", list(STYLES), labels=["保守（55 根突破，只做多突破）", "激进（20 根突破，多空突破）"],
                   group="1 仓位", label="风格", desc="保守：信号少、持仓久、单笔赚得多；激进：信号多，突破也做空")
    aggr = Float(0.5, min=0, max=1, step=0.05, group="1 仓位", label="激进度",
                 desc="0 = 最保守，1 = 最激进。决定自动保证金：一年 95% 情况下的最大回撤 ≈ 权益的 10%（0）到 50%（1）")
    margin_usdt = Float(0, min=0, max=100000, step=5, group="1 仓位", label="每笔保证金（USDT）",
                        desc="0 = 按激进度从权益自动算；手填则固定这个数（例如 125），超过建议上限时卡片上会提示")
    leverage = Float(8, min=1, max=20, step=1, group="1 仓位", label="杠杆", desc="名义仓位 = 保证金 × 杠杆")
    loss_cap = Float(2.0, min=0.5, max=2.0, step=0.1, group="1 仓位", label="单笔最大亏损（× 保证金）",
                     desc="止损时最多亏保证金的这么多倍（安全阈值，最高 200%）。8 倍杠杆、2 倍 = 止损最远 25%")
    max_pos = Int(5, min=1, max=10, group="1 仓位", label="最多同时持仓")

    # ---- 2 选币与大盘状态 ----
    top_n = Int(200, min=30, max=300, group="2 选币", label="监控前 N 个山寨币",
                desc="按 30 天成交额取前 N 个（不含 BTC/ETH，上线不足 30 天的不算）。研究发现只看前 50 几乎没有收益")
    long_breadth = Float(0.5, min=0.3, max=0.8, step=0.05, group="2 选币", label="做多要求的市场宽度",
                         desc="市场宽度 = 站上 7 日均线的山寨币比例。做多突破要求 BTC 在 20 日均线上方且宽度高于此值")
    short_breadth = Float(0.4, min=0.1, max=0.6, step=0.05, group="2 选币", label="做空弱势币的市场宽度",
                          desc="宽度低于此值（多数山寨币在跌）才做空弱势币")

    # ---- 3 趋势腿 ----
    use_trend = Bool(True, group="3 趋势突破", label="启用趋势突破")
    n_override = Int(0, min=0, max=120, group="3 趋势突破", label="突破周期（根 4H）", desc="0 = 跟随风格（保守 55 / 激进 20）")
    vol_override = Float(0, min=0, max=5, step=0.1, group="3 趋势突破", label="放量倍数",
                         desc="近 24 小时成交额 ÷ 近 7 天日均；0 = 跟随风格（保守 1.5 / 激进 1.2）")
    stop_atr = Float(3, min=1, max=6, step=0.25, group="3 趋势突破", label="止损（ATR）",
                     desc="止损距离 = 这么多个 4H ATR(14)，夹在 1% 和「单笔最大亏损」之间", ai_tunable=True)

    # ---- 4 弱势做空腿 ----
    use_short = Bool(True, group="4 弱势做空", label="启用熊市做空弱势币")
    short_pct = Float(0.05, min=0.01, max=0.2, step=0.01, group="4 弱势做空", label="做空最弱的比例",
                      desc="过去 72 小时相对 BTC 的残差收益排在最后这个比例的币")
    short_hold = Float(72, min=12, max=168, step=12, group="4 弱势做空", label="持有小时数")

    cooldown_h = Float(4, min=0, max=48, step=1, group="5 其他", label="平仓后冷却（小时）")
    notify = Bool(False, group="5 其他", label="每次开仓发消息")

    # ---------------------------------------------------------------- 生命周期
    def on_start(self, ctx):
        self._buf = {}        # inst -> {"ts": [...], "o": [...], ...}，最旧在前
        self._last = ""       # 最近一次的状态行
        s = self._style()
        ctx.log(f"启动：风格 {self.style}（突破 {s['n']} 根 4H，放量 {s['vol_x']}x），杠杆 {self.leverage:g}，"
                f"最多 {self.max_pos} 笔，单笔最多亏 {self.loss_cap:g} 倍保证金")

    def on_universe_change(self, ctx, added, removed):
        # 新进入监控范围的币：拉一次历史，之后每根 K 线由 on_bars 追加。
        # 移出的币先保留缓存（12 小时成交额在第 300 名附近的币会反复进出，持仓中的币还要算离场），
        # 只是不再参与选币；超过 7 天没更新且没有持仓的缓存才清掉（见 _gc）。
        for inst in added:
            v = self._buf.get(inst)
            if v is None or not v["ts"] or ctx.now - v["ts"][-1] > 2 * BAR_MS:
                self._load(ctx, inst)

    def on_bars(self, ctx, bars):
        for inst, b in bars.items():
            if inst not in self._buf:
                self._load(ctx, inst)
                continue
            self._push(inst, b)
        latest = max(b.ts for b in bars.values())
        held = {p.inst_id for p in ctx.positions()}
        for inst in held - set(bars):                       # 持仓的币已不在监控范围：单独补最新 K 线，离场照常判断
            if inst not in self._buf:                       # 例如重启后：缓存是空的，先拉历史
                self._load(ctx, inst)
            else:
                for row in zip(*(getattr(ctx.bars(inst, "4H", 6), k) for k in ("ts", "o", "h", "l", "c", "volq"))):
                    self._push(inst, Bar(inst, "4H", dict(zip(("ts", "o", "h", "l", "c", "volq"), row))))
        self._gc(ctx, held)

        btc = ctx.bars(BTC, "4H", 130)
        if len(btc) < 121:
            return None
        btc_up = btc.c[-1] > ta.ema(btc.c, 120)[-1]          # BTC 在 20 日（120 根 4H）均线上方
        cols = self._universe(btc, latest, set(ctx.universe))
        if not cols:
            return None
        breadth = float(np.mean([v["c"][-1] > v["ema42"] for v in cols.values()]))  # 站上 7 日均线的比例
        bull = btc_up and breadth > self.long_breadth
        bear_break = (not btc_up) and breadth < 0.5
        weak_mkt = breadth < self.short_breadth

        out = self._exits(ctx)
        free = self.max_pos - len(held)   # 本根发出的平仓下一根才成交，不立刻腾出名额
        legs = ctx.state.setdefault("legs", {})
        last_close = ctx.state.setdefault("closed", {})
        cool = self.cooldown_h * 3600_000

        cands = []
        st = self._style()
        if self.use_trend:
            n = self.n_override or st["n"]
            vx = self.vol_override or st["vol_x"]
            for inst, v in cols.items():
                if len(v["c"]) < n + 2:
                    continue
                hh, ll = float(np.max(v["h"][-n - 1:-1])), float(np.min(v["l"][-n - 1:-1]))
                if v["volx"] <= vx:
                    continue
                if bull and v["c"][-1] > hh:
                    cands.append((10 + v["volx"], inst, "long", 1, f"放量 {v['volx']:.1f}x 突破 {n} 根 4H 新高"))
                elif st["breakdown"] and bear_break and v["c"][-1] < ll:
                    cands.append((10 + v["volx"], inst, "short", 1, f"放量 {v['volx']:.1f}x 跌破 {n} 根 4H 新低"))
        if self.use_short and weak_mkt:
            rr = {inst: v["resid72"] for inst, v in cols.items() if math.isfinite(v["resid72"])}
            if rr:
                k = max(1, int(len(cols) * self.short_pct))
                taken = {c[1] for c in cands}
                for rank, inst in enumerate(sorted(rr, key=rr.get)[:k]):
                    if inst not in taken:
                        cands.append((1 - rank / len(cols), inst, "short", 2,
                                      f"市场宽度 {breadth:.0%}，72h 相对 BTC 最弱（残差 {rr[inst]:+.1%}）"))
        cands.sort(key=lambda x: -x[0])

        acct = ctx.account() or {}
        m, warn = self._margin(acct)
        for score, inst, side, leg, why in cands:
            if free <= 0 or m <= 0:
                break
            if inst in held or ctx.now - last_close.get(inst, 0) < cool:
                continue
            v = cols[inst]
            sig = self._open(ctx, acct, m, inst, side, leg, v, why)
            if sig is None:
                continue
            out.append(sig)
            legs[inst] = leg
            free -= 1
            if self.notify:
                ctx.notify(f"{inst.replace('-USDT-SWAP', '')} {'开多' if side == 'long' else '开空'}：{why}")

        state = "牛市" if bull else ("弱市" if weak_mkt else "中性")
        self._last = (f"{state}：宽度 {breadth:.0%}，BTC {'在' if btc_up else '不在'} 20 日线上方；监控 {len(cols)} 个；"
                      f"持仓 {len(held)}/{self.max_pos}；每笔保证金 {m:.0f}U{warn}")
        return out

    def on_position_closed(self, ctx, trade):
        ctx.state.setdefault("closed", {})[trade.inst_id] = ctx.now
        ctx.state.setdefault("legs", {}).pop(trade.inst_id, None)

    def on_signal_result(self, ctx, result):
        if result.outcome not in ("ordered", "recorded", "pending"):
            ctx.log(f"信号 #{result.signal_id} {result.outcome}：{result.reason}", level="warn")

    def status(self, ctx):
        return self._last or "等待第一根 4H K 线"

    # ---------------------------------------------------------------- 仓位
    def _style(self):
        return STYLES[self.style]

    def suggested_margin(self, equity: float) -> float:
        """按激进度算出的每笔保证金上限（USDT）。

        回测里每笔保证金固定为 1 份时，一年 95% 分位的最大回撤约为 dd95 份（随同时持仓数线性放大）。
        想让这个回撤不超过权益的 budget（10%–50%），每笔保证金就不能超过 equity × budget ÷ dd95。
        另一道保险：所有持仓同时打到最远止损，总亏损不超过权益的一半。"""
        if equity <= 0:
            return 0.0
        budget = 0.10 + 0.40 * self.aggr
        dd = self._style()["dd95"] * self.max_pos / 5
        by_dd = equity * budget / dd
        by_worst = equity * 0.5 / (self.max_pos * self.loss_cap)
        return max(0.0, min(by_dd, by_worst))

    def _margin(self, acct):
        eq = float(acct.get("equity") or 0)
        sug = self.suggested_margin(eq)
        if self.margin_usdt > 0:
            warn = f"（建议 ≤ {sug:.0f}U）" if self.margin_usdt > sug * 1.05 else ""
            return float(self.margin_usdt), warn
        return sug, ""

    def _open(self, ctx, acct, m, inst, side, leg, v, why):
        """名义 = 保证金 × 杠杆；止损距离 d 来自 ATR，夹在 [1%, min(loss_cap ÷ 杠杆, 50%)]；
        订单层按「权益 × riskPct × strength ÷ 止损距离」算数量，所以 strength = 名义 × d ÷ (权益 × riskPct)。"""
        px = float(v["c"][-1])
        atr = float(v["atr"])
        if not (px > 0 and math.isfinite(atr) and atr > 0):
            return None
        d = min(max(self.stop_atr * atr / px, 0.01), self.loss_cap / self.leverage, MAX_STOP)
        notional = m * self.leverage
        eq = float(acct.get("equity") or 0)
        lim = acct.get("limits") or {}
        risk_pct = float(lim.get("riskPct") or 0)
        if eq <= 0 or risk_pct <= 0:
            return None
        strength = notional * d / (eq * risk_pct)
        if strength > 1:
            ctx.log(f"账户「单笔风险」{risk_pct:.1%} 太低：{inst} 需要 {notional * d / eq:.1%} 才能开满 {notional:.0f}U 名义，"
                    f"本笔按上限开（请把账户单笔风险调到 ≥ {m * self.loss_cap / eq:.1%}）", level="warn")
            strength = 1.0
        stop = px * (1 - d) if side == "long" else px * (1 + d)
        tl = f"{self.short_hold:g}h" if leg == 2 else None
        return Signal.open(inst, side, stop_loss=stop, strength=strength, leverage=self.leverage, time_limit=tl,
                           reason=f"{why}；名义 {notional:.0f}U，止损 {d:.1%}（亏 {notional * d:.0f}U）")

    # ---------------------------------------------------------------- 离场
    def _exits(self, ctx):
        """趋势腿：收盘跌回通道中轨（多）/ 涨回中轨（空）就离场。弱势做空腿靠 time_limit 到时平仓。"""
        out = []
        legs = ctx.state.get("legs", {})
        n = self.n_override or self._style()["n"]
        half = max(2, n // 2)
        for p in ctx.positions():
            if legs.get(p.inst_id, 1) != 1:
                continue
            v = self._buf.get(p.inst_id)
            if not v or len(v["c"]) < half:
                continue
            h, l = np.asarray(v["h"][-half:]), np.asarray(v["l"][-half:])
            mid = (h.max() + l.min()) / 2
            c = v["c"][-1]
            if (p.pos_side == "long" and c < mid) or (p.pos_side == "short" and c > mid):
                out.append(Signal.close(p.inst_id, reason=f"收盘 {c:.6g} 回到通道中轨 {mid:.6g} {'下方' if p.pos_side == 'long' else '上方'}"))
        return out

    # ---------------------------------------------------------------- 数据
    def _load(self, ctx, inst):
        b = ctx.bars(inst, "4H", KEEP)
        self._buf[inst] = {"ts": list(b.ts), "o": list(b.o), "h": list(b.h), "l": list(b.l), "c": list(b.c),
                           "q": list(b.volq)}

    def _push(self, inst, b):
        v = self._buf[inst]
        if v["ts"] and b.ts <= v["ts"][-1]:
            return
        for k, x in (("ts", b.ts), ("o", b.o), ("h", b.h), ("l", b.l), ("c", b.c), ("q", b.volq)):
            v[k].append(x)
            if len(v[k]) > KEEP:
                del v[k][0]

    def _gc(self, ctx, held):
        for inst in [i for i, v in self._buf.items() if i not in held and v["ts"] and ctx.now - v["ts"][-1] > 7 * 86400_000]:
            del self._buf[inst]

    def _universe(self, btc, latest, current):
        """监控范围：当前币池里、本根有新 K 线、上线 ≥ 30 天、按 30 天成交额取前 top_n 的山寨币，并算好要用的指标。
        （已移出币池但还有持仓的币只用来判断离场，不参与选币、宽度和排名。）"""
        rows = []
        for inst, v in self._buf.items():
            if inst not in current or inst in SKIP or len(v["c"]) < 30 * DAY or v["ts"][-1] != latest or not (v["c"][-1] > 0):
                continue
            rows.append((float(np.nansum(v["q"][-30 * DAY:])), inst))
        rows.sort(reverse=True)
        insts = [inst for _, inst in rows[: self.top_n]]
        if not insts:
            return {}
        # 200 个币一起算：把最近 30 天（180 根）拼成矩阵，指标按时间只循环一遍（比逐个币调用 ta 快几十倍）
        L = 30 * DAY

        def mat(k, dtype=float):
            return np.array([self._buf[i][k][-L:] for i in insts], dtype=dtype)

        C, Hm, Lm, Q, TS = mat("c"), mat("h"), mat("l"), mat("q"), mat("ts", np.int64)
        ema42 = _smooth_last(C, 42, 2 / 43)                              # = ta.ema(c, 42)[-1]
        prev = np.concatenate([np.full((len(insts), 1), np.nan), C[:, :-1]], axis=1)
        with np.errstate(invalid="ignore"):
            TR = np.fmax(Hm - Lm, np.fmax(np.abs(Hm - prev), np.abs(Lm - prev)))
        TR[:, 0] = Hm[:, 0] - Lm[:, 0]
        atr = _smooth_last(TR, 14, 1 / 14)                               # = ta.atr(h, l, c, 14)[-1]
        q7 = np.nansum(Q[:, -7 * DAY:], axis=1) / 7
        volx = np.where(q7 > 0, np.nansum(Q[:, -DAY:], axis=1) / np.where(q7 > 0, q7, 1), 0.0)
        resid = _resid72(C, TS, btc)
        return {inst: dict(c=C[k], h=Hm[k], l=Lm[k], ema42=float(ema42[k]), atr=float(atr[k]), volx=float(volx[k]),
                           resid72=float(resid[k])) for k, inst in enumerate(insts)}


def _smooth_last(X, n, alpha):
    """每行做 EMA / RMA（种子 = 前 n 个值的均值，缺失值跳过、保持上一状态），返回最后一个值。与 ta.ema / ta.rma 同口径。"""
    if X.shape[1] < n:
        return np.full(X.shape[0], np.nan)
    prev = np.mean(X[:, :n], axis=1)
    for t in range(n, X.shape[1]):
        v = X[:, t]
        prev = np.where(np.isfinite(v), alpha * v + (1 - alpha) * prev, prev)
    return prev


def _resid72(C, TS, btc, beta_n=42, hold=18):
    """过去 72 小时（18 根 4H）相对 BTC 的残差收益：Σ(r − β·r_btc)，β 用最近 7 天（42 根）估计。缺数据的币为 NaN。"""
    out = np.full(C.shape[0], np.nan)
    if len(btc) < beta_n + 2 or C.shape[1] < beta_n + 1:
        return out
    with np.errstate(invalid="ignore", divide="ignore"):
        R = np.diff(np.log(C[:, -beta_n - 1:]), axis=1)
        bts, brt = btc.ts[1:], np.diff(np.log(btc.c))
        tsx = TS[:, -beta_n:]
        k = np.clip(np.searchsorted(bts, tsx), 0, len(bts) - 1)
        RB = np.where(bts[k] == tsx, brt[k], np.nan)
        ok = np.isfinite(R) & np.isfinite(RB)
        n = ok.sum(axis=1)
        r0, b0 = np.where(ok, R, 0.0), np.where(ok, RB, 0.0)
        mr, mb = r0.sum(1) / np.maximum(n, 1), b0.sum(1) / np.maximum(n, 1)
        cov = (np.where(ok, (R - mr[:, None]) * (RB - mb[:, None]), 0.0)).sum(1)
        var = (np.where(ok, (RB - mb[:, None]) ** 2, 0.0)).sum(1)
        beta = np.where(var > 0, cov / np.where(var > 0, var, 1), 0.0)
        res = R - beta[:, None] * np.nan_to_num(RB)
        val = np.nansum(res[:, -hold:], axis=1)
    return np.where(n >= beta_n * 0.8, val, np.nan)
