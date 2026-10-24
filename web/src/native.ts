// iOS app shell (Capacitor). The same build runs in the browser and in the app: in the app the
// page is served from capacitor://localhost, so API calls go to production, and new web builds
// arrive as over-the-air bundles published with scripts/ios-ota.sh (docs/ios.md).
import { Capacitor, registerPlugin } from '@capacitor/core'
import type { Router } from 'vue-router'

export const isNative = Capacitor.isNativePlatform()

/** Origin of the API server: same origin in the browser, production in the app. */
export const apiOrigin = isNative ? 'https://ctg.gugenzzz.top' : ''

/** Absolute URL for an API path, for places that bypass fetch (img src, links). */
export const apiUrl = (path: string) => (path.startsWith('/api/') ? apiOrigin + path : path)

interface BundleFile { path: string; sha256: string }
interface LiveUpdateState { runtime: string; binary: string; build: string; running: string; staged: string; downloading: boolean; failed: string[] }
interface LiveUpdatePlugin {
  getState(): Promise<LiveUpdateState>
  download(o: { version: string; baseUrl: string; files: BundleFile[] }): Promise<{ version: string }>
  ready(): Promise<{ running: string }>
  reset(): Promise<void>
}
export const LiveUpdate = registerPlugin<LiveUpdatePlugin>('LiveUpdate')

interface ShellPlugin {
  capture(o: { position: number }): Promise<void>
  didNavigate(o: { position: number; top: number; bottom: number }): Promise<void>
  reset(): Promise<void>
}
const Shell = registerPlugin<ShellPlugin>('Shell')

/**
 * Native back swipe (ShellPlugin): the page being left is snapshotted before each route change; after
 * a navigation the shell learns the history position and the fixed header / tab bar heights, which
 * stay in place while the content slides.
 */
function nativeSwipeBack(router: Router) {
  const position = () => (history.state as { position?: number } | null)?.position ?? 0
  const fixed = () => {
    const header = document.querySelector('.topbar')?.getBoundingClientRect()
    const tabbar = document.querySelector('.tabbar')
    const tab = tabbar && getComputedStyle(tabbar).display !== 'none' ? tabbar.getBoundingClientRect() : null
    return { top: header ? Math.max(0, header.bottom) : 0, bottom: tab ? Math.max(0, innerHeight - tab.top) : 0 }
  }
  // history.state already points at the target during popstate, so remember where we are.
  let current = position()
  Shell.reset().catch(() => {})
  router.beforeEach(async (_to, from) => {
    if (from.matched.length) await Shell.capture({ position: current }).catch(() => {})
  })
  const report = () => requestAnimationFrame(() => requestAnimationFrame(() => {
    current = position()
    Shell.didNavigate({ position: current, ...fixed() }).catch(() => {})
  }))
  router.afterEach(report)
  addEventListener('resize', report)
}

/** Route relative /api requests to the API origin. Call before anything fetches. */
function patchNetwork() {
  const fetch0 = window.fetch.bind(window)
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) =>
    fetch0(typeof input === 'string' ? apiUrl(input) : input, init)
  const ES = window.EventSource
  window.EventSource = class extends ES {
    constructor(url: string | URL, init?: EventSourceInit) { super(typeof url === 'string' ? apiUrl(url) : url, init) }
  }
}

/** Over-the-air bundles published by scripts/ios-ota.sh (static files on the software source). */
const OTA_MANIFEST = 'https://apps.gugenzzz.top/ota/ctg/manifest.json'

let checking = false
/** Download the published web bundle when it differs from ours; it is switched to in the background. */
async function checkUpdate() {
  if (checking) return
  checking = true
  try {
    const [state, local, remote] = await Promise.all([
      LiveUpdate.getState(),
      fetch('/bundle.json').then(r => r.json()) as Promise<{ version: string }>,
      fetch(OTA_MANIFEST, { cache: 'no-store' }).then(r => r.json()) as Promise<{ version: string; runtime: string; baseUrl: string; files: BundleFile[] }>,
    ])
    if (remote.runtime !== state.runtime) return // needs a new binary
    if (remote.version === local.version || remote.version === state.staged || state.failed.includes(remote.version)) return
    await LiveUpdate.download({ version: remote.version, baseUrl: remote.baseUrl, files: remote.files })
  } catch (e) {
    console.warn('live update:', e)
  } finally {
    checking = false
  }
}

export async function bundleInfo() {
  if (!isNative) return null
  const [state, local] = await Promise.all([LiveUpdate.getState(), fetch('/bundle.json').then(r => r.json())])
  return { ...state, version: local.version as string }
}

/** Before the app starts: route API calls, hook the router for the back swipe. */
export function initNative(router: Router) {
  if (!isNative) return
  document.documentElement.classList.add('native')
  patchNetwork()
  nativeSwipeBack(router)
}

/** After the app has mounted: confirm this bundle (otherwise it is rolled back), then look for a newer one. */
export function nativeReady() {
  if (!isNative) return
  LiveUpdate.ready().catch(() => {})
  setTimeout(checkUpdate, 3000)
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') checkUpdate() })
}
