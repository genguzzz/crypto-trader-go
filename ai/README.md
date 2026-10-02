# AI worker

Optional AI review layer for paper-trading signals. The Go side (`internal/aigate`) owns the credential, the audit log, the per-strategy experience documents and parameter proposals; this Python worker only turns one request into one Claude call through the [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/python). Contract: [docs/trading-design.md](../docs/trading-design.md) §4–§6.

## How a review works

1. A strategy instance in `ai` exec mode emits a signal. The order layer validates it and calls `Reviewer.Review` with a deadline equal to the signal's TTL.
2. `aigate` attaches the latest experience document of the strategy and sends `{"type":"review",...}` over `$CTG_RUN_DIR/ai.sock`.
3. The worker renders the request as a compact Markdown context pack (`contextpack.py`: tables of numbers, not raw JSON) and runs **one stateless** `claude_agent_sdk.query` with the versioned system prompt `prompts/review_v1.md`. No session is kept between calls.
4. The model answers with one structured decision (JSON Schema output): `action` execute|skip, `size_scale` 0–1, optional stop/target, `confidence`, `rationale`, optional `param_change`.
5. `aigate` sanitizes it (clamps numbers, drops a stop on the wrong side, keeps only AI-tunable parameters within range), appends an audit row (full request + sha256, memory version, prompt version, model, decision, tool calls, cost, latency, error) and returns it to the order layer, which then applies risk limits and sizing as for any other signal.

Daily at 00:05 UTC+8, `aigate` asks the worker to *reflect*: the last decisions of each strategy with new activity and their recorded outcomes are distilled (`prompts/reflect_v1.md`) into a new version of the strategy's experience document. Versions are append-only; reviews always read the latest one.

## What the AI can and cannot do

Can:
- say execute or skip for one signal, and scale its size **down** (0–1 of the strategy's risk size);
- suggest a different stop or target (ignored when on the wrong side of the price);
- propose parameter changes for parameters the strategy marks `ai_tunable`, within their range. A proposal is stored as `pending` and does nothing until the owner approves it (`POST /api/v1/ai/proposals/{id}`).

Cannot:
- place, modify or close orders, or bypass any account limit (risk per trade, leverage, margin per instrument, max positions, daily loss breaker, cooldown, mandatory stop) — those are enforced by the Go order layer after the decision;
- use any tool: the CLI runs with `tools=[]`, no MCP servers, `permission_mode="dontAsk"`, no settings files and no session persistence; the structured answer is its only output;
- reach anything but the Anthropic API through the configured proxy.

When the worker is offline, not ready or has no credential, `Available()` is false and the order layer skips AI-mode signals.

## Credentials

Two kinds, set in the web UI (AI settings) or with `PUT /api/v1/ai/credential` (owner key required):

| kind | value | passed to the CLI as |
|---|---|---|
| `api_key` | an Anthropic API key from the Console | `ANTHROPIC_API_KEY` |
| `oauth_token` | a long-lived token the owner generated **on their own machine** with `claude setup-token` | `CLAUDE_CODE_OAUTH_TOKEN` |

There is no claude.ai login flow in ctg, by design: the Agent SDK terms do not allow third-party products to offer claude.ai login. The owner pastes a token they created themselves.

Storage: the credential is kept in the `settings` table, encrypted with AES-256-GCM when `CTG_SECRET_KEY` is set in ctg's environment (otherwise stored in clear, and `GET /api/v1/ai/status` says so). The API only ever returns a masked form (`••••` + last 4 characters); it is never logged, and errors coming back from the worker are scrubbed of it. The worker receives it in a `config` message, keeps it in memory and sets it in the CLI's environment per call; credentials in the worker's own environment are ignored.

Costs: every call's `total_cost_usd` (an estimate from the SDK; under an OAuth token it is notional) is recorded; `GET /api/v1/ai/status` shows today's total (UTC+8).

### Rotating the token or key

1. Create the new credential (Console → API keys, or run `claude setup-token` again).
2. Paste it in the AI settings page (or `PUT /api/v1/ai/credential {"kind":"oauth_token","value":"…"}`). ctg pushes it to the worker immediately; calls already running finish with the old one.
3. Press *Test* (`POST /api/v1/ai/test`) to make a minimal call.
4. Revoke the old credential where it was issued. `DELETE /api/v1/ai/credential` removes it from ctg entirely.

If `CTG_SECRET_KEY` changes, the stored credential can no longer be decrypted; the status shows that and you paste it again.

## Network: proxy only

All Anthropic traffic must go through the proxy in **`CTG_AI_PROXY`** (for example `http://127.0.0.1:17080` in production, a reverse SSH tunnel on the host loopback). The worker:

- refuses every call with `no proxy configured` when `CTG_AI_PROXY` is empty — there is no fallback to a direct connection;
- sets `HTTPS_PROXY`/`HTTP_PROXY`/`https_proxy`/`http_proxy` (and `NO_PROXY=127.0.0.1,localhost`) process-wide and again in each call's CLI environment;
- checks `GET https://api.anthropic.com/api/hello` through the proxy (expects HTTP 200, 10 s timeout) before declaring itself ready, every 60 s while healthy, every 10 s while failing and right after a call fails with a connection error. While failing it reports `ready:false` with the error, and `GET /api/v1/ai/status` shows `proxyOk` / `proxyError`.

## Environment

| variable | default | meaning |
|---|---|---|
| `CTG_AI_PROXY` | — (required) | HTTP proxy for all Anthropic traffic |
| `CTG_RUN_DIR` | `data/run` (`/run/ctg` in the image) | directory of `ai.sock`, shared with ctg |
| `CTG_AI_SOCKET` | `$CTG_RUN_DIR/ai.sock` | explicit socket path |
| `CTG_AI_LOG` | `INFO` | log level |
| `HOME` | `/tmp/ctg-ai-home` in the image | must be writable (CLI state) |

The model is chosen in ctg (`PUT /api/v1/ai/model`, default `claude-sonnet-5`) and sent in the `config` message.

## Running

Locally (Python ≥ 3.10):

```sh
python -m venv .venv && .venv/bin/pip install -r ai/requirements.txt
CTG_AI_PROXY=http://127.0.0.1:7080 CTG_RUN_DIR=data/run .venv/bin/python ai/worker.py
```

Docker: `docker build -t ctg-ai ai/`. The `claude-agent-sdk` wheel bundles the Claude Code CLI (no Node.js needed); the image is roughly 420 MB (python:3.12-slim ≈ 150 MB + the 230 MB CLI binary). In compose the `ctg-ai` service runs with `network_mode: host` (the proxy listens on the host loopback), uid 65532, the shared `run` volume at `/run/ctg`, a tmpfs `/tmp`, a read-only root filesystem and `CTG_AI_PROXY` set.

Tests (no SDK or network needed; the SDK is faked): `cd ai && python -m unittest discover -s tests`.
