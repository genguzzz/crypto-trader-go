#!/usr/bin/env python3
"""crypto-analyzer v2 — altcoin perp risk analysis (CTG self-hosted data + OKX + on-chain + events).

All commands support -f json → {"ok":true,"cmd":...,"data":...}.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys

SCRIPT_DIR = os.path.dirname(os.path.realpath(__file__))
sys.path.insert(0, SCRIPT_DIR)

from lib import ctg, emit, events, indicators as ind, micro, okx, onchain, render, report  # noqa: E402


def _inst(symbol: str) -> str:
    return ctg.inst_id(symbol)


def _pick(sym: str, market: str) -> tuple[str, str]:
    return okx.resolve_inst_id(sym, market)


def _candles(inst: str, bar: str, limit: int) -> list[dict]:
    if inst.endswith("-SWAP") and ctg.available() and bar in ("1m", "5m", "15m", "1H", "4H"):
        try:
            return ctg.candles(inst, bar, limit)
        except Exception:  # noqa: BLE001 — fall back to OKX
            pass
    if not inst.endswith("-SWAP") and ctg.available() and bar in ("1m", "5m", "15m", "1H", "4H"):
        try:
            return ctg.candles(inst + "-SWAP", bar, limit, market="spot")
        except Exception:  # noqa: BLE001
            pass
    return okx.candles(inst, "1Dutc" if bar == "1D" else bar, limit)


# ── commands ───────────────────────────────────────────────────

def cmd_analyze(a):
    r = report.analyze(a.symbol, deep=not a.quick)
    if a.format == "json" and not a.full:
        r.pop("factors", None)
        r["features"].pop("book", None)
        (r["features"].get("flow") or {}).pop("cvd_series", None)
    emit.emit("analyze", r, a.format, render.analyze)


def cmd_scan(a):
    emit.emit("scan", report.scan(a.top, a.min_vol, a.scope), a.format, render.scan)


def cmd_market(a):
    emit.emit("market", report.market(), a.format, render.market)


def cmd_events(a):
    sym = okx.normalize_symbol(a.symbol) if a.symbol else None
    emit.emit("events", events.collect(sym, a.name, a.hours), a.format, render.events)


def cmd_news(a):
    if a.read:
        return emit.emit("article", events.read_article(a.read, a.chars), a.format, render.article)
    d = events.news(a.hours, okx.normalize_symbol(a.keyword) if a.keyword else None, a.name, a.limit)
    emit.emit("news", d, a.format, lambda x: render.events({"symbol_news" if a.keyword else "market_alerts": x["items"]}))


# one call for a full coin analysis: every section runs in parallel as its own `ca` process (and the
# ctg sandbox's `rev` scorer when present); output is the text views in a fixed order, each capped
BRIEF = [  # (title, argv with {s} for the symbol, char cap); analyze already covers funding/OI/flow/book/technicals
    ("大盘", ["market"], 900),
    ("反转评分 1H", ["@rev", "{s}", "-b", "1H"], 700),
    ("风险、衍生品、技术面与计划", ["analyze", "{s}", "--quick"], 3200),
    ("清算（已发生 + 估算密集区）", ["liq", "{s}"], 800),
    ("新闻与事件", ["events", "{s}"], 1800),
]


def cmd_brief(a):
    import subprocess
    from concurrent.futures import ThreadPoolExecutor
    sym = okx.normalize_symbol(a.symbol)
    # ctg's sandbox scorer (util-linux also ships a `rev`, so look for ours by path)
    rev = next((p for p in ("/opt/ca/bin/rev", os.path.expanduser("~/.local/bin/ctg-rev")) if os.path.exists(p)), None)

    def run(item):
        title, argv, cap = item
        argv = [x.format(s=sym) for x in argv]
        if argv[0] == "@rev":
            if not rev:
                return title, ""
            cmd = [rev] + argv[1:]
        else:
            cmd = [sys.executable, os.path.abspath(__file__)] + argv
        try:
            out = subprocess.run(cmd, capture_output=True, text=True, timeout=90).stdout.strip()
        except subprocess.TimeoutExpired:
            out = "（超时）"
        if argv[0] == "market":  # its headline list repeats the news section
            out = re.sub(r"\n 重大新闻\n(?:   .*\n?)*", "\n", out)
        if len(out) > cap:
            out = out[:cap].rsplit("\n", 1)[0] + "\n…"
        return title, out

    with ThreadPoolExecutor(len(BRIEF)) as ex:
        parts = list(ex.map(run, BRIEF))
    print(f"# {sym} 数据包（并行抓取；需要更细再单独调对应命令）")
    for title, out in parts:
        if out:
            print(f"\n## {title}\n{out}")


def cmd_quote(a):
    if ctg.available() and a.market != "spot":
        d = ctg.instrument(_inst(a.symbol))
        d.pop("row", None)
        return emit.emit("quote", d, a.format, lambda x: print(
            f"{x['instId']} ${render.px(x['last'])}  24h {render.p(x['last']/x['open24h']-1 if x.get('open24h') else None)}  "
            f"成交 {render.usd(x.get('turnover24h'))}  OI {render.usd(x.get('oiUsd'))}  "
            f"费率 {render.p(x.get('fundingRate'),4)}/{x.get('fundingIntervalH')}h  市值 {render.usd((x.get('coin') or {}).get('marketCap'))}"))
    inst, m = _pick(a.symbol, a.market)
    t = okx.ticker(inst)
    emit.emit("quote", t, a.format, lambda x: print(f"{inst} ${x['last']}  24h高/低 {x['high24h']}/{x['low24h']}"))


def cmd_kline(a):
    inst = _inst(a.symbol) if a.market != "spot" else _pick(a.symbol, "spot")[0]
    rows = _candles(inst, a.bar, a.limit)

    def show(rs):
        for r in rs[-a.show:]:
            print(f"{render.ts(r['t'])}  O {r['o']:<10.6g} H {r['h']:<10.6g} L {r['l']:<10.6g} C {r['c']:<10.6g} 额 {render.usd(r.get('q'))}")
        print("Close " + render.sparkline([r["c"] for r in rs]))
    emit.emit("kline", rows, a.format, show)


def cmd_indicators(a):
    inst = _inst(a.symbol) if a.market != "spot" else _pick(a.symbol, "spot")[0]
    rows = _candles(inst, a.bar, a.limit)
    cl = [r["c"] for r in rows]
    m = ind.macd(cl)
    bb = ind.bollinger(cl)
    atr = ind.atr(rows)
    d = {"instId": inst, "bar": a.bar, "close": cl[-1], "rsi": ind.last_valid(ind.rsi(cl, 14)),
         "macd": ind.last_valid(m["macd"]), "signal": ind.last_valid(m["signal"]), "hist": ind.last_valid(m["histogram"]),
         "bb_upper": ind.last_valid(bb["upper"]), "bb_mid": ind.last_valid(bb["middle"]), "bb_lower": ind.last_valid(bb["lower"]),
         "ema20": ind.last_valid(ind.ema(cl, 20)), "ema50": ind.last_valid(ind.ema(cl, 50)),
         "atr": atr, "atr_pct": atr / cl[-1] if atr else None, "er_24": ind.efficiency_ratio(cl, 24),
         "swings": ind.swings(rows[-90:]), "wicks": ind.wick_spikes(rows, atr)}
    emit.emit("indicators", d, a.format, lambda x: print(json.dumps(x, ensure_ascii=False, indent=1, default=str)))


def cmd_depth(a):
    inst = _inst(a.symbol) if a.market != "spot" else _pick(a.symbol, "spot")[0]
    book = ctg.orderbook(inst, a.size) if (ctg.available() and inst.endswith("-SWAP")) else okx.order_book(inst, a.size)
    st = micro.book_stats(book)

    def show(x):
        print(f"{inst} mid {render.px(x['mid'])}  点差 {x['spread_bps']:.1f}bps")
        for k, b in x["bands"].items():
            print(f"  ±{k:<4}% 买 {render.usd(b['bid']):>9}  卖 {render.usd(b['ask']):>9}  买占比 {render.p(b['imb'],0,False)}")
        for side in ("bid_walls", "ask_walls"):
            for w in x[side]:
                print(f"  {'买墙' if side=='bid_walls' else '卖墙'} {render.px(w['px'])} ({w['dist_pct']:+.2f}%) {render.usd(w['usd'])} ×{w['x_median'] or 0:.0f}中位")
    emit.emit("depth", st, a.format, show)


def cmd_flow(a):
    inst = _inst(a.symbol)
    fl = ctg.orderflow(inst, a.bar, a.limit)
    st = micro.flow_stats(fl.get("bars") or [], ctg.candles(inst, a.bar, a.limit))
    st["bars"] = fl.get("bars")

    def show(x):
        for b in (x["bars"] or [])[-12:]:
            tot = b["buy"] + b["sell"]
            print(f"  {render.ts(b['t'])} 买 {render.usd(b['buy']):>9} 卖 {render.usd(b['sell']):>9} 净 {render.usd(b['buy']-b['sell']):>9}  买占比 {b['buy']/tot:.0%}")
        print(f"  4根 {x['w4']}  \n  24根 {x['w24']}\n  背离 {x.get('divergence')}  Kyleλ {render.n(x.get('kyle_lambda_bps_per_m'),1)}  "
              f"Amihud {render.n(x.get('amihud_bps_per_m'),1)}  VPIN {render.n(x.get('vpin'))}")
        print("  CVD " + render.sparkline(x.get("cvd_series") or []))
    emit.emit("flow", st, a.format, show)


def cmd_liq(a):
    inst = _inst(a.symbol)
    import time
    lq, lm = ctg.liquidations(inst, a.bar, a.limit, 20), ctg.liqmap(inst, a.days)
    d = {"realized": micro.liq_stats(lq, int(time.time() * 1000)), "map": micro.liqmap_stats(lm), "recent": lq.get("recent")}

    def show(x):
        r, m = x["realized"], x["map"]
        for k in ("h4", "h24", "h48"):
            print(f"  已清算 {k}: 多 {render.usd(r[k]['long'])}  空 {render.usd(r[k]['short'])}")
        if m:
            print(f"  估算（{m['note']}）")
            print(f"  上方5%/10% 空头清算 {render.usd(m['short_liq_above_5'])}/{render.usd(m['short_liq_above_10'])}；"
                  f"下方5%/10% 多头清算 {render.usd(m['long_liq_below_5'])}/{render.usd(m['long_liq_below_10'])}")
            for c in m["short_clusters"]:
                print(f"    空头密集 {render.px(c['px'])} ({c['dist_pct']:+.1f}%) {render.usd(c['usd'])}")
            for c in m["long_clusters"]:
                print(f"    多头密集 {render.px(c['px'])} ({c['dist_pct']:+.1f}%) {render.usd(c['usd'])}")
    emit.emit("liq", d, a.format, show)


def cmd_funding(a):
    inst = _inst(a.symbol)
    if ctg.available():
        d = ctg.instrument(inst)
        hist = ctg.series(inst, "funding", a.hours)
        cur = {"rate": d.get("fundingRate"), "interval_h": d.get("fundingIntervalH"), "next": d.get("fundingTime")}
    else:
        fr = okx.funding_rate(inst)
        hist = okx.funding_history(inst, 30)
        cur = {"rate": float(fr["fundingRate"]), "interval_h": None, "next": int(fr["nextFundingTime"])}
    out = {"instId": inst, "current": cur, "history": hist}

    def show(x):
        c = x["current"]
        print(f"{inst} 当前 {render.p(c['rate'],4)}/{c['interval_h']}h  下次 {render.ts(c['next'])}")
        for h in x["history"][-20:]:
            print(f"  {render.ts(h['t'])}  {render.p(h['v'],4)}")
    emit.emit("funding", out, a.format, show)


def cmd_oi(a):
    inst = _inst(a.symbol)
    s = ctg.series(inst, "oi", a.hours)
    vals = [x["u"] for x in s]
    d = {"instId": inst, "last_usd": vals[-1] if vals else None, "first_usd": vals[0] if vals else None,
         "chg": (s[-1]["v"] / s[0]["v"] - 1) if len(s) > 1 and s[0]["v"] else None, "points": len(s)}
    emit.emit("oi", d if a.format == "text" else {**d, "series": s[:: max(1, len(s) // 200)]}, a.format,
              lambda x: print(f"{inst} OI {render.usd(x['last_usd'])}  {a.hours}h 币本位变化 {render.p(x['chg'])}  {render.sparkline(vals)}"))


def cmd_longshort(a):
    inst = _inst(a.symbol)
    from lib import http
    r, _ = http.parallel({"all": lambda: okx.ls_account_ratio(inst, a.period, a.limit),
                          "top_acc": lambda: okx.ls_top_account_ratio(inst, a.period, a.limit),
                          "top_pos": lambda: okx.ls_top_position_ratio(inst, a.period, a.limit)})

    def show(x):
        print(f"{inst} 多空比（散户账户 / 大户账户 / 大户持仓）")
        al, ta, tp = x.get("all") or [], x.get("top_acc") or [], x.get("top_pos") or []
        for i in range(-min(12, len(al)), 0):
            print(f"  {render.ts(al[i]['t'])}  {al[i]['v']:.2f}  /  {ta[i]['v'] if len(ta) >= -i else 0:.2f}  /  {tp[i]['v'] if len(tp) >= -i else 0:.2f}")
    emit.emit("longshort", r, a.format, show)


def cmd_rank(a):
    rows = okx.rank(a.market.upper(), a.dir, a.top, a.min_vol)
    emit.emit("rank", rows, a.format, lambda xs: [print(f"  {i:2}. {r['symbol']:<10} {r['change_pct']:+.2f}%  {render.usd(r['vol_usdt'])}  ${r['last']:.6g}")
                                                    for i, r in enumerate(xs, 1)])


def cmd_security(a):
    r = onchain.scan(a.address, a.chain)

    def show(x):
        print(f"安全分 {x['score']}  红旗 {x['flags'] or '无'}  持币 {x['holder_count']}  开源 {x['is_open_source']}  买/卖税 {x['buy_tax']}/{x['sell_tax']}")
        print(f"Top10 {x['top10_pct']:.1f}%  交易所 {x['cex_pct']:.1f}%  锁仓/LP {x['nonfloat_pct']:.1f}%  未标注大户 {x['whale10_pct']:.1f}%")
        for h in x["holders"][:15]:
            print(f"  {h['percent']:6.2f}%  {h['class']:<8} {h['tag'][:16]:<16} {h['address']}")
    emit.emit(a.cmd, r, a.format, show)


def cmd_tokenomics(a):
    cid = None
    if ctg.available():
        try:
            cid = (ctg.instrument(_inst(a.symbol)).get("coin") or {}).get("id")
        except Exception:  # noqa: BLE001
            pass
    cid = cid or onchain.cg_search(okx.normalize_symbol(a.symbol))
    if not cid:
        raise RuntimeError(f"CoinGecko 找不到 {a.symbol}")
    emit.emit("tokenomics", onchain.cg_coin(cid), a.format, lambda x: print(json.dumps(x, ensure_ascii=False, indent=1)))


def cmd_dex(a):
    t = a.target
    pools = onchain.dex_by_token(t)[:10] if t.startswith("0x") or len(t) > 30 else onchain.dex_search(t)[:10]
    emit.emit("dex", pools, a.format, lambda xs: [print(f"  {p['chain']:<8} {p['dex']:<12} {p['symbol']:<8} liq {render.usd(p['liquidity_usd'])}  vol {render.usd(p['volume_24h'])}  {p['address']}") for p in xs])


def cmd_alerts(a):
    al = ctg.alerts(a.limit)
    if a.symbol:
        al = [x for x in al if x.get("instId") == _inst(a.symbol)]
    emit.emit("alerts", al, a.format, lambda xs: [print(json.dumps(x, ensure_ascii=False)) for x in xs])


def cmd_rules(a):
    rs = ctg.rules()
    if a.track:
        rs["tracks"] = {r["id"]: report._track_summary(ctg.rule_track(r["id"])) for r in rs["rules"]}

    def show(x):
        for r in x["rules"]:
            print(f"{r['id']:<22} {r['name']}\n    long {r['long']}  short {r['short']}\n    {r['description'][:120]}")
            if x.get("tracks"):
                print(f"    实盘: {x['tracks'].get(r['id'])}")
    emit.emit("rules", rs, a.format, show)


def cmd_ctg(a):
    params = dict(kv.split("=", 1) for kv in a.params)
    emit.emit("ctg", ctg.get(a.path if a.path.startswith("/") else "/" + a.path, params), "json")


def cmd_status(a):
    from lib import http
    r, e = http.parallel({"me": ctg.me, "status": ctg.status})
    emit.emit("status", {**r, "errors": e, "base": ctg.BASE, "key_configured": ctg.available()}, "json")


# ── parser ─────────────────────────────────────────────────────

def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="crypto-analyzer v2 (-f json for agents)")
    sub = p.add_subparsers(dest="cmd", required=True)

    def sp(name, fn, help_, *args):
        x = sub.add_parser(name, help=help_)
        for spec in args:
            flags, kw = spec
            x.add_argument(*flags, **kw)
        emit.add_format(x)
        x.set_defaults(fn=fn)
        return x

    SYM = (["symbol"], {})
    MKT = (["--market", "-m"], {"default": "swap", "choices": ["swap", "spot", "auto"]})
    sp("analyze", cmd_analyze, "一键：做多/做空危险度 + 控盘/庄 + 交易计划 + 事件", SYM,
       (["--quick"], {"action": "store_true", "help": "跳过链上/规则战绩，更快"}),
       (["--full"], {"action": "store_true", "help": "JSON 带全部因子明细"}))
    sp("scan", cmd_scan, "全池扫描：逼空风险/多头拥挤/OI市值比/异动/规则信号",
       (["--top"], {"type": int, "default": 10}), (["--min-vol"], {"type": float, "default": 3e6}),
       (["--scope"], {"default": "all", "choices": ["all", "active", "watch"]}))
    sp("brief", cmd_brief, "一次拿齐分析一个币需要的数据：大盘/反转/风险/费率/OI/清算/主动买卖/指标/新闻", SYM)
    sp("market", cmd_market, "大盘：BTC/ETH、广度、费率、恐惧贪婪、宏观、重大新闻")
    sp("events", cmd_events, "事件风险：新闻/上下架公告/宏观日历", (["symbol"], {"nargs": "?"}),
       (["--name"], {"default": None}), (["--hours"], {"type": int, "default": 48}))
    sp("news", cmd_news, "新闻摘要（同一事件合并、带 id）；--read <id|url> 读正文", (["keyword"], {"nargs": "?"}), (["--name"], {"default": None}),
       (["--hours"], {"type": int, "default": 24}), (["--limit"], {"type": int, "default": 20}),
       (["--read"], {"default": None, "help": "上次列表里的 id（如 n3）或文章 URL：抓取并提取正文"}),
       (["--chars"], {"type": int, "default": 1800, "help": "--read 正文最多字数"}))
    sp("quote", cmd_quote, "行情快照", SYM, MKT)
    sp("kline", cmd_kline, "K线", SYM, MKT, (["--bar", "-b"], {"default": "1H"}), (["--limit", "-l"], {"type": int, "default": 100}),
       (["--show"], {"type": int, "default": 15}))
    sp("indicators", cmd_indicators, "RSI/MACD/BB/EMA/ATR/摆动点/插针", SYM, MKT, (["--bar", "-b"], {"default": "1H"}),
       (["--limit", "-l"], {"type": int, "default": 200}))
    sp("depth", cmd_depth, "盘口深度/墙/拉砸成本", SYM, MKT, (["--size"], {"type": int, "default": 400}))
    sp("flow", cmd_flow, "主动买卖/CVD/背离/Kyle λ/VPIN", SYM, (["--bar", "-b"], {"default": "1H"}),
       (["--limit", "-l"], {"type": int, "default": 48}))
    sp("liq", cmd_liq, "已发生清算 + 估算清算地图", SYM, (["--bar", "-b"], {"default": "1H"}),
       (["--limit", "-l"], {"type": int, "default": 48}), (["--days"], {"type": int, "default": 7}))
    sp("funding", cmd_funding, "资金费率", SYM, (["--hours"], {"type": int, "default": 168}))
    sp("oi", cmd_oi, "持仓量序列", SYM, (["--hours"], {"type": int, "default": 72}))
    sp("longshort", cmd_longshort, "多空比（散户/大户账户/大户持仓）", SYM, (["--period"], {"default": "1H"}),
       (["--limit", "-l"], {"type": int, "default": 48}))
    sp("rank", cmd_rank, "OKX 涨跌榜", (["--market"], {"default": "swap", "choices": ["spot", "swap"]}),
       (["--dir"], {"default": "gain", "choices": ["gain", "loss"]}), (["--top"], {"type": int, "default": 30}),
       (["--min-vol"], {"type": float, "default": 2e6}))
    for name in ("security", "holders"):
        sp(name, cmd_security, "合约安全 + 持仓分布（GoPlus）", (["address"], {}), (["--chain"], {"default": "bsc"}))
    sp("tokenomics", cmd_tokenomics, "代币经济学（CoinGecko）", SYM)
    sp("dex", cmd_dex, "DEX 流动性", (["target"], {}))
    sp("alerts", cmd_alerts, "CTG 监控告警", (["symbol"], {"nargs": "?"}), (["--limit"], {"type": int, "default": 50}))
    sp("rules", cmd_rules, "CTG 打分规则（--track 带实盘战绩）", (["--track"], {"action": "store_true"}))
    sp("ctg", cmd_ctg, "CTG 原始接口透传：ctg /path k=v ...", (["path"], {}), (["params"], {"nargs": "*"}))
    sp("status", cmd_status, "CTG key 与数据新鲜度")
    return p


def main():
    a = build_parser().parse_args()
    try:
        a.fn(a)
    except Exception as e:  # noqa: BLE001
        emit.emit_error(a.cmd, f"{type(e).__name__}: {e}", getattr(a, "format", "text"))


if __name__ == "__main__":
    main()
