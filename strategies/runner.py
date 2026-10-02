"""Strategy supervisor: connects to the Go host as `supervise`, lists strategies and runs one process
per instance. It never restarts anything on its own; Go decides (docs/trading-design.md §2)."""

from __future__ import annotations

import collections
import json
import os
import subprocess
import sys
import threading
import time

ROOT = os.path.dirname(os.path.abspath(__file__))
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)

from ctg.wire import Conn  # noqa: E402

STDERR_TAIL = 4000
DESCRIBE_TIMEOUT = 30


def socket_path() -> str:
    return os.environ.get("CTG_SOCKET") or os.path.join(os.environ.get("CTG_RUN_DIR", "data/run"), "strategy.sock")


def child_env(extra: dict[str, str]) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items()
           if k in ("PATH", "LANG", "LC_ALL", "TZ", "HOME", "CTG_MEM_LIMIT_MB", "CTG_CPU_LIMIT_S")}
    # One BLAS thread keeps numpy inside the address-space limit.
    env.update(OPENBLAS_NUM_THREADS="1", OMP_NUM_THREADS="1", MKL_NUM_THREADS="1",
               PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1", PYTHONPATH=ROOT)
    env.update(extra)
    return env


class Child:
    def __init__(self, sup: "Supervisor", instance: int, token: str, proc: subprocess.Popen, code: str):
        self.sup, self.instance, self.token, self.proc = sup, instance, token, proc
        self.tail: collections.deque[str] = collections.deque(maxlen=200)
        threading.Thread(target=self._feed, args=(code,), daemon=True).start()
        threading.Thread(target=self._wait, daemon=True).start()

    def _feed(self, code: str) -> None:
        try:
            self.proc.stdin.write(code.encode())
            self.proc.stdin.close()
        except OSError:
            pass

    def _wait(self) -> None:
        for raw in self.proc.stderr:
            line = raw.decode(errors="replace").rstrip("\n")
            self.tail.append(line)
            print(f"[instance {self.instance}] {line}", file=sys.stderr, flush=True)
        code = self.proc.wait()
        self.sup.child_exited(self, code, "\n".join(self.tail)[-STDERR_TAIL:])


class Supervisor:
    def __init__(self, conn: Conn):
        self.conn = conn
        self.children: dict[int, Child] = {}
        self.lock = threading.Lock()

    def send(self, msg: dict) -> None:
        try:
            self.conn.send(msg)
        except OSError:
            pass  # the host is gone; serve() notices on its next read

    def log(self, level: str, msg: str) -> None:
        print(f"supervisor: {msg}", file=sys.stderr, flush=True)
        self.send({"type": "log", "level": level, "msg": msg})

    def serve(self) -> None:
        self.conn.send({"type": "supervise"})
        try:
            while True:
                msg = self.conn.recv()
                if msg is None:
                    return
                t = msg.get("type")
                if t == "describe":
                    threading.Thread(target=self.describe, args=(msg.get("id"),), daemon=True).start()
                elif t == "check":
                    threading.Thread(target=self.check, args=(msg.get("id"), msg.get("code") or ""), daemon=True).start()
                elif t == "spawn":
                    self.spawn(msg)
                elif t == "kill":
                    self.kill(int(msg.get("instance") or 0), msg.get("token") or "")
                else:
                    self.log("warn", f"unknown command {t!r}")
        finally:
            with self.lock:
                children = list(self.children.values())
                self.children.clear()
            for c in children:
                c.proc.kill()

    def describe(self, req_id) -> None:
        try:
            p = subprocess.run([sys.executable, "-m", "ctg.describe", ROOT], cwd=ROOT, env=child_env({}),
                               capture_output=True, timeout=DESCRIBE_TIMEOUT)
            if p.returncode != 0:
                raise RuntimeError(p.stderr.decode(errors="replace")[-STDERR_TAIL:])
            strategies = json.loads(p.stdout)
        except Exception as e:  # report instead of dying: describe runs user code
            strategies = [{"file": "library", "error": f"describe failed: {e}"}]
        self.send({"type": "describe_result", "id": req_id, "strategies": strategies})

    def check(self, req_id, code: str) -> None:
        """Checks editor code in a throwaway process (ctg.check)."""
        try:
            p = subprocess.run([sys.executable, "-m", "ctg.check"], cwd=ROOT, env=child_env({}), input=code.encode(),
                               capture_output=True, timeout=DESCRIBE_TIMEOUT)
            if p.returncode != 0:
                raise RuntimeError(p.stderr.decode(errors="replace")[-STDERR_TAIL:])
            result = json.loads(p.stdout)
        except subprocess.TimeoutExpired:
            result = {"diagnostics": [{"line": 1, "col": 0, "severity": "error",
                                       "message": f"check timed out after {DESCRIBE_TIMEOUT}s (code runs at import?)"}]}
        except Exception as e:
            result = {"diagnostics": [{"line": 1, "col": 0, "severity": "error", "message": f"check failed: {e}"}]}
        self.send({"type": "check_result", "id": req_id, "result": result})

    def spawn(self, msg: dict) -> None:
        instance, token = int(msg.get("instance") or 0), str(msg.get("token") or "")
        file, code = str(msg.get("file") or ""), msg.get("code") or ""
        try:
            if not code:
                path = os.path.realpath(os.path.join(ROOT, file))
                if not path.startswith(os.path.realpath(os.path.join(ROOT, "library")) + os.sep):
                    raise ValueError(f"file {file!r} is outside strategies/library")
                with open(path, encoding="utf-8") as f:
                    code = f.read()
            self.kill(instance, "")
            env = child_env({"CTG_SOCKET": os.path.abspath(socket_path()), "CTG_INSTANCE": str(instance),
                             "CTG_TOKEN": token, "CTG_FILE": file, "CTG_CLASS": str(msg.get("class") or ""),
                             "CTG_NAME": str(msg.get("name") or ""), "CTG_CODE_STDIN": "1"})
            proc = subprocess.Popen([sys.executable, "-u", os.path.join(ROOT, "instance.py")], cwd=ROOT, env=env,
                                    stdin=subprocess.PIPE, stderr=subprocess.PIPE, close_fds=True)
            with self.lock:
                self.children[instance] = Child(self, instance, token, proc, code)
        except Exception as e:
            self.log("error", f"spawn instance {instance}: {e}")
            self.send({"type": "exited", "instance": instance, "token": token, "code": -1,
                       "stderr": f"spawn failed: {e}"})

    def kill(self, instance: int, token: str) -> None:
        with self.lock:
            c = self.children.get(instance)
            if c is None or (token and c.token != token):
                return
            del self.children[instance]
        c.proc.kill()

    def child_exited(self, c: Child, code: int, tail: str) -> None:
        with self.lock:
            if self.children.get(c.instance) is c:
                del self.children[c.instance]
        self.send({"type": "exited", "instance": c.instance, "token": c.token, "code": code, "stderr": tail})


def main() -> None:
    path = socket_path()
    print(f"supervisor: connecting to {path}", file=sys.stderr, flush=True)
    waiting = False
    while True:
        try:
            conn = Conn.connect(path)
        except OSError as e:
            if not waiting:
                print(f"supervisor: waiting for host ({e})", file=sys.stderr, flush=True)
                waiting = True
            time.sleep(1)
            continue
        waiting = False
        print("supervisor: connected", file=sys.stderr, flush=True)
        try:
            Supervisor(conn).serve()
        except Exception as e:
            print(f"supervisor: {e!r}", file=sys.stderr, flush=True)
        finally:
            conn.close()
        print("supervisor: disconnected", file=sys.stderr, flush=True)
        time.sleep(1)


if __name__ == "__main__":
    main()
