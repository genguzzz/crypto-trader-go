"""Entry point of one strategy instance process (spawned by runner.py).

Environment: CTG_SOCKET, CTG_INSTANCE, CTG_TOKEN, CTG_FILE (path under strategies/), CTG_CLASS, CTG_NAME.
With CTG_CODE_STDIN=1 the strategy source is read from stdin (the exact version Go asked for);
otherwise CTG_FILE is read from disk.
"""

from __future__ import annotations

import os
import sys

ROOT = os.path.dirname(os.path.abspath(__file__))


def main() -> int:
    from ctg.limits import apply

    apply(parent_death=True)
    file = os.environ.get("CTG_FILE", "")
    if os.environ.get("CTG_CODE_STDIN") == "1":
        code = sys.stdin.read()
    else:
        with open(os.path.join(ROOT, file), encoding="utf-8") as f:
            code = f.read()

    from ctg.runtime import load_classes, pick_class, run_instance
    from ctg.wire import Conn

    cls = pick_class(load_classes(code, file), os.environ.get("CTG_CLASS", ""), os.environ.get("CTG_NAME", ""))
    conn = Conn.connect(os.environ["CTG_SOCKET"])
    return run_instance(conn, cls, int(os.environ["CTG_INSTANCE"]), os.environ["CTG_TOKEN"])


if __name__ == "__main__":
    if ROOT not in sys.path:
        sys.path.insert(0, ROOT)
    sys.exit(main())
