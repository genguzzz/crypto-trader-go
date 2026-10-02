<script setup lang="ts">
import { computed } from 'vue'
import { SliderRoot, SliderTrack, SliderRange, SliderThumb } from 'reka-ui'
import NumInput from '../ui/NumInput.vue'

/**
 * Risk per trade (fraction of equity, 0.01–5 = 1%–500%, internal/trade validLimits) as a log-scale
 * slider plus a number field, kept in sync. Log scale keeps 1–10% usable while still reaching 500%;
 * above 100% (a stop-out loses more than the whole equity) the control turns to the warning tone.
 */
const MIN = 0.01, MAX = 5, STEPS = 1000
const model = defineModel<number>({ required: true })
defineProps<{ id?: string }>()

const toPos = (v: number) => Math.round((Math.log(Math.min(MAX, Math.max(MIN, v)) / MIN) / Math.log(MAX / MIN)) * STEPS)
/** Slider position → value, rounded to 2 significant digits of the percent (1.2%, 15%, 230%). */
function fromPos(p: number) {
  const v = MIN * Math.pow(MAX / MIN, p / STEPS)
  const pctv = v * 100, mag = Math.pow(10, Math.floor(Math.log10(pctv)) - 1)
  return Math.min(MAX, Math.max(MIN, (Math.round(pctv / mag) * mag) / 100))
}
const pos = computed({ get: () => [isFinite(model.value) ? toPos(model.value) : 0], set: (p: number[]) => (model.value = +fromPos(p[0]).toPrecision(6)) })
const ticks = [0.01, 0.05, 0.2, 1, 5].map(v => ({ v, at: (toPos(v) / STEPS) * 100, label: v * 100 + '%' }))
const over = computed(() => model.value > 1)
</script>

<template>
  <div class="rs" :class="{ over }">
    <div class="sl">
      <SliderRoot v-model="pos" :min="0" :max="STEPS" :step="1" class="sr" aria-label="单笔风险">
        <SliderTrack class="st"><SliderRange class="sg" /></SliderTrack>
        <SliderThumb class="th" aria-label="单笔风险" />
      </SliderRoot>
      <div class="tk"><span v-for="t in ticks" :key="t.v" :style="{ left: t.at + '%' }" class="tkl" @click="model = t.v">{{ t.label }}</span></div>
    </div>
    <NumInput :id="id" v-model="model" unit="%" :scale="100" :min="1" :max="500" step="any" width="60px" aria-label="单笔风险（%）" />
  </div>
</template>

<style scoped>
.rs { display: flex; align-items: center; gap: 14px; width: 360px; max-width: 100%; }
.sl { flex: 1; min-width: 0; padding-bottom: 16px; position: relative; }
.sr { position: relative; display: flex; align-items: center; height: 20px; touch-action: none; user-select: none; }
.st { position: relative; flex: 1; height: 4px; border-radius: 2px; background: var(--bg-3); }
.sg { position: absolute; height: 100%; border-radius: 2px; background: var(--accent); }
.th { display: block; width: 16px; height: 16px; border-radius: 50%; background: var(--text-1); border: 3px solid var(--accent); cursor: grab; }
.th:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.over .sg { background: var(--warn); }
.over .th { border-color: var(--warn); }
.over :deep(.ni) { border-color: var(--warn); }
.tk { position: absolute; left: 8px; right: 8px; bottom: 0; height: 14px; }
.tkl { position: absolute; transform: translateX(-50%); font-size: 10px; color: var(--text-3); cursor: pointer; white-space: nowrap; }
@media (hover: hover) { .tkl:hover { color: var(--text-1); } }
</style>
