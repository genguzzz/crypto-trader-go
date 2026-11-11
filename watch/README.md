# OKX Watch (OKX 手表助手)

A small iPhone companion app with an embedded Apple Watch app: the owner sees pending trade proposals on
the wrist and approves or rejects them, and looks at the open OKX positions and the trading desk's
state. Separate from the main app (`mobile/`): its own bundle ids, signed with the workstation's
development certificate.

```
watch (OKXWatchApp, SwiftUI) ──WatchConnectivity sendMessage {op}──▶ iPhone (OKXWatch, Phone/Relay.swift)
   待审核 · 持仓 · 交易台                                              URLSession, X-CTG-Admin from the keychain
        ◀── reply {snapshot | result | error} ──────────────────────  GET realtrade/proposals?status=pending
        ◀── application context (last snapshot, when unreachable)     GET account/positions · GET autopilot
                                                                      POST realtrade/proposals/{id}/approve|reject
```

- The watch never talks to the server and never holds the owner key: the phone makes every request in
  Swift (no web view or JS runtime), so a message from the watch is answered even when the companion
  app is not running — WatchConnectivity wakes it in the background.
- Approving is two taps on purpose: the proposal's detail, then a confirmation that names the order and
  says whether the server is in live or dry-run mode. The server re-validates as for any approval
  (`internal/realtrade`); the watch shows its verdict.
- One snapshot = pending proposals, positions with equity, trading desk status. A part that fails is
  listed, the rest still shows.

| Path | What |
|---|---|
| `project.yml` | XcodeGen definition: iOS app `top.gugenzzz.okxwatch` embedding watchOS app `top.gugenzzz.okxwatch.watchkitapp` |
| `Shared/Models.swift` | snapshot types (server JSON field names), wire keys, formatting |
| `Phone/Relay.swift` | keychain, the three reads and approve / reject, the `WCSessionDelegate` that answers the watch |
| `Phone/PhoneApp.swift` | the companion's one screen: owner key, connection state, what the watch sees |
| `Watch/Store.swift` | asks the phone, keeps the newest snapshot, haptics on a new proposal |
| `Watch/WatchApp.swift` | the three pages and the proposal detail |

## Build and install

Needs Xcode with the watchOS SDK, XcodeGen (`brew install xcodegen`) and an Apple Development identity;
the iPhone and the watch connected and paired with this Mac.

```bash
cd watch
DEVELOPMENT_TEAM=<team id of the signing certificate> xcodegen          # writes OKXWatch.xcodeproj (not committed)
# once per watch: registers it in the team's profile
xcodebuild -project OKXWatch.xcodeproj -scheme OKXWatchApp -configuration Release -derivedDataPath build \
  -destination 'platform=watchOS,id=<watch udid>' -allowProvisioningUpdates -allowProvisioningDeviceRegistration build
xcodebuild -project OKXWatch.xcodeproj -scheme OKXWatch -configuration Release -derivedDataPath build \
  -destination 'generic/platform=iOS' -archivePath build/OKXWatch.xcarchive -allowProvisioningUpdates archive
APP=build/OKXWatch.xcarchive/Products/Applications/OKXWatch.app
xcrun devicectl device install app --device <iphone udid> "$APP"
xcrun devicectl device install app --device <watch udid> "$APP/Watch/OKXWatchApp.app"   # or let the Watch app install it
```

`xcrun devicectl list devices` shows the ids. An IPA is the same bundle zipped as `Payload/OKXWatch.app`.

## Signing notes

- The watch app lives inside the iOS bundle (`OKXWatch.app/Watch/OKXWatchApp.app`), so one IPA carries
  both; iOS offers it to the paired watch (installed by itself when "Automatic App Install" is on).
- Each bundle has its own profile. A development or ad-hoc profile must list the **watch's** UDID for
  the watch bundle, not only the phone's. A re-signing tool therefore has to sign the nested watch app
  with a profile that covers `<bundle id>.watchkitapp` and that watch, and keep
  `WKCompanionAppBundleIdentifier` equal to the iOS bundle id; tools that drop the `Watch` folder or
  sign with a phone-only profile install the phone app without the watch app.
- A free (personal team) profile is valid for 7 days: rebuild and reinstall when it expires.
- First launch of the phone app: trust the developer under Settings → General → VPN & Device Management.
