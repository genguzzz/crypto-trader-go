# OKX Helper (native iOS) — working rules

Native iOS client of OKX Helper (ctg). Lives in `mobile/` on branch **`ios`**; the Go backend and the Vue web app stay on `main`. Same stack and conventions as mediahub/Murmur: Expo SDK 58, React Native 0.88, Expo Router, `NativeTabs` (system tab bar → Liquid Glass on iOS 26), left drawer, expo-updates against our own server. Runs side by side with the Capacitor app (`web/ios`, bundle `top.gugenzzz.ctg`): this one is `top.gugenzzz.okxhelper`, display name `OKX Helper`, its own runtime versions. Most new requirements target this client. Read `AGENTS.md` (Expo: don't trust memory, check versioned docs).

## Architecture

```
iPhone OKX Helper ──HTTPS──▶ ctg.gugenzzz.top (ctg-prod nginx → ctg :8686)
   REST /api/v1/* (same routes as web/src/api.ts)      SSE /api/v1/stream (expo/fetch streaming)
   WebView /embed/chart/:id (web PriceChart, no chrome) Expo Updates /api/v1/expo/manifest + /assets
   POST /api/v1/app/events (usage) · /api/v1/app/logs (owner key)
```

- Backend pieces are shared: change them on `main` (main checkout, small commits), then `git merge main` into `ios`. Before every push to `main` check `curl -s https://ctg.gugenzzz.top/api/v1/ai/chats` has no `"running": true` (a push deploys and restarts the server). Never force-push `main`.
- Server side of the app: `internal/api/mobile.go` (OTA, events, logs; data in `/opt/ctg/data/mobile/` on ctg-prod = `/data/mobile` in the container) and `web/src/views/EmbedChart.vue`.
- Owner key (`X-CTG-Admin`): `lib/adminKey.ts` (keychain); `lib/api.ts` asks once through `AdminKeySheet` when a write returns 401 `admin_required`, then retries.

## Ship it

- Before every commit: `cd mobile && npx tsc --noEmit && npx eslint src`. Commit to `ios` and push.
- JS-only change → `mobile/scripts/ota.sh "说明"` (~1 min): export, ssh-copy into `data/mobile/updates/<runtime>/<uuid>/`, verifies the manifest serves it. Roll back by deleting the newest dir there. Check what phones ask for: `ssh ctg-prod 'docker logs ctg 2>&1 | grep "expo manifest" | tail'` (`runtime`, `current` → `served`).
- Native change (new/upgraded native package, config plugin, permission, SDK) → bump `RUNTIME_VERSION` in `app.config.ts`, then `mobile/scripts/release.sh X.Y.Z "新增：…；修复：…"`. The tag `m-vX.Y.Z` runs `.github/workflows/mobile-ios.yml` (image `xcode-27`, ~15 min, free: public repo): GitHub Release + publish to the 全能签 source through Murmur's publisher on ctg-prod (deploy key from environment `production`, token read on the server). Pushes to `ios` touching `mobile/**` build only (artifact). Verify: `curl -s https://apps.gugenzzz.top/appstore.json | grep okxhelper`.
- Swift can't build on tutu; native changes are verified only by CI. Push them alone first, watch with `gh run watch <id>` in the background.
- Web preview: `npx expo export --platform web --output-dir dist-web`, serve with SPA fallback and `/api/*` proxied to production (on web `API_ORIGIN` is same-origin), screenshot with Playwright (Chromium needs `--proxy-server=http://127.0.0.1:7080` for the chart iframe). Tabs render on top on web.

## App conventions

- Tabs (`src/app/(tabs)`): 扫描 · 自选 · AI · 交易. Drawer (`components/Drawer.tsx`; menu button in `TabHeader` or left-edge swipe): 规则与战绩, 监控告警, 系统状态, 帮助, 设置, Dev. Pushed screens use `TopBar`; settings is a modal; `/chart/[id]` is a full-screen landscape modal. Pages not built yet use `Placeholder` (what will come + open the web page).
- Interactions and looks follow the web app: tokens in `lib/theme.ts` mirror `web/src/style.css` (black canvas, lime `#bcff2f` accent, up `#25a750`, down `#ca3f64`); number/time strings come from `lib/fmt.ts` (port of `web/src/fmt.ts`, display zone default UTC+8); types in `lib/api.ts` mirror `web/src/api.ts`. When the web changes a definition, port it.
- K-line chart = the web chart in a WebView (`components/ChartView.tsx` → `/embed/chart/:id?spot=&oi=&bar=&layer=&full=`). Don't rebuild it natively. While a finger is on it the page scroll is locked; full-screen button posts `fullscreen` → `/chart/[id]`. Small secondary charts use `components/Charts.tsx` (SVG `LineChart` / `BarChart`).
- Data: `useQuery(key, fn, {interval, staleMs})` from `lib/query.ts` (shared cache, persisted, polls only while focused and active); `mutate`/`load` after actions. Market data in `lib/market.ts`: `useMeta`, `useUniverse` (15 s poll + stream-triggered refetch), `useLivePrice` (SSE `prices`), `toggleWatch` (optimistic), `useRuleId`.
- Pull-to-refresh: `<Pull onRefresh>`. Every tap gets haptics (`tap()`), results `toast()` / `toastError()`, destructive actions `confirm()`. UI copy Simplified Chinese, plain and precise, no emoji or exclamation marks.
- Device features only through `lib/device.ts` (`has(capability)` first — OTA code can run on an older binary). Runtime 1 ships the capability pack: glass, haptics, blur, symbols, secure-store, sqlite, local notifications, speech recognition, audio recording (microphone permission for AI voice), clipboard, sharing, linking, web-browser, WebView, network, keep-awake, quick actions, Face ID, orientation. Settings → 能力自检 exercises them.
- Drawer pages (phase 2b): `rules/index.tsx` + `rules/[id].tsx` (track record, weight editor), `alerts.tsx`, `status.tsx` (3 s poll while focused), `help/index.tsx` + `help/[slug].tsx`, `settings.tsx` (+ `components/settings/*`: server settings with a save bar, interaction channel, AI authorization, developer), `dev.tsx` (keys, access, request log, endpoint reference; the tester stays on the web). Their endpoints/types live in `lib/sysApi.ts` (port from web `api.ts`/`tradeApi.ts`). Shared form bits in `components/Form.tsx`: `Field`, `Input`, `NumInput`, `Toggle`, `Meter`, `SectionHead`, and `InfoTip` (web ⓘ → bottom sheet; rich text with `P`/`B`/`Code`). Copy the web page's tip text verbatim.
- Help articles: `node scripts/gen-docs.mjs` bundles `web/src/docs/strategy/*.md` into `src/content/strategyDocs.ts` (generated, commit it; rerun after the web docs change). `components/Markdown.tsx` renders them (headings, lists, tables, quotes, code blocks with copy and Python highlighting; no HTML). Links `/help/strategy/x` stay in the app, other paths open the web.
- Logging: `logger("area")` (ids, counts, timings, errors; never keys). Settings → 上传日志 → `data/mobile/logs/<id>.log`. Usage events: `track(name, props)` → `data/mobile/events/YYYY-MM-DD.jsonl`.

## Traps

- Number inputs inside rows: RN-web gives `TextInput` an intrinsic width; `NumInput` sets `minWidth: 0` so values don't render outside the box.
- The web preview server must block non-GET `/api/*` (read-only checks against production).
- `react-native-webview` 14 types `WebView` as `never` under strict TS; `ChartView.tsx` casts it to `ComponentType<WebViewProps>`.
- Header values must be ASCII (fetch throws otherwise); `api.ts` strips non-ASCII from `x-app-version`.
- An OTA only reaches binaries with the same runtime. If a runtime bump's IPA never got built, phones stay on the old runtime and see nothing.
- The scanner list freezes its order while dragging/momentum (like the web), so taps land on the intended row.
