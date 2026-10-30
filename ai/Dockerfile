# AI worker (Claude Agent SDK). Build context: ai/.
# The claude-agent-sdk wheel bundles the Claude Code CLI binary, so no Node.js is needed.
# Runs as uid 65532 with network_mode: host (the proxy in CTG_AI_PROXY listens on the host loopback);
# the only other I/O is the Unix socket in $CTG_RUN_DIR shared with the ctg container, the chat
# agent's sandbox socket in /run/sbx and its session transcripts under $HOME (a volume in compose).
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

COPY requirements.txt /tmp/requirements.txt
RUN pip install -r /tmp/requirements.txt && rm /tmp/requirements.txt

# HOME must be writable (the CLI keeps small state files there); /tmp is a tmpfs in compose.
ENV CTG_RUN_DIR=/run/ctg \
    HOME=/tmp/ctg-ai-home \
    CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK=1

WORKDIR /app
COPY --chown=0:0 worker.py llm.py contextpack.py agent.py /app/
COPY --chown=0:0 prompts /app/prompts
COPY --chown=0:0 agent /app/agent

USER 65532:65532
CMD ["python", "-u", "worker.py"]
