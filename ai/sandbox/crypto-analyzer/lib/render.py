"""Human-readable (Chinese) text renderers."""

from __future__ import annotations

from .events import fmt_time

BAR = "─" * 64


def p(x, d=1, sign=True):
    if x is None:
        return "?"
    return f"{x*100:+.{d}f}%" if sign else f"{x*100:.{d}f}%"


def usd(x):
    if x is None:
        return "?"
    a = abs(x)
    return f"${x/1e9:.2f}B" if a >= 1e9 else f"${x/1e6:.2f}M" if a >= 1e6 else f"${x/1e3:.1f}K" if a >= 1e3 else f"${x:.0f}"


def n(x, d=2):
    return "?" if x is None else f"{x:.{d}f}"


def px(x):
    return "?" if x is None else f"{x:.6g}"


def ts(ms, fmt="%m-%d %H:%M"):
    return fmt_time(ms, fmt)


def gauge(score):
    if score is None:
        return "[??????????]"
    k = int(round(score / 10))
    return "[" + "█" * k + "·" * (10 - k) + "]"


def sparkline(values, width=40):
    if not values:
        return ""
    lo, hi = min(values), max(values)
    if hi == lo:
        return "─" * min(len(values), width)
    chars = "▁▂▃▄▅▆▇█"
    step = max(1, len(values) // width)
    return "".join(chars[int((v - lo) / (hi - lo) * 7)] for v in values[::step][:width])


def analyze(r: dict) -> None:
    s, f = r["summary"], r["features"]
    print(BAR)
    print(f" {r['instId']}  ${px(r['price'])}   1h {p(f.get('ret_1h'))}  24h {p(f.get('ret_24h'))}  "
          f"7d {p(f.get('ret_7d'))}  30d {p(f.get('ret_30d'))}   ({r['elapsed_s']}s)")
    print(BAR)
    ld, sd, c = s["long_danger"], s["short_danger"], s["control"]
    print(f" 做多危险度 {gauge(ld['score'])} {n(ld['score'],0):>3} {ld['level']}")
    for d in ld["drivers"][:4]:
        print(f"    · {d}")
    print(f" 做空危险度 {gauge(sd['score'])} {n(sd['score'],0):>3} {sd['level']}")
    for d in sd["drivers"][:4]:
        print(f"    · {d}")
    print(f" 控盘/庄   {gauge(c['score'])} {n(c['score'],0):>3} {c['label']}   阶段推断：{c['phase']['name']}")
    for d in c["drivers"][:4]:
        print(f"    · {d}")
    for w in c["phase"]["why"]:
        print(f"    ▸ {w}")

    pl = s["plan"]
    print(f"\n ▶ 结论：{pl['bias']}")
    print(f"   最大杠杆 ≤{pl['max_leverage']}x，参考止损距离 {pl['stop_distance_pct']}%（1.5×4H ATR）；{pl['position_hint']}")
    if pl["resistances"]:
        print("   压力：" + "；".join(f"{px(x['px'])}({x['dist_pct']:+.1f}% {x['src']})" for x in pl["resistances"][:4]))
    if pl["supports"]:
        print("   支撑：" + "；".join(f"{px(x['px'])}({x['dist_pct']:+.1f}% {x['src']})" for x in pl["supports"][:4]))

    if s["warnings"]:
        print("\n ⚠ 事件提醒")
        for w in s["warnings"][:8]:
            icon = {"severe": "🔴", "supply": "🟠", "listing": "🟢", "positive": "🟢", "macro": "🔵"}.get(w["level"], "·")
            print(f"   {icon} [{w['source']} {fmt_time(w['t'])}] {w['title'][:90]}")

    print(f"\n{BAR}\n 衍生品")
    print(f"   资金费 {p(f.get('funding_8h'),4)}/8h（间隔{f.get('funding_interval_h')}h，7日均 {p(f.get('funding_7d_avg_8h'),4)}，"
          f"负费率占比 {p(f.get('funding_neg_share_7d'),0,False)}）  基差 {p(f.get('basis'),2)}")
    print(f"   OI {usd(f.get('oi_usd'))}  1h {p(f.get('oi_chg_1h'))} 4h {p(f.get('oi_chg_4h'))} 24h {p(f.get('oi_chg_24h'))} "
          f"72h {p(f.get('oi_chg_72h'))}   OI/市值 {p(f.get('oi_mcap'),1,False)}  OI/成交 {n(f.get('oi_turnover'))}")
    print(f"   多空账户比 散户 {n(f.get('ls_all'))}（24h前 {n(f.get('ls_all_24h_ago'))}） 大户账户 {n(f.get('ls_top_acc'))} "
          f"大户持仓 {n(f.get('ls_top_pos'))}（24h前 {n(f.get('ls_top_pos_24h_ago'))}）")
    lq = f.get("liq") or {}
    if lq:
        print(f"   清算 24h：多 {usd(lq['h24']['long'])} / 空 {usd(lq['h24']['short'])}   4h：多 {usd(lq['h4']['long'])} / 空 {usd(lq['h4']['short'])}")
    lm = f.get("liqmap") or {}
    if lm:
        print(f"   估算清算带：上方5% 空头 {usd(lm['short_liq_above_5'])} / 下方5% 多头 {usd(lm['long_liq_below_5'])}  "
              f"±10% 比 {n(lm.get('magnet_ratio_10'))}（>1 向上磁吸）")

    print(" 盘口/成交")
    bk = f.get("book") or {}
    if bk:
        print(f"   点差 {n(bk['spread_bps'],1)}bps  ±1%买占比 {p(bk['imb_1'],0,False)}  ±2%买占比 {p(bk['imb_2'],0,False)}  "
              f"拉升2%需 {usd(bk['cost_up_2pct'])} / 砸2%需 {usd(bk['cost_down_2pct'])}")
        for side, key in (("买墙", "bid_walls"), ("卖墙", "ask_walls")):
            if bk.get(key):
                print(f"   {side}：" + "，".join(f"{px(w['px'])}({w['dist_pct']:+.1f}%) {usd(w['usd'])}" for w in bk[key][:3]))
    fl = f.get("flow") or {}
    if fl:
        w4, w24 = fl["w4"], fl["w24"]
        print(f"   主动买占比 4h {p(w4['buy_ratio'],0,False)} / 24h {p(w24['buy_ratio'],0,False)}  净主动 24h {usd(w24['delta'])}  "
              f"背离 {fl.get('divergence') or '无'}  VPIN {n(fl.get('vpin'))}  Amihud {n(fl.get('amihud_bps_per_m'),1)}")
        if fl.get("cvd_series"):
            print(f"   CVD 48h {sparkline(fl['cvd_series'])}")

    print(" 技术面")
    print(f"   RSI 1h/4h/1d {n(f.get('rsi_1h'),0)}/{n(f.get('rsi_4h'),0)}/{n(f.get('rsi_1d'),0)}  "
          f"4H EMA20/50 {px(f.get('ema20_4h'))}/{px(f.get('ema50_4h'))}  偏离 {n(f.get('ext_atr'),1)}×ATR  "
          f"ATR 1h/4h {p(f.get('atr1h_pct'),2,False)}/{p(f.get('atr4h_pct'),2,False)}")
    print(f"   趋势分位 {n(f.get('trend_pct'),0)}  效率比 {n(f.get('er_24h'))}  β(BTC) {n(f.get('beta'))}  "
          f"30日区间位置 {p(f.get('pos_30d'),0,False)}  放量 {n(f.get('vol_surprise_x'),1)}x  插针 {f.get('wicks')}")

    print(" 筹码/基本面")
    print(f"   市值 {usd(f.get('mcap'))}  FDV {usd(f.get('fdv'))}  MC/FDV {n(f.get('mc_fdv'))}  24h成交 {usd(f.get('turnover24h'))}  "
          f"现货 {'有' if f.get('has_spot') else '无'}  合约/现货成交比 {n(f.get('perp_spot_ratio'),1)}")
    h = f.get("holders")
    if h:
        print(f"   持币 {h['holder_count']} 地址  Top10 {h['top10_pct']:.1f}%（交易所 {h['cex_pct']:.1f}% / 锁仓LP {h['nonfloat_pct']:.1f}% / "
              f"未标注大户 {h['whale10_pct']:.1f}%）  安全分 {h['score']} {'⚠ ' + ','.join(h['flags']) if h['flags'] else ''}")
    if f.get("dex_liq_usd") is not None:
        print(f"   DEX 流动性 {usd(f['dex_liq_usd'])}  DEX 24h量 {usd(f.get('dex_vol_24h'))}")

    rules = f.get("rules") or {}
    if rules:
        print(" CTG 规则分（池内排名，未验证）")
        for rid, x in rules.items():
            live = x.get("live") or {}
            ic = (live.get("4h") or live.get("1h") or {})
            ics = f"  实盘IC {n(ic.get('ic'),3)} t={n(ic.get('t'),1)}" if ic else ""
            print(f"   {rid:<22} 多 {n(x['long'],0):>3} 空 {n(x['short'],0):>3} {x.get('gate') or ''}{ics}")
    if r.get("errors"):
        print(f"\n (部分数据源失败：{', '.join(r['errors'])})")
    print(BAR)


def scan(r: dict) -> None:
    print(f"CTG 监控池扫描 n={r['n']}  asOf {ts(r['asOf'])}  默认规则 {r['default_rule']}")

    def tbl(title, rows, extra):
        print(f"\n■ {title}")
        for x in rows:
            print(f"  {x['instId'].replace('-USDT-SWAP',''):<10} 24h {p(x['ret_24h']):>7}  费率 {p(x['funding_8h'],3):>8}/8h  "
                  f"OI4h {p(x['oi_chg_4h']):>7}  基差 {p(x['basis'],2):>7}  OI/市值 {p(x['oi_mcap'],0,False):>5}  {extra(x)}")

    tbl("逼空风险高（别空）", r["squeeze_risk"], lambda x: f"squeeze={x['squeeze']:.0f}")
    tbl("多头拥挤（别追多）", r["long_crowded"], lambda x: f"crowd={x['long_crowd']:.0f}")
    tbl("OI/市值最高（合约主导，易控盘）", r["oi_mcap"], lambda x: f"OI {usd(x['oiUsd'])} 市值 {usd(x['mcap'])}")
    tbl("放量异动", r["volume_spike"], lambda x: f"放量 {x['vol_x']:.1f}x")
    tbl("残差强（剔除BTC后独立走强）", r["resid_up"], lambda x: f"残差4h {p(x['resid_4h'])}")
    tbl("残差弱", r["resid_down"], lambda x: f"残差4h {p(x['resid_4h'])}")
    tbl(f"规则 {r['default_rule']} 看多", r["rule_long"], lambda x: f"多 {n(x['rule_long'],0)}")
    tbl(f"规则 {r['default_rule']} 看空", r["rule_short"], lambda x: f"空 {n(x['rule_short'],0)}")


def market(r: dict) -> None:
    b = r["breadth"]
    print(BAR)
    for k in ("btc", "eth"):
        x = r[k]
        extra = ""
        if k == "btc" and x.get("rsi_4h") is not None:
            trend = "多头排列" if (x.get("ema20_4h") or 0) > (x.get("ema50_4h") or 0) else "空头排列"
            extra = f"  4H RSI {x['rsi_4h']:.0f} {trend}"
            if x.get("liq"):
                extra += f"  24h清算 多{usd(x['liq']['long'])}/空{usd(x['liq']['short'])}"
        print(f" {k.upper()} ${px(x['last'])}  4h {p(x['ret_4h'])} 24h {p(x['ret_24h'])}  费率 {p(x['funding_8h'],4)}/8h  OI {usd(x['oi_usd'])}{extra}")
    print(f" 广度：{b['n']} 个币 上涨占比 {p(b['up_share'],0,False)}  中位涨跌 {p(b['median_ret_24h'])}  "
          f"中位费率 {p(b['median_funding_8h'],4)}/8h  负费率占比 {p(b['neg_funding_share'],0,False)}")
    fg = r.get("fng")
    if fg:
        print(f" 恐惧贪婪：{fg['value']} {fg['label']}  近7日 {fg['week']}")
    print(BAR)
    if r.get("macro"):
        print(" 宏观日历（高影响）")
        for m in r["macro"]:
            print(f"   {fmt_time(m['t'])} ({m['in_hours']:+.0f}h) {m['country']} {m['title']}  预期 {m['forecast'] or '-'} 前值 {m['previous'] or '-'}")
    if r.get("market_alerts"):
        print(" 重大新闻")
        for a in r["market_alerts"][:8]:
            print(f"   [{','.join(a['tags'])}] {fmt_time(a['t'])} {a['title'][:90]}")
    if r.get("ctg_alerts"):
        print(" CTG 监控告警")
        for a in r["ctg_alerts"][:8]:
            print(f"   {ts(a.get('t') or a.get('ts'))} {a.get('instId')} {a.get('rule') or a.get('name')}: {a.get('message') or a.get('value')}")
    ds = r.get("data_status") or {}
    print(f" 数据：phase={ds.get('phase')} live={ds.get('live')} asOf={ts(ds.get('asOfMinute'))}")


def events(r: dict) -> None:
    if r.get("warnings"):
        print("⚠ 需要注意")
        for w in r["warnings"]:
            print(f"  [{w['level']}] {fmt_time(w['t'])} {w['source']}: {w['title'][:100]}")
    for title, key in (("币种相关新闻", "symbol_news"), ("交易所公告（上/下架）", "announcements"), ("市场重大新闻", "market_alerts")):
        items = r.get(key) or []
        if items:
            print(f"\n■ {title}")
            for i in items[:12]:
                tg = ",".join(i.get("tags") or [])
                print(f"  {fmt_time(i['t'])} [{i['source']}{'|'+tg if tg else ''}] {i['title'][:100]}")
                if i.get("url"):
                    print(f"      {i['url'][:120]}")
    if r.get("macro"):
        print("\n■ 宏观日历（高影响，本周）")
        for m in r["macro"]:
            print(f"  {fmt_time(m['t'])} ({m['in_hours']:+.0f}h) {m['country']} {m['title']}")
    if r.get("fng"):
        print(f"\n恐惧贪婪 {r['fng']['value']} {r['fng']['label']}")
