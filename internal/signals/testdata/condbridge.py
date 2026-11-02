"""Test bridge: the production script relay (runner.CondWorker, which runs ctg.condworker) over
stdin/stdout, one JSON request per line in, one JSON reply per line out. Used by script_worker_test.go."""

import json
import os
import sys

sys.path.insert(0, os.path.abspath(sys.argv[1]))  # strategies/
from runner import CondWorker  # noqa: E402

w = CondWorker()
print(json.dumps({"ready": True}), flush=True)
for line in sys.stdin:
    if line.strip():
        print(json.dumps(w.request(json.loads(line)), allow_nan=False), flush=True)
w.stop()
