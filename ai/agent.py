"""Chat agent: one multi-turn Claude Agent SDK session per ctg conversation.

Tools: the ctg MCP endpoint (market data, read-only, in-memory key from Go), WebSearch/WebFetch, a
`shell` tool that runs in the separate ctg-sandbox container, the `perp-analysis` skill and two
debate subagents (bull / bear). Progress is streamed to Go as compact events; the session is resumed
by id on the next turn (transcripts live under $HOME/.claude, a persistent volume in compose).
"""

from __future__ import annotations

import asyncio
import json
import os
import time
from pathlib import Path
from typing import Any, Awaitable, Callable

import llm

AGENT_DIR = Path(__file__).resolve().parent / "agent"  # cwd: holds .claude/skills
CHAT_PROMPT = "chat_v1"
CHAT_MAX_TURNS = 60
SANDBOX_SOCKET = os.environ.get("CTG_SANDBOX_SOCKET", "/run/sbx/sbx.sock")

Emit = Callable[[dict], Awaitable[None]]

# Short Chinese labels for the progress chips in the app.
TOOL_LABELS = {
    "get_instrument": "合约详情", "get_candles": "K 线", "get_series": "持仓/资金费历史", "get_orderflow": "主动买卖",
    "get_orderbook": "盘口", "get_liquidations": "强平记录", "get_liquidation_map": "清算地图",
    "compare_with_majors": "对比 BTC/ETH", "get_universe": "监控池", "search_instruments": "搜索合约",
    "list_rules": "规则", "get_rule_track_record": "规则战绩", "get_alert_events": "告警", "get_system_status": "数据状态",
    "list_fields": "字段定义", "get_usage": "接口额度", "WebSearch": "联网搜索", "WebFetch": "读取网页", "shell": "沙箱计算", "Skill": "加载分析流程",
    "Task": "子分析", "Agent": "子分析",
}

BULL_PROMPT = """你是多头研究员。根据给你的证据摘要（可以再用工具补查数据），给出做多这个合约最有力的 3 条论据，
每条附具体数字和时间窗，再写出会让多头观点失效的条件（价位/事件）。最后一行给出多头信心 0-10。中文，简洁，不超过 250 字。"""
BEAR_PROMPT = """你是空头研究员。根据给你的证据摘要（可以再用工具补查数据），给出做空这个合约最有力的 3 条论据，
每条附具体数字和时间窗，再写出会让空头观点失效的条件（价位/事件）。最后一行给出空头信心 0-10。中文，简洁，不超过 250 字。"""


def label_for(name: str) -> str:
    short = name.split("__")[-1]
    return TOOL_LABELS.get(short, short)


def brief_input(name: str, inp: Any) -> str:
    """One line describing a tool call for the progress chip."""
    if not isinstance(inp, dict):
        return ""
    short = name.split("__")[-1]
    if short == "shell":
        return str(inp.get("cmd", ""))[:160]
    if short == "WebSearch":
        return str(inp.get("query", ""))[:120]
    if short == "WebFetch":
        return str(inp.get("url", ""))[:160]
    if short == "Skill":
        return str(inp.get("skill") or inp.get("command") or "")
    if short in ("Task", "Agent"):
        return str(inp.get("subagent_type") or inp.get("description") or "")[:80]
    keep = [f"{k}={v}" for k, v in inp.items() if k in ("instId", "bar", "field", "hours", "limit", "days", "q", "scope", "market")]
    return " ".join(keep)[:160]


async def sandbox_exec(chat: str, cmd: str, timeout: int, key: str) -> dict:
    r, w = await asyncio.open_unix_connection(SANDBOX_SOCKET, limit=1 << 20)
    try:
        w.write((json.dumps({"chat": chat, "cmd": cmd, "timeout": timeout, "key": key}) + "\n").encode())
        await w.drain()
        line = await asyncio.wait_for(r.readline(), timeout + 15)
        return json.loads(line)
    finally:
        w.close()


def sandbox_server(chat: str, key: str):
    from claude_agent_sdk import create_sdk_mcp_server, tool

    @tool("shell", "Run a bash command in the isolated analysis sandbox (Python 3 + numpy/pandas, curl, jq, the `ca` "
          "crypto-analyzer CLI). The working directory persists for this conversation. No access to ctg's host. "
          "Output is capped at 30k characters.", {"cmd": str, "timeout": int})
    async def shell(args: dict) -> dict:
        try:
            res = await sandbox_exec(chat, str(args.get("cmd", "")), int(args.get("timeout") or 60), key)
        except (OSError, asyncio.TimeoutError, ValueError) as e:
            return {"content": [{"type": "text", "text": f"sandbox unavailable: {e}"}], "is_error": True}
        text = f"exit {res.get('exit')} · {res.get('ms')} ms\n"
        if res.get("stdout"):
            text += res["stdout"]
        if res.get("stderr"):
            text += "\n[stderr]\n" + res["stderr"]
        return {"content": [{"type": "text", "text": text}], "is_error": res.get("exit") not in (0, None)}

    return create_sdk_mcp_server(name="sandbox", version="1.0.0", tools=[shell])


def build_chat_options(config: dict, proxy: str, chat: str, session: str):
    from claude_agent_sdk import AgentDefinition, ClaudeAgentOptions

    agent = config.get("agent") or {}
    env = llm.build_env(config.get("credential"), proxy)
    prompt, _ = llm.load_prompt(CHAT_PROMPT)
    mcp: dict[str, Any] = {"sandbox": sandbox_server(chat, agent.get("sandboxToken", ""))}
    allowed = ["mcp__sandbox__shell", "WebSearch", "WebFetch", "Skill", "Task", "Agent"]
    if agent.get("mcpUrl") and agent.get("mcpToken"):
        mcp["ctg"] = {"type": "http", "url": agent["mcpUrl"], "headers": {"Authorization": "Bearer " + agent["mcpToken"]}}
        allowed.append("mcp__ctg")
    data_tools = ["mcp__ctg", "mcp__sandbox__shell", "WebSearch", "WebFetch"]
    kw: dict[str, Any] = dict(
        system_prompt=prompt,
        model=config.get("model") or llm.DEFAULT_MODEL,
        tools=["WebSearch", "WebFetch", "Skill", "Task", "Agent"],  # Task was renamed Agent in newer CLIs
        allowed_tools=allowed,
        mcp_servers=mcp,
        strict_mcp_config=True,
        setting_sources=["project"],  # skills from agent/.claude/skills (no CLAUDE.md there)
        skills=["perp-analysis"],
        permission_mode="dontAsk",
        max_turns=CHAT_MAX_TURNS,
        env=env,
        cwd=str(AGENT_DIR),
        include_partial_messages=True,
        agents={
            "bull": AgentDefinition(description="多头研究员：给出做多的最强论据与失效条件", prompt=BULL_PROMPT, tools=data_tools, model="inherit", maxTurns=12),
            "bear": AgentDefinition(description="空头研究员：给出做空的最强论据与失效条件", prompt=BEAR_PROMPT, tools=data_tools, model="inherit", maxTurns=12),
        },
    )
    if session:
        kw["resume"] = session
    if config.get("effort"):
        kw["effort"] = config["effort"]
    return ClaudeAgentOptions(**kw)


async def chat(msg: dict, config: dict, proxy: str, emit: Emit) -> dict:
    """Run one user turn; streams events through emit and returns the final result."""
    from claude_agent_sdk import AssistantMessage, ResultMessage, ToolResultBlock, ToolUseBlock, UserMessage, query
    from claude_agent_sdk.types import StreamEvent

    chat_id = str(msg.get("chatId") or "")
    text = str(msg.get("message") or "")
    inst = str(msg.get("instId") or "")
    prompt = f"[当前页面: {inst}]\n{text}" if inst else text
    options = build_chat_options(config, proxy, chat_id, str(msg.get("sessionId") or ""))

    parts: list[dict] = []  # ordered: {"t":"text","v"} | {"t":"tool","id","name","label","input","ok","sub"}
    tools: dict[str, dict] = {}
    started = time.monotonic()
    result = None

    def text_part() -> dict:
        if not parts or parts[-1]["t"] != "text":
            parts.append({"t": "text", "v": ""})
        return parts[-1]

    try:
        async for m in query(prompt=prompt, options=options):
            if isinstance(m, StreamEvent):
                if m.parent_tool_use_id:
                    continue  # subagent tokens: only their tool calls are shown
                ev = m.event or {}
                if ev.get("type") == "content_block_delta" and (ev.get("delta") or {}).get("type") == "text_delta":
                    d = ev["delta"].get("text", "")
                    if d:
                        text_part()["v"] += d
                        await emit({"type": "text", "delta": d})
                elif ev.get("type") == "content_block_start" and (ev.get("content_block") or {}).get("type") == "text":
                    if parts and parts[-1]["t"] == "text" and parts[-1]["v"].strip():
                        parts.append({"t": "text", "v": ""})  # a new paragraph (e.g. after a subagent returned)
                        await emit({"type": "text_start"})
                elif ev.get("type") == "content_block_start" and (ev.get("content_block") or {}).get("type") in ("tool_use", "server_tool_use"):
                    b = ev["content_block"]
                    p = {"t": "tool", "id": b.get("id", ""), "name": b.get("name", ""), "label": label_for(b.get("name", "")), "input": "", "ok": None, "sub": False}
                    parts.append(p)
                    tools[p["id"]] = p
                    await emit({"type": "tool", "id": p["id"], "name": p["name"], "label": p["label"]})
            elif isinstance(m, AssistantMessage):
                for b in m.content:
                    if isinstance(b, ToolUseBlock):
                        p = tools.get(b.id)
                        if p is None:  # a subagent's call (no stream events for those)
                            p = {"t": "tool", "id": b.id, "name": b.name, "label": label_for(b.name), "input": "", "ok": None,
                                 "sub": bool(m.parent_tool_use_id)}
                            parts.append(p)
                            tools[b.id] = p
                            await emit({"type": "tool", "id": b.id, "name": b.name, "label": p["label"], "sub": p["sub"]})
                        p["input"] = brief_input(b.name, b.input)
                        await emit({"type": "tool_input", "id": b.id, "input": p["input"]})
            elif isinstance(m, UserMessage) and isinstance(m.content, list):
                for b in m.content:
                    if isinstance(b, ToolResultBlock) and b.tool_use_id in tools:
                        tools[b.tool_use_id]["ok"] = not b.is_error
                        await emit({"type": "tool_done", "id": b.tool_use_id, "ok": not b.is_error})
            elif isinstance(m, ResultMessage):
                result = m
    except Exception as e:  # noqa: BLE001
        if result is None:
            raise llm.CallError(f"{type(e).__name__}: {e}", network=llm.looks_network(str(e))) from e
    if result is None:
        raise llm.CallError("no result from the model")
    if result.is_error or result.subtype != "success":
        detail = "; ".join(result.errors or []) or (result.result or "")
        raise llm.CallError(f"model call failed: {result.subtype} {detail}".strip(), network=llm.looks_network(detail))
    for p in parts:
        if p["t"] == "tool" and p["ok"] is None:
            p["ok"] = True
    final = "\n\n".join(p["v"].strip() for p in parts if p["t"] == "text" and p["v"].strip()) or (result.result or "")
    return {
        "text": final, "sessionId": result.session_id, "cost": float(result.total_cost_usd or 0),
        "steps": parts, "turns": result.num_turns, "latencyMs": int((time.monotonic() - started) * 1000),
    }
