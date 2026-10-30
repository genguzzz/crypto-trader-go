"""Unified output for agent (json) and human (text)."""

from __future__ import annotations

import json
import sys
from typing import Any, Callable


def emit(cmd: str, data: Any, fmt: str = "text", render: Callable[[Any], None] | None = None) -> None:
    if fmt == "json":
        payload = {"ok": True, "cmd": cmd, "data": data}
        print(json.dumps(payload, indent=2, ensure_ascii=False, default=_default))
    elif render:
        render(data)
    else:
        print(json.dumps(data, indent=2, default=_default))


def emit_error(cmd: str, error: str, fmt: str = "text") -> None:
    if fmt == "json":
        print(json.dumps({"ok": False, "cmd": cmd, "error": error}, indent=2, ensure_ascii=False), file=sys.stdout)
        sys.exit(1)
    print(f"Error: {error}", file=sys.stderr)
    sys.exit(1)


def _default(o: Any) -> Any:
    if isinstance(o, float):
        return round(o, 8)
    raise TypeError(f"Object of type {type(o)} is not JSON serializable")


def add_format(p) -> None:
    p.add_argument(
        "--format", "-f",
        choices=["text", "json"],
        default="text",
        help="Output format: text (human) or json (agent)",
    )
