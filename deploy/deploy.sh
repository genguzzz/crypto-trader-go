#!/usr/bin/env bash
# Build locally and hot-swap the binary on the server (no image build).
#
#   deploy/deploy.sh              build web + linux/amd64 binary, upload, swap, restart, health-check
#   deploy/deploy.sh --skip-build upload the existing bin/ctg-linux-amd64
#   deploy/deploy.sh --init       first-time setup: dirs, compose file, data ownership, migrate from systemd
#   deploy/deploy.sh --rollback   restore the previous binary (bin/ctg.prev) and restart
#   deploy/deploy.sh --status     container state, health and last log lines
#
# Env: CTG_HOST (default: ssh alias "greencloud" from ~/.ssh/config; CI passes a secret), CTG_DIR (default /opt/ctg), CTG_ARCH (default amd64)
set -euo pipefail
HOST=${CTG_HOST:-greencloud}
DIR=${CTG_DIR:-/opt/ctg}
ARCH=${CTG_ARCH:-amd64}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin/ctg-linux-$ARCH"

SSH_OPTS=${CTG_SSH_OPTS:-}
remote() { ssh -o BatchMode=yes $SSH_OPTS "$HOST" "$@" 2> >(grep -v -E 'post-quantum|store now|upgraded' >&2); }

# The API answers within seconds of start; phase becomes "live" after the gap fill (~1 min).
wait_healthy() {
  remote "for i in \$(seq 1 60); do
      if s=\$(curl -fsS -m 3 http://127.0.0.1:8686/api/v1/status 2>/dev/null); then
        echo \"  \$(echo \"\$s\" | grep -o '\"phase\":\"[a-z0-9-]*\"')\"; exit 0; fi; sleep 2; done; exit 1"
}

case "${1:-}" in
  --status)
    remote "cd $DIR && docker compose ps && docker compose logs --tail 15 ctg"; exit ;;
  --rollback)
    remote "set -e; cd $DIR; test -f bin/ctg.prev; mv bin/ctg bin/ctg.bad; mv bin/ctg.prev bin/ctg; docker compose restart ctg >/dev/null"
    wait_healthy && echo "✓ rolled back"; exit ;;
  --init)
    remote "set -e; mkdir -p $DIR/bin $DIR/data
      [ -f $DIR/ctg.env ] || { umask 077; : > $DIR/ctg.env; }
      chmod 600 $DIR/ctg.env
      if systemctl is-enabled ctg >/dev/null 2>&1; then   # migrate from the earlier systemd deployment
        systemctl disable --now ctg; rm -f /etc/systemd/system/ctg.service; systemctl daemon-reload
        if [ -x $DIR/ctg ] && [ ! -f $DIR/bin/ctg ]; then mv $DIR/ctg $DIR/bin/ctg; fi
        if id ctg >/dev/null 2>&1; then userdel ctg; fi
        echo '  migrated from systemd'
      fi
      chown -R 65532:65532 $DIR/data"
    scp -q $SSH_OPTS "$ROOT/deploy/docker-compose.yml" "$HOST:$DIR/docker-compose.yml"
    remote "cd $DIR && docker compose pull -q && if [ -f bin/ctg ]; then docker compose up -d; else echo '  no binary yet: run deploy/deploy.sh'; fi"
    if [ "$(remote "test -f $DIR/bin/ctg && echo y || true")" = y ]; then wait_healthy; fi
    exit ;;
  --skip-build|"") ;;
  *) sed -n '2,10p' "$0"; exit 1 ;;
esac

if [ "${1:-}" != --skip-build ]; then
  echo "▸ building web + linux/$ARCH binary"
  (cd "$ROOT/web" && npm run build >/dev/null)
  (cd "$ROOT" && GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$BIN" ./cmd/server)
fi
# Python sidecars (strategy runner, AI worker): upload their dirs and rebuild an image only when its
# Dockerfile/requirements changed. Strategy code is bind-mounted, so new strategies need no rebuild.
sync_sidecars() {
  echo "▸ syncing sidecars (strategies/, ai/) and compose file"
  tar -C "$ROOT" --exclude='__pycache__' --exclude='*.pyc' --exclude='tests' -czf - strategies ai deploy/docker-compose.yml |
    remote "set -e; cd $DIR; mkdir -p data/run; chown 65532:65532 data/run; rm -rf .sync && mkdir .sync && tar -xzf - -C .sync
      cp .sync/deploy/docker-compose.yml docker-compose.yml
      for d in strategies ai; do
        old=\$( [ -d \$d ] && cat \$d/Dockerfile \$d/requirements.txt 2>/dev/null | sha256sum | cut -c1-16 || echo none)
        rm -rf \$d && mv .sync/\$d \$d
        new=\$(cat \$d/Dockerfile \$d/requirements.txt 2>/dev/null | sha256sum | cut -c1-16)
        if [ \"\$old\" != \"\$new\" ] || ! docker image inspect ctg-\${d%ies}\$( [ \$d = strategies ] && echo y ):local >/dev/null 2>&1; then
          echo \"  building \$d image\"; docker compose build -q ctg-\$( [ \$d = strategies ] && echo strategy || echo ai )
        fi
      done
      rm -rf .sync"
}
sync_sidecars

echo "▸ uploading $(du -h "$BIN" | cut -f1) → server:$DIR/bin/ctg.new"
scp -q $SSH_OPTS "$BIN" "$HOST:$DIR/bin/ctg.new"
echo "▸ swapping binary and restarting container"
remote "set -e; cd $DIR; chmod 755 bin/ctg.new; if [ -f bin/ctg ]; then cp -p bin/ctg bin/ctg.prev; fi; mv bin/ctg.new bin/ctg
  rm -f bin/ctg.bad
  # up -d recreates services whose compose config or env_file changed; restart picks up the new binary.
  docker compose up -d >/dev/null 2>&1; docker compose restart ctg ctg-strategy ctg-ai >/dev/null 2>&1"
if wait_healthy; then
  echo "✓ deployed $(git -C "$ROOT" rev-parse --short HEAD)$(git -C "$ROOT" diff --quiet HEAD || echo '+dirty')"
else
  echo "✗ health check failed — rolling back"; "$0" --rollback; exit 1
fi
