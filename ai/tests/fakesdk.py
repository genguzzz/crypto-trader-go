"""A stand-in for claude_agent_sdk so tests never start the CLI or call a model."""

from __future__ import annotations

import sys
import types
from dataclasses import dataclass, field
from typing import Any


@dataclass
class TextBlock:
    text: str


@dataclass
class ToolUseBlock:
    id: str
    name: str
    input: dict


@dataclass
class AssistantMessage:
    content: list
    model: str


@dataclass
class ResultMessage:
    subtype: str = "success"
    duration_ms: int = 1200
    duration_api_ms: int = 1000
    is_error: bool = False
    num_turns: int = 2
    session_id: str = "sess-1"
    total_cost_usd: float | None = 0.0123
    usage: dict | None = None
    result: str | None = None
    structured_output: Any = None
    errors: list | None = None
    api_error_status: int | None = None


class ClaudeAgentOptions:
    def __init__(self, **kw):
        self.kw = kw
        for k, v in kw.items():
            setattr(self, k, v)


@dataclass
class State:
    messages: list = field(default_factory=list)
    raise_exc: Exception | None = None
    calls: list = field(default_factory=list)


state = State()


async def query(*, prompt, options=None, transport=None):
    state.calls.append({"prompt": prompt, "options": options})
    for m in state.messages:
        yield m
    if state.raise_exc is not None:
        raise state.raise_exc


def install() -> State:
    """Register the fake as claude_agent_sdk and reset its state."""
    mod = types.ModuleType("claude_agent_sdk")
    for name in ("TextBlock", "ToolUseBlock", "AssistantMessage", "ResultMessage", "ClaudeAgentOptions", "query"):
        setattr(mod, name, globals()[name])
    mod.__version__ = "0.0.test"
    sys.modules["claude_agent_sdk"] = mod
    state.messages, state.raise_exc, state.calls = [], None, []
    return state
