import { reactive, ref } from 'vue'

/**
 * Owner key (header X-CTG-Admin), required for every /api/v1/ request (internal/api/admin.go).
 * Kept only in this browser (localStorage `ctg.adminKey`) unless the owner unticks "remember", in
 * which case it lives in memory for this tab. The native app's web views (/embed/*) get it injected
 * as window.ctgAdminKey before the page loads (memory only). Until login (#1) exists this is the only guard.
 */
const KEY = 'ctg.adminKey'
const read = () => { try { return localStorage.getItem(KEY) || '' } catch { return '' } }
let memoryKey = ''

/** Whether a key is remembered on this device (drives the Settings row). */
export const adminKeySet = ref(!!read())

export function getAdminKey() { return read() || memoryKey }
export function setAdminKey(v: string, remember = true) {
  memoryKey = v
  try {
    if (remember && v) localStorage.setItem(KEY, v)
    else localStorage.removeItem(KEY)
  } catch { /* private mode: memory only */ }
  adminKeySet.value = !!read()
}
export function clearAdminKey() { setAdminKey('', false) }

/** Global prompt state rendered by components/AdminKeyModal.vue (mounted once in App.vue). */
export const adminPrompt = reactive({ open: false, error: '' })
let waiters: ((k: string | null) => void)[] = []

/** Ask the owner for the key; resolves with the key, or null when cancelled. Concurrent asks share one dialog. */
export function askAdminKey(error = ''): Promise<string | null> {
  adminPrompt.open = true
  adminPrompt.error = error
  return new Promise(res => waiters.push(res))
}
export function resolveAdminKey(k: string | null) {
  adminPrompt.open = false
  const w = waiters
  waiters = []
  w.forEach(f => f(k))
}

/** Same-origin or production /api/v1 URL (the Capacitor app calls production; see native.ts). */
const isApi = (url: string) => ['', location.origin, 'https://ctg.gugenzzz.top'].some(o => url.startsWith(o + '/api/v1/'))

/** URL with the key as ?key=, for <img> and EventSource, which cannot send headers (server allows it
 *  only for the live stream and coin icons). */
export function withKeyParam(url: string) {
  const k = getAdminKey()
  return k && isApi(url) ? url + (url.includes('?') ? '&' : '?') + 'key=' + encodeURIComponent(k) : url
}

/**
 * Every /api/v1 fetch carries the owner key; EventSource gets it as a query parameter. Outside the
 * embedded pages a GET answered 401 admin_required asks for the key once and retries (callers that
 * pass their own X-CTG-Admin header handle the 401 themselves). Call before anything fetches.
 */
export function installKeyedFetch(prompt: boolean) {
  const injected = (window as unknown as { ctgAdminKey?: string }).ctgAdminKey
  if (injected) setAdminKey(injected, false)
  const fetch0 = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : ''
    if (!url || !isApi(url)) return fetch0(input, init)
    const headers = new Headers(init?.headers)
    const own = headers.has('X-CTG-Admin')
    const send = () => {
      const k = getAdminKey()
      if (!own && k) headers.set('X-CTG-Admin', k)
      return fetch0(input, { ...init, headers })
    }
    const r = await send()
    const method = (init?.method || 'GET').toUpperCase()
    if (r.status !== 401 || own || !prompt || method !== 'GET') return r
    const body = await r.clone().json().catch(() => ({}))
    if (body?.code !== 'admin_required') return r
    const k = await askAdminKey(getAdminKey() ? '密钥不正确，请重新输入' : '')
    return k ? send() : r
  }
  const ES = window.EventSource
  window.EventSource = class extends ES {
    constructor(url: string | URL, init?: EventSourceInit) { super(withKeyParam(typeof url === 'string' ? url : url.href), init) }
  }
}
