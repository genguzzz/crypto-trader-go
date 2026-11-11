"""A stand-in for claude_agent_sdk so tests never start the CLI or call a model."""

from __future__ import annotations

import asyncio
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
class ToolResultBlock:
    tool_use_id: str
    content: Any = None
    is_error: bool | None = None


@dataclass
class AssistantMessage:
    content: list
    model: str
    parent_tool_use_id: str | None = None


@dataclass
class UserMessage:
    content: Any
    parent_tool_use_id: str | None = None


@dataclass
class StreamEvent:
    event: dict
    uuid: str = "u"
    session_id: str = "sess-1"
    parent_tool_use_id: str | None = None


def text_delta(text: str) -> StreamEvent:
    return StreamEvent({"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": text}})


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
    followups: list = field(default_factory=list)  # streaming input: the messages of each later prompt's run
    raise_exc: Exception | None = None
    calls: list = field(default_factory=list)


state = State()


async def _play(batch):
    """A run's messages; a callable in the list is called there (a test acting mid-turn)."""
    for m in batch:
        if callable(m):
            r = m()
            if asyncio.iscoroutine(r):
                await r
            continue
        yield m


async def query(*, prompt, options=None, transport=None):
    call = {"prompt": prompt, "options": options, "inputs": []}
    state.calls.append(call)
    if isinstance(prompt, str):
        call["inputs"].append(prompt)
        async for m in _play(state.messages):
            yield m
    else:
        # like the CLI: stdin is read while the run goes on, and every prompt written gets a run (and a result)
        got, done = asyncio.Event(), asyncio.Event()

        async def read():
            async for x in prompt:
                call["inputs"].append(x["message"]["content"])
                got.set()
            done.set()
            got.set()

        reader = asyncio.ensure_future(read())
        batches = [state.messages, *state.followups]
        run = 0
        while True:
            while len(call["inputs"]) <= run and not done.is_set():
                got.clear()
                await got.wait()
            if len(call["inputs"]) <= run:
                break
            async for m in _play(batches[run] if run < len(batches) else [ResultMessage()]):
                yield m
            run += 1
        await reader
    if state.raise_exc is not None:
        raise state.raise_exc


def tool(name, description, schema):
    def wrap(fn):
        fn.tool_name = name
        return fn
    return wrap


def create_sdk_mcp_server(name, version="1.0.0", tools=None):
    return {"type": "sdk", "name": name, "tools": [getattr(t, "tool_name", "") for t in tools or []]}


def install() -> State:
    """Register the fake as claude_agent_sdk and reset its state."""
    mod = types.ModuleType("claude_agent_sdk")
    for name in ("TextBlock", "ToolUseBlock", "ToolResultBlock", "AssistantMessage", "UserMessage", "ResultMessage", "ClaudeAgentOptions", "query",
                 "tool", "create_sdk_mcp_server"):
        setattr(mod, name, globals()[name])
    mod.__version__ = "0.0.test"
    sub = types.ModuleType("claude_agent_sdk.types")
    sub.StreamEvent = StreamEvent
    mod.types = sub
    sys.modules["claude_agent_sdk"] = mod
    sys.modules["claude_agent_sdk.types"] = sub
    state.messages, state.followups, state.raise_exc, state.calls = [], [], None, []
    return state
