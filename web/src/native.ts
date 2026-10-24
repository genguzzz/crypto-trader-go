// iOS app shell (Capacitor). The same build runs in the browser and in the app: in the app the
// page is served from capacitor://localhost, so API calls go to production, and new web builds
// arrive as over-the-air bundles published with scripts/ios-ota.sh (docs/ios.md).
import { Capacitor, registerPlugin } from '@capacitor/core'
import { reactive } from 'vue'

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
  apply(): Promise<void> // binaries before 1.0.8 lack it: the bundle then switches when the app goes to the background
  reset(): Promise<void>
}
export const LiveUpdate = registerPlugin<LiveUpdatePlugin>('LiveUpdate')

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
/** The software source the IPA is installed from (全能签). */
const APP_SOURCE = 'https://apps.gugenzzz.top/appstore.json'
const BUNDLE_ID = 'top.gugenzzz.ctg'
const DISMISS_KEY = 'ctg.update.dismissedBinary'

/** What the update prompt (AppUpdateBar) shows. */
export const appUpdate = reactive({
  /** A new web bundle is downloaded and can be switched to now. */
  ready: false,
  message: '',
  applying: false,
  /** apply() is missing in this binary: the bundle switches when the app goes to the background. */
  applyLater: false,
  /** A newer IPA is in the software source. */
  binary: null as null | { version: string; notes: string },
})

const newer = (a: string, b: string) => {
  const pa = a.split('.').map(Number), pb = b.split('.').map(Number)
  for (let i = 0; i < 3; i++) if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) > (pb[i] || 0)
  return false
}

async function checkBinary(state: LiveUpdateState) {
  const src = await fetch(APP_SOURCE, { cache: 'no-store' }).then(r => r.json()) as { apps: { bundleId: string; version: string; versionDescription?: string }[] }
  const app = src.apps.find(a => a.bundleId === BUNDLE_ID)
  if (app && state.binary && newer(app.version, state.binary) && localStorage.getItem(DISMISS_KEY) !== app.version) {
    appUpdate.binary = { version: app.version, notes: app.versionDescription || '' }
  }
}

export type UpdateCheck = 'latest' | 'ready' | 'binary' | 'busy' | 'error'

let checking = false
/** Look for a newer web bundle (downloaded in the background) and a newer IPA; both end up in appUpdate. */
export async function checkUpdate(): Promise<UpdateCheck> {
  if (checking) return 'busy'
  checking = true
  try {
    const [state, local, remote] = await Promise.all([
      LiveUpdate.getState(),
      fetch('/bundle.json').then(r => r.json()) as Promise<{ version: string }>,
      fetch(OTA_MANIFEST, { cache: 'no-store' }).then(r => r.json()) as Promise<{ version: string; runtime: string; message?: string; baseUrl: string; files: BundleFile[] }>,
    ])
    await checkBinary(state).catch(e => console.warn('app source:', e))
    if (remote.runtime !== state.runtime || remote.version === local.version || state.failed.includes(remote.version)) {
      return appUpdate.binary ? 'binary' : 'latest'
    }
    if (remote.version !== state.staged) await LiveUpdate.download({ version: remote.version, baseUrl: remote.baseUrl, files: remote.files })
    appUpdate.message = remote.message || ''
    appUpdate.ready = true
    return 'ready'
  } catch (e) {
    console.warn('live update:', e)
    return 'error'
  } finally {
    checking = false
  }
}

/** Switch to the downloaded bundle now (the page reloads). */
export async function applyUpdate() {
  appUpdate.applying = true
  try {
    await LiveUpdate.apply()
  } catch {
    appUpdate.applying = false
    appUpdate.applyLater = true
  }
}

export function dismissBinary() {
  if (appUpdate.binary) localStorage.setItem(DISMISS_KEY, appUpdate.binary.version)
  appUpdate.binary = null
}

export async function bundleInfo() {
  if (!isNative) return null
  const [state, local] = await Promise.all([LiveUpdate.getState(), fetch('/bundle.json').then(r => r.json())])
  return { ...state, version: local.version as string }
}

/** Before the app starts: route API calls. */
export function initNative() {
  if (!isNative) return
  document.documentElement.classList.add('native')
  patchNetwork()
}

/** After the app has mounted: confirm this bundle (otherwise it is rolled back), then look for a newer one. */
export function nativeReady() {
  if (!isNative) return
  LiveUpdate.ready().catch(() => {})
  setTimeout(checkUpdate, 3000)
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') checkUpdate() })
}
