#!/bin/sh
# Publish a JS-only over-the-air update of the iOS client (~1 min):
#   mobile/scripts/ota.sh "修复：xxx"
# Exports the iOS bundle and copies it over ssh into the ctg data dir
# (ctg-prod:/opt/ctg/data/mobile/updates/<runtime>/<uuid>/, = /data/mobile/… in the container); the Go
# server (internal/api/mobile.go) serves the newest one at /api/v1/expo/manifest. Phones on the same
# runtimeVersion download it on launch/foreground and apply it after a restart.
# Native changes (dependencies, app.config.ts) need RUNTIME_VERSION bumped and a new IPA via
# release.sh; this script refuses when it sees them since the last m-v tag (FORCE=1 overrides).
# Roll back: delete the newest update dir on the server; the previous one is served again.
set -e
cd "$(dirname "$0")/.."
MESSAGE="${1:-$(git log -1 --format=%s)}"
HOST="${CTG_HOST:-ctg-prod}"
DIR="${CTG_DIR:-/opt/ctg}/data/mobile/updates"

LAST_TAG=$(git describe --tags --abbrev=0 --match 'm-v*' 2>/dev/null || true)
if [ -n "$LAST_TAG" ] && [ "${FORCE:-0}" != "1" ]; then
  NATIVE_CHANGED=$(node -e '
    const { execSync } = require("child_process");
    const old = JSON.parse(execSync(`git show ${process.argv[1]}:mobile/package.json`));
    const cur = require("./package.json");
    const diff = Object.keys({ ...old.dependencies, ...cur.dependencies }).filter((k) => old.dependencies?.[k] !== cur.dependencies?.[k]);
    const cfg = execSync(`git diff ${process.argv[1]} -- app.config.ts`).toString();
    if (diff.length) console.log("dependencies: " + diff.join(", "));
    if (cfg.trim()) console.log("app.config.ts changed");
  ' "$LAST_TAG")
  if [ -n "$NATIVE_CHANGED" ]; then
    echo "Native-side changes since $LAST_TAG:" >&2
    echo "$NATIVE_CHANGED" | sed 's/^/  /' >&2
    echo "Ship these with scripts/release.sh (bump RUNTIME_VERSION if native code changed). JS-only? rerun with FORCE=1." >&2
    exit 1
  fi
fi

RV=$(npx expo config --json --type public 2>/dev/null | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>console.log(JSON.parse(s).runtimeVersion))')
COMMIT=$(git rev-parse --short HEAD)$( [ -z "$(git status --porcelain -- .)" ] || echo "-dirty")
ID=$(node -e 'console.log(require("crypto").randomUUID())')
echo "Exporting iOS bundle (runtime $RV, $COMMIT, update $ID)…"
rm -rf dist-ota
npx expo export --platform ios --output-dir dist-ota >/dev/null
npx expo config --json --type public > dist-ota/expoConfig.json 2>/dev/null
node -e 'const [m,c]=process.argv.slice(1); console.log(JSON.stringify({createdAt:new Date().toISOString(),message:m,commit:c}))' "$MESSAGE" "$COMMIT" > dist-ota/update.json

# upload into a dot dir (ignored by the server), then rename into place
tar czf - -C dist-ota . | ssh -o BatchMode=yes "$HOST" "set -e; mkdir -p $DIR/$RV/.incoming-$ID; tar -xzf - -C $DIR/$RV/.incoming-$ID --no-same-owner; \
  mv $DIR/$RV/.incoming-$ID $DIR/$RV/$ID; chown -R 65532:65532 ${DIR%/updates}; ls $DIR/$RV | wc -l | xargs echo updates for runtime $RV:"

CODE=$(curl -s -o /dev/null -w '%{http_code}' https://ctg.gugenzzz.top/api/v1/expo/manifest -H 'expo-platform: ios' -H "expo-runtime-version: $RV" -H 'expo-protocol-version: 1' -H 'accept: application/expo+json')
SERVED=$(curl -s https://ctg.gugenzzz.top/api/v1/expo/manifest -H 'expo-platform: ios' -H "expo-runtime-version: $RV" -H 'expo-protocol-version: 1' -H 'accept: application/expo+json' | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{try{console.log(JSON.parse(s).id)}catch{console.log("?")}})')
[ "$SERVED" = "$ID" ] || { echo "manifest serves $SERVED (HTTP $CODE), expected $ID" >&2; exit 1; }
echo "published OTA $ID for runtime $RV: $MESSAGE"
