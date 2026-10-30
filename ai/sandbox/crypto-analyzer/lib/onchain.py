"""On-chain & supply: CoinGecko (tokenomics/contract), GoPlus (security + holders), DexScreener."""

from __future__ import annotations

import urllib.parse

from . import http
from .okx import CHAIN_MAP

CG = "https://api.coingecko.com/api/v3"
GOPLUS = "https://api.gopluslabs.io/api/v1"

# CoinGecko platform key → GoPlus chain id
CG_PLATFORMS = [
    ("binance-smart-chain", "56"), ("ethereum", "1"), ("base", "8453"),
    ("arbitrum-one", "42161"), ("polygon-pos", "137"), ("solana", "solana"),
]

RED_FLAGS = {
    "is_honeypot": "蜜罐", "cannot_sell_all": "无法全部卖出", "is_mintable": "可增发",
    "can_take_back_ownership": "可收回所有权", "hidden_owner": "隐藏 owner",
    "transfer_pausable": "可暂停转账", "is_blacklisted": "有黑名单", "selfdestruct": "可自毁",
    "slippage_modifiable": "税率可改", "owner_change_balance": "owner 可改余额",
    "is_proxy": "代理合约(可升级)", "anti_whale_modifiable": "反鲸参数可改",
    # Solana keys
    "freezable": "可冻结", "mintable": "可增发", "closable": "可关闭", "balance_mutable_authority": "余额可改",
}

KNOWN_WALLETS = {
    "0xf977814e90da44bfa03b6295a0616a897441acec": "Binance 8",
    "0x8894e0a0c962cb723c1976a4421c95949be2d4e3": "Binance 14",
    "0x28c6c06298d640db81d04062c8d73671d1bd2170": "Binance 14",
    "0x21a31ee1afc51d94c2efccaa2092ed1028245545": "Binance 15",
    "0x5a52e96bacdabb82fd05763e25335261b270efcb": "Binance 28",
    "0x3c783c21a0383057d128bae431894a5c19f9cf06": "Binance Hot",
    "0x6cc5f688a315f3dc28a7781717a9a798a59fda7b": "OKX",
    "0x5041ed759dd4afc3a72b8192c143f72f4724081a": "OKX 7",
    "0x0d0707963952f2fba59dd06f2b425ace40b492fe": "Gate.io",
    "0x1ab4973a48dc892cd9971ece8e01dcc7688f8f23": "Bitget",
    "0xf89d7b9c864f589bbf53a82105107622b35eaa40": "Bybit",
    "0x000000000000000000000000000000000000dead": "Burn",
    "0x0000000000000000000000000000000000000000": "Null",
}
CEX_WORDS = ("binance", "okx", "okex", "bybit", "gate", "bitget", "kucoin", "mexc", "htx", "huobi",
             "coinbase", "kraken", "upbit", "bithumb", "crypto.com", "exchange", "hot wallet")
NON_FLOAT_WORDS = ("burn", "dead", "null", "lock", "vesting", "pinklock", "unicrypt", "team.finance",
                   "pancake", "uniswap", "raydium", "pool", "lp", "staking", "bridge", "multisig")


# ── CoinGecko ──────────────────────────────────────────────────

def cg_search(symbol: str) -> str | None:
    """Exact-symbol match with best market-cap rank (avoids HOME→GOHOME style mismatches)."""
    data = http.get_json(f"{CG}/search", {"query": symbol}, ttl=86400)
    q = symbol.upper().strip()
    exact = [c for c in data.get("coins") or [] if (c.get("symbol") or "").upper() == q]
    if not exact:
        return None
    exact.sort(key=lambda c: c.get("market_cap_rank") or 10**9)
    return exact[0]["id"]


def cg_coin(coin_id: str) -> dict:
    d = http.get_json(f"{CG}/coins/{urllib.parse.quote(coin_id)}",
                      {"localization": "false", "tickers": "false", "community_data": "false",
                       "developer_data": "false"}, ttl=1800)
    m = d.get("market_data") or {}
    mcap = (m.get("market_cap") or {}).get("usd")
    fdv = (m.get("fully_diluted_valuation") or {}).get("usd")
    return {
        "id": d.get("id"), "name": d.get("name"), "symbol": (d.get("symbol") or "").upper(),
        "categories": d.get("categories") or [],
        "price_usd": (m.get("current_price") or {}).get("usd"),
        "market_cap": mcap, "fdv": fdv, "mc_fdv": round(mcap / fdv, 3) if mcap and fdv else None,
        "change_24h": m.get("price_change_percentage_24h"), "change_7d": m.get("price_change_percentage_7d"),
        "change_30d": m.get("price_change_percentage_30d"), "volume_24h": (m.get("total_volume") or {}).get("usd"),
        "circulating": m.get("circulating_supply"), "total_supply": m.get("total_supply"),
        "max_supply": m.get("max_supply"), "ath_usd": (m.get("ath") or {}).get("usd"),
        "ath_change_pct": (m.get("ath_change_percentage") or {}).get("usd"),
        "genesis_date": d.get("genesis_date"),
        "platforms": {k: v for k, v in (d.get("platforms") or {}).items() if k and v},
    }


def pick_contract(platforms: dict) -> tuple[str, str] | None:
    for cg_key, chain in CG_PLATFORMS:
        if platforms.get(cg_key):
            return platforms[cg_key], chain
    return None


# ── GoPlus ─────────────────────────────────────────────────────

def resolve_chain(chain: str) -> str:
    c = chain.lower().strip()
    if c not in CHAIN_MAP:
        raise ValueError(f"Unknown chain {chain}: use eth/bsc/base/arbitrum/polygon/solana")
    return CHAIN_MAP[c]


def _goplus_raw(address: str, chain_id: str) -> dict:
    if chain_id == "solana":
        url = f"{GOPLUS}/solana/token_security"
    else:
        url = f"{GOPLUS}/token_security/{chain_id}"
    data = http.get_json(url, {"contract_addresses": address}, ttl=1800, timeout=20)
    if data.get("code") != 1:
        raise RuntimeError(f"GoPlus: {data.get('message')}")
    res = data.get("result") or {}
    info = res.get(address.lower()) or res.get(address) or (next(iter(res.values())) if res else None)
    if not info:
        raise RuntimeError(f"GoPlus has no data for {address} on {chain_id}")
    return info


def _flag_on(v) -> bool:
    if isinstance(v, dict):  # Solana style {"status": "1", ...}
        v = v.get("status")
    return str(v) == "1"


def _holder_class(addr: str, tag: str, is_contract, is_locked) -> str:
    """cex | nonfloat (burn/lock/LP/contract) | whale (unlabeled EOA)."""
    label = (KNOWN_WALLETS.get(addr.lower(), "") + " " + (tag or "")).lower()
    if any(w in label for w in CEX_WORDS):
        return "cex"
    if str(is_locked) == "1" or any(w in label for w in NON_FLOAT_WORDS):
        return "nonfloat"
    if str(is_contract) == "1":
        return "nonfloat"
    return "whale"


def scan(address: str, chain: str = "bsc") -> dict:
    chain_id = resolve_chain(chain) if not chain.isdigit() and chain != "solana" else chain
    info = _goplus_raw(address, chain_id)
    flags = [label for k, label in RED_FLAGS.items() if k in info and _flag_on(info[k])]
    holders = []
    for h in (info.get("holders") or [])[:20]:
        addr = h.get("address") or h.get("account") or ""
        pct = float(h.get("percent") or 0) * 100
        tag = h.get("tag") or KNOWN_WALLETS.get(addr.lower(), "")
        holders.append({"address": addr, "percent": pct, "tag": tag,
                        "is_contract": h.get("is_contract"), "is_locked": h.get("is_locked"),
                        "class": _holder_class(addr, tag, h.get("is_contract"), h.get("is_locked"))})
    top10 = sum(h["percent"] for h in holders[:10])
    cex = sum(h["percent"] for h in holders if h["class"] == "cex")
    nonfloat = sum(h["percent"] for h in holders if h["class"] == "nonfloat")
    whales = [h for h in holders if h["class"] == "whale"]
    whale10 = sum(h["percent"] for h in whales[:10])
    sell_tax = info.get("sell_tax")
    try:
        if sell_tax not in (None, "") and float(sell_tax) > 0.1:
            flags.append(f"卖税 {float(sell_tax)*100:.0f}%")
    except ValueError:
        pass
    return {
        "address": address, "chain_id": chain_id,
        "score": max(0, 100 - 12 * len(flags)), "flags": flags,
        "holder_count": int(float(info.get("holder_count") or 0)),
        "buy_tax": info.get("buy_tax"), "sell_tax": sell_tax,
        "is_open_source": str(info.get("is_open_source")) == "1",
        "owner_percent": float(info.get("owner_percent") or 0) * 100,
        "creator_percent": float(info.get("creator_percent") or 0) * 100,
        "top10_pct": top10,
        "cex_pct": cex,              # 交易所托管（含币安/OKX 热冷钱包）
        "nonfloat_pct": nonfloat,    # 锁仓/销毁/LP/合约
        "whale10_pct": whale10,      # 前 10 个未标注 EOA 合计 —— 「庄」筹码代理
        "top_whale_pct": whales[0]["percent"] if whales else 0.0,
        "holders": holders,
        "dex_pools": info.get("dex") or [],
    }


# ── DexScreener ────────────────────────────────────────────────

def _pairs(pairs: list) -> list[dict]:
    out = []
    for p in pairs:
        out.append({
            "chain": p.get("chainId"), "dex": p.get("dexId"), "pair": p.get("pairAddress"),
            "symbol": (p.get("baseToken") or {}).get("symbol"), "address": (p.get("baseToken") or {}).get("address"),
            "price_usd": p.get("priceUsd"), "liquidity_usd": float((p.get("liquidity") or {}).get("usd") or 0),
            "volume_24h": float((p.get("volume") or {}).get("h24") or 0),
            "change_24h": (p.get("priceChange") or {}).get("h24"),
            "txns_24h": (p.get("txns") or {}).get("h24"),
        })
    return out


def dex_by_token(address: str) -> list[dict]:
    d = http.get_json(f"https://api.dexscreener.com/latest/dex/tokens/{address}", ttl=120)
    return sorted(_pairs(d.get("pairs") or []), key=lambda p: p["liquidity_usd"], reverse=True)


def dex_search(q: str) -> list[dict]:
    d = http.get_json("https://api.dexscreener.com/latest/dex/search", {"q": q}, ttl=120)
    return sorted(_pairs(d.get("pairs") or []), key=lambda p: p["volume_24h"], reverse=True)[:20]
