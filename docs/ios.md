# iOS app

The web UI also ships as an iOS app (**OKX Helper**, `top.gugenzzz.ctg`): a [Capacitor](https://capacitorjs.com) shell around the same Vue build, installed from the owner's software source and signed on the phone by 全能签.

## How it fits together

| Piece | Where | What |
|---|---|---|
| Shell | `web/ios/` (Xcode project, Swift Package Manager), `web/capacitor.config.ts` | WKWebView serving the bundled web build from `capacitor://localhost`; plugins: splash screen, status bar, live update |
| API access | `web/src/native.ts` | In the app, `/api/...` fetches and the SSE stream go to `https://ctg.gugenzzz.top` (`apiUrl()` for `<img>`/links); the server answers the app's origin with CORS (`internal/api/app.go`) |
| Live update (native) | `web/native/live-update` (local Capacitor plugin) | Downloads a bundle file by file (unchanged files copied from the current bundle), verifies SHA-256, stages it where Capacitor loads persisted deploys from; switches when the app goes to the background or on next launch; rolls back a bundle that does not call `ready()` within 15 s |
| Live update (publishing) | `scripts/ios-ota.sh` | Builds `web/` on this machine and uploads it to ctg-prod `/var/www/apps/ota/ctg/<version>/`; `https://apps.gugenzzz.top/ota/ctg/manifest.json` (version, runtime, base URL, files with SHA-256) points at the newest; keeps 5 versions |
| Bootstrap | `GET /api/v1/app/bundle` | Same manifest for the web build embedded in the server binary; only IPAs 1.0.1–1.0.3 ask here (their built-in code predates `ios-ota.sh`), after one update they follow the static manifest |
| Runtime id | `web/scripts/native-runtime.mjs` | Hash of `web/ios`, `web/native`, `capacitor.config.ts` and native plugin versions. The web build writes it into `dist/bundle.json`, CI into the IPA's `Info.plist` (`CTGRuntime`); a bundle is only installed by a binary with the same id |
| IPA | `.github/workflows/ios.yml` | Unsigned archive on `macos-latest` → GitHub Release `ios-1.0.<run>` → over the deploy SSH key, Murmur's publisher on ctg-prod (`127.0.0.1:8791/admin/publish`, token read on the server) → listed at `https://apps.gugenzzz.top/appstore.json` |

## Update prompts

`src/components/AppUpdateBar.vue` (app only, above the tab bar): **新版本已就绪** once a web bundle is downloaded — 立即更新 calls `LiveUpdate.apply()` and the page reloads into it (binaries before 1.0.8 lack `apply()`: the bar says to background the app instead); **App 有新版本** when `appstore.json` lists a higher IPA version than the running binary (dismissed per version).

## Shipping changes

- **Web-only change** (anything under `web/src`, styles, docs pages): `scripts/ios-ota.sh "说明"` publishes it to the apps in about a minute, straight from the workstation (no CI). Installed apps download it on their next launch/foreground and switch to it the next time they go to the background. The bundle calls the production API, so deploy backend changes it needs first; push to `main` as usual so the website gets the change too. The script refuses when native files differ from the last released IPA (`FORCE=1` overrides).
- **Native change** (plugin added or upgraded, `web/ios`, `web/native`, `capacitor.config.ts`): push to `main`. The runtime id changes, so `ios.yml` builds and publishes a new IPA; old binaries stop taking web updates until the new IPA is installed from the source. Run `npx cap sync ios` in `web/` after adding a plugin so `web/ios/App/CapApp-SPM/Package.swift` is updated, and commit it.
- **Rebuild an IPA anyway**: `gh workflow run ios.yml`. A push that changes only `package-lock.json` without changing the runtime id skips the build.

## Local work

```bash
cd web
npm run build && npx cap sync ios     # copy dist into ios/App/App/public (gitignored) and refresh plugins
node scripts/native-runtime.mjs       # runtime id of the current tree
```

Building the app needs Xcode (macOS); on Linux only the web side and `cap sync` work. To test the app shell's web code in a browser, nothing special is needed: `isNative` is false there and the build behaves as the normal web UI.

## Gotchas

- Capacitor hard-codes `scrollView.bounces = false`; `ShellPlugin` turns the page rubber-band back on. CSS `overscroll-behavior: none` on `html`/`body` would kill it again.
- `scripts/ios-ota.sh` hashes the working tree for the runtime id: stash uncommitted native edits before publishing a bundle for the released binary.

- `capacitor://localhost` is a different origin: anything that bypasses `fetch`/`EventSource` (image `src`, links) must go through `apiUrl()`.
- Hidden files and `bundle.json` are excluded from the bundle version hash; `bundle.json` itself is part of the file list so a downloaded bundle knows its own version.
- Capacitor clears the staged bundle when a new binary is installed (it compares `CFBundleVersion`), so a fresh IPA always starts from its own bundled build.
- A bundle that fails to start is recorded as failed on the device and never downloaded again; a later build gets a new version hash.
