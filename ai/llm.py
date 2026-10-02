"""Claude Agent SDK calls: one stateless query per review / reflection / test.

Each call runs the bundled Claude Code CLI with no built-in tools (tools=[]), no MCP servers, no
filesystem settings and no session persistence. The model answers through the SDK's structured
output (a JSON Schema validated by the CLI), which is its only tool. The credential and the proxy
are passed per call in ``ClaudeAgentOptions.env``.
"""

from __future__ import annotations

import hashlib
import os
import tempfile
import time
from pathlib import Path
from typing import Any

import contextpack

PROMPT_DIR = Path(__file__).resolve().parent / "prompts"
REVIEW_PROMPT = "review_v1"
REFLECT_PROMPT = "reflect_v1"
DEFAULT_MODEL = "claude-sonnet-5"
REVIEW_MAX_TURNS = 4  # one structured answer plus schema-validation retries
REFLECT_MAX_TURNS = 4
TEST_MAX_TURNS = 1
NO_PROXY = "127.0.0.1,localhost"
CREDENTIAL_ENV = {"api_key": "ANTHROPIC_API_KEY", "oauth_token": "CLAUDE_CODE_OAUTH_TOKEN"}
# Never inherited from the worker's own environment: the credential comes from Go only.
STRIPPED_ENV = ("ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL")

DECISION_SCHEMA: dict[str, Any] = {
    "type": "object",
    "additionalProperties": False,
    "properties": {
        "action": {"type": "string", "enum": ["execute", "skip"]},
        "size_scale": {"type": "number", "minimum": 0, "maximum": 1,
                       "description": "Fraction of the strategy's risk size; 0 when skipping."},
        "stop_loss": {"type": "number", "minimum": 0,
                      "description": "Absolute stop price; 0 keeps the strategy's stop."},
        "take_profit": {"type": "number", "minimum": 0,
                        "description": "Absolute target price; 0 keeps the strategy's target."},
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "rationale": {"type": "string", "maxLength": 1200},
        "param_change": {"type": "object", "additionalProperties": {"type": "number"},
                         "description": "Optional proposal for AI-tunable parameters; owner approval required."},
    },
    "required": ["action", "size_scale", "confidence", "rationale"],
}

MEMORY_SCHEMA: dict[str, Any] = {
    "type": "object",
    "additionalProperties": False,
    "properties": {
        "memory": {"type": "string", "maxLength": 8000, "description": "The complete new experience document (Markdown)."},
        "changes": {"type": "string", "maxLength": 1000, "description": "What changed versus the previous version."},
    },
    "required": ["memory", "changes"],
}


class CallError(Exception):
    """A model call failed. ``network`` hints that the proxy should be re-checked."""

    def __init__(self, msg: str, network: bool = False):
        super().__init__(msg)
        self.network = network


def load_prompt(name: str) -> tuple[str, str]:
    """Prompt text and the first 12 hex of its sha256 (recorded in the audit)."""
    body = (PROMPT_DIR / f"{name}.md").read_text(encoding="utf-8")
    return body, hashlib.sha256(body.encode()).hexdigest()[:12]


def proxy_env(proxy: str) -> dict[str, str]:
    return {
        "HTTPS_PROXY": proxy, "HTTP_PROXY": proxy, "https_proxy": proxy, "http_proxy": proxy,
        "NO_PROXY": NO_PROXY, "no_proxy": NO_PROXY,
    }


def build_env(credential: dict | None, proxy: str) -> dict[str, str]:
    """Per-call environment for the CLI subprocess: proxy (mandatory), credential, quiet CLI."""
    if not proxy:
        raise CallError("no proxy configured")
    if not credential or not credential.get("value"):
        raise CallError("no credential configured")
    var = CREDENTIAL_ENV.get(credential.get("kind", ""))
    if var is None:
        raise CallError("unknown credential kind")
    env = proxy_env(proxy)
    env[var] = credential["value"]
    env.update({
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",  # no telemetry / update checks
        "DISABLE_AUTOUPDATER": "1",
        "ENABLE_TOOL_SEARCH": "false",
        "CLAUDE_CODE_MAX_RETRIES": "3",  # fail within the signal TTL instead of 11 retries
    })
    return env


def workdir() -> str:
    d = os.path.join(tempfile.gettempdir(), "ctg-ai-cwd")
    os.makedirs(d, exist_ok=True)
    return d


def build_options(*, system_prompt: str, model: str, env: dict[str, str], schema: dict | None, max_turns: int,
                  effort: str | None = None):
    from claude_agent_sdk import ClaudeAgentOptions

    kw: dict[str, Any] = dict(
        system_prompt=system_prompt,
        model=model or DEFAULT_MODEL,
        tools=[],  # no built-in tools at all
        allowed_tools=["StructuredOutput"] if schema else [],
        mcp_servers={},
        strict_mcp_config=True,
        setting_sources=[],  # ignore user/project settings, hooks, CLAUDE.md
        permission_mode="dontAsk",  # anything not pre-approved is denied, never prompted
        max_turns=max_turns,
        env=env,
        cwd=workdir(),
        extra_args={"no-session-persistence": None},
    )
    if schema is not None:
        kw["output_format"] = {"type": "json_schema", "schema": schema}
    if effort:
        kw["effort"] = effort
    return ClaudeAgentOptions(**kw)


async def run_query(prompt: str, options) -> dict[str, Any]:
    """Run one query; returns {structured, text, model, cost, latency_ms, usage, num_turns, tool_calls, session_id}."""
    from claude_agent_sdk import AssistantMessage, ResultMessage, TextBlock, ToolUseBlock, query

    started = time.monotonic()
    out: dict[str, Any] = {"structured": None, "text": "", "model": "", "cost": 0.0, "usage": None,
                           "num_turns": 0, "tool_calls": [], "session_id": ""}
    result = None
    try:
        async for msg in query(prompt=prompt, options=options):
            if isinstance(msg, AssistantMessage):
                out["model"] = msg.model or out["model"]
                for b in msg.content:
                    if isinstance(b, ToolUseBlock):
                        out["tool_calls"].append({"name": b.name, "input": b.input})
                    elif isinstance(b, TextBlock):
                        out["text"] += b.text
            elif isinstance(msg, ResultMessage):
                result = msg
    except Exception as e:  # SDK raises after an error result, or on CLI/connection failure
        if result is None:
            raise CallError(f"{type(e).__name__}: {e}", network=looks_network(str(e))) from e
    out["latency_ms"] = int((time.monotonic() - started) * 1000)
    if result is None:
        raise CallError("no result from the model")
    out["cost"] = float(result.total_cost_usd or 0)
    out["usage"] = result.usage
    out["num_turns"] = result.num_turns
    out["session_id"] = result.session_id
    out["structured"] = result.structured_output
    if result.result and not out["text"]:
        out["text"] = result.result
    if result.is_error or result.subtype != "success":
        detail = "; ".join(result.errors or []) or (result.result or "")
        status = f" (HTTP {result.api_error_status})" if result.api_error_status else ""
        raise CallError(f"model call failed: {result.subtype}{status} {detail}".strip(), network=looks_network(detail))
    return out


def looks_network(msg: str) -> bool:
    m = msg.lower()
    return any(s in m for s in ("connection", "econnrefused", "timed out", "timeout", "proxy", "network", "socket", "dns", "enotfound"))


def meta(prompt_version: str, prompt_sha: str, res: dict, model: str) -> dict[str, Any]:
    return {"promptVersion": prompt_version, "promptSha": prompt_sha, "model": res.get("model") or model,
            "costUsd": res.get("cost", 0.0), "latencyMs": res.get("latency_ms", 0), "usage": res.get("usage"),
            "numTurns": res.get("num_turns", 0), "sessionId": res.get("session_id", ""), "toolCalls": res.get("tool_calls", [])}


async def review(request: dict, config: dict, proxy: str) -> tuple[dict, dict]:
    """Returns (decision, meta). decision uses the Go field names (trade.Decision without id)."""
    system, sha = load_prompt(REVIEW_PROMPT)
    model = config.get("model") or DEFAULT_MODEL
    opts = build_options(system_prompt=system, model=model, env=build_env(config.get("credential"), proxy),
                         schema=DECISION_SCHEMA, max_turns=REVIEW_MAX_TURNS, effort=config.get("effort"))
    prompt = contextpack.render_review(request) + "\nReview this signal and answer with the structured decision."
    res = await run_query(prompt, opts)
    s = res["structured"]
    if not isinstance(s, dict) or s.get("action") not in ("execute", "skip"):
        raise CallError("the model returned no valid structured decision")
    m = meta(REVIEW_PROMPT, sha, res, model)
    decision = {
        "action": s["action"],
        "sizeScale": float(s.get("size_scale") or 0),
        "stopLoss": float(s.get("stop_loss") or 0),
        "takeProfit": float(s.get("take_profit") or 0),
        "confidence": float(s.get("confidence") or 0),
        "rationale": str(s.get("rationale") or ""),
        "paramChange": {k: float(v) for k, v in (s.get("param_change") or {}).items() if isinstance(v, (int, float))},
        "model": m["model"],
        "costUsd": m["costUsd"],
        "latencyMs": m["latencyMs"],
    }
    return decision, m


async def reflect(request: dict, config: dict, proxy: str) -> dict:
    system, sha = load_prompt(REFLECT_PROMPT)
    model = config.get("model") or DEFAULT_MODEL
    opts = build_options(system_prompt=system, model=model, env=build_env(config.get("credential"), proxy),
                         schema=MEMORY_SCHEMA, max_turns=REFLECT_MAX_TURNS, effort=config.get("effort"))
    prompt = contextpack.render_reflect(request) + "\nWrite the new experience document."
    res = await run_query(prompt, opts)
    s = res["structured"]
    if not isinstance(s, dict) or not str(s.get("memory") or "").strip():
        raise CallError("the model returned no memory document")
    m = meta(REFLECT_PROMPT, sha, res, model)
    m.update({"memory": str(s["memory"]).strip(), "changes": str(s.get("changes") or "")})
    return m


async def test(config: dict, proxy: str) -> dict:
    model = config.get("model") or DEFAULT_MODEL
    opts = build_options(system_prompt="You are a connectivity check. Reply with the single word OK.", model=model,
                         env=build_env(config.get("credential"), proxy), schema=None, max_turns=TEST_MAX_TURNS)
    res = await run_query("Reply with OK.", opts)
    m = meta("test", "", res, model)
    m["reply"] = (res.get("text") or "").strip()[:200]
    return m
