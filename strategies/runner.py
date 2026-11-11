"""Strategy supervisor: connects to the Go host as `supervise`, lists strategies and runs one process
per instance. It never restarts anything on its own; Go decides (docs/trading-design.md §2)."""

from __future__ import annotations

import collections
import json
import os
import queue
import signal
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
COND_MAX_LINE = 16 << 20  # signal-script requests carry bar history (≤ 10 instruments × 1000 bars)
COND_START_S = 10  # worker start (numpy import) on top of a request's own timeout
COND_RETRIES = 3  # re-runs of the items after a crash or a hang, each in a fresh worker


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


class CondWorker:
    """The signal-script worker (ctg.condworker): started on first use, one request at a time, killed and
    started afresh when it dies or a request outlives its timeout. Call it from one long-lived thread:
    the worker dies with the thread that started it (PR_SET_PDEATHSIG). In an eval batch the item that was
    running at that moment gets the error ("crash": true) and the items after it run again in a new
    process, so one bad script cannot take the others down (docs/trading-design.md §8, script signals)."""

    def __init__(self, argv: list[str] | None = None):
        self.argv = argv or [sys.executable, "-u", "-m", "ctg.condworker"]
        self.proc: subprocess.Popen | None = None
        self.lines: queue.Queue = queue.Queue()
        self.tail: collections.deque[str] = collections.deque(maxlen=40)
        self.lock = threading.Lock()

    def _start(self) -> str:
        """Starts the process and waits for its ready line; returns an error or ""."""
        self.tail.clear()
        proc = subprocess.Popen(self.argv, cwd=ROOT, env=child_env({}), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, close_fds=True)
        lines: queue.Queue = queue.Queue()

        def read_out():
            try:
                for raw in proc.stdout:
                    lines.put(raw)
            except (OSError, ValueError):  # closed by stop()
                pass
            lines.put(None)

        def read_err():
            try:
                for raw in proc.stderr:
                    line = raw.decode(errors="replace").rstrip("\n")
                    self.tail.append(line)
                    print(f"[signal scripts] {line}", file=sys.stderr, flush=True)
            except (OSError, ValueError):
                pass

        threading.Thread(target=read_out, daemon=True).start()
        threading.Thread(target=read_err, daemon=True).start()
        self.proc, self.lines = proc, lines
        try:
            first = lines.get(timeout=COND_START_S)
        except queue.Empty:
            first = None
        if first is None or not json.loads(first).get("ready"):
            self.stop()
            return "信号脚本进程启动失败：" + ("\n".join(self.tail)[-500:] or "无输出")
        return ""

    def stop(self) -> None:
        p, self.proc = self.proc, None
        if p is not None:
            p.kill()
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                pass
            for f in (p.stdin, p.stdout, p.stderr):
                try:
                    f.close()
                except OSError:
                    pass

    def _exchange(self, req: dict, timeout: float) -> tuple[list[dict], dict | None, str]:
        """Sends one request; returns the lines before the end line, the end line (None when the process
        died or went silent for `timeout` seconds) and what happened then ("" when the process only ran
        out of its lifetime CPU limit, CTG_CPU_LIMIT_S: not the running script's fault)."""
        if self.proc is None or self.proc.poll() is not None:
            err = self._start()
            if err:
                return [], {"error": err, "infra": True}, ""
        try:
            self.proc.stdin.write(json.dumps(req, separators=(",", ":"), allow_nan=False).encode() + b"\n")
            self.proc.stdin.flush()
        except OSError:
            pass  # it died: the reader reports EOF below
        got: list[dict] = []
        while True:
            try:
                raw = self.lines.get(timeout=timeout)
            except queue.Empty:
                self.stop()
                return got, None, f"超时（{timeout:.0f} 秒无响应），已强制结束"
            if raw is None:
                code = self.proc.wait() if self.proc else -1
                self.stop()
                if code == -signal.SIGXCPU:
                    return got, None, ""
                tail = "\n".join(self.tail).strip()[-300:]
                return got, None, f"异常退出（代码 {code}）" + (f"：{tail}" if tail else "")
            msg = json.loads(raw)
            if msg.get("end"):
                msg.pop("end")
                return got, msg, ""
            got.append(msg)

    def request(self, req: dict) -> dict:
        with self.lock:
            if req.get("op") == "eval":
                return self._eval(req)
            timeout = float(req.get("maxTotalS") or 60) + 5 if req.get("op") == "replay" else DESCRIBE_TIMEOUT
            _, end, fail = self._exchange(req, timeout)
            return end if end is not None else {"error": "信号脚本进程" + (fail or "到达 CPU 时间上限，已重启；请重试"), "infra": not fail}

    def _eval(self, req: dict) -> dict:
        items = list(req.get("items") or [])
        per_item = max(1, int(req.get("budgetMs") or 200)) / 1000 * 5 + 2
        out: list[dict | None] = [None] * len(items)
        start = 0
        for _ in range(COND_RETRIES + 1):
            got, end, fail = self._exchange(dict(req, items=items[start:]), per_item)
            for r in got:
                r["i"] = start + int(r.get("i") or 0)
                out[r["i"]] = r
            if end is not None:
                if end.get("error"):
                    return end
                break
            k = start + len(got)  # the item running when the process died or hung
            if not fail:  # the worker's lifetime CPU limit: run that item again in a fresh process
                start = k
                continue
            out[k] = {"i": k, "key": items[k].get("key"), "fire": None, "values": {}, "note": "", "ms": 0.0,
                      "error": "脚本让执行进程" + fail, "crash": True}
            start = k + 1
            if start >= len(items):
                break
        else:
            return {"error": f"信号脚本进程连续 {COND_RETRIES + 1} 次崩溃", "infra": True,
                    "items": [r for r in out if r is not None]}
        return {"items": [r for r in out if r is not None]}


class Supervisor:
    def __init__(self, conn: Conn):
        self.conn = conn
        self.children: dict[int, Child] = {}
        self.lock = threading.Lock()
        self.conds = CondWorker()
        self.cond_q: queue.Queue = queue.Queue()
        threading.Thread(target=self.cond_loop, daemon=True).start()

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
                elif t == "cond":
                    self.cond_q.put((msg.get("id"), msg.get("req") or {}))
                else:
                    self.log("warn", f"unknown command {t!r}")
        finally:
            with self.lock:
                children = list(self.children.values())
                self.children.clear()
            for c in children:
                c.proc.kill()
            self.cond_q.put(None)  # cond_loop stops the script worker and ends

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

    def cond_loop(self) -> None:
        """Runs signal-script requests (check / eval / replay) one after another on this one thread."""
        while True:
            job = self.cond_q.get()
            if job is None:
                self.conds.stop()
                return
            req_id, req = job
            try:
                reply = self.conds.request(req)
            except Exception as e:  # report instead of dying
                reply = {"error": f"信号脚本请求失败：{e}", "infra": True}
            self.send({"type": "cond_result", "id": req_id, "reply": reply})

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
            conn = Conn.connect(path, COND_MAX_LINE)
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
