# iOS app

The web UI also ships as an iOS app (**OKX Helper**, `top.gugenzzz.ctg`): a [Capacitor](https://capacitorjs.com) shell around the same Vue build, installed from the owner's software source and signed on the phone by 全能签.

## How it fits together

| Piece | Where | What |
|---|---|---|
| Shell | `web/ios/` (Xcode project, Swift Package Manager), `web/capacitor.config.ts` | WKWebView serving the bundled web build from `capacitor://localhost`; plugins: splash screen, status bar, live update |
| API access | `web/src/native.ts` | In the app, `/api/...` fetches and the SSE stream go to `https://ctg.gugenzzz.top` (`apiUrl()` for `<img>`/links); the server answers the app's origin with CORS (`internal/api/app.go`) |
| Live update (native) | `web/native/live-update` (local Capacitor plugin) | Downloads a bundle file by file (unchanged files copied from the current bundle), verifies SHA-256, stages it where Capacitor loads persisted deploys from; switches when the app goes to the background or on next launch; rolls back a bundle that does not call `ready()` within 15 s |
| Live update (server) | `GET /api/v1/app/bundle` | Version, native runtime and file list (path, SHA-256, size) of the web build embedded in the running binary; files are fetched from `/` |
| Runtime id | `web/scripts/native-runtime.mjs` | Hash of `web/ios`, `web/native`, `capacitor.config.ts` and native plugin versions. The web build writes it into `dist/bundle.json`, CI into the IPA's `Info.plist` (`CTGRuntime`); a bundle is only installed by a binary with the same id |
| IPA | `.github/workflows/ios.yml` | Unsigned archive on `macos-latest` → GitHub Release `ios-1.0.<run>` → over the deploy SSH key, Murmur's publisher on ctg-prod (`127.0.0.1:8791/admin/publish`, token read on the server) → listed at `https://apps.gugenzzz.top/appstore.json` |

## Shipping changes

- **Web-only change** (anything under `web/src`, styles, docs pages): push to `main`. `deploy.yml` deploys the server; the server now serves the new bundle, installed apps download it on their next launch/foreground and switch to it the next time they go to the background. Nothing else to do.
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

- `capacitor://localhost` is a different origin: anything that bypasses `fetch`/`EventSource` (image `src`, links) must go through `apiUrl()`.
- Hidden files and `bundle.json` are excluded from the bundle version hash; `bundle.json` itself is part of the file list so a downloaded bundle knows its own version.
- Capacitor clears the staged bundle when a new binary is installed (it compares `CFBundleVersion`), so a fresh IPA always starts from its own bundled build.
- A bundle that fails to start is recorded as failed on the device and never downloaded again; a later build gets a new version hash.
