<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import type { DepthPoint, Sweep } from '../market/depth'
import { sweepTo } from '../market/depth'
import { usd, price } from '../../fmt'

/**
 * Cumulative order-book depth in USDT around the mid price (x = % offset, y = cumulative USDT).
 * Hover / touch anywhere to read "how much USDT moves the price to here".
 */
const props = withDefaults(defineProps<{ bids: DepthPoint[]; asks: DepthPoint[]; mid: number; range: number; height?: number }>(), { height: 240 })
const emit = defineEmits<{ probe: [Sweep | null] }>()

const wrap = ref<HTMLElement>()
const W = ref(600)
let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(es => (W.value = Math.max(260, es[0].contentRect.width)))
  ro.observe(wrap.value!)
})
onUnmounted(() => ro?.disconnect())

const pad = { l: 8, r: 58, t: 10, b: 22 }
const H = computed(() => props.height)
const inRange = (ps: DepthPoint[]) => ps.filter(p => Math.abs(p.off) <= props.range)
const maxY = computed(() => {
  const edge = (ps: DepthPoint[]) => { const r = inRange(ps); return r.length ? r[r.length - 1].cum : 0 }
  return Math.max(edge(props.bids), edge(props.asks), 1) * 1.08
})
const x = (off: number) => pad.l + ((off + props.range) / (2 * props.range)) * (W.value - pad.l - pad.r)
const y = (v: number) => H.value - pad.b - (v / maxY.value) * (H.value - pad.t - pad.b)
const offAt = (px: number) => ((px - pad.l) / (W.value - pad.l - pad.r)) * 2 * props.range - props.range

/** Step path from mid outwards; cumulative stays flat until the next level's offset. */
function stepPath(ps: DepthPoint[], dir: 1 | -1) {
  const pts = inRange(ps)
  let d = `M${x(0)},${y(0)}`
  let cum = 0
  for (const p of pts) { d += `H${x(p.off)}V${y(p.cum)}`; cum = p.cum }
  d += `H${x(dir * props.range)}`
  return { line: d, area: `${d}V${y(0)}Z`, cum }
}
const bidPath = computed(() => stepPath(props.bids, -1))
const askPath = computed(() => stepPath(props.asks, 1))

const xTicks = computed(() => [-1, -0.5, 0, 0.5, 1].map(k => k * props.range))
const yTicks = computed(() => [0.25, 0.5, 0.75, 1].map(k => (k * maxY.value) / 1.08))

const hover = ref<{ px: number; sweep: Sweep } | null>(null)
function onMove(e: PointerEvent) {
  const r = wrap.value!.getBoundingClientRect()
  const px = e.clientX - r.left
  if (px < pad.l || px > W.value - pad.r) { hover.value = null; emit('probe', null); return }
  const off = Math.round(offAt(px) * 100) / 100 // snap to 0.01%
  const sweep = sweepTo(off, props.bids, props.asks, props.mid)
  hover.value = { px: x(off), sweep }
  emit('probe', sweep)
}
// On touch, keep the reading after the finger lifts (tap anywhere on the chart to move it).
function onLeave(e: PointerEvent) { if (e.pointerType === 'touch') return; hover.value = null; emit('probe', null) }
const tipLeft = computed(() => (hover.value ? Math.min(Math.max(hover.value.px + 12, 4), W.value - 214) : 0))
</script>

<template>
  <div ref="wrap" class="depth" :style="{ height: H + 'px' }" @pointermove="onMove" @pointerdown="onMove" @pointerleave="onLeave">
    <svg :width="W" :height="H">
      <g class="grid">
        <line v-for="t in yTicks" :key="'y' + t" :x1="pad.l" :x2="W - pad.r" :y1="y(t)" :y2="y(t)" />
      </g>
      <path :d="bidPath.area" class="area bid" /><path :d="bidPath.line" class="line bid" />
      <path :d="askPath.area" class="area ask" /><path :d="askPath.line" class="line ask" />
      <line :x1="x(0)" :x2="x(0)" :y1="pad.t" :y2="H - pad.b" class="midline" />
      <g class="axis">
        <text v-for="t in xTicks" :key="'x' + t" :x="x(t)" :y="H - 6" :text-anchor="t === -range ? 'start' : t === range ? 'end' : 'middle'">{{ t > 0 ? '+' : '' }}{{ +t.toFixed(2) }}%</text>
        <text v-for="t in yTicks" :key="'yl' + t" :x="W - pad.r + 6" :y="y(t) + 4">{{ usd(t) }}</text>
      </g>
      <g v-if="hover" class="cross">
        <line :x1="hover.px" :x2="hover.px" :y1="pad.t" :y2="H - pad.b" />
        <line :x1="pad.l" :x2="W - pad.r" :y1="y(hover.sweep.usdt)" :y2="y(hover.sweep.usdt)" />
        <circle :cx="hover.px" :cy="y(hover.sweep.usdt)" r="4" :class="hover.sweep.side" />
      </g>
    </svg>
    <div v-if="hover" class="dtip" :style="{ left: tipLeft + 'px' }">
      <div class="t1" :class="hover.sweep.side">
        {{ hover.sweep.side === 'buy' ? '买入推高到' : '卖出压低到' }} {{ hover.sweep.targetPct > 0 ? '+' : '' }}{{ hover.sweep.targetPct.toFixed(2) }}%
      </div>
      <div class="row"><span>目标价</span><b>{{ price(hover.sweep.targetPx) }}</b></div>
      <div class="row"><span>需吃掉</span><b>{{ usd(hover.sweep.usdt) }} USDT</b></div>
      <div class="row"><span>成交均价</span><b>{{ price(hover.sweep.avgPx) }}</b></div>
      <div class="row"><span>均价滑点</span><b>{{ hover.sweep.slipPct > 0 ? '+' : '' }}{{ hover.sweep.slipPct.toFixed(3) }}%</b></div>
      <div class="row"><span>档数</span><b>{{ hover.sweep.levels }}</b></div>
      <div v-if="hover.sweep.beyondData" class="warn">超出 OKX 公开盘口深度，实际需要更多</div>
    </div>
  </div>
</template>

<style scoped>
.depth { position: relative; width: 100%; touch-action: pan-y; cursor: crosshair; user-select: none; }
svg { display: block; }
.grid line { stroke: var(--line); stroke-width: 1; }
.area.bid { fill: rgba(14, 203, 129, .16); } .area.ask { fill: rgba(246, 70, 93, .16); }
.line { fill: none; stroke-width: 1.5; } .line.bid { stroke: var(--up); } .line.ask { stroke: var(--down); }
.midline { stroke: var(--text-3); stroke-dasharray: 3 3; }
.axis text { fill: var(--text-3); font-size: 10px; font-variant-numeric: tabular-nums; }
.cross line { stroke: var(--text-2); stroke-dasharray: 2 3; }
.cross circle { stroke: var(--bg-0); stroke-width: 2; } .cross circle.buy { fill: var(--down); } .cross circle.sell { fill: var(--up); }
.dtip {
  position: absolute; top: 8px; width: 206px; pointer-events: none; background: var(--bg-2); border: 1px solid var(--line-2);
  border-radius: 8px; padding: 8px 10px; font-size: var(--fs-xs); box-shadow: 0 8px 24px rgba(0, 0, 0, .45);
}
.t1 { font-weight: 600; font-size: var(--fs-sm); margin-bottom: 4px; }
.t1.buy { color: var(--up); } .t1.sell { color: var(--down); }
.row { display: flex; justify-content: space-between; color: var(--text-3); line-height: 1.7; }
.row b { color: var(--text-1); font-weight: 600; font-variant-numeric: tabular-nums; }
.warn { color: var(--warn); margin-top: 4px; }
</style>
