# Alerts [监控告警](/alerts)

Left side: the alert rule list (enable toggle, cooldown, push channels, last trigger time, edit/delete). Right side: push channel settings and "Recent triggers" (最近触发). MCP `get_alert_events` reads recent trigger events (without delivery details).

## Rule types

| Type | Parameters | Condition |
|---|---|---|
| Price move (价格异动) | Window 15m / 1h / 4h / 24h; direction both / up / down (双向 / 涨 / 跌); change threshold % | **Simple** close-to-close change over the window ≥ threshold (from facts-panel ret_*) |
| Rule score (规则分数) | Scoring rule; long score / short score (多头分 / 空头分); score ≥ N (default 80) | That side's score for the chosen rule ≥ N (scores are unvalidated) |
| Funding (资金费) | Source settled / predicted (已结算 / 预测); sign positive / negative / ± (正 / 负 / ±); hourly funding threshold % | Funding converted to per hour (4h/8h already normalized) reaches the threshold; predicted is live-only |

Common settings: name, **scope** (active pool / watchlist / single contract — 活跃池 / 自选 / 单个合约; single contract takes the full instId), **cooldown** (minutes), push to Telegram and/or Webhook, enable toggle. If the chosen channel isn't configured, triggers only go to history (marked "log only", 仅记录).

## When and how it triggers

- **Check timing**: once per confirmed closed minute, using that minute's facts panel and rule scores. The 10-second intraday preview never triggers.
- **Edge-triggered**: fires once in the minute the condition goes from false to true; staying true doesn't repeat; falling back and becoming true again re-fires.
- **Cooldown**: minimum interval between two alerts for the same rule and contract; new triggers inside it are ignored.
- **Missing data**: if a required field is missing (new coin, data gap), the check is skipped, not counted as false. Rule scores are computed only for the active pool and watchlist, so score rules don't work for contracts outside the pool.
- **First minute**: after a rule is created/edited or the service restarts, the first minute only records state and sends nothing; contracts already meeting the condition fire only after falling back and meeting it again (avoids alerting the whole pool at once). So "my new rule didn't fire" is usually normal; to verify the channel works, use "Send test" (发送测试).

## Push channels

- **Telegram**: create a bot with @BotFather to get the Bot Token; send the bot a message first (for groups, add the bot to the group), then look up the Chat ID (negative for groups). Alerts in the same minute are merged into one message.
- **Webhook**: one POST per alert with JSON `{ruleId, rule, instId, type, value, message, ts}` (test messages include `test: true`); if a secret is set it goes in header `X-CTG-Secret` for the receiver to verify.
- Tokens, secrets and Webhook URLs are stored only on the server; the page shows the last 4 characters; saving blank = unchanged. Failed sends (network error / 429 / 5xx) retry 3 times.

## Recent triggers

Shows the latest 100 (server keeps 30 days, max 5000), auto-refreshing every closed minute. Delivery status: delivered / sending / failed (已送达 / 发送中 / 失败; hover or tap for the reason).

## Difference from other notifications

- Paper-trading manual confirmations, circuit-breaker events etc. go through the interaction channel ([设置](/settings) → Interaction channel (交互通道): Telegram bot + Pending page), not these alert rules.
- `ctx.notify` in strategy code also goes through the interaction channel/Webhook; see [发通知](/help/strategy/notify).
