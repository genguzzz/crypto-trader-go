"""Steering: a message for a running chat turn is injected into it and answered in the same call."""

import asyncio
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import fakesdk  # noqa: E402

fakesdk.install()

import agent  # noqa: E402
import worker  # noqa: E402
from test_worker import CONFIG, Harness, arun, gate  # noqa: E402

CHAT_CONFIG = {"credential": {"kind": "api_key", "value": "sk-ant-api03-x"}, "model": "claude-sonnet-5", "agent": {}}


async def no_emit(ev):
    return None


class AgentSteer(unittest.TestCase):
    def setUp(self):
        self.sdk = fakesdk.install()

    def test_message_injected_into_running_turn(self):
        inbox = agent.Inbox()
        accepted = []
        self.sdk.messages = [
            fakesdk.text_delta("先看看仓位。"),
            lambda: accepted.append(inbox.put("✅ #3 已成交 · 开仓做多 BTC")),  # arrives mid-turn
            fakesdk.ResultMessage(total_cost_usd=0.02, num_turns=2),
        ]
        self.sdk.followups = [[fakesdk.text_delta("止盈止损已核对。"), fakesdk.ResultMessage(total_cost_usd=0.01, num_turns=1)]]
        out = asyncio.run(asyncio.wait_for(agent.chat({"chatId": 7, "message": "BTC 开多", "sessionId": "s1"}, CHAT_CONFIG, "http://p", no_emit, inbox), 5))
        self.assertEqual(accepted, [True])
        self.assertEqual(self.sdk.calls[0]["inputs"], ["BTC 开多", "✅ #3 已成交 · 开仓做多 BTC"])
        self.assertIn("先看看仓位", out["text"])
        self.assertIn("止盈止损已核对", out["text"])
        self.assertAlmostEqual(out["cost"], 0.03)
        self.assertEqual(out["turns"], 3)
        self.assertEqual(self.sdk.calls[0]["options"].kw["resume"], "s1")
        self.assertFalse(inbox.put("too late"), "a steer after the call must be refused")

    def test_steer_after_the_turn_ended_is_refused(self):
        inbox = agent.Inbox()
        late = []
        self.sdk.messages = [fakesdk.text_delta("答完了"), fakesdk.ResultMessage(), lambda: late.append(inbox.put("⚡ 信号 #2 触发"))]
        out = asyncio.run(asyncio.wait_for(agent.chat({"chatId": 7, "message": "hi"}, CHAT_CONFIG, "http://p", no_emit, inbox), 5))
        self.assertEqual(late, [False])  # Go keeps it queued and starts a new turn after this one
        self.assertEqual(self.sdk.calls[0]["inputs"], ["hi"])
        self.assertEqual(out["text"], "答完了")

    def test_labels(self):
        self.assertEqual(agent.label_for("mcp__ctg__propose_trade", {"kind": "add"}), "提议补仓")
        self.assertEqual(agent.label_for("mcp__ctg__propose_trade", {"kind": "reduce"}), "提议减仓")
        self.assertEqual(agent.label_for("mcp__ctg__propose_trade", {"kind": "close"}), "提议平仓")
        self.assertEqual(agent.label_for("mcp__ctg__propose_trade", {}), "提议开仓")
        self.assertEqual(agent.label_for("mcp__ctg__propose_trade"), "提交交易提议")
        self.assertEqual(agent.brief_input("mcp__ctg__propose_trade", {"kind": "reduce", "instId": "BTC-USDT-SWAP", "side": "long",
                                                                       "fraction": 0.5, "orderType": "limit", "price": 102500}),
                         "BTC 多 · 50% · 限价 @102500")
        self.assertEqual(agent.brief_input("mcp__ctg__propose_trade", {"instId": "SOL-USDT-SWAP", "side": "short", "marginMode": "cross", "sl": 98.5}),
                         "SOL 空 · 市价 · 全仓 · 止损 98.5")


class SteerCalls:
    """llm stand-in whose chat waits for a steer and answers with it."""

    async def chat(self, msg, config, proxy, emit, inbox):
        text = await asyncio.wait_for(inbox.get(), 5)
        inbox.close()
        return {"text": "收到：" + text, "sessionId": "s1", "cost": 0, "steps": []}


class WorkerSteer(unittest.TestCase):
    def test_steer_reaches_the_running_chat(self):
        async def go():
            g = gate()
            await g.check_now()
            async with Harness(g, SteerCalls()) as h:
                await h.recv_type("hello")
                h.send(CONFIG)
                h.send({"type": "steer", "id": "s0", "callId": "nope", "message": "x"})
                self.assertEqual((await h.recv_type("error"))["id"], "s0")
                h.send({"type": "chat", "id": "c1", "chatId": 7, "message": "BTC 开多"})
                await asyncio.sleep(0.05)
                h.send({"type": "steer", "id": "s1", "callId": "c1", "message": "✅ #3 已成交"})
                got = {}
                while len(got) < 2:
                    m = await h.recv()
                    if m.get("id") in ("s1", "c1"):
                        got[m["id"]] = m
                self.assertEqual(got["s1"]["type"], "result")
                self.assertEqual(got["c1"]["result"]["text"], "收到：✅ #3 已成交")
                h.send({"type": "steer", "id": "s2", "callId": "c1", "message": "late"})  # the call is over
                self.assertEqual((await h.recv_type("error"))["id"], "s2")
        arun(go())


if __name__ == "__main__":
    unittest.main()
