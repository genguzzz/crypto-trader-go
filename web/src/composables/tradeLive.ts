import { onMounted, onUnmounted, watch } from 'vue'
import { store } from '../store'

/**
 * Keep a trading page fresh: refetch on SSE `trade` events (only those of `accountId` when given;
 * events without an account id refresh everything), on `interact` events when `interact` is set,
 * and poll every `pollMs` as a fallback while the tab is visible.
 */
export function useTradeLive(load: () => unknown, opts: { accountId?: () => number; interact?: boolean; pollMs?: number } = {}) {
  let timer: number | undefined
  let queued = false
  // coalesce bursts of events (a fill brings order + fill + position events at once)
  const kick = () => {
    if (queued) return
    queued = true
    setTimeout(() => { queued = false; load() }, 250)
  }
  watch(() => store.tradeSeq, () => {
    const want = opts.accountId?.()
    if (!want || !store.tradeAccount || store.tradeAccount === want) kick()
  })
  if (opts.interact) watch(() => store.interactSeq, kick)
  onMounted(() => {
    load()
    timer = window.setInterval(() => { if (!document.hidden) load() }, opts.pollMs ?? 10_000)
  })
  onUnmounted(() => clearInterval(timer))
}
