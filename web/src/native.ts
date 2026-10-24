// iOS app shell (Capacitor). The same build runs in the browser and in the app: in the app the
// page is served from capacitor://localhost, so API calls go to production, and new web builds
// arrive as over-the-air bundles (docs/ios.md).
import { Capacitor, registerPlugin } from '@capacitor/core'

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

let checking = false
/** Download the server's web bundle when it differs from ours; it is switched to in the background. */
async function checkUpdate() {
  if (checking) return
  checking = true
  try {
    const [state, local, remote] = await Promise.all([
      LiveUpdate.getState(),
      fetch('/bundle.json').then(r => r.json()) as Promise<{ version: string }>,
      fetch(`${apiOrigin}/api/v1/app/bundle`, { cache: 'no-store' }).then(r => r.json()) as Promise<{ version: string; runtime: string; files: BundleFile[] }>,
    ])
    if (String(remote.runtime) !== state.runtime) return // needs a new binary
    if (remote.version === local.version || remote.version === state.staged || state.failed.includes(remote.version)) return
    await LiveUpdate.download({ version: remote.version, baseUrl: apiOrigin, files: remote.files })
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
