import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import contextpack as cp  # noqa: E402

TS = 1_790_000_000_000  # 2026-09-21 ~ UTC


def sample_request():
    return {
        "signal": {"id": 7, "instId": "BTC-USDT-SWAP", "action": "open_long", "strength": 0.8, "price": 60000.0,
                   "stopLoss": 58800.0, "takeProfit": 63600.0, "ttlMs": 300000, "leverage": 3, "reason": "ema 20/50 cross | up",
                   "ts": TS},
        "account": {"id": 2, "name": "paper-1", "mode": "live", "equity": 10250.5, "cash": 8000, "dayPnl": -200, "halted": False,
                    "openCount": 1,
                    "limits": {"maxPositions": 5, "maxLeverage": 5, "riskPct": 0.01, "maxInstPct": 0.3, "dailyLossPct": 0.05,
                               "cooldownMin": 30, "requireStop": True, "takerFeeBp": 5}},
        "instance": {"id": 3, "configVersion": 2, "exec": "ai", "params": {"fast": 20, "slow": 50}},
        "strategy": {"id": "abc123", "name": "majors-trend", "timeframe": "4H", "description": "Trend following on majors.",
                     "params": [{"name": "fast", "default": 20, "min": 5, "max": 100, "aiTunable": True, "desc": "fast EMA"},
                                {"name": "slow", "default": 50}]},
        "positions": [{"instId": "ETH-USDT-SWAP", "posSide": "long", "qty": 10, "ctVal": 0.1, "entryPx": 2500, "markPx": 2550,
                       "unrealizedPnl": 50, "leverage": 3, "liqPx": 1700, "stopLoss": 2400, "openedAt": TS - 3600_000}],
        "market": {
            "funding": {"rate": 0.0001, "nextTs": TS + 3600_000},
            "factors": {"rsi14": 61.234567, "atr14Pct": 0.021, "missing": None},
            "bars4H": [[TS - i * 14_400_000, 60000 - i, 60100 - i, 59900 - i, 60050 - i, 1234.5, 7.4e7] for i in range(40)][::-1],
            "closes": list(range(100)),
            "rank": 1,
            "note": "line1\nline2",
        },
        "recent": [{"ts": TS - 86_400_000, "instId": "BTC-USDT-SWAP", "action": "open_long", "decision": "execute 1.0",
                    "result": "+1.2% in 6h"}],
        "memory": "## Rules of thumb\n- skip when stop < 0.5 ATR",
    }


class Formatting(unittest.TestCase):
    def test_num(self):
        self.assertEqual(cp.num(None), "n/a")
        self.assertEqual(cp.num(float("nan")), "n/a")
        self.assertEqual(cp.num(5), "5")
        self.assertEqual(cp.num(12345), "12,345")
        self.assertEqual(cp.num(60000.0), "60,000")
        self.assertEqual(cp.num(123.456), "123.46")
        self.assertEqual(cp.num(10250.5), "10,250.5")
        self.assertEqual(cp.num(0.000123456789), "0.000123457")
        self.assertEqual(cp.num(True), "True")

    def test_pct(self):
        self.assertEqual(cp.pct(-0.02), "-2.00%")
        self.assertEqual(cp.plain_pct(0.01), "1.00%")
        self.assertEqual(cp.pct(None), "n/a")

    def test_text_is_one_table_safe_line(self):
        self.assertEqual(cp.text("a |b\n c"), "a /b c")
        self.assertTrue(cp.text("x" * 1000).endswith("…"))

    def test_ts_in_utc8(self):
        self.assertEqual(cp.ts(0), "n/a")
        self.assertEqual(cp.ts(1_790_000_000_000), "09-21 22:13")


class ReviewPack(unittest.TestCase):
    def setUp(self):
        self.pack = cp.render_review(sample_request())

    def test_sections_in_order(self):
        order = ["# Signal to review", "# Market context", "# Account", "# Open positions", "# Strategy",
                 "# Recent decisions", "# Strategy experience notes"]
        idx = [self.pack.index(s) for s in order]
        self.assertEqual(idx, sorted(idx))

    def test_signal_risk_numbers(self):
        self.assertIn("**BTC-USDT-SWAP**", self.pack)
        self.assertIn("stop 58,800 (-2.00% from price, 1R = 1200)", self.pack)
        self.assertIn("target 63,600 (+6.00% from price, 3.00R)", self.pack)
        self.assertIn("TTL 300 s", self.pack)
        self.assertIn("ema 20/50 cross / up", self.pack)  # pipes cannot break tables

    def test_account_and_limits(self):
        self.assertIn("equity 10,250.5 USDT", self.pack)
        self.assertIn("risk per trade 1.00%", self.pack)
        self.assertIn("daily loss budget used: 39%", self.pack)
        self.assertIn("open positions 1/5", self.pack)

    def test_positions_table(self):
        self.assertIn("| ETH-USDT-SWAP | long | 10 | 2550 | 2500 | 2550 | 50 |", self.pack)

    def test_strategy_params(self):
        self.assertIn("| fast | 20 | 20 | 5 … 100 | yes | fast EMA |", self.pack)
        self.assertIn("| slow | 50 | 50 | -∞ … ∞ | no |", self.pack)

    def test_market_is_tabular_not_json(self):
        self.assertNotIn("{", self.pack.split("# Market context")[1].split("# Account")[0])
        self.assertIn("| factors.missing | n/a |", self.pack)
        self.assertIn("| factors.rsi14 | 61.2346 |", self.pack)
        self.assertIn("**bars4H** (last 30 of 40)", self.pack)
        self.assertIn("| ts | o | h | l | c | vol | volq |", self.pack)
        self.assertIn("(last 24 of 100, oldest first)", self.pack)
        self.assertIn("| funding.nextTs | 09-21 23:13 |", self.pack)
        self.assertIn("| note | line1 line2 |", self.pack)

    def test_recent_and_memory(self):
        self.assertIn("| +1.2% in 6h |", self.pack)
        self.assertIn("skip when stop < 0.5 ATR", self.pack)

    def test_missing_everything(self):
        pack = cp.render_review({})
        self.assertIn("stop: none", pack)
        self.assertIn("(no market context provided)", pack)
        self.assertIn("(none yet)", pack)

    def test_size_cap(self):
        req = sample_request()
        req["market"] = {f"k{i}": "x" * 200 for i in range(2000)}
        pack = cp.render_review(req)
        self.assertLess(len(pack), cp.MAX_PACK + 1000)
        self.assertIn("market context truncated", pack)
        self.assertIn("# Strategy experience notes", pack)


class ReflectPack(unittest.TestCase):
    def test_render(self):
        pack = cp.render_reflect({
            "strategy": "majors-trend", "timeframe": "4H", "description": "d", "currentVersion": 2, "current": "old notes",
            "decisions": [
                {"id": 1, "ts": TS, "instId": "BTC-USDT-SWAP", "signalAction": "open_long", "price": 60000, "action": "execute",
                 "sizeScale": 1, "confidence": 0.7, "signalReason": "cross", "rationale": "ok", "outcomes": [{"result": "stopped -0.8%"}]},
                {"id": 2, "ts": TS, "instId": "ETH-USDT-SWAP", "signalAction": "open_short", "action": "skip", "sizeScale": 0,
                 "confidence": 0.6, "rationale": "funding"},
            ]})
        self.assertIn("version 2", pack)
        self.assertIn("old notes", pack)
        self.assertIn("2 total, 1 executed, 1 skipped", pack)
        self.assertIn("| stopped -0.8% |", pack)
        self.assertIn("| funding | n/a |", pack)


if __name__ == "__main__":
    unittest.main()
