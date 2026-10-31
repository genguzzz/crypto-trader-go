#!/bin/sh
# Publish the current web build to installed iOS apps over the air, straight from this machine
# (~1 min; no CI, no server deploy). See docs/ios.md.
#   scripts/ios-ota.sh "修复：xxx"
# Builds web/ locally, uploads it to ctg-prod /var/www/apps/ota/ctg/<version>/ (served by
# https://apps.gugenzzz.top) and points ota/ctg/manifest.json at it. Apps download it on their next
# launch/foreground and switch the next time they go to the background.
# Refuses when native files changed since the last IPA release (the bundle would need a new
# binary; push to main so ios.yml builds one). Override: FORCE=1.
# The bundle talks to the production API, so ship backend changes it relies on first.
set -e
cd "$(dirname "$0")/../web"
MESSAGE="${1:-$(git log -1 --format=%s)}"
HOST=${OTA_HOST:-ctg-prod}
ROOT=/var/www/apps/ota/ctg
KEEP=5

RUNTIME=$(node scripts/native-runtime.mjs)
RELEASED=$(gh release list --limit 20 --json tagName --jq '[.[] | select(.tagName | startswith("ios-"))][0].tagName // ""')
REL_RUNTIME=$( [ -n "$RELEASED" ] && gh release view "$RELEASED" --json body --jq .body | sed -n 's/^runtime: //p' )
if [ "$RUNTIME" != "$REL_RUNTIME" ] && [ "${FORCE:-0}" != "1" ]; then
  echo "native runtime $RUNTIME differs from the released IPA $RELEASED ($REL_RUNTIME):" >&2
  echo "no installed app would take this bundle. Commit + push native changes so CI builds an IPA." >&2
  exit 1
fi

echo "building web…"
npx vite build >/dev/null
touch dist/.gitkeep
VERSION=$(node -p "require('./dist/bundle.json').version")
COMMIT=$(git rev-parse --short HEAD)$( [ -z "$(git status --porcelain -- src public index.html vite.config.ts)" ] || echo "-dirty")
MANIFEST=$(mktemp)
node -e '
const fs = require("fs"), path = require("path"), crypto = require("crypto")
const [version, runtime, message, commit] = process.argv.slice(1)
const walk = d => fs.readdirSync(d, { withFileTypes: true }).flatMap(e => e.name.startsWith(".") ? [] : e.isDirectory() ? walk(path.join(d, e.name)) : [path.join(d, e.name)])
const files = walk("dist").map(f => { const b = fs.readFileSync(f); return { path: path.relative("dist", f).split(path.sep).join("/"), sha256: crypto.createHash("sha256").update(b).digest("hex"), size: b.length } })
  .sort((a, b) => a.path < b.path ? -1 : 1)
console.log(JSON.stringify({ version, runtime, message, commit, createdAt: new Date().toISOString(), baseUrl: `https://apps.gugenzzz.top/ota/ctg/${version}`, files }))
' "$VERSION" "$RUNTIME" "$MESSAGE" "$COMMIT" > "$MANIFEST"

echo "uploading bundle $VERSION (runtime $RUNTIME, $COMMIT)…"
# An existing version dir is reused only when complete: a dist rebuilt mid-upload (e.g. by a parallel
# deploy) once left a partial bundle that later runs skipped.
NFILES=$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).files.length)' "$MANIFEST")
tar -C dist --exclude='./.*' -czf - . | ssh "$HOST" "set -e
  mkdir -p $ROOT && cd $ROOT
  if [ ! -d $VERSION ] || [ \$(find $VERSION -type f | wc -l) -lt $NFILES ]; then rm -rf .$VERSION.part && mkdir .$VERSION.part && tar -xzf - -C .$VERSION.part --no-same-owner && chmod -R a+rX .$VERSION.part && rm -rf $VERSION && mv .$VERSION.part $VERSION; else cat >/dev/null; fi
  [ \$(find $VERSION -type f | wc -l) -ge $NFILES ] || { echo 'upload incomplete: dist changed during the upload' >&2; exit 1; }"
ssh "$HOST" "set -e; cd $ROOT; cat > .manifest.tmp && chmod 644 .manifest.tmp && mv .manifest.tmp manifest.json
  ls -1t | grep -v '^manifest.json\$' | grep -vx '$VERSION' | tail -n +$KEEP | xargs -r rm -rf" < "$MANIFEST"
rm -f "$MANIFEST"
curl -fsS "https://apps.gugenzzz.top/ota/ctg/manifest.json" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const m=JSON.parse(s);console.log(`published ${m.version} · ${m.files.length} files · ${m.message}`)})'
