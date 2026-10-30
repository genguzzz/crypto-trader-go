"""OKX public REST — spot, swap, rubik stats, announcements. Fallback/complement to CTG."""

from __future__ import annotations

from . import http

BASES = ("https://www.okx.com", "https://aws.okx.com")

CHAIN_MAP = {
    "eth": "1", "ethereum": "1", "1": "1",
    "bsc": "56", "bnb": "56", "56": "56",
    "base": "8453", "8453": "8453",
    "polygon": "137", "137": "137",
    "arbitrum": "42161", "arb": "42161", "42161": "42161",
    "sol": "solana", "solana": "solana",
}


class OKXError(RuntimeError):
    def __init__(self, code: str, msg: str) -> None:
        self.code = code
        super().__init__(f"OKX error {code}: {msg}")


class InstrumentNotFoundError(RuntimeError):
    pass


def _get(path: str, params: dict | None = None, ttl: float = 5) -> dict:
    last: Exception | None = None
    for base in BASES:
        try:
            data = http.get_json(base + path, params, timeout=15, retries=1, ttl=ttl)
        except Exception as e:  # noqa: BLE001 — try the next host
            last = e
            continue
        if str(data.get("code")) == "0":
            return data
        raise OKXError(str(data.get("code")), data.get("msg") or "unknown")
    raise RuntimeError(f"OKX request failed: {last}")


def normalize_symbol(sym: str) -> str:
    s = sym.upper().strip().replace("/", "-")
    for suffix in ("-USDT-SWAP", "-USDT", "USDT"):
        if s.endswith(suffix) and len(s) > len(suffix):
            s = s[: -len(suffix)]
            break
    return s.replace("-", "")


def _exists(inst_id: str, inst_type: str) -> bool:
    try:
        return bool(_get("/api/v5/public/instruments", {"instType": inst_type, "instId": inst_id}, ttl=3600).get("data"))
    except OKXError as e:
        if e.code == "51001":
            return False
        raise


def resolve_inst_id(symbol: str, market: str = "auto") -> tuple[str, str]:
    base = normalize_symbol(symbol)
    spot, swap = f"{base}-USDT", f"{base}-USDT-SWAP"
    if market == "spot":
        if _exists(spot, "SPOT"):
            return spot, "spot"
        raise InstrumentNotFoundError(f"{symbol} 在 OKX 现货没有交易对 ({spot})")
    if market in ("swap", "perp", "futures", "contract"):
        if _exists(swap, "SWAP"):
            return swap, "swap"
        raise InstrumentNotFoundError(f"{symbol} 在 OKX 永续没有交易对 ({swap})")
    if _exists(swap, "SWAP"):
        return swap, "swap"
    if _exists(spot, "SPOT"):
        return spot, "spot"
    raise InstrumentNotFoundError(f"{symbol} 不在 OKX 上市：{spot} / {swap} 均不存在")


def instrument(inst_id: str) -> dict:
    t = "SWAP" if inst_id.endswith("-SWAP") else "SPOT"
    return _get("/api/v5/public/instruments", {"instType": t, "instId": inst_id}, ttl=3600)["data"][0]


def ticker(inst_id: str) -> dict:
    return _get("/api/v5/market/ticker", {"instId": inst_id}, ttl=3)["data"][0]


def tickers(inst_type: str = "SPOT") -> list[dict]:
    return _get("/api/v5/market/tickers", {"instType": inst_type}, ttl=15)["data"]


def candles(inst_id: str, bar: str = "1H", limit: int = 100) -> list[dict]:
    """Oldest first. bar: 1m..1H, 4H, 1D(UTC: 1Dutc), 1W."""
    raw = _get("/api/v5/market/candles", {"instId": inst_id, "bar": bar, "limit": str(min(limit, 300))}, ttl=15)["data"]
    return [{"t": int(c[0]), "o": float(c[1]), "h": float(c[2]), "l": float(c[3]), "c": float(c[4]),
             "vol": float(c[5]), "q": float(c[7]) if len(c) > 7 and c[7] else float(c[6]), "x": c[-1] == "1"}
            for c in reversed(raw)]


def order_book(inst_id: str, size: int = 400) -> dict:
    d = _get("/api/v5/market/books", {"instId": inst_id, "sz": str(size)}, ttl=2)["data"][0]
    ct_val = 1.0
    if inst_id.endswith("-SWAP"):
        try:
            ct_val = float(instrument(inst_id).get("ctVal") or 1)
        except RuntimeError:
            pass
    # Same shape as CTG: [price, usdt_notional, contracts]
    bids = [[float(p), float(p) * float(q) * ct_val, float(q)] for p, q, *_ in d["bids"]]
    asks = [[float(p), float(p) * float(q) * ct_val, float(q)] for p, q, *_ in d["asks"]]
    mid = (bids[0][0] + asks[0][0]) / 2 if bids and asks else None
    return {"instId": inst_id, "ts": int(d.get("ts") or 0), "bids": bids, "asks": asks, "mid": mid,
            "bestBid": bids[0][0] if bids else None, "bestAsk": asks[0][0] if asks else None,
            "spreadBps": (asks[0][0] - bids[0][0]) / mid * 1e4 if mid else None}


def funding_rate(inst_id: str) -> dict:
    return _get("/api/v5/public/funding-rate", {"instId": inst_id}, ttl=10)["data"][0]


def funding_history(inst_id: str, limit: int = 30) -> list[dict]:
    raw = _get("/api/v5/public/funding-rate-history", {"instId": inst_id, "limit": str(limit)}, ttl=60)["data"]
    return [{"t": int(r["fundingTime"]), "v": float(r["realizedRate"] or r["fundingRate"])} for r in reversed(raw)]


def open_interest(inst_id: str) -> dict:
    return _get("/api/v5/public/open-interest", {"instId": inst_id}, ttl=10)["data"][0]


def _ratio(path: str, inst_id: str, period: str, limit: int) -> list[dict]:
    raw = _get(path, {"instId": inst_id, "period": period, "limit": str(limit)}, ttl=60)["data"]
    return [{"t": int(ts), "v": float(v)} for ts, v in reversed(raw)]


def ls_account_ratio(inst_id: str, period: str = "1H", limit: int = 48) -> list[dict]:
    """All accounts long/short account ratio (retail sentiment)."""
    return _ratio("/api/v5/rubik/stat/contracts/long-short-account-ratio-contract", inst_id, period, limit)


def ls_top_account_ratio(inst_id: str, period: str = "1H", limit: int = 48) -> list[dict]:
    """Top-trader (top 5% by position) long/short account ratio."""
    return _ratio("/api/v5/rubik/stat/contracts/long-short-account-ratio-contract-top-trader", inst_id, period, limit)


def ls_top_position_ratio(inst_id: str, period: str = "1H", limit: int = 48) -> list[dict]:
    """Top-trader long/short POSITION ratio (size-weighted, 'smart money')."""
    return _ratio("/api/v5/rubik/stat/contracts/long-short-position-ratio-contract-top-trader", inst_id, period, limit)


def announcements(ann_type: str | None = None, page: int = 1) -> list[dict]:
    params = {"page": str(page)}
    if ann_type:
        params["annType"] = ann_type
    data = _get("/api/v5/support/announcements", params, ttl=600)["data"]
    out = []
    for blk in data:
        for a in blk.get("details", []):
            out.append({"source": "OKX", "type": a.get("annType"), "title": a.get("title"),
                        "url": a.get("url"), "t": int(a.get("pTime") or 0)})
    return out


def rank(inst_type: str = "SWAP", direction: str = "gain", top: int = 30, min_vol: float = 2_000_000) -> list[dict]:
    rows = []
    for t in tickers(inst_type):
        iid = t.get("instId", "")
        if inst_type == "SWAP" and not iid.endswith("-USDT-SWAP"):
            continue
        if inst_type == "SPOT" and (not iid.endswith("-USDT")):
            continue
        o, last = float(t.get("open24h") or 0), float(t.get("last") or 0)
        if o <= 0:
            continue
        vol = float(t.get("volCcy24h") or 0)
        if inst_type == "SWAP":
            vol *= last
        if vol < min_vol:
            continue
        rows.append({"symbol": iid.replace("-USDT-SWAP", "").replace("-USDT", ""), "instId": iid,
                     "change_pct": (last - o) / o * 100, "last": last, "vol_usdt": vol})
    rows.sort(key=lambda x: x["change_pct"], reverse=(direction == "gain"))
    return rows[:top]
