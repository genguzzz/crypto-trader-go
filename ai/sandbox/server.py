"""ctg-sandbox: runs the chat agent's shell commands in an isolated container.

Protocol: one JSON request per connection over $SBX_SOCKET (default /run/sbx/sbx.sock):
{"chat": "<id>", "cmd": "<bash>", "timeout": 60} → {"exit": n, "stdout": "...", "stderr": "...", "ms": n, "truncated": bool}

Each chat gets /work/<chat> as cwd and HOME (kept until the container restarts or the directory is
older than 7 days). The process environment is rebuilt from scratch: PATH, locale, the ctg open
API base and a read-only, in-memory data key. Nothing else from the worker reaches here.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import re
import shutil
import signal
import time

SOCKET = os.environ.get("SBX_SOCKET", "/run/sbx/sbx.sock")
WORK = os.environ.get("SBX_WORK", "/work")
MAX_OUT = 30_000
MAX_TIMEOUT = 180
KEEP_DAYS = 7
log = logging.getLogger("ctg-sandbox")


def chat_dir(chat: str) -> str:
    chat = re.sub(r"[^A-Za-z0-9_-]", "", chat)[:40] or "default"
    d = os.path.join(WORK, chat)
    os.makedirs(d, exist_ok=True)
    return d


def env_for(home: str, key: str) -> dict[str, str]:
    return {
        "PATH": os.environ.get("SBX_EXTRA_PATH", "/opt/ca/bin") + ":/usr/local/bin:/usr/bin:/bin",
        "HOME": home, "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "TZ": "Asia/Shanghai", "CRYPTO_TZ": "Asia/Shanghai",
        "PYTHONDONTWRITEBYTECODE": "1", "MPLBACKEND": "Agg",
        "CTG_BASE": os.environ.get("CTG_BASE", "https://ctg.gugenzzz.top/api/open/v1"), "CTG_API_KEY": key,
    }


def clip(b: bytes) -> tuple[str, bool]:
    s = b.decode("utf-8", "replace")
    if len(s) <= MAX_OUT:
        return s, False
    half = MAX_OUT // 2
    return s[:half] + f"\n…[{len(s) - MAX_OUT} chars cut]…\n" + s[-half:], True


async def run(req: dict, key: str) -> dict:
    cmd = str(req.get("cmd") or "")
    if not cmd.strip():
        return {"exit": 2, "stdout": "", "stderr": "empty command", "ms": 0, "truncated": False}
    timeout = max(1, min(MAX_TIMEOUT, int(req.get("timeout") or 60)))
    home = chat_dir(str(req.get("chat") or ""))
    t0 = time.monotonic()
    p = await asyncio.create_subprocess_exec(
        "bash", "-c", cmd, cwd=home, env=env_for(home, key), stdin=asyncio.subprocess.DEVNULL,
        stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE, start_new_session=True)
    try:
        out, err = await asyncio.wait_for(p.communicate(), timeout)
        code = p.returncode
    except asyncio.TimeoutError:
        os.killpg(p.pid, signal.SIGKILL)
        out, err = await p.communicate()
        code = -9
        err += f"\n[killed after {timeout}s]".encode()
    so, t1 = clip(out)
    se, t2 = clip(err)
    return {"exit": code, "stdout": so, "stderr": se, "ms": int((time.monotonic() - t0) * 1000), "truncated": t1 or t2}


def sweep() -> None:
    cut = time.time() - KEEP_DAYS * 86400
    for name in os.listdir(WORK):
        p = os.path.join(WORK, name)
        try:
            if os.path.isdir(p) and os.path.getmtime(p) < cut:
                shutil.rmtree(p, ignore_errors=True)
        except OSError:
            pass


async def main() -> None:
    logging.basicConfig(level="INFO", format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    key = os.environ.pop("CTG_SANDBOX_KEY", "")  # optional static key; normally sent per request by the worker
    os.makedirs(WORK, exist_ok=True)
    sweep()
    if os.path.exists(SOCKET):
        os.remove(SOCKET)

    async def handle(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            req = json.loads(await asyncio.wait_for(reader.readline(), 10))
            res = await run(req, req.pop("key", "") or key)
            log.info("chat=%s exit=%s ms=%s cmd=%.120r", req.get("chat"), res["exit"], res["ms"], req.get("cmd"))
        except Exception as e:  # noqa: BLE001
            res = {"exit": 2, "stdout": "", "stderr": f"sandbox error: {e}", "ms": 0, "truncated": False}
        writer.write((json.dumps(res, ensure_ascii=False) + "\n").encode())
        try:
            await writer.drain()
        finally:
            writer.close()

    server = await asyncio.start_unix_server(handle, SOCKET, limit=1 << 20)
    os.chmod(SOCKET, 0o660)
    log.info("listening on %s, work %s", SOCKET, WORK)
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
