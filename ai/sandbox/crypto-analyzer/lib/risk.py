"""Danger & manipulation scoring. Pure functions over the feature dict built in report.py.

Each factor yields points = weight × intensity (intensity ∈ [0,1]). Factors are combined as
noisy-OR evidence: score = 100 × (1 − Π(1 − points/50)), so one maxed 12-weight factor ≈ 24, several
independent warnings stack toward 100 without a weighted average diluting them. Missing inputs add
nothing and lower `coverage` — read a low score with low coverage as "unknown", not "safe".

References behind the factors (heuristics, not validated alpha):
- Funding / basis / long-short crowding as squeeze fuel: Coinglass/Laevitas-style positioning dashboards.
- Price × OI quadrant (new longs / short covering / new shorts / long liquidation): standard futures OI analysis.
- Kyle's λ (Kyle 1985) and Amihud (2002) illiquidity: how cheaply price can be pushed → manipulability.
- VPIN-style flow imbalance (Easley, López de Prado, O'Hara 2012): toxic one-sided flow.
- Kaufman efficiency ratio: stair-step "controlled" moves vs. noisy two-way trade.
- Wyckoff accumulation/markup/distribution/markdown for phase inference.
"""

from __future__ import annotations

import math


def clamp(x: float | None, lo: float = 0.0, hi: float = 1.0) -> float | None:
    if x is None:
        return None
    return max(lo, min(hi, x))


class Score:
    def __init__(self, name: str) -> None:
        self.name = name
        self.items: list[dict] = []

    def add(self, key: str, weight: float, intensity: float | None, why: str) -> None:
        if intensity is None:
            self.items.append({"key": key, "weight": weight, "points": None, "why": "数据缺失"})
            return
        self.items.append({"key": key, "weight": weight, "points": round(weight * clamp(intensity), 2), "why": why})

    def result(self) -> dict:
        have = [i for i in self.items if i["points"] is not None]
        tot_w = sum(i["weight"] for i in have)
        all_w = sum(i["weight"] for i in self.items)
        miss = 1.0
        for i in have:
            miss *= 1 - min(i["points"], 45) / 50
        score = round(100 * (1 - miss), 1) if have else None
        top = sorted((i for i in have if i["points"] > 0), key=lambda i: i["points"], reverse=True)
        return {"score": score, "level": level(score), "coverage": round(tot_w / all_w, 2) if all_w else 0,
                "drivers": [f"{i['why']}（+{i['points']:.0f}/{i['weight']}）" for i in top[:6]],
                "factors": self.items}


def level(s: float | None) -> str:
    if s is None:
        return "未知"
    return "极高" if s >= 70 else "高" if s >= 50 else "中" if s >= 30 else "低"


def pct(x: float | None, d: int = 1) -> str:
    return "?" if x is None else f"{x*100:+.{d}f}%"


# ── 庄 / control ───────────────────────────────────────────────

def control_score(f: dict) -> dict:
    s = Score("control")
    h = f.get("holders") or {}
    w10 = h.get("whale10_pct")
    s.add("concentration", 20, None if w10 is None else (w10 - 15) / 45,
          f"链上前10未标注大户合计 {w10:.1f}%（剔除交易所/锁仓/LP）" if w10 is not None else "")
    mcf = f.get("mc_fdv")
    s.add("low_float", 10, None if mcf is None else (0.6 - mcf) / 0.45, f"流通市值/FDV={mcf:.2f}，低流通高 FDV" if mcf else "")
    om = f.get("oi_mcap")
    s.add("perp_dominance", 15, None if om is None else (om - 0.03) / 0.22, f"OI/流通市值={om:.1%}，合约主导定价" if om else "")
    ami, lam = f.get("amihud"), f.get("kyle_lambda")
    if ami is not None:
        thin_i = (math.log10(max(ami, 1e-6)) - 0.5) / 2.5
        thin_why = f"Amihud 非流动性≈{ami:.0f}bps/百万U成交（盘口薄、少量资金即可推动价格）"
    elif lam is not None:
        thin_i = 0 if lam <= 10 else (math.log10(lam) - 1) / 2
        thin_why = f"Kyle λ≈{lam:.0f}bps/百万U净主动单"
    elif f.get("depth2_oi") is not None:
        thin_i = (0.05 - f["depth2_oi"]) / 0.045
        thin_why = f"±2%深度/OI={f['depth2_oi']:.1%}"
    else:
        thin_i, thin_why = None, ""
    s.add("thin_market", 15, thin_i, thin_why)
    beta = f.get("beta")
    s.add("decoupled", 10, None if beta is None else (0.7 - beta) / 0.7, f"对 BTC β={beta:.2f}，走势独立于大盘" if beta is not None else "")
    wk = f.get("wicks") or {}
    nwick = (wk.get("up") or 0) + (wk.get("down") or 0)
    pump = f.get("range_7d")
    ab = max(clamp(nwick / 4) or 0, clamp(((pump or 0) - 0.3) / 0.9) or 0)
    s.add("abnormal_moves", 15, ab, f"48h 插针 {nwick} 次，7日振幅 {(pump or 0)*100:.0f}%")
    r8, basis = f.get("funding_8h"), f.get("basis")
    fb = None
    if r8 is not None or basis is not None:
        fb = max(clamp((abs(r8 or 0) - 0.0003) / 0.0012) or 0, clamp((abs(basis or 0) - 0.002) / 0.008) or 0)
    s.add("funding_basis", 8, fb, f"资金费 {pct(r8, 3)}/8h，基差 {pct(basis, 2)}")
    lq = f.get("liq") or {}
    oi = f.get("oi_usd")
    hunt = None
    if lq and oi:
        l48, s48 = lq["h48"]["long"], lq["h48"]["short"]
        hunt = min(clamp(l48 / (0.02 * oi)) or 0, clamp(s48 / (0.02 * oi)) or 0)
    s.add("liq_hunting", 7, hunt, "48h 多空双向都有较大清算（双向猎杀）")
    r = s.result()
    r["label"] = {"极高": "强庄/高度控盘", "高": "明显控盘", "中": "有一定控盘迹象", "低": "控盘迹象弱"}.get(r["level"], "未知")
    r["phase"] = phase(f)
    return r


def phase(f: dict) -> dict:
    """Wyckoff-style phase guess. Heuristic: say 推断, not fact."""
    r24, r7, r30 = f.get("ret_24h"), f.get("ret_7d"), f.get("ret_30d")
    er, pos = f.get("er_24h"), f.get("pos_30d")
    oi24, br = f.get("oi_chg_24h"), f.get("buy_ratio_24h")
    r8, wk = f.get("funding_8h") or 0, f.get("wicks") or {}
    lq = (f.get("liq") or {}).get("h24") or {}
    tpr = f.get("top_pos_ratio_chg")
    why = []
    if r24 is None:
        return {"name": "未知", "why": []}
    if (r24 > 0.08 or (r7 or 0) > 0.30) and (r8 <= 0.0001 or (oi24 or 0) > 0.05):
        why = [f"24h {pct(r24)} / 7d {pct(r7)}", f"资金费 {pct(r8, 3)}", f"OI 24h {pct(oi24)}"]
        name = "拉升/逼空" if r8 <= 0 else "拉升"
    elif (pos or 0) > 0.75 and ((r30 or 0) > 0.5 or (r7 or 0) > 0.3) and (
            (br is not None and br < 0.48) or r8 > 0.0003 or (tpr is not None and tpr < -0.05)):
        name = "派发（高位出货）"
        why = [f"处于30日区间 {pos:.0%} 高位", f"主动买占比 {br:.0%}" if br is not None else "",
               f"大户持仓多空比24h变化 {pct(tpr)}" if tpr is not None else "", f"资金费 {pct(r8, 3)}"]
    elif r24 < -0.08 and (oi24 or 0) < 0 and lq.get("long", 0) > lq.get("short", 0):
        name = "砸盘/出货"
        why = [f"24h {pct(r24)}", f"OI 24h {pct(oi24)}", f"多头清算 ${lq.get('long', 0):,.0f}"]
    elif (wk.get("down") or 0) >= 2 and r24 > -0.03 and (r7 or 0) > 0:
        name = "洗盘（下插针后收回）"
        why = [f"48h 下影插针 {wk.get('down')} 次", f"24h {pct(r24)}，7d {pct(r7)}"]
    elif (pos is not None and pos < 0.4) and (er is not None and er < 0.3) and (
            (oi24 or 0) > 0.03 or (br is not None and br > 0.52)) and r8 <= 0.0001:
        name = "吸筹（低位横盘+OI/主动买增加）"
        why = [f"30日区间 {pos:.0%} 低位", f"趋势效率 {er:.2f} 震荡", f"OI 24h {pct(oi24)}",
               f"主动买 {br:.0%}" if br is not None else ""]
    else:
        name = "无明显庄家阶段特征"
    return {"name": name, "why": [w for w in why if w]}


# ── long / short danger ────────────────────────────────────────

def _news_hits(f: dict, tags: set) -> list[str]:
    return [w["title"] for w in f.get("warnings") or [] if w["level"] in tags]


def _shared(s: Score, f: dict) -> None:
    """Risks that hurt both directions: volatility and a leverage surge."""
    a4 = f.get("atr4h_pct")
    s.add("volatility", 10, None if a4 is None else (a4 - 0.015) / 0.05, f"4H ATR {(a4 or 0)*100:.1f}%：波动大，止损易被打")
    o72 = f.get("oi_chg_72h") if f.get("oi_chg_72h") is not None else f.get("oi_chg_24h")
    s.add("leverage_surge", 8, None if o72 is None else (o72 - 0.3) / 1.0, f"OI 72h {pct(o72, 0)}：杠杆资金涌入，双向爆仓燃料")


def short_danger(f: dict, ctrl: float | None) -> dict:
    """做空危险度 = 被逼空 / 被插针爆仓的风险。"""
    s = Score("short")
    r8 = f.get("funding_8h")
    s.add("funding_neg", 12, None if r8 is None else (-r8 - 0.0001) / 0.0009,
          f"资金费为负 {pct(r8, 3)}/8h，空头拥挤付费")
    b = f.get("basis")
    s.add("basis_neg", 6, None if b is None else (-b - 0.0005) / 0.0045, f"永续贴水现货 {pct(b, 2)}（空头压价/现货买盘）")
    ls, tp = f.get("ls_all"), f.get("ls_top_pos")
    crowd = None
    if ls is not None:
        crowd = (clamp((1.0 - ls) / 0.5) or 0) + (0.3 if tp and tp > 1.1 else 0)
    s.add("crowd_short", 10, crowd, f"散户多空账户比 {ls:.2f}，大户持仓比 {tp or 0:.2f}" if ls else "")
    r24, oi24 = f.get("ret_24h"), f.get("oi_chg_24h")
    build = None
    if r24 is not None and oi24 is not None:
        build = clamp(oi24 / 0.25) if r24 < 0.01 else (clamp(oi24 / 0.25) * 0.6 if (r8 or 0) < 0 else 0)
    s.add("shorts_building", 10, build, f"价格 {pct(r24)} 而 OI {pct(oi24)}：新空单堆积 = 逼空燃料")
    lm, oi = f.get("liqmap") or {}, f.get("oi_usd")
    above = None
    if lm and oi:
        above = clamp(lm["short_liq_above_5"] / (0.12 * oi)) or 0
        if (lm.get("magnet_ratio_10") or 0) > 1.5:
            above = min(1, above + 0.25)
    s.add("liq_above", 10, above, f"上方5%内估算空头清算 ${lm.get('short_liq_above_5', 0):,.0f}（磁吸向上）" if lm else "")
    imb = f.get("imb_2")
    thin = None
    if imb is not None:
        thin = clamp((imb - 0.55) / 0.25) or 0
        if f.get("cost_up_2pct") and oi and f["cost_up_2pct"] < 0.01 * oi:
            thin = min(1, thin + 0.4)
    s.add("thin_asks", 8, thin, f"±2%买盘占比 {imb or 0:.0%}，拉升2%仅需 ${f.get('cost_up_2pct') or 0:,.0f}")
    fl = f.get("flow") or {}
    bf = None
    if fl:
        bf = 1.0 if fl.get("divergence") == "bullish" else clamp(((fl.get("w4") or {}).get("buy_ratio", 0.5) - 0.52) / 0.1)
    s.add("bullish_flow", 8, bf, "价跌但主动买占优（CVD 背离，有人吸收抛压）" if fl.get("divergence") == "bullish"
          else f"近4h主动买占比 {(fl.get('w4') or {}).get('buy_ratio') or 0:.0%}")
    te, er = f.get("trend_pct"), f.get("er_24h")
    tr = None
    if te is not None:
        tr = clamp((te - 60) / 35) * (0.6 + 0.4 * (er or 0))
    s.add("uptrend", 10, tr, f"趋势强度分位 {te or 0:.0f}，效率比 {er or 0:.2f}：逆势做空")
    s.add("strong_hand", 12, None if ctrl is None else (ctrl - 30) / 50, f"控盘分 {ctrl}：强庄可随时拉爆空头")
    rsi4 = f.get("rsi_4h")
    s.add("oversold", 4, None if rsi4 is None else (35 - rsi4) / 15, f"4H RSI {rsi4 or 0:.0f} 超卖")
    lst, pos = _news_hits(f, {"listing"}), _news_hits(f, {"positive"})
    s.add("listing_news", 14, 1 if lst else 0, f"上所/新合约事件：{lst[0][:40]}" if lst else "")
    s.add("positive_news", 6, 1 if pos else 0, f"利好消息：{pos[0][:40]}" if pos else "")
    mac = _news_hits(f, {"macro"})
    s.add("macro_window", 4, 1 if mac else 0, f"24h 内重大宏观：{mac[0][:30]}" if mac else "")
    fg = f.get("fng")
    s.add("sentiment", 3, None if fg is None else (25 - fg) / 15, f"恐惧贪婪 {fg}（极度恐惧易反抽）")
    _shared(s, f)
    return s.result()


def long_danger(f: dict, ctrl: float | None, ph: str) -> dict:
    """做多危险度 = 高位接盘 / 多头踩踏 / 利空砸盘的风险。"""
    s = Score("long")
    r8 = f.get("funding_8h")
    s.add("funding_pos", 12, None if r8 is None else (r8 - 0.0002) / 0.0008, f"资金费偏高 {pct(r8, 3)}/8h，多头拥挤")
    b = f.get("basis")
    s.add("basis_pos", 6, None if b is None else (b - 0.001) / 0.004, f"永续升水现货 {pct(b, 2)}（杠杆多头追价）")
    ls, tp = f.get("ls_all"), f.get("ls_top_pos")
    crowd = None
    if ls is not None:
        crowd = (clamp((ls - 1.3) / 1.5) or 0) + (0.3 if tp is not None and tp < 0.9 and ls > 1.3 else 0)
    s.add("crowd_long", 10, crowd, f"散户多空账户比 {ls:.2f} vs 大户持仓比 {tp or 0:.2f}（散户多/大户空）" if ls else "")
    r24, oi24 = f.get("ret_24h"), f.get("oi_chg_24h")
    late = None
    if r24 is not None and oi24 is not None:
        late = (clamp(oi24 / 0.3) * clamp(r24 / 0.15)) ** 0.5 if r24 > 0.03 and oi24 > 0 else (0.4 if r24 > 0.05 and oi24 < -0.05 else 0)
    s.add("late_longs", 10, late, f"价格 {pct(r24)} + OI {pct(oi24)}：杠杆追涨 / 空头回补行情后劲不足")
    lm, oi = f.get("liqmap") or {}, f.get("oi_usd")
    below = None
    if lm and oi:
        below = clamp(lm["long_liq_below_5"] / (0.12 * oi)) or 0
        if lm.get("magnet_ratio_10") is not None and lm["magnet_ratio_10"] < 0.67:
            below = min(1, below + 0.25)
    s.add("liq_below", 10, below, f"下方5%内估算多头清算 ${lm.get('long_liq_below_5', 0):,.0f}（踩踏链）" if lm else "")
    imb = f.get("imb_2")
    thin = None
    if imb is not None:
        thin = clamp((0.45 - imb) / 0.25) or 0
        if f.get("cost_down_2pct") and oi and f["cost_down_2pct"] < 0.01 * oi:
            thin = min(1, thin + 0.4)
    s.add("thin_bids", 8, thin, f"±2%买盘占比 {imb or 0:.0%}，砸2%仅需 ${f.get('cost_down_2pct') or 0:,.0f}")
    fl = f.get("flow") or {}
    bf = None
    if fl:
        bf = 1.0 if fl.get("divergence") == "bearish" else clamp((0.48 - (fl.get("w4") or {}).get("buy_ratio", 0.5)) / 0.1)
    s.add("bearish_flow", 8, bf, "价涨但主动卖占优（CVD 背离，上涨无主动买支撑）" if fl.get("divergence") == "bearish"
          else f"近4h主动卖占优（买占比 {(fl.get('w4') or {}).get('buy_ratio') or 0:.0%}）")
    rsi4, ext, r7 = f.get("rsi_4h"), f.get("ext_atr"), f.get("ret_7d")
    ov = None
    if rsi4 is not None or ext is not None:
        ov = max(clamp(((rsi4 or 50) - 70) / 15) or 0, clamp(((ext or 0) - 2.5) / 3) or 0, clamp(((r7 or 0) - 0.4) / 0.8) or 0)
    s.add("overextended", 10, ov, f"4H RSI {rsi4 or 0:.0f}，偏离4H EMA50 {ext or 0:.1f}×ATR，7d {pct(r7)}")
    dist = None
    if ctrl is not None:
        dist = 1.0 if ph.startswith(("派发", "砸盘")) else clamp((ctrl - 40) / 60) * 0.6
        h = f.get("holders") or {}
        if (f.get("mc_fdv") or 1) < 0.3 or (h.get("whale10_pct") or 0) > 40:
            dist = min(1, dist + 0.3)
    s.add("dump_risk", 12, dist, f"庄阶段：{ph}；控盘分 {ctrl}；低流通/高集中 = 出货砸盘风险")
    te = f.get("trend_pct")
    s.add("downtrend", 8, None if te is None else (40 - te) / 35, f"趋势强度分位 {te or 0:.0f}：逆势做多")
    sev, sup = _news_hits(f, {"severe"}), _news_hits(f, {"supply"})
    s.add("severe_news", 30, 1 if sev else 0, f"重大利空（黑客/下架/监管）：{sev[0][:40]}" if sev else "")
    s.add("supply_news", 14, 1 if sup else 0, f"解锁/大额转入交易所/抛售：{sup[0][:40]}" if sup else "")
    mac = _news_hits(f, {"macro"})
    s.add("macro_window", 4, 1 if mac else 0, f"24h 内重大宏观：{mac[0][:30]}" if mac else "")
    fg = f.get("fng")
    s.add("sentiment", 3, None if fg is None else (fg - 75) / 15, f"恐惧贪婪 {fg}（极度贪婪）")
    _shared(s, f)
    return s.result()


# ── plan ───────────────────────────────────────────────────────

def plan(f: dict, ld: dict, sd: dict, ctrl: dict) -> dict:
    L, S, C = ld["score"] or 0, sd["score"] or 0, ctrl["score"] or 0
    px = f.get("price")
    atr4 = f.get("atr4h_pct") or f.get("atr1h_pct", 0.01) * 2
    stop_pct = max(1.5 * atr4, 0.01)
    lev = 1 / (2.5 * stop_pct)
    worst = max(L, S)
    lev /= 3 if worst >= 70 else 2 if worst >= 50 else 1
    if C >= 60:
        lev = min(lev, 3)
    lev = int(max(1, min(20, lev)))

    if L >= 55 and S >= 55:
        bias = "观望：多空双向高危（典型庄控/高波动），不宜持有杠杆仓位"
    elif S >= 60 and L < 45:
        bias = "不宜做空（逼空风险高）；如做多需等回踩确认"
    elif L >= 60 and S < 45:
        bias = "不宜做多（接盘/踩踏风险高）；如做空需等反弹衰竭"
    elif abs(L - S) < 10:
        bias = "无明显优势方向，按区间对待（高抛低吸或等待突破）"
    elif L < S:
        bias = "做多相对更安全（仍需止损）"
    else:
        bias = "做空相对更安全（仍需止损）"

    sup, res = [], []
    bk = f.get("book") or {}
    for w in bk.get("bid_walls", []):
        sup.append({"px": w["px"], "src": f"买墙 ${w['usd']:,.0f}", "dist_pct": w["dist_pct"]})
    for w in bk.get("ask_walls", []):
        res.append({"px": w["px"], "src": f"卖墙 ${w['usd']:,.0f}", "dist_pct": w["dist_pct"]})
    for tf in ("1h", "4h"):
        sw = f.get(f"swings_{tf}") or {}
        for lo in sw.get("lows", [])[:3]:
            if px and lo < px:
                sup.append({"px": lo, "src": f"{tf} 摆动低点", "dist_pct": (lo / px - 1) * 100})
        for hi in sw.get("highs", [])[:3]:
            if px and hi > px:
                res.append({"px": hi, "src": f"{tf} 摆动高点", "dist_pct": (hi / px - 1) * 100})
    lm = f.get("liqmap") or {}
    for c in lm.get("long_clusters", [])[:2]:
        sup.append({"px": c["px"], "src": f"多头清算密集 ${c['usd']:,.0f}（易被插针扫）", "dist_pct": c["dist_pct"]})
    for c in lm.get("short_clusters", [])[:2]:
        res.append({"px": c["px"], "src": f"空头清算密集 ${c['usd']:,.0f}（易被拉上去扫）", "dist_pct": c["dist_pct"]})
    def dedupe(xs):
        out = []
        for x in xs:
            if out and abs(x["px"] / out[-1]["px"] - 1) < 0.002:
                out[-1]["src"] += " + " + x["src"]
            else:
                out.append(dict(x))
        return out
    sup = dedupe(sorted((x for x in sup if -25 < x["dist_pct"] < 0), key=lambda x: -x["dist_pct"]))[:6]
    res = dedupe(sorted((x for x in res if 0 < x["dist_pct"] < 25), key=lambda x: x["dist_pct"]))[:6]

    return {
        "bias": bias,
        "max_leverage": lev,
        "stop_distance_pct": round(stop_pct * 100, 2),
        "position_hint": "单笔风险≤账户1%：仓位名义价值 = 账户×1% ÷ 止损距离",
        "supports": sup, "resistances": res,
        "stop_tips": [
            "止损放在清算密集区/明显摆动点之外 0.5×ATR，不要放在整数位和墙后面（庄常扫这些位置）",
            "强庄币（控盘≥60）避免挂市价止损于盘口薄处，宜用较低杠杆+宽止损",
        ],
    }
