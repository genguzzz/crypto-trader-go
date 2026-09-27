import { reactive } from 'vue'
import { api, type Meta, type Snapshot } from './api'

export const store = reactive({
  meta: null as Meta | null,
  snap: null as Snapshot | null,
  prices: {} as Record<string, [number, number]>,
  ruleId: localStorage.getItem('ruleId') || '',
  connected: false,
  lastEventAt: 0,
  error: '',
})

export function setRule(id: string) {
  store.ruleId = id
  localStorage.setItem('ruleId', id)
}

let pending = false
async function refreshUniverse() {
  if (pending) return
  pending = true
  try {
    store.snap = await api.universe('active')
    store.error = ''
  } catch (e: any) {
    store.error = e.message
  } finally {
    pending = false
  }
}

export async function refreshMeta() {
  store.meta = await api.meta()
  const def = store.meta.settings?.defaultRule
  if (!store.meta.rules.find(r => r.id === store.ruleId)) setRule(def || store.meta.rules[0]?.id || '')
}

/** Rule options for USelect: grouped presets vs user rules, all flagged unvalidated. */
export function ruleOptions() {
  return (store.meta?.rules || []).map(r => ({
    value: r.id, label: r.name, badge: '未验证', group: r.preset ? '对照规则' : '我的规则',
    desc: `v${r.version} · ${r.description.length > 48 ? r.description.slice(0, 48) + '…' : r.description}`,
  }))
}

export function fieldLabel(key: string) {
  return store.meta?.fields.find(f => f.key === key)?.label || key
}

export function startStream() {
  refreshMeta().catch(e => (store.error = e.message))
  refreshUniverse()
  const es = new EventSource('/api/v1/stream')
  es.addEventListener('hello', () => { store.connected = true; refreshUniverse() })
  es.addEventListener('universe', () => { store.lastEventAt = Date.now(); refreshUniverse() })
  es.addEventListener('prices', (ev: MessageEvent) => {
    store.lastEventAt = Date.now()
    store.prices = JSON.parse(ev.data)
  })
  es.onerror = () => { store.connected = false }
  // Fallback poll in case a proxy buffers SSE.
  setInterval(() => { if (Date.now() - store.lastEventAt > 70_000) refreshUniverse() }, 30_000)
}
