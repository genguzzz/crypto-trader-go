#!/bin/sh
# Cut a native release of the iOS client: bump mobile/package.json, commit, tag m-vX.Y.Z (annotated,
# message = notes), push branch ios + tag. The tag build (.github/workflows/mobile-ios.yml, ~15 min)
# makes a GitHub Release and publishes the IPA to the 全能签 source (https://apps.gugenzzz.top/appstore.json).
#   mobile/scripts/release.sh 0.2.0 "新增：xxx；修复：yyy"
# Bump RUNTIME_VERSION in app.config.ts first when native code changed.
set -e
cd "$(dirname "$0")/.."

VERSION="$1"
NOTES="${2:-OKX Helper $1}"
echo "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "usage: $0 X.Y.Z [notes]" >&2; exit 1; }
[ "$(git rev-parse --abbrev-ref HEAD)" = ios ] || { echo "release from branch ios" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "working tree not clean; commit first" >&2; exit 1; }
git rev-parse "m-v$VERSION" >/dev/null 2>&1 && { echo "tag m-v$VERSION already exists" >&2; exit 1; }

CURRENT=$(node -p "require('./package.json').version")
LOWER=$(printf '%s\n%s\n' "$CURRENT" "$VERSION" | sort -V | head -1)
if [ "$CURRENT" != "$VERSION" ] && [ "$LOWER" = "$VERSION" ]; then
  echo "version $VERSION is lower than current $CURRENT" >&2; exit 1
fi
if [ "$CURRENT" != "$VERSION" ]; then
  npm version "$VERSION" --no-git-tag-version >/dev/null
  git add package.json package-lock.json
  git -c user.name=genguzzz -c user.email=38418451+genguzzz@users.noreply.github.com commit -q -m "Release m-v$VERSION"
fi
git tag -a "m-v$VERSION" -m "$NOTES"
git push origin HEAD:ios "m-v$VERSION"
echo "pushed m-v$VERSION — watch: gh run list --workflow mobile-ios.yml --limit 1"
