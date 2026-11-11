#!/usr/bin/env bash
# Build, sign and install OKX Watch on the paired iPhone and Apple Watch (see README.md).
#
#   watch/install.sh              build + install both
#   watch/install.sh --build      build and package only (build/OKXWatch.ipa)
#
# Env: DEVELOPMENT_TEAM (team id of the Apple Development identity; default: the team Xcode last used),
#      PHONE_UDID / WATCH_UDID (default: the first physical iPhone / Apple Watch devicectl lists).
# Run it from a normal terminal: Xcode's signed-in account is not visible to sandboxed shells.
# A free personal team's profiles last 7 days: run this again when they expire.
set -euo pipefail
cd "$(dirname "$0")"

TEAM=${DEVELOPMENT_TEAM:-$(defaults read com.apple.dt.Xcode IDEProvisioningTeamByIdentifier 2>/dev/null | sed -n 's/.*teamID = \([A-Z0-9]*\);.*/\1/p' | head -1)}
[ -n "$TEAM" ] || { echo "set DEVELOPMENT_TEAM (no team found in Xcode's settings)" >&2; exit 1; }
devices=$(xcrun devicectl list devices 2>/dev/null | grep physical || true)
PHONE=${PHONE_UDID:-$(echo "$devices" | grep -i iphone | grep -oE '[0-9A-F]{8}-[0-9A-F]{16}' | head -1)}
WATCH=${WATCH_UDID:-$(echo "$devices" | grep -i watch | grep -oE '[0-9A-F]{8}-[0-9A-F]{16}' | head -1)}
[ -n "$WATCH" ] || { echo "no Apple Watch connected (xcrun devicectl list devices)" >&2; exit 1; }

sign=(-allowProvisioningUpdates -allowProvisioningDeviceRegistration)
quiet() { grep -E "error:|warning: .*provision|\*\* [A-Z]+ (SUCCEEDED|FAILED) \*\*" | sort -u || true; }

echo "▸ project (team $TEAM)"
DEVELOPMENT_TEAM=$TEAM xcodegen >/dev/null

echo "▸ watch app + widget for the watch (registers it in the profiles)"
xcodebuild -project OKXWatch.xcodeproj -scheme OKXWatchApp -configuration Release -derivedDataPath build \
  -destination "platform=watchOS,id=$WATCH" "${sign[@]}" build 2>&1 | quiet

if [ -n "$PHONE" ]; then
  echo "▸ iPhone app for the phone (registers it in the profiles)"
  xcodebuild -project OKXWatch.xcodeproj -scheme OKXWatch -configuration Release -derivedDataPath build \
    -destination "platform=iOS,id=$PHONE" "${sign[@]}" build 2>&1 | quiet
fi

echo "▸ archive (iPhone app with its widget and the embedded watch app)"
rm -rf build/OKXWatch.xcarchive
xcodebuild -project OKXWatch.xcodeproj -scheme OKXWatch -configuration Release -derivedDataPath build \
  -destination 'generic/platform=iOS' -archivePath build/OKXWatch.xcarchive "${sign[@]}" archive 2>&1 | quiet
APP=build/OKXWatch.xcarchive/Products/Applications/OKXWatch.app
[ -d "$APP" ] || { echo "✗ archive failed" >&2; exit 1; }

rm -rf build/ipa && mkdir -p build/ipa/Payload && cp -R "$APP" build/ipa/Payload/
(cd build/ipa && zip -qry ../OKXWatch.ipa Payload)
echo "▸ packaged build/OKXWatch.ipa"
[ "${1:-}" = "--build" ] && exit 0

if [ -n "$PHONE" ]; then
  echo "▸ installing on the iPhone"
  xcrun devicectl device install app --device "$PHONE" "$APP" 2>&1 | grep -E "bundleID|error" || true
fi
echo "▸ installing on the watch"
xcrun devicectl device install app --device "$WATCH" "$APP/Watch/OKXWatchApp.app" 2>&1 | grep -E "bundleID|error" || true
echo "✓ done"
