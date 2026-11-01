import { reactive } from 'vue'
import { api, type Meta, type Snapshot } from './api'
import { interactApi } from './tradeApi'

// Until 2026-09-28 the automatically chosen default rule was saved as if picked by hand, pinning
// every browser to the old default (trend-oi-confirm). Drop that once; only explicit picks persist now.
try {
  if (!localStorage.getItem('ctg.ruleMigrated')) {
    if (localStorage.getItem('ruleId') === 'trend-oi-confirm') localStorage.removeItem('ruleId')
    localStorage.setItem('ctg.ruleMigrated', '1')
  }
} catch { /* storage unavailable */ }

export const store = reactive({
  meta: null as Meta | null,
  snap: null as Snapshot | null,
  prices: {} as Record<string, [number, number]>,
  ruleId: localStorage.getItem('ruleId') || '',
  connected: false,
  lastEventAt: 0,
  error: '',
  // trading: bumped by SSE `trade` / `interact` events so visible pages refetch
  tradeSeq: 0,
  tradeAccount: 0, // account id of the latest trade event (0 = unknown / all)
  interactSeq: 0,
  openPrompts: 0, // open confirmation prompts (navigation badge)
  realtradeSeq: 0, // bumped by SSE `realtrade`: an AI trade proposal changed (composables/realtrade.ts refetches)
})

export function setRule(id: string) {
  store.ruleId = id
  localStorage.setItem('ruleId', id)
}

/** Forget this browser's explicit pick so it follows the server default again. */
export function followDefaultRule() {
  try { localStorage.removeItem('ruleId') } catch { /* storage unavailable */ }
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
  const picked = localStorage.getItem('ruleId')
  // no explicit pick (or it was deleted) → follow the server default without saving it
  if (!picked || !store.meta.rules.find(r => r.id === picked)) store.ruleId = def || store.meta.rules[0]?.id || ''
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
  es.addEventListener('trade', (ev: MessageEvent) => {
    let id = 0
    try { id = Number(JSON.parse(ev.data)?.accountId) || 0 } catch { /* malformed: refetch everything */ }
    store.tradeAccount = id
    store.tradeSeq++
  })
  es.addEventListener('interact', () => { store.interactSeq++; refreshPrompts() })
  es.addEventListener('realtrade', () => { store.realtradeSeq++ })
  es.onerror = () => { store.connected = false }
  refreshPrompts()
  setInterval(() => { if (!document.hidden) refreshPrompts() }, 30_000)
  // Fallback poll in case a proxy buffers SSE.
  setInterval(() => { if (Date.now() - store.lastEventAt > 70_000) refreshUniverse() }, 30_000)
}

/** Open confirmation count for the navigation badge (silently 0 while the endpoint is unavailable). */
export async function refreshPrompts() {
  try { store.openPrompts = (await interactApi.prompts('open')).length } catch { /* endpoint not deployed yet */ }
}
