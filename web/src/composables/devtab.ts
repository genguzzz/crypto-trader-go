import { ref } from 'vue'

const KEY = 'ctg.devTab'
const read = () => { try { return localStorage.getItem(KEY) !== '0' } catch { return true } }

/** Dev tab visibility (this browser only; shown unless turned off in Settings → 开发者). */
export const devTab = ref(read())
export function setDevTab(on: boolean) {
  devTab.value = on
  try { localStorage.setItem(KEY, on ? '1' : '0') } catch { /* private mode: session only */ }
}
