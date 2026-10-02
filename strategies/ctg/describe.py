"""Lists the strategy classes in library/*.py as JSON (run in a throwaway process by runner.py)."""

from __future__ import annotations

import json
import os
import sys
import traceback


def describe(root: str) -> list[dict]:
    from .runtime import load_classes

    lib = os.path.join(root, "library")
    out: list[dict] = []
    for fn in sorted(os.listdir(lib)) if os.path.isdir(lib) else []:
        if not fn.endswith(".py") or fn.startswith("_"):
            continue
        rel = f"library/{fn}"
        try:
            with open(os.path.join(lib, fn), encoding="utf-8") as f:
                code = f.read()
            for cls in load_classes(code, rel):
                m = cls.meta()
                m.update(file=rel, code=code)
                out.append(m)
        except Exception:
            out.append({"file": rel, "error": traceback.format_exc(limit=5)[-4000:]})
    return out


def main() -> None:
    from .limits import apply

    apply()
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    sys.stdout.write(json.dumps(describe(root), allow_nan=False))


if __name__ == "__main__":
    main()
