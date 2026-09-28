#!/usr/bin/env bash
# Local development helper. Every checkout (the main clone or a worktree) runs its own server on
# its own port with its own database, so parallel work never shares state.
#
#   scripts/dev.sh restart            build web + binary, restart this checkout's server, wait until live
#   scripts/dev.sh stop               stop this checkout's server
#   scripts/dev.sh status             port, pid and health of this checkout's server
#   scripts/dev.sh shots [path] [dir] desktop + phone screenshots of this checkout's server (web/scripts/shot.mjs)
#   scripts/dev.sh wt-new <branch>    create ../ctg-<slug> worktree on a new branch from origin/main,
#                                     with npm deps, a copy of the main database and a free port
#   scripts/dev.sh wt-rm <branch>     stop its server, remove the worktree and the local branch
#
# Port: data/port if present, else 8686 (the main clone). GOMEMLIMIT defaults to 384MiB: the resident
# heap is ~180 MB and a limit near it makes the GC spin (see docs/operations.md).
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"
port() { cat data/port 2>/dev/null || echo 8686; }
pids() { pgrep -f "bin/ctg -addr 127.0.0.1:$(port) " || true; }
main_root() { git worktree list --porcelain | awk '/^worktree /{print $2; exit}'; }

stop() {
  local p; p=$(pids)
  if [ -n "$p" ]; then kill $p; while [ -n "$(pids)" ]; do sleep 0.5; done; echo "stopped :$(port)"; fi
}

restart() {
  [ -d web/node_modules ] || (cd web && npm ci --no-audit --no-fund)
  (cd web && npm run build >/dev/null)
  go build -o bin/ctg ./cmd/server
  stop
  mkdir -p data
  GOMEMLIMIT=${GOMEMLIMIT:-384MiB} nohup ./bin/ctg -addr "127.0.0.1:$(port)" -db data/ctg.db >data/server.log 2>&1 &
  echo "starting :$(port) (log: data/server.log)"
  for _ in $(seq 1 300); do
    grep -q 'bootstrap complete' data/server.log 2>/dev/null && break
    [ -n "$(pids)" ] || { tail -20 data/server.log; echo "server exited" >&2; exit 1; }
    sleep 2
  done
  curl -s "127.0.0.1:$(port)/api/v1/health"; echo
}

status() {
  echo "port $(port)  pid $(pids | tr '\n' ' ')"
  curl -s --max-time 3 "127.0.0.1:$(port)/api/v1/health" || echo "not responding"
  echo
}

shots() {
  node web/scripts/shot.mjs "http://127.0.0.1:$(port)/${1:-}" "${2:-data/shots}"
}

free_port() {
  local p
  for p in $(seq 8687 8699); do
    lsof -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1 && continue
    grep -qx "$p" "$(dirname "$(main_root)")"/ctg-*/data/port 2>/dev/null && continue
    echo "$p"; return
  done
  echo "no free port in 8687-8699" >&2; exit 1
}

wt_new() {
  local branch=${1:?branch, e.g. feat/5-i18n}
  local dir; dir="$(dirname "$(main_root)")/ctg-$(basename "$branch")"
  git fetch -q origin main
  git worktree add -b "$branch" "$dir" origin/main
  local p; p=$(free_port)
  mkdir -p "$dir/data"
  echo "$p" >"$dir/data/port"
  if [ -f "$(main_root)/data/ctg.db" ]; then
    sqlite3 "$(main_root)/data/ctg.db" ".backup '$dir/data/ctg.db'"   # consistent copy while the main server runs
  fi
  [ -d "$(main_root)/data/icons" ] && cp -R "$(main_root)/data/icons" "$dir/data/"
  (cd "$dir/web" && npm ci --no-audit --no-fund >/dev/null)
  echo "worktree $dir  branch $branch  port $p"
  echo "next: cd $dir && scripts/dev.sh restart"
}

wt_rm() {
  local branch=${1:?branch}
  local dir; dir="$(dirname "$(main_root)")/ctg-$(basename "$branch")"
  if [ -f "$dir/data/port" ]; then
    local p; p=$(cat "$dir/data/port")
    pkill -f "bin/ctg -addr 127.0.0.1:$p " || true
  fi
  git worktree remove --force "$dir"
  git branch -D "$branch" 2>/dev/null || true
  echo "removed $dir"
}

case "${1:-}" in
  restart) restart ;;
  stop) stop ;;
  status) status ;;
  shots) shift; shots "$@" ;;
  wt-new) shift; wt_new "$@" ;;
  wt-rm) shift; wt_rm "$@" ;;
  *) sed -n '2,15p' "$0"; exit 2 ;;
esac
