import { reactive, ref } from 'vue'

/**
 * Owner key for state-changing trading / interaction / AI requests (header X-CTG-Admin).
 * Kept only in this browser (localStorage `ctg.adminKey`) unless the owner unticks "remember", in
 * which case it lives in memory for this tab. Until login (#1) exists this is the only guard.
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
