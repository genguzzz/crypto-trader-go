"""Chat agent: one multi-turn Claude Agent SDK session per ctg conversation.

Tools: the ctg MCP endpoint (small market-data lookups, read-only, in-memory key from Go),
WebSearch/WebFetch, a `shell` tool that runs in the separate ctg-sandbox container, `remember`, and
the skills `perp-analysis` and `ctg-platform`. One agent argues both sides of a trade itself. Progress is streamed to Go as compact events; the session is resumed
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
CHAT_PROMPT = "chat_v2"
CHAT_MAX_TURNS = 60
CHAT_MODELS = ("claude-sonnet-5", "claude-opus-5-5")  # chosen per turn in the app
SANDBOX_SOCKET = os.environ.get("CTG_SANDBOX_SOCKET", "/run/sbx/sbx.sock")
# ctg data tools the agent may call directly: small, parameterised answers (a few thousand characters).
# Raw series (candles, minute OI, liquidation map, compare, track records: 15k–95k characters) are only
# reachable through the shell, where Python reduces them to the numbers the answer needs.
CTG_DIRECT = ("get_instrument", "get_universe", "search_instruments", "get_orderbook", "get_orderflow", "get_liquidations",
              "list_rules", "list_fields", "get_alert_events", "get_system_status")
SHELL_MODEL_LIMIT = 8000  # characters of shell output the model sees; the rest stays in files
CTG_HIDDEN = ("get_candles", "get_series", "get_liquidation_map", "compare_with_majors", "get_rule_track_record", "get_usage")
MCP_OUTPUT_TOKENS = "6000"  # hard cap per data-tool result (Claude Code truncates beyond it)

Emit = Callable[[dict], Awaitable[None]]

# Short Chinese labels for the progress chips in the app.
TOOL_LABELS = {
    "get_instrument": "合约详情", "get_candles": "K 线", "get_series": "持仓/资金费历史", "get_orderflow": "主动买卖",
    "get_orderbook": "盘口", "get_liquidations": "强平记录", "get_liquidation_map": "清算地图",
    "compare_with_majors": "对比 BTC/ETH", "get_universe": "监控池", "search_instruments": "搜索合约",
    "list_rules": "规则", "get_rule_track_record": "规则战绩", "get_alert_events": "告警", "get_system_status": "数据状态",
    "list_fields": "字段定义", "get_usage": "接口额度", "remember": "记住偏好", "WebSearch": "联网搜索", "WebFetch": "读取网页", "Skill": "加载分析流程",
}
# the analysis CLI's commands, as the owner would call them
CA_LABELS = {
    "analyze": "风险评分", "scan": "全市场扫描", "market": "大盘", "events": "新闻与事件", "news": "新闻", "flow": "主动买卖",
    "liq": "清算分析", "funding": "资金费", "oi": "持仓量", "longshort": "多空比", "indicators": "技术指标", "kline": "K 线",
    "depth": "盘口深度", "quote": "报价", "rank": "涨跌榜", "rules": "规则战绩", "alerts": "告警", "ctg": "查询数据",
    "tokenomics": "代币经济", "holders": "持仓分布", "security": "合约安全", "dex": "DEX 数据", "status": "数据状态",
}

def label_for(name: str, inp: Any = None) -> str:
    """Owner-facing label: what the step does, never how (no "sandbox" / tool ids)."""
    short = name.split("__")[-1]
    if short == "shell" and isinstance(inp, dict):
        cmd = str(inp.get("cmd", "")).strip()
        first = cmd.split()[:3]
        if first and first[0] == "ca" and len(first) > 1:
            return CA_LABELS.get(first[1], "数据分析")
        if first and first[0] == "rev":
            return "反转扫描" if len(first) > 1 and first[1] == "scan" else "反转评分"
        if "python" in cmd[:40]:
            return "Python 计算"
        return "计算"
    if short == "shell":
        return "计算"
    return TOOL_LABELS.get(short, short)


def brief_input(name: str, inp: Any) -> str:
    """Short subject shown next to the label (e.g. the instrument or the search query)."""
    if not isinstance(inp, dict):
        return ""
    short = name.split("__")[-1]
    if short == "shell":
        words = str(inp.get("cmd", "")).split()
        if len(words) > 1 and words[0] == "rev" and words[1] != "scan":
            return words[1]
        if len(words) > 2 and words[0] == "ca":
            subj = [w for w in words[2:] if not w.startswith("-") and "=" not in w][:1]
            inst = [w.split("=", 1)[1] for w in words[2:] if w.startswith("instId=")][:1]
            return " ".join(inst or subj)[:60]
        return ""
    if short == "WebSearch":
        return str(inp.get("query", ""))[:80]
    if short == "WebFetch":
        return str(inp.get("url", "")).split("//")[-1][:80]
    if short == "Skill":
        return ""
    if short == "remember":
        return str(inp.get("text", ""))[:80]
    keep = [str(v) for k, v in inp.items() if k in ("instId", "q")]
    keep += [f"{v}" for k, v in inp.items() if k in ("bar", "field", "days")]
    return " · ".join(keep)[:80]


def full_input(name: str, inp: Any) -> str:
    """The call's arguments for the expanded view: the command itself, or JSON."""
    if isinstance(inp, dict) and name.split("__")[-1] == "shell":
        return str(inp.get("cmd", ""))[:4000]
    try:
        return json.dumps(inp, ensure_ascii=False, indent=1)[:4000]
    except (TypeError, ValueError):
        return str(inp)[:4000]


def result_text(content: Any, limit: int = 6000) -> str:
    """A tool result as plain text, trimmed for the expanded view."""
    if isinstance(content, list):
        content = "\n".join(str(c.get("text", "")) for c in content if isinstance(c, dict) and c.get("type") == "text")
    text = str(content or "")
    if text.startswith("exit 0 · "):
        text = text.split("\n", 1)[1] if "\n" in text else ""
    return text if len(text) <= limit else text[:limit] + f"\n…（共 {len(text)} 字，已截断）"


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
          "Output is capped at 30k characters.",
          # a full JSON schema: the short {name: type} form makes every key required, and calls without
          # `timeout` failed validation ("'timeout' is a required property")
          {"type": "object", "properties": {"cmd": {"type": "string", "description": "bash command"},
                                            "timeout": {"type": "integer", "description": "seconds, default 60, max 300"}},
           "required": ["cmd"]})
    async def shell(args: dict) -> dict:
        try:
            res = await sandbox_exec(chat, str(args.get("cmd", "")), max(5, min(300, int(args.get("timeout") or 60))), key)
        except (OSError, asyncio.TimeoutError, ValueError) as e:
            return {"content": [{"type": "text", "text": f"sandbox unavailable: {e}"}], "is_error": True}
        text = f"exit {res.get('exit')} · {res.get('ms')} ms\n"
        if res.get("stdout"):
            text += res["stdout"]
        if res.get("stderr"):
            text += "\n[stderr]\n" + res["stderr"][-2000:]
        if len(text) > SHELL_MODEL_LIMIT:
            text = (text[:SHELL_MODEL_LIMIT] + f"\n…[输出 {len(text)} 字，已截断。把数据写进文件，用 Python 只打印需要的结果]")
        return {"content": [{"type": "text", "text": text}], "is_error": res.get("exit") not in (0, None)}

    return create_sdk_mcp_server(name="sandbox", version="1.0.0", tools=[shell])


def memory_server(emit: Emit):
    from claude_agent_sdk import create_sdk_mcp_server, tool

    @tool("remember", "Save one lasting preference or fact about the owner (e.g. 杠杆偏好：多空都用 8 倍). Use when the owner "
          "states a standing preference or asks you to remember something. Pass replaces=<id> to update an existing note "
          "from the list in your instructions instead of adding a duplicate.",
          {"type": "object", "properties": {"text": {"type": "string"},
                                            "replaces": {"type": "integer", "description": "id of the note this one updates"}},
           "required": ["text"]})
    async def remember(args: dict) -> dict:
        text = str(args.get("text", "")).strip()[:300]
        if not text:
            return {"content": [{"type": "text", "text": "empty note"}], "is_error": True}
        await emit({"type": "memory", "text": text, "replaces": int(args.get("replaces") or 0)})
        return {"content": [{"type": "text", "text": f"已记住：{text}"}]}

    return create_sdk_mcp_server(name="memory", version="1.0.0", tools=[remember])


def notes_block(notes: Any) -> str:
    lines = [f"[{n.get('id')}] {n.get('text')}" for n in (notes if isinstance(notes, list) else []) if isinstance(n, dict) and n.get("text")]
    return "\n\n<owner_notes>\n" + ("\n".join(lines) or "(none yet)") + "\n</owner_notes>\n"


def build_chat_options(config: dict, proxy: str, chat: str, session: str, notes: Any = None, emit: Emit | None = None):
    from claude_agent_sdk import ClaudeAgentOptions

    agent = config.get("agent") or {}
    env = llm.build_env(config.get("credential"), proxy)
    prompt, _ = llm.load_prompt(CHAT_PROMPT)
    prompt += notes_block(notes)
    mcp: dict[str, Any] = {"sandbox": sandbox_server(chat, agent.get("sandboxToken", ""))}
    allowed = ["mcp__sandbox__shell", "WebSearch", "WebFetch", "Skill"]
    if emit is not None:
        mcp["memory"] = memory_server(emit)
        allowed.append("mcp__memory__remember")
    ctg_tools: list[str] = []
    if agent.get("mcpUrl") and agent.get("mcpToken"):
        mcp["ctg"] = {"type": "http", "url": agent["mcpUrl"], "headers": {"Authorization": "Bearer " + agent["mcpToken"]}}
        ctg_tools = [f"mcp__ctg__{t}" for t in CTG_DIRECT]
        allowed += ctg_tools
    env["MAX_MCP_OUTPUT_TOKENS"] = MCP_OUTPUT_TOKENS
    kw: dict[str, Any] = dict(
        system_prompt=prompt,
        model=config.get("model") or llm.DEFAULT_MODEL,
        tools=["WebSearch", "WebFetch", "Skill"],  # one agent argues both sides itself: no subagents
        allowed_tools=allowed,
        disallowed_tools=[f"mcp__ctg__{t}" for t in CTG_HIDDEN],  # not even listed to the model
        mcp_servers=mcp,
        strict_mcp_config=True,
        setting_sources=["project"],  # skills from agent/.claude/skills (no CLAUDE.md there)
        skills=["perp-analysis", "ctg-platform"],
        permission_mode="dontAsk",
        max_turns=CHAT_MAX_TURNS,
        env=env,
        cwd=str(AGENT_DIR),
        include_partial_messages=True,
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
    if msg.get("model") in CHAT_MODELS:
        config = {**config, "model": msg["model"]}
    options = build_chat_options(config, proxy, chat_id, str(msg.get("sessionId") or ""), msg.get("notes"), emit)

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
                            p = {"t": "tool", "id": b.id, "name": b.name, "label": label_for(b.name, b.input), "input": "", "ok": None,
                                 "sub": bool(m.parent_tool_use_id)}
                            parts.append(p)
                            tools[b.id] = p
                            await emit({"type": "tool", "id": b.id, "name": b.name, "label": p["label"], "sub": p["sub"]})
                        p["input"] = brief_input(b.name, b.input)
                        p["args"] = full_input(b.name, b.input)
                        p["label"] = label_for(b.name, b.input)
                        await emit({"type": "tool_input", "id": b.id, "input": p["input"], "args": p["args"], "label": p["label"]})
            elif isinstance(m, UserMessage) and isinstance(m.content, list):
                for b in m.content:
                    if isinstance(b, ToolResultBlock) and b.tool_use_id in tools:
                        p = tools[b.tool_use_id]
                        p["ok"] = not b.is_error
                        p["output"] = result_text(b.content)
                        await emit({"type": "tool_done", "id": b.tool_use_id, "ok": p["ok"], "output": p["output"]})
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
