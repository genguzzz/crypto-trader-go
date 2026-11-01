<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import PriceChart from '../components/PriceChart.vue'
import { api } from '../api'
import { setAdminKey } from '../composables/adminKey'

/**
 * Bare K-line chart for the native iOS app (mobile/ on branch ios), loaded in a WebView:
 * /embed/chart/BTC-USDT-SWAP?spot=1&oi=1&bar=15m&layer=k&full=0
 * spot / oi say whether the contract has a spot pair and open interest (fetched when absent);
 * bar / layer preset the chart's own choices (it remembers them in this web view afterwards);
 * full=1 is the app's landscape full-screen chart; proposal=<n> draws that AI trade proposal's levels. The app injects its owner key as window.ctgAdminKey
 * before the page loads (kept in memory only) so the chart can draw the owner's position.
 * Messages to the app (window.ReactNativeWebView): {type:'live', price} for the forming bar, {type:'fullscreen'} / {type:'exitFullscreen'} from the
 * chart's full-screen button.
 */
const props = defineProps<{ id: string }>()
const route = useRoute()
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : undefined)

// presets go into the keys PriceChart reads on setup (it runs after this)
const presets: Record<string, string> = { bar: 'bar', layer: 'ctg.chartLayer' }
for (const [k, key] of Object.entries(presets)) {
  const v = q(k)
  if (v) try { localStorage.setItem(key, v) } catch { /* storage unavailable */ }
}

const injected = (window as unknown as { ctgAdminKey?: string }).ctgAdminKey
if (injected) setAdminKey(injected, false)

const full = q('full') === '1'
const proposal = Number(q('proposal')) || undefined
const hasSpot = ref<boolean | null>(q('spot') === undefined ? null : q('spot') === '1')
const hasOi = ref<boolean | null>(q('oi') === undefined ? null : q('oi') === '1')
const ready = computed(() => hasSpot.value != null && hasOi.value != null)
const err = ref('')
onMounted(async () => {
  if (ready.value) return
  try {
    const info = await api.instrument(props.id)
    hasSpot.value = !!info.spotId
    hasOi.value = info.oiUsd > 0
  } catch (e: any) {
    err.value = e.message
  }
})

type Bridge = { postMessage: (s: string) => void }
const post = (msg: Record<string, unknown>) => (window as unknown as { ReactNativeWebView?: Bridge }).ReactNativeWebView?.postMessage(JSON.stringify(msg))
let lastPx = 0
function onLive(price: number) {
  if (price === lastPx) return
  lastPx = price
  post({ type: 'live', price })
}
</script>

<template>
  <div class="embed">
    <PriceChart v-if="ready" :id="id" :has-spot="!!hasSpot" :has-oi="!!hasOi" fill :embed="full ? 'full' : 'inline'" :proposal="proposal" @live="onLive" @fullscreen="post({ type: full ? 'exitFullscreen' : 'fullscreen' })" />
    <div v-else class="note pad">{{ err || '加载中…' }}</div>
  </div>
</template>

<style>
html, body { background: #0e0e0e; overscroll-behavior: none; }
</style>
<style scoped>
.embed { height: 100vh; height: 100dvh; display: flex; flex-direction: column; }
.embed > :deep(.panel) { flex: 1; min-height: 0; border: none; border-radius: 0; padding: 8px 10px 6px; }
.pad { padding: 16px; }
</style>
