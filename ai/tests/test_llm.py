import asyncio
import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import fakesdk  # noqa: E402
import llm  # noqa: E402
import worker  # noqa: E402

PROXY = "http://127.0.0.1:17080"
KEY = "sk-ant-api03-SECRETSECRETSECRET"
CONFIG = {"credential": {"kind": "api_key", "value": KEY}, "model": "claude-sonnet-5"}
REQUEST = {"signal": {"instId": "BTC-USDT-SWAP", "action": "open_long", "price": 100, "stopLoss": 95},
           "strategy": {"name": "majors-trend", "params": []}}


def run(coro):
    return asyncio.run(coro)


class Env(unittest.TestCase):
    def test_proxy_and_credential(self):
        env = llm.build_env(CONFIG["credential"], PROXY)
        for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"):
            self.assertEqual(env[k], PROXY)
        self.assertEqual(env["NO_PROXY"], "127.0.0.1,localhost")
        self.assertEqual(env["ANTHROPIC_API_KEY"], KEY)
        self.assertNotIn("CLAUDE_CODE_OAUTH_TOKEN", env)
        env = llm.build_env({"kind": "oauth_token", "value": "sk-ant-oat01-x"}, PROXY)
        self.assertEqual(env["CLAUDE_CODE_OAUTH_TOKEN"], "sk-ant-oat01-x")
        self.assertNotIn("ANTHROPIC_API_KEY", env)

    def test_refuses_without_proxy_or_credential(self):
        with self.assertRaisesRegex(llm.CallError, "no proxy"):
            llm.build_env(CONFIG["credential"], "")
        with self.assertRaisesRegex(llm.CallError, "no credential"):
            llm.build_env(None, PROXY)
        with self.assertRaisesRegex(llm.CallError, "kind"):
            llm.build_env({"kind": "cookie", "value": "x"}, PROXY)

    def test_prepare_environment(self):
        with mock.patch.dict(os.environ, {"ANTHROPIC_API_KEY": "leak", "CLAUDE_CODE_OAUTH_TOKEN": "leak", "HOME": "/dev/null/home"}, clear=False):
            worker.prepare_environment(PROXY)
            self.assertNotIn("ANTHROPIC_API_KEY", os.environ)
            self.assertNotIn("CLAUDE_CODE_OAUTH_TOKEN", os.environ)
            self.assertEqual(os.environ["HTTPS_PROXY"], PROXY)
            self.assertEqual(os.environ["http_proxy"], PROXY)
            self.assertEqual(os.environ["HOME"], "/tmp")
        with mock.patch.dict(os.environ, {"HTTPS_PROXY": "http://elsewhere:1"}, clear=False):
            worker.prepare_environment("")
            self.assertNotIn("HTTPS_PROXY", os.environ)


class Calls(unittest.TestCase):
    def setUp(self):
        self.sdk = fakesdk.install()

    def test_review_options_and_mapping(self):
        self.sdk.messages = [
            fakesdk.AssistantMessage(content=[fakesdk.ToolUseBlock("t1", "StructuredOutput", {"action": "execute"})],
                                     model="claude-sonnet-5-20260901"),
            fakesdk.ResultMessage(structured_output={"action": "execute", "size_scale": 0.5, "stop_loss": 96, "confidence": 0.7,
                                                     "rationale": "趋势确认", "param_change": {"fast": 30}}),
        ]
        decision, meta = run(llm.review(REQUEST, CONFIG, PROXY))
        self.assertEqual(decision, {"action": "execute", "sizeScale": 0.5, "stopLoss": 96.0, "takeProfit": 0.0, "confidence": 0.7,
                                    "rationale": "趋势确认", "paramChange": {"fast": 30.0}, "model": "claude-sonnet-5-20260901",
                                    "costUsd": 0.0123, "latencyMs": meta["latencyMs"]})
        self.assertEqual(meta["promptVersion"], "review_v1")
        self.assertEqual(len(meta["promptSha"]), 12)
        self.assertEqual(meta["toolCalls"][0]["name"], "StructuredOutput")
        call = self.sdk.calls[0]
        opts = call["options"].kw
        self.assertEqual(opts["tools"], [])
        self.assertEqual(opts["permission_mode"], "dontAsk")
        self.assertEqual(opts["setting_sources"], [])
        self.assertEqual(opts["mcp_servers"], {})
        self.assertEqual(opts["model"], "claude-sonnet-5")
        self.assertEqual(opts["output_format"]["type"], "json_schema")
        self.assertEqual(opts["output_format"]["schema"]["properties"]["action"]["enum"], ["execute", "skip"])
        self.assertIn("no-session-persistence", opts["extra_args"])
        self.assertEqual(opts["env"]["HTTPS_PROXY"], PROXY)
        self.assertEqual(opts["env"]["https_proxy"], PROXY)
        self.assertEqual(opts["env"]["ANTHROPIC_API_KEY"], KEY)
        self.assertIn("risk-aware reviewer", opts["system_prompt"])
        self.assertIn("# Signal to review", call["prompt"])
        self.assertIn("**BTC-USDT-SWAP**", call["prompt"])

    def test_review_without_structured_output_fails(self):
        self.sdk.messages = [fakesdk.ResultMessage(structured_output=None, result="I think...")]
        with self.assertRaisesRegex(llm.CallError, "no valid structured decision"):
            run(llm.review(REQUEST, CONFIG, PROXY))

    def test_error_result(self):
        self.sdk.messages = [fakesdk.ResultMessage(subtype="error_during_execution", is_error=True, errors=["Connection error."],
                                                   api_error_status=None)]
        with self.assertRaises(llm.CallError) as cm:
            run(llm.review(REQUEST, CONFIG, PROXY))
        self.assertTrue(cm.exception.network)

    def test_sdk_exception_before_result(self):
        self.sdk.raise_exc = RuntimeError("CLI connection failed: ECONNREFUSED")
        with self.assertRaises(llm.CallError) as cm:
            run(llm.review(REQUEST, CONFIG, PROXY))
        self.assertTrue(cm.exception.network)

    def test_reflect_and_test(self):
        self.sdk.messages = [fakesdk.ResultMessage(structured_output={"memory": "## Record\n- 2 decisions", "changes": "first"})]
        out = run(llm.reflect({"strategy": "s", "decisions": []}, CONFIG, PROXY))
        self.assertEqual(out["memory"], "## Record\n- 2 decisions")
        self.assertEqual(out["promptVersion"], "reflect_v1")
        self.assertEqual(self.sdk.calls[0]["options"].kw["output_format"]["schema"], llm.MEMORY_SCHEMA)

        self.sdk.messages = [fakesdk.AssistantMessage(content=[fakesdk.TextBlock("OK")], model="m"), fakesdk.ResultMessage()]
        out = run(llm.test(CONFIG, PROXY))
        self.assertEqual(out["reply"], "OK")
        opts = self.sdk.calls[1]["options"].kw
        self.assertNotIn("output_format", opts)
        self.assertEqual(opts["env"]["HTTP_PROXY"], PROXY)

    def test_prompts_exist(self):
        for name in (llm.REVIEW_PROMPT, llm.REFLECT_PROMPT):
            body, sha = llm.load_prompt(name)
            self.assertIn("StructuredOutput", body)
            self.assertEqual(len(sha), 12)


if __name__ == "__main__":
    unittest.main()
