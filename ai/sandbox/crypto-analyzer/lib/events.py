"""Event risk: news flashes, exchange listing/delisting notices, macro calendar, Fear & Greed.

Every item is tagged with a risk class so the agent can surface what matters:
  severe   — hack/exploit/delist/regulatory/insolvency/depeg: avoid or exit exposure
  supply   — token unlock / large transfers to exchanges / team selling: long danger
  listing  — new listing (esp. Binance/Upbit/Coinbase spot): strong short danger
  positive — buyback/burn/ETF inflow/partnership: mild short danger
  macro    — FOMC/CPI/NFP/tariffs: volatility window for everything
"""

from __future__ import annotations

import os
import re
import time
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from html import unescape

from . import http, okx

RSS_FEEDS = [
    ("PANews", "https://rss.panewslab.com/zh/tvsq/rss"),
    ("CoinTelegraph", "https://cointelegraph.com/rss"),
    ("CoinDesk", "https://www.coindesk.com/arc/outboundfeeds/rss"),
    ("Decrypt", "https://decrypt.co/feed"),
]
BINANCE_CMS = "https://www.binance.com/bapi/composite/v1/public/cms/article/list/query"
FF_CALENDAR = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"
FNG = "https://api.alternative.me/fng/"

# English terms are word-bounded (so "struggle" ≠ rug); Chinese terms match as substrings.
TAGS = [
    ("severe", r"\b(hack(ed|er|s)?|exploit(ed|er)?|drain(ed)?|stolen|attack(ed|er)?|delist(s|ed|ing)?|rug ?pull|rugged|"
               r"SEC|lawsuit|sued|indict(ed|ment)?|investigation|insolven(t|cy)|bankrupt(cy)?|depeg(ged)?|scam|arrest(ed)?|"
               r"sanction(s|ed)?|frozen|withdrawals? (halt|pause|suspend)\w*|monitoring tag)\b|"
               r"被盗|黑客|攻击|漏洞|下架|退市|终止.{0,4}交易|跑路|起诉|调查|破产|暂停(提|充)|脱锚|诈骗|逮捕|制裁|冻结|观察标签"),
    ("supply", r"\b(unlock(s|ed|ing)?|vesting|token release|(deposit(ed|s)?|transferr?(ed|s)?|moved?|sent) .{0,40}to (binance|okx|bybit|coinbase|kraken|bitget|exchange|falconx|wintermute)|"
               r"whales? .{0,20}(sold|dump(ed|s)?|sell(s|ing)?)|team .{0,15}(sold|sell(s|ing)?))\b|"
               r"解锁|释放|(转入|转至|存入|充值).{0,12}(交易所|Binance|OKX|币安|Coinbase|Bybit|Bitget|Gate|Kraken|Upbit|FalconX|Wintermute|做市)|"
               r"向.{0,6}(交易所|Binance|OKX|币安|Coinbase|Bybit|Bitget|Gate|Kraken|Upbit|FalconX|Wintermute).{0,20}(转入|存入|充值)|大额转(账|入)|鲸鱼.{0,6}(卖|抛)|抛售|增发"),
    ("listing", r"\b(will list|lists|listing|launch(es|ed)? .{0,25}perpetual|adds? .{0,15}(to|on) (binance|coinbase|upbit|robinhood)|"
                r"upbit .{0,15}(list|add)|coinbase .{0,15}(list|add)|binance alpha)\b|上线|上币|开放.{0,6}交易|永续合约上线|将上市"),
    ("positive", r"\b(buyback|burn(s|ed)?|ETF|partnership|partners with|integrat(es|ion)|airdrop|mainnet|inflows?)\b|"
                 r"回购|销毁|合作|集成|空投|主网|净流入"),
    ("macro", r"\b(FOMC|Fed|Powell|CPI|PCE|nonfarm|payrolls|NFP|rate (cut|hike)s?|tariffs?|treasury yields?|recession|shutdown)\b|"
              r"美联储|鲍威尔|非农|降息|加息|关税|国债|衰退|停摆"),
]
_TAG_RE = [(k, re.compile(p, re.I)) for k, p in TAGS]


def tag(text: str) -> list[str]:
    tags = [k for k, rx in _TAG_RE if rx.search(text or "")]
    if "supply" in tags:  # "X 转入 Coinbase" 是抛压，不是上所利好
        tags = [t for t in tags if t not in ("listing", "positive")]
    return tags


def symbol_regex(symbol: str, name: str | None = None) -> re.Pattern:
    """Case-sensitive ticker (so HOME ≠ home) or $sym, plus case-insensitive project name."""
    parts = [rf"(?<![A-Za-z0-9]){re.escape(symbol.upper())}(?![A-Za-z0-9])", rf"\${re.escape(symbol)}\b"]
    if name and len(name) >= 4 and name.upper() != symbol.upper():
        parts.append(rf"(?i:\b{re.escape(name)}\b)")
    return re.compile("|".join(parts))


# ── news ───────────────────────────────────────────────────────

def _parse_rss(xml: str, source: str) -> list[dict]:
    items = re.findall(r"<item>(.*?)</item>", xml, re.S) or re.findall(r"<entry>(.*?)</entry>", xml, re.S)
    out = []
    for it in items:
        def grab(tagname):
            m = re.search(rf"<{tagname}[^>]*>(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?</{tagname}>", it, re.S)
            return unescape(re.sub(r"<[^>]+>", "", m.group(1))).strip() if m else ""
        title = grab("title")
        if not title:
            continue
        link = grab("link") or (re.search(r'href="([^"]+)"', it) or [None, ""])[1]
        ds = grab("pubDate") or grab("published") or grab("updated")
        t = 0
        for parse in (parsedate_to_datetime, lambda x: datetime.fromisoformat(x.replace("Z", "+00:00"))):
            try:
                t = int(parse(ds).timestamp() * 1000)
                break
            except (ValueError, TypeError, IndexError):
                continue
        out.append({"source": source, "title": title, "summary": clean_summary(grab("description"), title), "url": link, "t": t})
    return out


_BOILER = re.compile(r"(The post .{0,200}? appeared first on .*$|Read more.*$|Continue reading.*$|点击阅读.*$|原文链接.*$|\[…\]|\[\.\.\.\])", re.S | re.I)


def clean_summary(text: str, title: str = "", limit: int = 180) -> str:
    """Feed description → one or two plain sentences (no boilerplate, no repeat of the title)."""
    t = re.sub(r"\s+", " ", _BOILER.sub("", unescape(re.sub(r"<[^>]+>", " ", text or "")))).strip()
    if title and t.startswith(title[:30]):
        t = t[len(title):].lstrip(" .:-—|")
    if len(t) <= limit:
        return t
    cut = max(t.rfind(x, 0, limit) for x in ("。", ". ", "！", "? ", "；"))
    return (t[:cut + 1] if cut > limit * 0.5 else t[:limit].rstrip() + "…").strip()


# ── same story from several outlets → one entry ────────────────

_STOP = set("the a an and or of to in on for with by at from as is are was were be has have had its it this that after over into "
            "says said will may could new amid about than more most first".split())


def _tokens(title: str) -> set[str]:
    t = title.lower()
    words = {w for w in re.findall(r"[a-z][a-z0-9.$-]{2,}|\d+(?:\.\d+)?", t) if w not in _STOP}
    cjk = re.findall(r"[\u4e00-\u9fff]+", t)
    for run in cjk:  # Chinese has no spaces: character bigrams
        words |= {run[i:i + 2] for i in range(len(run) - 1)}
    return words


def cluster(items: list[dict], thresh: float = 0.34) -> list[dict]:
    """Merge items that report the same story (token overlap of the titles, numbers weigh in).
    The kept entry is the one with the most informative summary; `sources` lists the others."""
    groups: list[dict] = []
    for it in items:
        tk = _tokens(it["title"])
        best, score = None, 0.0
        for g in groups:
            inter = tk & g["_tk"]
            if not inter:
                continue
            j = len(inter) / len(tk | g["_tk"])
            nums = {x for x in inter if x[0].isdigit()}
            j += 0.15 * len(nums)  # "$6.3 billion" in both titles is a strong hint
            if j > score:
                best, score = g, j
        if best is not None and score >= thresh:
            best["sources"].append(it["source"])
            best["_tk"] |= tk
            best["t"] = max(best["t"], it["t"])
            best["tags"] = sorted(set(best.get("tags") or []) | set(it.get("tags") or []))
            if len(it.get("summary") or "") > len(best.get("summary") or ""):
                best.update({k: it[k] for k in ("title", "summary", "url") if it.get(k)})
            continue
        groups.append({**it, "sources": [it["source"]], "_tk": tk})
    for g in groups:
        g.pop("_tk", None)
        g["sources"] = list(dict.fromkeys(g["sources"]))
    return groups


# ── ids, so the agent can ask for one article without carrying URLs around ──

NEWS_CACHE = ".news.json"


def assign_ids(items: list[dict]) -> list[dict]:
    """Give each item a short id (n1, n2…) and remember id → url in the working directory."""
    import json
    try:
        known = json.load(open(NEWS_CACHE))
    except (OSError, ValueError):
        known = {}
    by_url = {v["url"]: k for k, v in known.items() if v.get("url")}
    nxt = 1 + max([int(k[1:]) for k in known if k[1:].isdigit()] or [0])
    for it in items:
        u = it.get("url") or ""
        if not u:
            continue
        k = by_url.get(u)
        if not k:
            k = f"n{nxt}"
            nxt += 1
            known[k] = {"url": u, "title": it["title"], "source": it["source"], "t": it["t"]}
            by_url[u] = k
        it["id"] = k
    try:
        json.dump(known, open(NEWS_CACHE, "w"), ensure_ascii=False)
    except OSError:
        pass
    return items


# ── reading one article: main text only ────────────────────────

_DROP_P = re.compile(r"(subscribe|newsletter|sign up|cookie|advertis|sponsored|disclaimer|all rights reserved|follow us|"
                     r"read more|related:|also read|this article|editorial|免责声明|版权|订阅|关注我们|相关阅读|推荐阅读|广告)", re.I)


def read_article(ref: str, limit: int = 1800) -> dict:
    """Fetch a news article (an id from the last news list, or a URL) and return its main text:
    the <article> body (or the page's longest run of paragraphs) without navigation and boilerplate."""
    import json
    meta = {}
    if not ref.startswith("http"):
        try:
            meta = json.load(open(NEWS_CACHE)).get(ref) or {}
        except (OSError, ValueError):
            meta = {}
        if not meta:
            raise ValueError(f"unknown news id {ref}: run `ca news` or `ca events` first")
        url = meta["url"]
    else:
        url = ref
    html = http.get_text(url, timeout=15, retries=1, ttl=1800)
    html = re.sub(r"(?is)<(script|style|noscript|svg|nav|header|footer|aside|form|figure)[^>]*>.*?</\1>", " ", html)
    def ps(block: str) -> list[str]:
        out_ = []
        for ptxt in re.findall(r"(?is)<p[^>]*>(.*?)</p>", block):
            t = re.sub(r"\s+", " ", unescape(re.sub(r"<[^>]+>", " ", ptxt))).strip()
            if len(t) >= 30 and not _DROP_P.search(t):
                out_.append(t)
        return out_
    # the story is the <article> (or page) with the most paragraph text; related-story cards are short
    blocks = re.findall(r"(?is)<article[^>]*>(.*?)</article>", html) + [html]
    body, paras = html, []
    for b in blocks:
        cand = ps(b)
        if sum(map(len, cand)) > sum(map(len, paras)) * 1.2:
            body, paras = b, cand
    if not paras:  # some Chinese sites put text in <div>s: fall back to visible text blocks
        txt = unescape(re.sub(r"<[^>]+>", "\n", body))
        paras = [x.strip() for x in txt.split("\n") if len(x.strip()) >= 40 and not _DROP_P.search(x)]
    title = meta.get("title") or unescape(re.sub(r"<[^>]+>", "", (re.search(r"(?is)<title[^>]*>(.*?)</title>", html) or [None, ""])[1])).strip()
    text, out = "", []
    for ptxt in dict.fromkeys(paras):  # keep order, drop repeats
        if len(text) + len(ptxt) > limit:
            if not out:
                out.append(ptxt[:limit] + "…")
            break
        out.append(ptxt)
        text += ptxt
    return {"id": ref if not ref.startswith("http") else "", "title": title, "source": meta.get("source", ""), "t": meta.get("t", 0),
            "url": url, "paragraphs": out, "truncated": sum(len(x) for x in paras) > sum(len(x) for x in out)}


# roundups, price predictions and promos carry no new information for a trade
_FILLER = re.compile(r"what happened in crypto today|price predictions?|price analysis|technical analysis:|daily recap|weekly recap|"
                     r"morning brief|newsletter|podcast|sponsored|press release|how to buy|best crypto to buy|早报|晚报|周报|日报|行情分析", re.I)


def _blockbeats(size: int = 30) -> list[dict]:
    key = os.environ.get("BLOCKBEATS_API_KEY")
    if not key:
        return []
    d = http.get_json("https://api.theblockbeats.news/v1/open-api/open-flash", {"size": size, "page": 1},
                      {"Authorization": f"Bearer {key}"}, ttl=300)
    items = ((d.get("data") or {}).get("data") if isinstance(d.get("data"), dict) else d.get("data")) or []
    return [{"source": "BlockBeats", "title": i.get("title"), "summary": re.sub(r"<[^>]+>", "", i.get("content") or "")[:280],
             "url": i.get("link") or "", "t": int(i.get("add_time") or 0) * 1000} for i in items]


def news(hours: int = 24, symbol: str | None = None, name: str | None = None, limit: int = 30) -> dict:
    tasks = {src: (lambda u=url, s=src: _parse_rss(http.get_text(u, timeout=12, retries=1, ttl=300), s)) for src, url in RSS_FEEDS}
    tasks["BlockBeats"] = _blockbeats
    res, errs = http.parallel(tasks)
    cutoff = (time.time() - hours * 3600) * 1000
    rx = symbol_regex(symbol, name) if symbol else None
    items, seen = [], set()
    for lst in res.values():
        for it in lst:
            if not it["t"] or it["t"] < cutoff:
                continue
            text = f"{it['title']} {it['summary']}"
            if rx and not rx.search(text):
                continue
            k = it["title"][:40]
            if k in seen:
                continue
            seen.add(k)
            if _FILLER.search(it["title"]):
                continue
            it["tags"] = tag(it["title"])
            items.append(it)
    items.sort(key=lambda x: x["t"], reverse=True)
    items = assign_ids(cluster(items)[:limit])
    return {"items": items, "errors": errs}


# ── exchange announcements ─────────────────────────────────────

def _binance(catalog: int, kind: str) -> list[dict]:
    d = http.get_json(BINANCE_CMS, {"type": 1, "catalogId": catalog, "pageNo": 1, "pageSize": 20}, ttl=600)
    arts = (((d.get("data") or {}).get("catalogs") or [{}])[0]).get("articles") or []
    return [{"source": "Binance", "type": kind, "title": a.get("title"), "t": a.get("releaseDate") or 0,
             "url": f"https://www.binance.com/en/support/announcement/{a.get('code')}"} for a in arts]


def announcements(symbol: str | None = None, name: str | None = None, days: int = 14) -> dict:
    tasks = {
        "okx_list": lambda: okx.announcements("announcements-new-listings"),
        "okx_delist": lambda: okx.announcements("announcements-delistings"),
        "bn_list": lambda: _binance(48, "listing"),
        "bn_delist": lambda: _binance(161, "delisting"),
    }
    res, errs = http.parallel(tasks)
    cutoff = (time.time() - days * 86400) * 1000
    rx = symbol_regex(symbol, name) if symbol else None
    out = []
    for k, lst in res.items():
        for a in lst:
            if a["t"] < cutoff or (rx and not rx.search(a["title"] or "")):
                continue
            a["tags"] = ["severe"] if "delist" in k else ["listing"]
            out.append(a)
    out.sort(key=lambda x: x["t"], reverse=True)
    return {"items": out, "errors": errs}


# ── macro & sentiment ──────────────────────────────────────────

def macro(hours_ahead: int = 72, hours_back: int = 12) -> list[dict]:
    rows = http.get_json(FF_CALENDAR, ttl=3600, retries=0, timeout=8)  # FF 限频严格：不重试，失败用旧缓存
    now = time.time()
    out = []
    for r in rows:
        if r.get("impact") != "High" or r.get("country") not in ("USD", "CNY", "JPY", "EUR"):
            continue
        try:
            t = datetime.fromisoformat(r["date"]).timestamp()
        except (ValueError, KeyError):
            continue
        if now - hours_back * 3600 <= t <= now + hours_ahead * 3600:
            out.append({"t": int(t * 1000), "country": r["country"], "title": r["title"],
                        "forecast": r.get("forecast"), "previous": r.get("previous"),
                        "in_hours": round((t - now) / 3600, 1)})
    return sorted(out, key=lambda x: x["t"])


def fear_greed() -> dict:
    d = http.get_json(FNG, {"limit": 7}, ttl=1800)["data"]
    return {"value": int(d[0]["value"]), "label": d[0]["value_classification"],
            "week": [int(x["value"]) for x in reversed(d)]}


def collect(symbol: str | None = None, name: str | None = None, hours: int = 48) -> dict:
    """Everything event-related in one parallel call (symbol optional)."""
    tasks = {
        "news_symbol": (lambda: news(hours * 3, symbol, name, 15)) if symbol else None,
        "news_market": lambda: news(min(hours, 24), None, None, 40),
        "announcements": lambda: announcements(symbol, name),
        "macro": macro,
        "fng": fear_greed,
    }
    res, errs = http.parallel({k: v for k, v in tasks.items() if v})
    market_news = (res.get("news_market") or {}).get("items", [])
    out = {
        "symbol_news": (res.get("news_symbol") or {}).get("items", []),
        "market_alerts": [n for n in market_news if {"severe", "macro"} & set(n["tags"])][:12],
        "announcements": (res.get("announcements") or {}).get("items", [])[:15],
        "macro": res.get("macro") or [],
        "fng": res.get("fng"),
        "errors": errs,
    }
    warn = []
    for n in out["symbol_news"]:
        for tg in n["tags"]:
            if tg in ("severe", "supply", "listing", "positive"):
                warn.append({"level": tg, "title": n["title"], "t": n["t"], "source": n["source"]})
    for a in out["announcements"]:
        if symbol:
            warn.append({"level": a["tags"][0], "title": a["title"], "t": a["t"], "source": a["source"]})
    for m in out["macro"]:
        if -2 <= m["in_hours"] <= 24:
            warn.append({"level": "macro", "title": f"{m['country']} {m['title']}（{m['in_hours']:+.1f}h）", "t": m["t"], "source": "ForexFactory"})
    out["warnings"] = warn
    return out


def _tz():
    try:
        from zoneinfo import ZoneInfo
        return ZoneInfo(os.environ.get("CRYPTO_TZ", "Asia/Shanghai"))
    except Exception:  # noqa: BLE001 — no tzdata: fall back to UTC+8
        from datetime import timedelta
        return timezone(timedelta(hours=8))


TZ = _tz()


def fmt_time(ms: int, fmt: str = "%m-%d %H:%M") -> str:
    if not ms:
        return "-"
    return datetime.fromtimestamp(ms / 1000, tz=TZ).strftime(fmt)
