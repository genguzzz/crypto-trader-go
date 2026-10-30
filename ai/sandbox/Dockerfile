# Sandbox for the chat agent's shell tool. Build context: ai/sandbox/.
# No credentials live here: the worker sends a read-only, in-memory ctg data key with each request.
# Compose runs it on its own bridge network (internet for OKX / news, no host loopback), read-only
# root, tmpfs /work and /tmp, uid 65532, small memory/pid limits.
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1
RUN pip install numpy pandas \
 && apt-get update && apt-get install -y --no-install-recommends curl jq bc && rm -rf /var/lib/apt/lists/*
COPY crypto-analyzer /opt/ca/crypto-analyzer
RUN mkdir -p /opt/ca/bin && printf '#!/bin/sh\nexec python3 /opt/ca/crypto-analyzer/crypto.py "$@"\n' > /opt/ca/bin/ca && chmod 755 /opt/ca/bin/ca
COPY server.py /app/server.py
USER 65532:65532
CMD ["python", "-u", "/app/server.py"]
