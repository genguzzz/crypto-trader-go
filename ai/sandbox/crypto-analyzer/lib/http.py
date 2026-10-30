"""Shared HTTP: JSON GET with retry, short-lived disk cache, parallel fan-out."""

from __future__ import annotations

import hashlib
import json
import os
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Callable

UA = "Mozilla/5.0 (crypto-analyzer/2.0)"
CACHE_DIR = os.path.join(tempfile.gettempdir(), "crypto-analyzer-cache")
NO_CACHE = os.environ.get("CRYPTO_NO_CACHE") == "1"


class HTTPError(RuntimeError):
    def __init__(self, status: int, msg: str) -> None:
        self.status = status
        super().__init__(f"HTTP {status}: {msg}")


def _cache_path(key: str) -> str:
    return os.path.join(CACHE_DIR, hashlib.sha1(key.encode()).hexdigest() + ".json")


def _cache_get(key: str, ttl: float) -> Any:
    if NO_CACHE or ttl <= 0:
        return None
    p = _cache_path(key)
    try:
        if time.time() - os.path.getmtime(p) < ttl:
            with open(p, encoding="utf-8") as f:
                return json.load(f)
    except (OSError, ValueError):
        pass
    return None


def _cache_get_any(key: str, max_age: float) -> Any:
    p = _cache_path(key)
    try:
        if time.time() - os.path.getmtime(p) < max_age:
            with open(p, encoding="utf-8") as f:
                return json.load(f)
    except (OSError, ValueError):
        pass
    return None


def _cache_put(key: str, data: Any) -> None:
    try:
        os.makedirs(CACHE_DIR, mode=0o700, exist_ok=True)
        tmp = _cache_path(key) + f".{os.getpid()}"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(data, f)
        os.replace(tmp, _cache_path(key))
    except OSError:
        pass


def get_text(url: str, params: dict | None = None, headers: dict | None = None,
             timeout: float = 15, retries: int = 2, ttl: float = 0) -> str:
    if params:
        url = f"{url}?{urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})}"
    key = "T:" + url
    hit = _cache_get(key, ttl)
    if hit is not None:
        return hit
    h = {"User-Agent": UA, "Accept": "*/*"}
    h.update(headers or {})
    last: Exception | None = None
    for attempt in range(retries + 1):
        try:
            req = urllib.request.Request(url, headers=h)
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                body = resp.read().decode("utf-8", "replace")
            if ttl > 0:
                _cache_put(key, body)
            return body
        except urllib.error.HTTPError as e:
            msg = e.read().decode("utf-8", "replace")[:300]
            last = HTTPError(e.code, msg)
            if e.code == 429:
                if attempt < retries:
                    time.sleep(min(float(e.headers.get("Retry-After") or 1.5 * (attempt + 1)), 3))
                continue
            if 400 <= e.code < 500:
                raise last
        except (urllib.error.URLError, TimeoutError, ConnectionError) as e:
            last = e
        if attempt < retries:
            time.sleep(0.6 * (attempt + 1))
    if ttl > 0:  # stale-on-error: a day-old copy beats nothing for news/calendar/on-chain
        stale = _cache_get_any(key, 86400)
        if stale is not None:
            return stale
    raise RuntimeError(f"GET {url.split('?')[0]} failed: {last}")


def get_json(url: str, params: dict | None = None, headers: dict | None = None,
             timeout: float = 15, retries: int = 2, ttl: float = 0) -> Any:
    return json.loads(get_text(url, params, headers, timeout, retries, ttl))


def parallel(tasks: dict[str, Callable[[], Any]], workers: int = 12) -> tuple[dict, dict]:
    """Run named callables concurrently. Returns (results, errors); a failure never aborts the rest."""
    results: dict[str, Any] = {}
    errors: dict[str, str] = {}
    if not tasks:
        return results, errors
    with ThreadPoolExecutor(max_workers=min(workers, len(tasks))) as ex:
        futs = {name: ex.submit(fn) for name, fn in tasks.items()}
        for name, fut in futs.items():
            try:
                results[name] = fut.result()
            except Exception as e:  # noqa: BLE001 — collected and reported per source
                errors[name] = f"{type(e).__name__}: {e}"[:300]
    return results, errors
