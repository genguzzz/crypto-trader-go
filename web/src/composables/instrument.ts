import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, type InstrumentInfo } from '../api'
import { store } from '../store'

/**
 * Instrument detail state shared by the mobile detail page and the desktop workbench:
 * info polled every 5 s, the forming-bar price pushed by the chart, funding countdown, current rule score.
 */
export function useInstrument(id: () => string) {
  const info = ref<InstrumentInfo | null>(null)
  const err = ref('')
  const nowMs = ref(Date.now())
  const chartPx = ref<number | null>(null) // forming bar close, refreshed every second by the chart
  let timer: number | undefined, clock: number | undefined

  async function load() {
    try {
      info.value = await api.instrument(id())
      err.value = ''
    } catch (e: any) {
      err.value = e.message
    }
  }
  onMounted(() => {
    load()
    timer = window.setInterval(load, 5000)
    clock = window.setInterval(() => (nowMs.value = Date.now()), 1000)
  })
  onUnmounted(() => { clearInterval(timer); clearInterval(clock) })

  const row = computed(() => info.value?.row)
  const base = computed(() => id().split('-')[0])
  const live = computed(() => chartPx.value ?? store.prices[id()]?.[0] ?? info.value?.last)
  const score = computed(() => row.value?.scores?.[store.ruleId])
  const ruleName = computed(() => store.meta?.rules.find(r => r.id === store.ruleId)?.name || store.ruleId)
  const spread = computed(() => {
    const i = info.value
    if (!i || !i.bid || !i.ask) return null
    return ((i.ask - i.bid) / ((i.ask + i.bid) / 2)) * 1e4
  })
  const countdown = computed(() => {
    const ft = info.value?.fundingTime
    if (!ft) return '—'
    const s = Math.max(0, Math.floor((ft - nowMs.value) / 1000))
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60
    return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(x).padStart(2, '0')}`
  })
  async function toggleWatch() {
    if (!info.value) return
    await api.watch(id(), !info.value.watch)
    load()
  }
  return { info, err, row, base, live, chartPx, score, ruleName, spread, countdown, toggleWatch, load }
}

export const chgClass = (v?: number | null) => (v == null || v === 0 ? '' : v > 0 ? 'up' : 'down')
export const statusText: Record<string, string> = { live: '实时', stale: '数据缺口，补洞中', warming: '预热中', new: '上市不足 12h' }
