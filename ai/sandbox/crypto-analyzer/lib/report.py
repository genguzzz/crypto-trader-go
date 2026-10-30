"""analyze / scan / market orchestration: parallel fetch → features → risk scores → render."""

from __future__ import annotations

import math
import time
from statistics import median

from . import ctg, events, http, indicators as ind, micro, okx, onchain, risk

H = 3600_000


def _exp(v):
    return None if v is None else math.exp(v) - 1


def _chg_at(series: list[dict], hours: float, now_ms: int, key: str = "v"):
    """Relative change of series[key] vs the sample nearest to now-hours."""
    if not series:
        return None
    target = now_ms - hours * H
    past = min(series, key=lambda p: abs(p["t"] - target))
    if abs(past["t"] - target) > max(2 * H, hours * H * 0.25) or not past.get(key):
        return None
    return series[-1][key] / past[key] - 1


# ── gather ─────────────────────────────────────────────────────

def gather(symbol: str, deep: bool = True) -> dict:
    inst = ctg.inst_id(symbol)
    base = inst.replace("-USDT-SWAP", "")
    t0 = time.time()
    use_ctg = ctg.available()
    tasks = {
        "c1d": lambda: okx.candles(inst, "1Dutc", 60),
        "ls_all": lambda: okx.ls_account_ratio(inst, "1H", 48),
        "ls_top_acc": lambda: okx.ls_top_account_ratio(inst, "1H", 48),
        "ls_top_pos": lambda: okx.ls_top_position_ratio(inst, "1H", 48),
        "spot": lambda: okx.ticker(f"{base}-USDT"),
    }
    if use_ctg:
        tasks.update({
            "detail": lambda: ctg.instrument(inst),
            "c1h": lambda: ctg.candles(inst, "1H", 200),
            "c4h": lambda: ctg.candles(inst, "4H", 180),
            "flow": lambda: ctg.orderflow(inst, "1H", 48),
            "book": lambda: ctg.orderbook(inst, 400),
            "liq": lambda: ctg.liquidations(inst, "1H", 72, 30),
            "liqmap": lambda: ctg.liqmap(inst, 7),
            "oi_series": lambda: ctg.series(inst, "oi", 74),
            "fund_series": lambda: ctg.series(inst, "funding", 168),
            "alerts": lambda: ctg.alerts(100),
        })
    else:
        tasks.update({
            "c1h": lambda: okx.candles(inst, "1H", 200),
            "c4h": lambda: okx.candles(inst, "4H", 180),
            "book": lambda: okx.order_book(inst, 400),
            "fund_series": lambda: okx.funding_history(inst, 42),
            "okx_funding": lambda: okx.funding_rate(inst),
            "okx_oi": lambda: okx.open_interest(inst),
            "okx_ticker": lambda: okx.ticker(inst),
        })
    raw, errs = http.parallel(tasks, workers=16)
    if "51001" in errs.get("spot", ""):
        errs.pop("spot")  # 无现货交易对是事实，不是故障
    if "c1h" not in raw and "detail" not in raw:
        raise okx.InstrumentNotFoundError(f"{inst} 无数据（CTG/OKX 都失败）: {errs}")

    coin = (raw.get("detail") or {}).get("coin") or {}
    name = coin.get("name")

    def onchain_chain():
        cid = coin.get("id") or onchain.cg_search(base)
        if not cid:
            return {"error": "CoinGecko id not found"}
        out = {"cg": onchain.cg_coin(cid)}
        pc = onchain.pick_contract(out["cg"]["platforms"])
        if pc:
            addr, chain = pc
            sub, e2 = http.parallel({"holders": lambda: onchain.scan(addr, chain),
                                     "dex": lambda: onchain.dex_by_token(addr)[:8]})
            out.update(sub)
            out["contract"] = {"address": addr, "chain": chain}
            if e2:
                out["errors"] = e2
        return out

    tasks2 = {"events": lambda: events.collect(base, name, 48)}
    if deep:
        tasks2["onchain"] = onchain_chain
        if use_ctg:
            tasks2["tracks"] = lambda: http.parallel({r["id"]: (lambda rid=r["id"]: _track_summary(ctg.rule_track(rid)))
                                                      for r in ctg.rules()["rules"]})[0]
    raw2, errs2 = http.parallel(tasks2)
    raw.update(raw2)
    errs.update(errs2)
    raw["_errors"] = errs
    raw["_inst"] = inst
    raw["_base"] = base
    raw["_elapsed"] = round(time.time() - t0, 1)
    return raw


def _track_summary(tr: dict) -> dict:
    out = {}
    for h in tr.get("live") or []:
        out[f"{h['horizonH']}h"] = {"ic": h.get("longIC"), "t": h.get("longT"), "hit": h.get("longHit"), "n": h.get("crossSections")}
    return out


# ── features ───────────────────────────────────────────────────

def features(raw: dict) -> dict:
    now = int(time.time() * 1000)
    d = raw.get("detail") or {}
    row = d.get("row") or {}
    v, p = row.get("values") or {}, row.get("pct") or {}
    c1h, c4h, c1d = raw.get("c1h") or [], raw.get("c4h") or [], raw.get("c1d") or []
    f: dict = {"instId": raw["_inst"], "symbol": raw["_base"], "now": now}

    ot = raw.get("okx_ticker") or {}
    px = d.get("last") or (c1h[-1]["c"] if c1h else None) or (float(ot["last"]) if ot else None)
    f["price"] = px
    f["turnover24h"] = d.get("turnover24h") or (float(ot.get("volCcy24h") or 0) * (px or 0) if ot else None)

    # trend / volatility
    cl1, cl4, cld = [c["c"] for c in c1h], [c["c"] for c in c4h], [c["c"] for c in c1d]
    f["ret_1h"] = _exp(v.get("ret_1h")) if v else (cl1[-1] / cl1[-2] - 1 if len(cl1) > 1 else None)
    f["ret_4h"] = _exp(v.get("ret_4h")) if v else (cl1[-1] / cl1[-5] - 1 if len(cl1) > 4 else None)
    f["ret_24h"] = _exp(v.get("ret_24h")) if v.get("ret_24h") is not None else (cl1[-1] / cl1[-25] - 1 if len(cl1) > 24 else None)
    f["ret_7d"] = px / cld[-8] - 1 if px and len(cld) > 7 else None
    f["ret_30d"] = px / cld[-31] - 1 if px and len(cld) > 30 else None
    f["rsi_1h"] = ind.last_valid(ind.rsi(cl1, 14))
    f["rsi_4h"] = ind.last_valid(ind.rsi(cl4, 14))
    f["rsi_1d"] = ind.last_valid(ind.rsi(cld, 14))
    a1, a4 = ind.atr(c1h, 14), ind.atr(c4h, 14)
    f["atr1h_pct"] = a1 / px if a1 and px else None
    f["atr4h_pct"] = a4 / px if a4 and px else None
    e20, e50 = ind.last_valid(ind.ema(cl4, 20)), ind.last_valid(ind.ema(cl4, 50))
    f["ema20_4h"], f["ema50_4h"] = e20, e50
    f["ext_atr"] = (px - e50) / a4 if px and e50 and a4 else None
    m = ind.macd(cl4)
    f["macd_hist_4h"] = ind.last_valid(m["histogram"])
    f["er_24h"] = v.get("eff_ratio_24h") if v.get("eff_ratio_24h") is not None else ind.efficiency_ratio(cl1, 24)
    te = p.get("trend_ema")
    if te is None and v.get("trend_ema") is not None:
        te = 50 + 50 * math.tanh(v["trend_ema"])
    if te is None and e20 and e50 and a4:
        te = 50 + 50 * math.tanh((e20 - e50) / a4)
    f["trend_pct"] = te
    f["beta"] = v.get("beta_btc_48h")
    f["resid_4h"] = _exp(v.get("resid_4h"))
    f["vol_surprise_x"] = math.exp(v["vol_surprise_4h"]) if v.get("vol_surprise_4h") is not None else None
    f["wicks"] = ind.wick_spikes(c1h, a1, 48)
    f["swings_1h"] = ind.swings(c1h[-72:], 3)
    f["swings_4h"] = ind.swings(c4h[-90:], 3)
    if len(c4h) >= 42:
        hi, lo = max(c["h"] for c in c4h[-42:]), min(c["l"] for c in c4h[-42:])
        f["range_7d"] = hi / lo - 1 if lo else None
    if len(c1d) >= 30 and px:
        hi, lo = max(c["h"] for c in c1d[-30:]), min(c["l"] for c in c1d[-30:])
        f["pos_30d"] = (px - lo) / (hi - lo) if hi > lo else 0.5
        f["hi_30d"], f["lo_30d"] = hi, lo
    f["vwap_24h"] = ind.vwap(c1h[-24:]) if len(c1h) >= 24 else None

    # derivatives
    fr = d.get("fundingRate")
    ih = d.get("fundingIntervalH") or 8
    if fr is None and raw.get("okx_funding"):
        fr = float(raw["okx_funding"]["fundingRate"])
        ih = 8
    f["funding_rate"], f["funding_interval_h"] = fr, ih
    f["funding_8h"] = fr / ih * 8 if fr is not None else None
    f["funding_pct_pool"] = p.get("funding_pred_hourly") or p.get("funding_hourly")
    fs = raw.get("fund_series") or []
    if fs:
        f["funding_7d_avg_8h"] = sum(x["v"] for x in fs) / len(fs) / ih * 8
        f["funding_neg_share_7d"] = sum(1 for x in fs if x["v"] < 0) / len(fs)
    oi_usd = d.get("oiUsd") or (float(raw["okx_oi"].get("oiUsd") or 0) if raw.get("okx_oi") else None)
    f["oi_usd"] = oi_usd
    ois = raw.get("oi_series") or []
    f["oi_chg_1h"] = _exp(v.get("oi_chg_1h"))
    f["oi_chg_4h"] = _exp(v.get("oi_chg_4h"))
    f["oi_chg_24h"] = _chg_at(ois, 24, now)
    f["oi_chg_72h"] = _chg_at(ois, 72, now)
    mcap = coin.get("marketCap") if (coin := d.get("coin") or {}) else None
    f["mcap"], f["fdv"] = mcap, coin.get("fdv")
    f["mc_fdv"] = mcap / coin["fdv"] if mcap and coin.get("fdv") else None
    f["oi_mcap"] = oi_usd / mcap if oi_usd and mcap else None
    if f["oi_mcap"] is not None and f["oi_mcap"] > 3:  # CoinGecko 映射错币（市值远小于 OI）
        f["mcap_suspect"] = True
        f["oi_mcap"] = f["mc_fdv"] = None
    f["oi_turnover"] = oi_usd / f["turnover24h"] if oi_usd and f.get("turnover24h") else None
    f["basis"] = v.get("basis")
    if f["basis"] is None and d.get("spotLast") and px:
        f["basis"] = math.log(px / d["spotLast"])
    sp = raw.get("spot")
    if sp and f.get("turnover24h"):
        spot_q = float(sp.get("volCcy24h") or 0)
        f["spot_turnover24h"] = spot_q
        f["perp_spot_ratio"] = f["turnover24h"] / spot_q if spot_q else None
    f["has_spot"] = bool(sp)

    for k in ("ls_all", "ls_top_acc", "ls_top_pos"):
        sr = raw.get(k) or []
        f[k] = sr[-1]["v"] if sr else None
        f[k + "_24h_ago"] = sr[-25]["v"] if len(sr) >= 25 else None
    if f.get("ls_top_pos") and f.get("ls_top_pos_24h_ago"):
        f["top_pos_ratio_chg"] = f["ls_top_pos"] / f["ls_top_pos_24h_ago"] - 1

    # microstructure
    bk = micro.book_stats(raw.get("book") or {})
    f["book"] = bk
    if bk:
        f["imb_1"], f["imb_2"] = bk["imb_1"], bk["imb_2"]
        f["cost_up_2pct"], f["cost_down_2pct"] = bk["cost_up_2pct"], bk["cost_down_2pct"]
        f["spread_bps"] = bk["spread_bps"]
        if oi_usd:
            f["depth2_oi"] = (bk["cost_up_2pct"] + bk["cost_down_2pct"]) / oi_usd
    fl = micro.flow_stats((raw.get("flow") or {}).get("bars") or [], c1h)
    f["flow"] = fl
    if fl:
        f["kyle_lambda"] = fl.get("kyle_lambda_bps_per_m")
        if f["kyle_lambda"] is not None and f["kyle_lambda"] < 0:
            f["kyle_lambda"] = None  # 负斜率无意义（样本噪声）
        f["amihud"] = fl.get("amihud_bps_per_m")
        f["vpin"] = fl.get("vpin")
        f["buy_ratio_24h"] = fl["w24"]["buy_ratio"]
        f["buy_ratio_4h"] = fl["w4"]["buy_ratio"]
    if raw.get("liq"):
        f["liq"] = micro.liq_stats(raw["liq"], now)
    if raw.get("liqmap"):
        f["liqmap"] = micro.liqmap_stats(raw["liqmap"])

    # on-chain
    oc = raw.get("onchain") or {}
    if oc.get("cg"):
        cg = oc["cg"]
        f["cg"] = {k: cg.get(k) for k in ("id", "name", "categories", "market_cap", "fdv", "mc_fdv", "circulating",
                                           "total_supply", "max_supply", "ath_change_pct", "change_7d", "change_30d")}
        f["mcap"] = f["mcap"] or cg.get("market_cap")
        f["mc_fdv"] = f["mc_fdv"] or cg.get("mc_fdv")
        if f.get("oi_mcap") is None and oi_usd and f["mcap"]:
            f["oi_mcap"] = oi_usd / f["mcap"]
    if oc.get("holders"):
        hd = oc["holders"]
        f["holders"] = {k: hd[k] for k in ("chain_id", "address", "holder_count", "top10_pct", "cex_pct", "nonfloat_pct",
                                          "whale10_pct", "top_whale_pct", "flags", "score", "owner_percent", "creator_percent")}
        f["holders"]["top"] = [{"pct": round(h["percent"], 2), "class": h["class"], "tag": h["tag"], "addr": h["address"]}
                               for h in hd["holders"][:10]]
    if oc.get("dex"):
        f["dex_liq_usd"] = sum(x["liquidity_usd"] for x in oc["dex"])
        f["dex_vol_24h"] = sum(x["volume_24h"] for x in oc["dex"])

    # rules (user's own scoring on CTG)
    sc = row.get("scores") or {}
    tr = raw.get("tracks") or {}
    f["rules"] = {rid: {"long": (s.get("long") or {}).get("score"), "short": (s.get("short") or {}).get("score"),
                        "gate": s.get("gate"), "live": tr.get(rid)} for rid, s in sc.items()}
    f["in_pool"] = row.get("active")

    # events
    ev = raw.get("events") or {}
    f["warnings"] = ev.get("warnings") or []
    f["fng"] = (ev.get("fng") or {}).get("value")
    f["ctg_alerts"] = [a for a in (raw.get("alerts") or []) if isinstance(a, dict) and a.get("instId") == raw["_inst"]][:5]
    return f


def analyze(symbol: str, deep: bool = True) -> dict:
    raw = gather(symbol, deep)
    f = features(raw)
    ctrl = risk.control_score(f)
    sd = risk.short_danger(f, ctrl["score"])
    ld = risk.long_danger(f, ctrl["score"], ctrl["phase"]["name"])
    pl = risk.plan(f, ld, sd, ctrl)
    ev = raw.get("events") or {}
    return {
        "symbol": f["symbol"], "instId": f["instId"], "price": f["price"], "elapsed_s": raw["_elapsed"],
        "summary": {
            "long_danger": {k: ld[k] for k in ("score", "level", "coverage", "drivers")},
            "short_danger": {k: sd[k] for k in ("score", "level", "coverage", "drivers")},
            "control": {k: ctrl[k] for k in ("score", "level", "label", "coverage", "drivers", "phase")},
            "plan": pl,
            "warnings": f["warnings"][:10],
        },
        "features": {k: v for k, v in f.items() if k not in ("warnings",)},
        "events": {k: ev.get(k) for k in ("symbol_news", "announcements", "macro", "market_alerts", "fng")},
        "factors": {"long": ld["factors"], "short": sd["factors"], "control": ctrl["factors"]},
        "errors": raw["_errors"],
    }


# ── scan (whole pool, 1 CTG call) ──────────────────────────────

def scan(top: int = 10, min_turnover: float = 3e6, scope: str = "all") -> dict:
    u = ctg.universe(scope=scope, category="1", rule="*", limit=500)
    rows = []
    for r in u.get("rows") or []:
        v = r.get("values") or {}
        if (r.get("turnover12h") or 0) * 2 < min_turnover or r["instId"].startswith(("USDC", "BTC-", "ETH-")):
            continue
        fh = v.get("funding_pred_hourly", v.get("funding_hourly"))
        x = {
            "instId": r["instId"], "last": r.get("last"), "turnover24h_est": (r.get("turnover12h") or 0) * 2,
            "ret_1h": _exp(v.get("ret_1h")), "ret_24h": _exp(v.get("ret_24h")), "oi_chg_4h": _exp(v.get("oi_chg_4h")),
            "funding_8h": fh * 8 if fh is not None else None, "basis": v.get("basis"),
            "vol_x": math.exp(v["vol_surprise_4h"]) if v.get("vol_surprise_4h") is not None else None,
            "resid_4h": _exp(v.get("resid_4h")), "beta": v.get("beta_btc_48h"),
            "oi_mcap": (r["oiUsd"] / r["mcap"]) if r.get("oiUsd") and r.get("mcap") and r["oiUsd"] < 3 * r["mcap"] else None,
            "mcap": r.get("mcap"), "oiUsd": r.get("oiUsd"),
            "scores": {k: ((s.get("long") or {}).get("score"), (s.get("short") or {}).get("score"), s.get("gate"))
                       for k, s in (r.get("scores") or {}).items()},
        }
        f8, oi4, r24 = x["funding_8h"] or 0, x["oi_chg_4h"] or 0, x["ret_24h"] or 0
        b = x["basis"] or 0
        # quick proxies (single-snapshot, for ranking only; deep-dive with analyze)
        x["squeeze"] = (risk.clamp((-f8 - 0.0001) / 0.0009) * 35 + risk.clamp(oi4 / 0.1) * 25
                        + risk.clamp(-b / 0.004) * 15 + risk.clamp(r24 / 0.2) * 10 + risk.clamp(((x["oi_mcap"] or 0) - 0.05) / 0.3) * 15)
        x["long_crowd"] = (risk.clamp((f8 - 0.0002) / 0.0008) * 35 + risk.clamp(oi4 / 0.1) * 20
                           + risk.clamp(b / 0.004) * 15 + risk.clamp(r24 / 0.25) * 20 + risk.clamp(((x["vol_x"] or 1) - 1.5) / 3) * 10)
        rows.append(x)

    def top_by(key, rev=True, flt=None):
        rs = [r for r in rows if r.get(key) is not None and (flt is None or flt(r))]
        return sorted(rs, key=lambda r: r[key], reverse=rev)[:top]

    rule = (ctg.rules() or {}).get("defaultRule")
    for r in rows:
        s = r["scores"].get(rule) or (None, None, None)
        r["rule_long"], r["rule_short"], r["rule_gate"] = s
    return {
        "asOf": u.get("asOfMinute"), "n": len(rows), "default_rule": rule,
        "squeeze_risk": top_by("squeeze", flt=lambda r: (r["funding_8h"] or 0) < 0 or (r["basis"] or 0) < -0.001),
        "long_crowded": top_by("long_crowd", flt=lambda r: (r["funding_8h"] or 0) > 0 and (r["ret_24h"] or 0) > 0),
        "oi_mcap": top_by("oi_mcap"),                # 合约主导、易被控盘
        "volume_spike": top_by("vol_x"),             # 放量异动
        "resid_up": top_by("resid_4h"), "resid_down": top_by("resid_4h", rev=False),
        "rule_long": top_by("rule_long", flt=lambda r: r["rule_gate"] == "PASS"),
        "rule_short": top_by("rule_short", flt=lambda r: r["rule_gate"] == "PASS"),
    }


# ── market overview ────────────────────────────────────────────

def market() -> dict:
    tasks = {
        "btc": lambda: ctg.instrument("BTC-USDT-SWAP"),
        "eth": lambda: ctg.instrument("ETH-USDT-SWAP"),
        "btc4h": lambda: ctg.candles("BTC-USDT-SWAP", "4H", 120),
        "uni": lambda: ctg.universe(scope="all", category="1", rule=None, limit=500),
        "status": ctg.status,
        "alerts": lambda: ctg.alerts(20),
        "events": lambda: events.collect(None, None, 24),
        "btc_liq": lambda: ctg.liquidations("BTC-USDT-SWAP", "1H", 24, 10),
    }
    r, errs = http.parallel(tasks)
    rows = (r.get("uni") or {}).get("rows") or []
    rets = [math.exp(x["values"]["ret_24h"]) - 1 for x in rows if (x.get("values") or {}).get("ret_24h") is not None]
    fund = [x["values"]["funding_hourly"] * 8 for x in rows if (x.get("values") or {}).get("funding_hourly") is not None]
    res = {"breadth": {
        "n": len(rets), "up_share": sum(1 for x in rets if x > 0) / len(rets) if rets else None,
        "median_ret_24h": median(rets) if rets else None,
        "median_funding_8h": median(fund) if fund else None,
        "neg_funding_share": sum(1 for x in fund if x < 0) / len(fund) if fund else None,
    }}
    for k in ("btc", "eth"):
        d = r.get(k) or {}
        v = (d.get("row") or {}).get("values") or {}
        res[k] = {"last": d.get("last"), "ret_24h": _exp(v.get("ret_24h")), "ret_4h": _exp(v.get("ret_4h")),
                  "funding_8h": (d["fundingRate"] / (d.get("fundingIntervalH") or 8) * 8) if d.get("fundingRate") is not None else None,
                  "oi_usd": d.get("oiUsd"), "trend_ema": v.get("trend_ema")}
    c4 = r.get("btc4h") or []
    if c4:
        cl = [c["c"] for c in c4]
        res["btc"]["rsi_4h"] = ind.last_valid(ind.rsi(cl, 14))
        res["btc"]["ema20_4h"] = ind.last_valid(ind.ema(cl, 20))
        res["btc"]["ema50_4h"] = ind.last_valid(ind.ema(cl, 50))
    if r.get("btc_liq"):
        res["btc"]["liq"] = micro.liq_stats(r["btc_liq"], int(time.time() * 1000))["h24"]
    ev = r.get("events") or {}
    res.update({"fng": ev.get("fng"), "macro": ev.get("macro"), "market_alerts": ev.get("market_alerts"),
                "ctg_alerts": (r.get("alerts") or [])[:10],
                "data_status": {k: (r.get("status") or {}).get(k) for k in ("phase", "live", "asOfMinute", "coverage")},
                "errors": errs})
    return res
