---
title: 发通知
summary: 用 ctx.notify 把消息推到 Telegram 或 Webhook：在哪里配置通道、消息长什么样、怎么校验 Webhook 签名、频率限制，以及回测和试运行里的行为。
---

# 发通知

`ctx.notify` 让策略主动给你发消息，比如“入场了”“BTC 快到止损了”“观察名单里出现了 SOL”。

```python
ctx.notify("BTC 开多 @ 65000：EMA20 上穿 EMA50")
ctx.notify("ETH 波动异常放大", level="warn", atr=152.3, ratio=2.4)
```

| 参数 | 说明 |
|---|---|
| `text` | 消息正文，可以有换行。最多 1000 个字符（汉字也按 1 个算），超出截断 |
| `level` | `info`（默认）、`warn`、`error`，影响标题 |
| `**data` | 任意附加字段：Telegram 里每个字段一行 `键: 值`，Webhook 里放在 JSON 的 `data` 中。值要能转成 JSON，否则会转成字符串 |

订单层自己也会推送开仓、成交、止损、熔断等事件，**不需要策略再发一遍**。`ctx.notify` 适合发订单层不知道的东西：策略的判断、观察到的异常、每天的小结。

## 配置通道

在 **设置页 → 交互通道** 配置：

- **Telegram**：填专用机器人的 Bot Token 和允许的用户 ID，启用。
- **Webhook**：填接收地址（`http://` 或 `https://`），可选填一个签名密钥。
- **推送的事件**：确认勾选了 **策略消息**，否则策略发的消息只记录不推送。Webhook 也可以单独指定要接收的事件。

## 消息长什么样

**Telegram**：

```
🔵 [模拟·我的账户] 策略消息 · BTC 趋势
BTC 开多 @ 65000：EMA20 上穿 EMA50
price: 65000
10-02 16:00:05 UTC+8 · inst-12
```

- 标题是 `策略消息 · <实例名>`；`level` 为 `warn` / `error` 时变成 `策略警告 · …` / `策略错误 · …`。
- 附加字段按键名排序，每个一行。
- 一条消息最多显示 10 行，每行 160 字，多出的会被省略。

**Webhook**：每条消息是一次 `POST`，`Content-Type: application/json`，请求头 `X-CTG-Event: strategy`。正文：

```json
{
  "type": "event",
  "kind": "strategy",
  "title": "策略消息 · BTC 趋势",
  "lines": ["BTC 开多 @ 65000：EMA20 上穿 EMA50", "price: 65000"],
  "account": "我的账户",
  "mode": "live",
  "ref": "inst-12",
  "data": {
    "instance": 12,
    "strategy": "majors-trend",
    "level": "info",
    "text": "BTC 开多 @ 65000：EMA20 上穿 EMA50",
    "data": {"price": 65000}
  },
  "ts": 1790928005000
}
```

程序处理时用 `data` 里的字段（`data.text` 是原文，`data.data` 是你传的附加字段），`lines` 是给人看的。订单层的其他事件（成交、止损等）也走同一个 Webhook，`kind` 不同，可以按 `kind` 分流。

接收方返回 2xx 视为成功；返回 4xx（408、429 除外）视为永久失败、丢弃这条；其他失败（超时、5xx 等）会间隔递增地重试。

## 校验 Webhook 签名

填了签名密钥后，每个请求都带一个请求头：

```
X-CTG-Signature: sha256=<十六进制>
```

它是用密钥对**原始请求体**做 HMAC-SHA256 的结果。接收方用同样的方法算一遍，对得上才说明消息确实来自本系统、没被篡改：

```python
import hashlib
import hmac

SECRET = b"你在设置里填的密钥"


def verify(body: bytes, header: str) -> bool:
    """body 是原始请求体字节，不要先解析成 JSON 再序列化。"""
    want = "sha256=" + hmac.new(SECRET, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, header or "")


# Flask 示例
# @app.post("/ctg")
# def ctg():
#     if not verify(request.get_data(), request.headers.get("X-CTG-Signature")):
#         abort(401)
#     msg = request.get_json()
#     if msg["kind"] == "strategy":
#         print(msg["data"]["text"], msg["data"]["data"])
#     return "ok"
```

要点：一定用**原始字节**计算（重新序列化的 JSON 字节可能不同）；用 `compare_digest` 比较，避免时间侧信道。

## 频率限制

为了防止一个写错的循环把手机轰炸掉，每个实例：

| 规则 | 说明 |
|---|---|
| 每小时最多 30 条 | 超出的只写进运行日志，标注 `未发送：每小时最多 30 条` |
| 相同文字 10 分钟内只发一次 | 重复的标注 `未发送：10 分钟内重复的消息` |

所以消息里可以带变化的内容（价格、时间），但不要在每根 1m K 线都发同样的话。需要定期汇报时，用 `schedule` 定时器每小时或每天发一次。

## 回测和试运行里

`ctx.notify` **只在实时账户真正推送**。回测和试运行里，消息只写进运行日志（类型是通知），不发到 Telegram / Webhook，也不计入频率限制。所以可以放心地在回测里保留通知代码，在试运行结果的日志里检查消息内容是否符合预期。

如果想让某条消息只在实时运行时生成（比如计算量大的日报），可以判断：

```python
if ctx.mode == "live":
    ctx.notify(self._daily_report(ctx))
```

## 例子：每天早上一条小结

```python
class Daily(Strategy):
    schedule = {"daily": "1D"}            # 每天 UTC 0 点（北京时间 8 点）

    def on_timer(self, ctx, name):
        if name != "daily":
            return None
        acc = ctx.account()
        pos = ctx.positions()
        lines = [f"{p.inst_id.replace('-USDT-SWAP', '')} {'多' if p.pos_side == 'long' else '空'} "
                 f"浮盈 {p.unrealized_pnl:+.1f}" for p in pos]
        ctx.notify(f"权益 {acc.equity:.0f} USDT，持仓 {len(pos)} 个\n" + "\n".join(lines))
```

下一篇：[试运行与回测](/help/strategy/backtest-trial)。
