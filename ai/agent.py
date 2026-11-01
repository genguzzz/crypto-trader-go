"""Chat agent: one multi-turn Claude Agent SDK session per ctg conversation.

A turn runs in streaming-input mode so the server can steer it: a further user message (a fired signal,
the result of an approved trade) is handed to the running turn through its Inbox and answered before
the call ends (Go: aigate.SteerChat → worker `steer`).

Tools: the ctg MCP endpoint (small market-data lookups, read-only, in-memory key from Go),
WebSearch/WebFetch, a `shell` tool that runs in the separate ctg-sandbox container, memory (memory_save / memory_search / memory_forget), and
the skills `perp-analysis` and `ctg-platform`. One agent argues both sides of a trade itself. Progress is streamed to Go as compact events; the session is resumed
by id on the next turn (transcripts live under $HOME/.claude, a persistent volume in compose).
"""

from __future__ import annotations

import asyncio
import re
import json
import os
import time
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Awaitable, Callable

import llm

TZ8 = timezone(timedelta(hours=8))

AGENT_DIR = Path(__file__).resolve().parent / "agent"  # cwd: holds .claude/skills
CHAT_PROMPT = "chat_v2"
CHAT_MAX_TURNS = 60
CHAT_MODELS = ("claude-sonnet-5", "claude-opus-5-5")  # chosen per turn in the app
CHAT_EFFORTS = ("off", "low", "medium", "high")  # "off" turns thinking off
SANDBOX_SOCKET = os.environ.get("CTG_SANDBOX_SOCKET", "/run/sbx/sbx.sock")
# ctg data tools the agent may call directly: small, parameterised answers (a few thousand characters).
# Raw series (candles, minute OI, liquidation map, compare, track records: 15k–95k characters) are only
# reachable through the shell, where Python reduces them to the numbers the answer needs.
CTG_DIRECT = ("get_instrument", "get_universe", "search_instruments", "get_orderbook", "get_orderflow", "get_liquidations",
              "list_rules", "list_fields", "get_alert_events", "get_system_status", "get_positions", "okx_api", "okx_history",
              "propose_trade", "get_trade_proposals", "subscribe_signal", "list_signals", "unsubscribe_signal",
              "test_signal_script")
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
    "list_fields": "字段定义", "get_usage": "接口额度", "get_positions": "我的仓位", "memory_save": "记一笔", "memory_search": "翻记忆", "memory_forget": "删掉记忆", "okx_api": "查询 OKX 账户", "okx_history": "OKX 账户统计", "propose_trade": "提交交易提议", "get_trade_proposals": "交易提议", "subscribe_signal": "订阅信号", "unsubscribe_signal": "取消订阅", "list_signals": "查看信号", "test_signal_script": "测试信号脚本", "list_chats": "查看历史对话", "read_chat": "读取对话", "WebSearch": "联网搜索", "WebFetch": "读取网页", "Skill": "加载分析流程",
}
# the analysis CLI's commands, as the owner would call them
CA_LABELS = {
    "brief": "数据包", "analyze": "风险评分", "scan": "全市场扫描", "market": "大盘", "events": "新闻与事件", "news": "新闻", "flow": "主动买卖",
    "liq": "清算分析", "funding": "资金费", "oi": "持仓量", "longshort": "多空比", "indicators": "技术指标", "kline": "K 线",
    "depth": "盘口深度", "quote": "报价", "rank": "涨跌榜", "rules": "规则战绩", "alerts": "告警", "ctg": "查询数据",
    "tokenomics": "代币经济", "holders": "持仓分布", "security": "合约安全", "dex": "DEX 数据", "status": "数据状态",
}

PROPOSE_LABELS = {"open": "提议开仓", "add": "提议补仓", "reduce": "提议减仓", "close": "提议平仓", "adjust": "提议调整止盈止损"}


def label_for(name: str, inp: Any = None) -> str:
    """Owner-facing label: what the step does, never how (no "sandbox" / tool ids). Shell steps are named
    after the command (the command line itself is the subject, see brief_input)."""
    short = name.split("__")[-1]
    if short == "propose_trade" and isinstance(inp, dict):
        return PROPOSE_LABELS.get(str(inp.get("kind") or "open"), TOOL_LABELS[short])
    if short == "shell":
        words = str(inp.get("cmd", "")).split()[:3] if isinstance(inp, dict) else []
        if len(words) > 1 and words[0] == "ca":
            if words[1] == "news" and "--read" in inp.get("cmd", ""):
                return "读新闻正文"
            return CA_LABELS.get(words[1], f"ca {words[1]}")
        if words and words[0] == "rev":
            return "反转扫描" if len(words) > 1 and words[1] == "scan" else "反转评分"
        return "命令"
    return TOOL_LABELS.get(short, short)


def shell_line(cmd: str, limit: int = 90) -> str:
    """A shell command on one line for the step chip: noise (stderr redirects, `| head`) dropped."""
    s = re.sub(r"\s*2>&1", "", cmd)
    s = re.sub(r"\s*\|\s*head(\s+-n?\s*\d+)?\s*$", "", s.strip(), flags=re.M)
    s = " ⏎ ".join(x.strip() for x in s.splitlines() if x.strip())
    return s if len(s) <= limit else s[: limit - 1] + "…"


def brief_input(name: str, inp: Any) -> str:
    """Short subject shown next to the label (e.g. the instrument, the search query, the command)."""
    if not isinstance(inp, dict):
        return ""
    short = name.split("__")[-1]
    if short == "shell":
        return shell_line(str(inp.get("cmd", "")))
    if short == "WebSearch":
        return str(inp.get("query", ""))[:80]
    if short == "WebFetch":
        return str(inp.get("url", "")).split("//")[-1][:80]
    if short == "Skill":
        return ""
    if short == "memory_save":
        return str(inp.get("text", ""))[:80]
    if short == "memory_search":
        return str(inp.get("query", ""))[:40]
    if short == "memory_forget":
        return f"#{inp.get('id', '')}"
    if short == "read_chat":
        return f"#{inp.get('id', '')}"
    if short == "list_chats":
        return str(inp.get("query", ""))[:40]
    if short == "subscribe_signal":
        spec = inp.get("spec")
        if isinstance(spec, str):
            try:
                spec = json.loads(spec)
            except ValueError:
                spec = {}
        spec = spec if isinstance(spec, dict) else {}
        if not spec and inp.get("code"):
            spec = {"type": "script"}
        return signal_brief(spec, str(inp.get("code") or ""))
    if short == "test_signal_script":
        return script_title(str(inp.get("code") or ""))
    if short == "unsubscribe_signal":
        return f"#{inp.get('id', '')}"
    if short == "propose_trade":
        return propose_brief(inp)
    keep = [str(v) for k, v in inp.items() if k in ("instId", "q")]
    keep += [f"{v}" for k, v in inp.items() if k in ("bar", "field", "days")]
    return " · ".join(keep)[:80]


def propose_brief(inp: dict) -> str:
    """A propose_trade call in a few words: coin, side, the order, what it changes."""
    coin = str(inp.get("instId") or "").upper().split("-")[0]
    parts = [f"{coin} {'多' if inp.get('side') == 'long' else '空'}"]
    kind = str(inp.get("kind") or "open")
    if kind == "reduce" and inp.get("fraction"):
        try:
            parts.append(f"{float(inp['fraction']) * 100:.0f}%")
        except (TypeError, ValueError):
            pass
    if kind != "adjust":
        parts.append(f"限价 @{inp['price']}" if inp.get("orderType") == "limit" and inp.get("price") else "市价")
    if kind == "open" and inp.get("marginMode") == "cross":
        parts.append("全仓")
    if kind in ("open", "add", "adjust") and inp.get("sl"):
        parts.append(f"止损 {inp['sl']}")
    return " · ".join(parts)[:80]


def script_title(code: str) -> str:
    """A signal script's one-line description (its docstring), else "脚本"."""
    m = re.search(r'class\s+\w+\s*\([^)]*\)\s*:\s*\n\s*(?:"""|\'\'\')\s*(.+?)\s*(?:"""|\'\'\'|\n)', code)
    return m.group(1)[:60] if m else "脚本"


def signal_brief(spec: dict, code: str = "") -> str:
    """The condition of a subscribe_signal call in a few words (the server writes the full Chinese description)."""
    kind = spec.get("type", "")
    if kind == "script":
        return "脚本：" + script_title(str(spec.get("code") or code))
    if kind in ("all", "any"):
        parts = [signal_brief(c, code) for c in spec.get("of") or [] if isinstance(c, dict)]
        return (" 且 " if kind == "all" else " 或 ").join(p for p in parts if p)[:80]
    coin = str(spec.get("instId", "")).split("-")[0].upper()
    if kind == "price":
        return f"{coin} {'≥' if spec.get('op') in ('above', '>', '>=') else '≤'} {spec.get('level', '')}" + (f" ({spec['bar']} 收盘)" if spec.get("bar") else "")
    if kind == "zone":
        return f"{coin} {spec.get('lo', '')}–{spec.get('hi', '')}"
    if kind == "field":
        return f"{coin} {spec.get('field', '')} {spec.get('op', '')} {spec.get('value', '')}"
    if kind == "moverisk":
        return f"{coin} {spec.get('metric', '')} {spec.get('op', '')} {spec.get('value', '')}".strip()
    if kind == "plan":
        return f"计划 #{spec.get('planId', '')} {spec.get('state', '')}"
    if kind == "time":
        return str(spec.get("at") or f"{spec.get('afterMin', '')} 分钟后")
    if kind == "alert":
        return f"告警 #{spec.get('ruleId', '')} {coin}".strip()
    return kind


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


def sessions_server(base: str, current: str, token: str):
    """The owner's other AI conversations: a list and one conversation's messages (questions and answers,
    not the tool steps). Read from ctg's own API on loopback with the per-process sessions token
    (header X-CTG-Agent; /api/v1 needs the owner key otherwise); the current chat is marked, not hidden."""
    from claude_agent_sdk import create_sdk_mcp_server, tool

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))  # loopback: never through the LLM proxy

    def fetch(path: str):
        req = urllib.request.Request(base + path, headers={"X-CTG-Agent": token})
        with opener.open(req, timeout=15) as r:
            return json.load(r)

    def when(ms: int) -> str:
        return datetime.fromtimestamp(ms / 1000, TZ8).strftime("%m-%d %H:%M")

    @tool("list_chats", "List the owner's AI conversations, newest first: id, last update, title (his first question), "
          "the coin page it started on, and whether it is still running. Use when he refers to an earlier conversation "
          "(\"上次/之前/另一个对话里…\") or asks what was discussed; then read_chat the one you need.",
          {"type": "object", "properties": {"query": {"type": "string", "description": "filter: words in the title or a coin, e.g. SOL"},
                                            "limit": {"type": "integer", "description": "default 20, max 50"}}})
    async def list_chats(args: dict) -> dict:
        try:
            cs = fetch("/api/v1/ai/chats")
        except (OSError, ValueError) as e:
            return {"content": [{"type": "text", "text": f"chats unavailable: {e}"}], "is_error": True}
        q = str(args.get("query") or "").strip().upper()
        if q:
            cs = [c for c in cs if q in (c.get("title") or "").upper() or q in (c.get("instId") or "")]
        lim = max(1, min(int(args.get("limit") or 20), 50))
        lines = []
        for c in cs[:lim]:
            tag = "（当前对话）" if str(c["id"]) == current else ""
            inst = f" · {c['instId'].split('-')[0]}" if c.get("instId") else ""
            run = " · 进行中" if c.get("running") else ""
            lines.append(f"#{c['id']} {when(c['updated'])}{inst}{run} {c.get('title', '')[:60]}{tag}")
        text = "\n".join(lines) or "没有匹配的对话"
        return {"content": [{"type": "text", "text": f"共 {len(cs)} 个对话，显示 {min(lim, len(cs))} 个：\n{text}"}]}

    @tool("read_chat", "Read one AI conversation by id: each question and answer with its time (answers' component JSON kept, "
          "tool steps left out). Long answers are cut; raise max_chars (default 6000, max 20000) only if you need more.",
          {"type": "object", "properties": {"id": {"type": "integer"},
                                            "max_chars": {"type": "integer", "description": "default 6000, max 20000"}},
           "required": ["id"]})
    async def read_chat(args: dict) -> dict:
        try:
            c = fetch(f"/api/v1/ai/chats/{int(args['id'])}")
        except (OSError, ValueError, KeyError) as e:
            return {"content": [{"type": "text", "text": f"chat not found: {e}"}], "is_error": True}
        cap = max(1000, min(int(args.get("max_chars") or 6000), 20000))
        msgs = c.get("messages") or []
        per = max(400, cap // max(1, len(msgs)))
        out = [f"#{c['id']} {c.get('title', '')}" + (f" · {c['instId']}" if c.get("instId") else "") + f" · {len(msgs)} 条消息"]
        for m in msgs:
            who = {"user": "他", "assistant": "你", "signal": "⚡信号", "trade": "交易执行"}.get(m["role"], "系统")
            body = (m.get("content") or "").strip()
            if len(body) > per:
                body = body[:per] + f"…（截断，原文 {len(body)} 字）"
            out.append(f"[{when(m['ts'])}] {who}：{body}")
        return {"content": [{"type": "text", "text": "\n\n".join(out)[:cap + 400]}]}

    return create_sdk_mcp_server(name="sessions", version="1.0.0", tools=[list_chats, read_chat])


MEMORY_KINDS = ["profile", "fact", "position", "event", "plan"]


def memory_server(emit: Emit, base: str = "", token: str = ""):
    """The owner's memory, two layers (Go: internal/aigate/notes.go). profile = standing preferences, sent with
    every turn; fact / position / event / plan = archival, the most relevant ones arrive with each turn and
    memory_search finds more (ctg's API on loopback with the sessions token). Writes go back to Go as events."""
    from claude_agent_sdk import create_sdk_mcp_server, tool

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))  # loopback: never through the LLM proxy

    @tool("memory_save", "Save one memory: a single self-contained sentence naming the specific thing (\"不做 meme 币和新上线的币\", "
          "not \"不做这类\"). kind: profile = a standing preference or rule, sent with every conversation (leverage, sizing, "
          "coins he avoids, timeframes; max 12 — merge rather than add); fact = something about him; position = a holding or "
          "plan for one he keeps outside ctg; event = something that happened (a trade he made, a call that worked or failed, "
          "his feedback on your analysis); plan = something to follow up (\"等 SOL 回踩 140 再看\", \"FOMC 后复盘\"). Only what he "
          "states or clearly confirms; never your own assumptions, defaults or placeholders. tags: 1-4 search words (coins, "
          "topics). A near-duplicate of the same kind is updated automatically; pass replaces=<id> to rewrite one.",
          {"type": "object", "properties": {"text": {"type": "string"}, "kind": {"type": "string", "enum": MEMORY_KINDS},
                                            "tags": {"type": "array", "items": {"type": "string"}},
                                            "replaces": {"type": "integer", "description": "id of the memory this one updates"}},
           "required": ["text", "kind"]})
    async def memory_save(args: dict) -> dict:
        text = str(args.get("text", "")).strip()[:300]
        kind = str(args.get("kind") or "profile")
        if not text or re.search(r"占位|placeholder|无需保存|测试|test", text, re.I):
            return {"content": [{"type": "text", "text": "empty memory"}], "is_error": True}
        if kind not in MEMORY_KINDS:
            return {"content": [{"type": "text", "text": f"kind must be one of {MEMORY_KINDS}"}], "is_error": True}
        tags = [str(t).strip()[:30] for t in (args.get("tags") or []) if str(t).strip()][:4]
        await emit({"type": "memory", "text": text, "kind": kind, "tags": tags, "replaces": int(args.get("replaces") or 0)})
        return {"content": [{"type": "text", "text": f"已记下（{kind}）：{text}"}]}

    @tool("memory_search", "Search the owner's memories by words (coins, topics), optionally one kind. Your instructions carry "
          "only <profile> and the few memories most relevant to this message; search when he says \"我说过 / 上次那个计划 / "
          "我的持仓\" or you need more background, and before saving something that may already exist.",
          {"type": "object", "properties": {"query": {"type": "string"}, "kind": {"type": "string", "enum": MEMORY_KINDS},
                                            "limit": {"type": "integer", "description": "default 10, max 50"}},
           "required": ["query"]})
    async def memory_search(args: dict) -> dict:
        if not base:
            return {"content": [{"type": "text", "text": "memory search unavailable"}], "is_error": True}
        q = urllib.parse.urlencode({k: v for k, v in {"q": str(args.get("query") or " "), "kind": args.get("kind") or "",
                                                      "limit": int(args.get("limit") or 10)}.items() if v})
        try:
            req = urllib.request.Request(f"{base}/api/v1/ai/notes?{q}", headers={"X-CTG-Agent": token})
            with opener.open(req, timeout=15) as r:
                ns = json.load(r)
        except (OSError, ValueError) as e:
            return {"content": [{"type": "text", "text": f"memory unavailable: {e}"}], "is_error": True}
        lines = [f"[{n.get('id')} {n.get('kind') or 'profile'}] {n.get('text')}" + (f"  #{' #'.join(n['tags'])}" if n.get("tags") else "")
                 + f"  ({datetime.fromtimestamp(n.get('ts', 0) / 1000, TZ8).strftime('%Y-%m-%d')})" for n in ns]
        return {"content": [{"type": "text", "text": "\n".join(lines) or "没有相关记忆"}]}

    @tool("memory_forget", "Delete one memory by id, only when he asks you to forget it or it is plainly wrong or outdated.",
          {"type": "object", "properties": {"id": {"type": "integer"}}, "required": ["id"]})
    async def memory_forget(args: dict) -> dict:
        await emit({"type": "memory_forget", "id": int(args.get("id") or 0)})
        return {"content": [{"type": "text", "text": f"已删除 #{int(args.get('id') or 0)}"}]}

    return create_sdk_mcp_server(name="memory", version="1.0.0", tools=[memory_save, memory_search, memory_forget])


def notes_block(notes: Any) -> str:
    """Memory for this turn: {profile: [...], recalled: [...]} from Go (a plain list = profile, older Go)."""
    def line(n: dict) -> str:
        kind = n.get("kind") or "profile"
        tags = f"  #{' #'.join(n['tags'])}" if n.get("tags") else ""
        return f"[{n.get('id')}{'' if kind == 'profile' else ' ' + kind}] {n.get('text')}{tags}"
    if isinstance(notes, dict):
        profile, recalled = notes.get("profile") or [], notes.get("recalled") or []
    else:
        profile, recalled = (notes if isinstance(notes, list) else []), []
    p = [line(n) for n in profile if isinstance(n, dict) and n.get("text")]
    r = [line(n) for n in recalled if isinstance(n, dict) and n.get("text")]
    return ("\n\n<profile>\n" + ("\n".join(p) or "(empty)") + "\n</profile>\n<recalled>\n"
            + ("\n".join(r) or "(nothing relevant to this message)") + "\n</recalled>\n")


def build_chat_options(config: dict, proxy: str, chat: str, session: str, notes: Any = None, emit: Emit | None = None):
    from claude_agent_sdk import ClaudeAgentOptions

    agent = config.get("agent") or {}
    env = llm.build_env(config.get("credential"), proxy)
    prompt, _ = llm.load_prompt(CHAT_PROMPT)
    prompt += notes_block(notes)
    mcp: dict[str, Any] = {"sandbox": sandbox_server(chat, agent.get("sandboxToken", ""))}
    web = [] if config.get("noWeb") else ["WebSearch", "WebFetch"]
    allowed = ["mcp__sandbox__shell", *web, "Skill"]
    # ctg's own API on the same loopback host as the MCP endpoint (other conversations, memory search)
    base = agent["mcpUrl"].split("/mcp")[0] if agent.get("mcpUrl") else ""
    if emit is not None:
        mcp["memory"] = memory_server(emit, base, agent.get("sessionsToken", ""))
        allowed += ["mcp__memory__memory_save", "mcp__memory__memory_search", "mcp__memory__memory_forget"]
    ctg_tools: list[str] = []
    if agent.get("mcpUrl"):
        mcp["sessions"] = sessions_server(base, chat, agent.get("sessionsToken", ""))
        allowed += ["mcp__sessions__list_chats", "mcp__sessions__read_chat"]
    if agent.get("mcpUrl") and agent.get("mcpToken"):
        # X-Ctg-Chat links a propose_trade call to this conversation (honoured for the owner key only)
        mcp["ctg"] = {"type": "http", "url": agent["mcpUrl"], "headers": {"Authorization": "Bearer " + agent["mcpToken"], "X-Ctg-Chat": chat}}
        ctg_tools = [f"mcp__ctg__{t}" for t in CTG_DIRECT]
        allowed += ctg_tools
    env["MAX_MCP_OUTPUT_TOKENS"] = MCP_OUTPUT_TOKENS
    kw: dict[str, Any] = dict(
        system_prompt=prompt,
        model=config.get("model") or llm.DEFAULT_MODEL,
        tools=[*web, "Skill"],  # one agent argues both sides itself: no subagents
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
        # newer models omit thinking text by default (signature only): ask for the summary so the owner
        # sees the reasoning stream instead of a long silent wait before the first tool call or answer
        thinking={"type": "adaptive", "display": "summarized"},
    )
    if session:
        kw["resume"] = session
    if config.get("effort") == "off":
        kw["thinking"] = {"type": "disabled"}
    elif config.get("effort"):
        kw["effort"] = config["effort"]
    return ClaudeAgentOptions(**kw)


class Inbox:
    """Further user messages for a running turn (steers). put() refuses once the turn has ended, so a
    message is either answered in this call or the caller (Go) retries it as a new turn."""

    def __init__(self) -> None:
        self._q: asyncio.Queue = asyncio.Queue()
        self.closed = False

    def put(self, text: str) -> bool:
        if self.closed or not text.strip():
            return False
        self._q.put_nowait(text)
        return True

    def close(self) -> None:
        if not self.closed:
            self.closed = True
            self._q.put_nowait(None)  # wakes the prompt stream after the messages already queued

    async def get(self) -> str | None:
        return await self._q.get()


def user_message(text: str) -> dict:
    return {"type": "user", "message": {"role": "user", "content": text}, "parent_tool_use_id": None, "session_id": ""}


async def prompt_stream(first: str, inbox: Inbox):
    """The turn's input: the question, then every steer until the inbox closes. The SDK writes each to
    the CLI as it comes and keeps the run open until every one written has its result."""
    yield user_message(first)
    while True:
        text = await inbox.get()
        if text is None:
            return
        yield user_message(text)


async def chat(msg: dict, config: dict, proxy: str, emit: Emit, inbox: Inbox | None = None) -> dict:
    """Run one user turn; streams events through emit and returns the final result. Messages put into
    inbox while it runs are injected into the session and answered in the same call."""
    from claude_agent_sdk import AssistantMessage, ResultMessage, ToolResultBlock, ToolUseBlock, UserMessage, query
    from claude_agent_sdk.types import StreamEvent

    chat_id = str(msg.get("chatId") or "")
    text = str(msg.get("message") or "")
    inst = str(msg.get("instId") or "")
    prompt = f"[当前页面: {inst}]\n{text}" if inst else text
    if msg.get("model") in CHAT_MODELS:
        config = {**config, "model": msg["model"]}
    # this turn's thinking effort and web search, picked in the app (unset: the configured ones)
    if msg.get("turnEffort") in CHAT_EFFORTS:
        config = {**config, "effort": msg["turnEffort"]}
    if msg.get("webSearch") is False:
        config = {**config, "noWeb": True}
    options = build_chat_options(config, proxy, chat_id, str(msg.get("sessionId") or ""), msg.get("notes"), emit)
    inbox = inbox or Inbox()

    # ordered: {"t":"text","v"} | {"t":"think","v","ms"} | {"t":"tool","id","name","label","input","ok","sub"}
    parts: list[dict] = []
    tools: dict[str, dict] = {}
    thinking: dict[int, tuple[dict, float]] = {}  # open thinking blocks by stream index
    started = time.monotonic()
    result = None
    cost, turns = 0.0, 0
    usage = {"input": 0, "output": 0, "cacheWrite": 0, "cacheRead": 0, "searches": 0}

    def text_part() -> dict:
        if not parts or parts[-1]["t"] != "text":
            parts.append({"t": "text", "v": ""})
        return parts[-1]

    try:
        async for m in query(prompt=prompt_stream(prompt, inbox), options=options):
            if isinstance(m, StreamEvent):
                if m.parent_tool_use_id:
                    continue  # subagent tokens: only their tool calls are shown
                ev = m.event or {}
                cb = ev.get("content_block") or {}
                if ev.get("type") == "content_block_start" and cb.get("type") == "thinking":
                    p = {"t": "think", "v": "", "ms": 0}
                    parts.append(p)
                    thinking[ev.get("index", -1)] = (p, time.monotonic())
                    await emit({"type": "think_start"})
                elif ev.get("type") == "content_block_delta" and (ev.get("delta") or {}).get("type") == "thinking_delta":
                    d = ev["delta"].get("thinking", "")
                    open_ = thinking.get(ev.get("index", -1))
                    if d and open_:
                        open_[0]["v"] += d
                        await emit({"type": "think", "delta": d})
                elif ev.get("type") == "content_block_stop" and ev.get("index", -1) in thinking:
                    p, t0 = thinking.pop(ev.get("index", -1))
                    p["ms"] = int((time.monotonic() - t0) * 1000)
                    await emit({"type": "think_end", "ms": p["ms"]})
                elif ev.get("type") == "content_block_delta" and (ev.get("delta") or {}).get("type") == "text_delta":
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
                # the first result ends what can be steered: later steers are refused (Go starts a new
                # turn); those already queued are still written and answered before the call ends
                inbox.close()
                result = m
                cost += float(m.total_cost_usd or 0)
                # tokens, as the app shows them (影库 has the same shape)
                u = m.usage or {}
                usage["input"] += int(u.get("input_tokens") or 0)
                usage["output"] += int(u.get("output_tokens") or 0)
                usage["cacheWrite"] += int(u.get("cache_creation_input_tokens") or 0)
                usage["cacheRead"] += int(u.get("cache_read_input_tokens") or 0)
                usage["searches"] += int((u.get("server_tool_use") or {}).get("web_search_requests") or 0)
                turns += int(m.num_turns or 0)
    except Exception as e:  # noqa: BLE001
        inbox.close()
        if result is None:
            raise llm.CallError(f"{type(e).__name__}: {e}", network=llm.looks_network(str(e))) from e
    finally:
        inbox.close()
    if result is None:
        raise llm.CallError("no result from the model")
    if result.is_error or result.subtype != "success":
        detail = "; ".join(result.errors or []) or (result.result or "")
        raise llm.CallError(f"model call failed: {result.subtype} {detail}".strip(), network=llm.looks_network(detail))
    for p in parts:
        if p["t"] == "tool" and p["ok"] is None:
            p["ok"] = True
    parts = [p for p in parts if p["t"] != "think" or p["v"].strip()]  # empty (omitted) thinking blocks
    final = "\n\n".join(p["v"].strip() for p in parts if p["t"] == "text" and p["v"].strip()) or (result.result or "")
    return {
        "text": final, "sessionId": result.session_id, "cost": cost, "usage": usage,
        "steps": parts, "turns": turns, "latencyMs": int((time.monotonic() - started) * 1000),
    }
