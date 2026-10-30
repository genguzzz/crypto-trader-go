"""CTG (OKX Helper) open API — self-hosted OKX USDT-perp research data.

Spec: <base>/openapi.json. Times are Unix ms, amounts USDT. Unknown values are
omitted/null (never 0). Key is read from scripts/.env/ctg.env or $CTG_API_KEY.
"""

from __future__ import annotations

import os
import urllib.parse

from . import http

SCRIPT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENV_FILE = os.path.join(SCRIPT_DIR, ".env", "ctg.env")
DEFAULT_BASE = "https://ctg.gugenzzz.top/api/open/v1"


def _load_env() -> dict:
    env: dict[str, str] = {}
    try:
        with open(ENV_FILE, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    k, v = line.split("=", 1)
                    env[k.strip()] = v.strip().strip('"').strip("'")
    except OSError:
        pass
    return env


_ENV = _load_env()
BASE = os.environ.get("CTG_BASE") or _ENV.get("CTG_BASE") or DEFAULT_BASE
KEY = os.environ.get("CTG_API_KEY") or _ENV.get("CTG_API_KEY") or ""


def available() -> bool:
    return bool(KEY)


def get(path: str, params: dict | None = None, ttl: float = 0, timeout: float = 20):
    if not KEY:
        raise RuntimeError(f"CTG key missing: put CTG_API_KEY in {ENV_FILE}")
    return http.get_json(BASE + path, params, {"Authorization": f"Bearer {KEY}"}, timeout=timeout, ttl=ttl)


def inst_id(symbol: str) -> str:
    s = symbol.upper().strip().replace("/", "-")
    if s.endswith("-USDT-SWAP"):
        return s
    for suf in ("-USDT", "USDT"):
        if s.endswith(suf) and len(s) > len(suf):
            s = s[: -len(suf)]
            break
    return f"{s.strip('-')}-USDT-SWAP"


# ── endpoints ──────────────────────────────────────────────────

def me():
    return get("/me")


def status():
    return get("/status", ttl=10)


def fields():
    return get("/fields", ttl=3600)


def rules():
    return get("/rules", ttl=600)


def rule_track(rule_id: str):
    return get(f"/rules/{urllib.parse.quote(rule_id)}/track", ttl=1800)


def instruments(q: str | None = None, category: str | None = None, limit: int = 50):
    return get("/instruments", {"q": q, "category": category, "limit": limit}, ttl=30)["items"]


def instrument(inst: str):
    return get(f"/instruments/{urllib.parse.quote(inst)}", ttl=5)


def universe(scope: str = "all", category: str | None = "1", rule: str | None = "*",
             sort: str = "turnover12h", order: str = "desc", limit: int = 500):
    return get("/universe", {"scope": scope, "category": category, "rule": rule,
                             "sort": sort, "order": order, "limit": limit}, ttl=20)


def candles(inst: str, bar: str = "1H", limit: int = 200, market: str = "swap"):
    return get("/candles", {"instId": inst, "bar": bar, "limit": limit, "market": market}, ttl=15)


def compare(inst: str, bar: str = "1H", limit: int = 72):
    return get("/compare", {"instId": inst, "bar": bar, "limit": limit}, ttl=30)


def orderbook(inst: str, depth: int = 400):
    return get("/orderbook", {"instId": inst, "depth": depth}, ttl=3)


def orderflow(inst: str, bar: str = "1H", limit: int = 48, levels: bool = False):
    return get("/orderflow", {"instId": inst, "bar": bar, "limit": limit,
                              "levels": "true" if levels else None}, ttl=15)


def liquidations(inst: str, bar: str = "1H", limit: int = 48, recent: int = 30):
    return get("/liquidations", {"instId": inst, "bar": bar, "limit": limit, "recent": recent}, ttl=15)


def liqmap(inst: str, days: int = 7):
    return get("/liqmap", {"instId": inst, "days": days}, ttl=60)


def series(inst: str, field: str, hours: int = 24):
    return get("/series", {"instId": inst, "field": field, "hours": hours}, ttl=30)


def alerts(limit: int = 50):
    return get("/alerts/events", {"limit": limit}, ttl=15)
