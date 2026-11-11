"""The signal-script worker: one long-lived process (started by runner.py) that runs every script
condition. Requests arrive on stdin and answers leave on stdout, one JSON object per line; every
answer ends with a line carrying "end": true. The first line it writes is {"ready": true}.

    {"op":"check","code"}                       → {ok, diagnostics, meta, end}
    {"op":"eval","budgetMs","items":[{key, code, now, bars:{inst:[[ts,o,h,l,c,vol,volq]…]}, factors:{inst:{…}}}]}
                                                → one line per item {i, key, fire, values, note, ms, error?, missing?}, then {end}
    {"op":"replay","code","bars","start","end","budgetMs","maxTotalS"}
                                                → {states, edges, edgeCount, errors, …, end} (ctg.condition.replay)

Compiled classes are cached by the hash of their code, so a subscription's script is loaded once per
process; the cache is rebuilt transparently after a restart because every request carries the code.
Results stream item by item, so when a script kills the process the supervisor knows which one it was.
"""

from __future__ import annotations

import json
import sys
from collections import OrderedDict

from .condition import Condition, ScriptError, check_source, code_hash, load, replay, run_check
from .context import Bars

CACHE = 64


class Worker:
    def __init__(self, out=None):
        self.out = out or sys.stdout
        self.cache: OrderedDict[str, tuple[type[Condition] | None, str]] = OrderedDict()

    def send(self, msg: dict) -> None:
        self.out.write(json.dumps(msg, separators=(",", ":"), ensure_ascii=False, allow_nan=False) + "\n")
        self.out.flush()

    def compiled(self, code: str) -> type[Condition]:
        """The script's class, from the cache when it was loaded before (a load error is cached too)."""
        h = code_hash(code)
        if h in self.cache:
            self.cache.move_to_end(h)
        else:
            try:
                self.cache[h] = (load(code), "")
            except ScriptError as e:
                self.cache[h] = (None, str(e))
            while len(self.cache) > CACHE:
                self.cache.popitem(last=False)
        cls, err = self.cache[h]
        if cls is None:
            raise ScriptError(err)
        return cls

    def handle(self, req: dict) -> None:
        op = req.get("op")
        budget = max(1, int(req.get("budgetMs") or 200)) / 1000
        if op == "check":
            self.send({**check_source(str(req.get("code") or "")), "end": True})
        elif op == "eval":
            for i, it in enumerate(req.get("items") or []):
                res = {"i": i, "key": it.get("key")}
                try:
                    cls = self.compiled(str(it.get("code") or ""))
                    bars = {inst: Bars(rows or []) for inst, rows in (it.get("bars") or {}).items()}
                    res.update(run_check(cls(), bars, it.get("factors") or {}, int(it.get("now") or 0), budget))
                except ScriptError as e:
                    res.update({"fire": None, "values": {}, "note": "", "ms": 0.0, "error": str(e)})
                self.send(res)
            self.send({"end": True})
        elif op == "replay":
            try:
                cls = self.compiled(str(req.get("code") or ""))
                out = replay(cls, req.get("bars") or {}, int(req.get("start") or 0), int(req.get("end") or 0), budget,
                             float(req.get("maxTotalS") or 60))
            except ScriptError as e:
                out = {"error": str(e)}
            self.send({**out, "end": True})
        else:
            self.send({"error": f"unknown op {op!r}", "end": True})

    def serve(self, inp=None) -> None:
        self.send({"ready": True})
        for line in inp or sys.stdin:
            line = line.strip()
            if not line:
                continue
            try:
                req = json.loads(line)
            except ValueError as e:
                self.send({"error": f"bad request: {e}", "end": True})
                continue
            try:
                self.handle(req)
            except Exception as e:  # a worker bug must not leave the supervisor waiting
                self.send({"error": f"worker: {type(e).__name__}: {e}", "end": True})


def main() -> None:
    from .limits import apply

    apply(parent_death=True)
    Worker().serve()


if __name__ == "__main__":
    main()
