import { reactive, ref, watch } from 'vue'
import { store } from '../store'
import { adminKeySet } from './adminKey'
import { realtradeApi, type RtMode, type TradeProposal } from '../realtradeApi'

/**
 * Shared state of the AI's real-trade proposals: the pending list (AI orb and panel badges), a cache
 * by id for the cards, and a clock for the expiry countdowns. Refreshed on the SSE `realtrade` event,
 * every 30 s while something is pending, and right after a countdown runs out. Owner only: without
 * the key (or with a refused one) everything stays empty and `owner` false.
 */
export const rt = reactive({
  owner: false,
  mode: '' as RtMode | '',
  pending: [] as TradeProposal[],
  byId: {} as Record<number, TradeProposal>,
  skew: 0, // server clock − this device's clock (ms)
})
/** Server-aligned "now", ticking every second while a proposal is pending (countdowns). */
export const rtNow = ref(Date.now())

export function remember(p: TradeProposal) { rt.byId[p.id] = p }

let busy = false
export async function refreshPending() {
  if (busy) return
  busy = true
  try {
    const d = await realtradeApi.list({ status: 'pending' })
    rt.owner = !!d
    if (!d) { rt.pending = []; return }
    rt.mode = d.mode
    if (d.now) rt.skew = d.now - Date.now()
    rt.pending = d.proposals || []
    rt.pending.forEach(remember)
  } catch { /* endpoint not deployed yet or offline: keep what is shown */ } finally {
    busy = false
  }
}

const inflight = new Map<number, Promise<TradeProposal | null>>()
/** One proposal (with its events) into the cache; concurrent asks for the same id share one request. */
export function fetchProposal(id: number): Promise<TradeProposal | null> {
  let f = inflight.get(id)
  if (!f) {
    f = realtradeApi.get(id).then(p => { if (p) { rt.owner = true; remember(p) } return p }).catch(() => null).finally(() => inflight.delete(id))
    inflight.set(id, f)
  }
  return f
}

/** "04:32" / "1:02:10" until the proposal expires, by the server clock. */
export function timeLeft(p: TradeProposal) {
  const s = Math.max(0, Math.floor((p.expiresAt - rtNow.value) / 1000))
  const hh = Math.floor(s / 3600), mm = Math.floor((s % 3600) / 60), ss = s % 60
  const p2 = (n: number) => String(n).padStart(2, '0')
  return hh ? `${hh}:${p2(mm)}:${p2(ss)}` : `${p2(mm)}:${p2(ss)}`
}

let started = false
/** Start following proposals (idempotent; the AI orb calls it once the app is up). */
export function startRealtrade() {
  if (started) return
  started = true
  refreshPending()
  watch(() => store.realtradeSeq, () => refreshPending())
  watch(adminKeySet, () => refreshPending())
  let tick = 0, poll = 0, lastExpiryCheck = 0
  watch(() => rt.pending.length > 0, on => {
    clearInterval(tick); clearInterval(poll)
    if (!on) return
    tick = window.setInterval(() => {
      rtNow.value = Date.now() + rt.skew
      // a countdown ran out: the server expires it, fetch the new state (at most every 5 s)
      if (rt.pending.some(p => p.expiresAt <= rtNow.value) && rtNow.value - lastExpiryCheck > 5000) {
        lastExpiryCheck = rtNow.value
        refreshPending()
      }
    }, 1000)
    poll = window.setInterval(() => { if (!document.hidden) refreshPending() }, 30_000)
  }, { immediate: true })
}
