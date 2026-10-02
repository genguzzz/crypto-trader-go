<script setup lang="ts">
import { computed } from 'vue'

/**
 * Number field with a unit suffix (the settings-page style). `scale` converts between the stored
 * value and the shown one, e.g. scale 100 shows the fraction 0.01 as "1" with unit "%".
 */
const props = withDefaults(defineProps<{ unit?: string; scale?: number; min?: number; max?: number; step?: number | 'any'; id?: string; width?: string; ariaLabel?: string; disabled?: boolean }>(), { scale: 1, step: 'any' })
const model = defineModel<number>({ required: true })
const shown = computed(() => (model.value == null || !isFinite(model.value) ? '' : +(model.value * props.scale).toPrecision(10)))
function set(e: Event) {
  const raw = (e.target as HTMLInputElement).value
  model.value = raw === '' ? NaN : Number(raw) / props.scale
}
</script>

<template>
  <span class="ni" :class="{ dis: disabled }">
    <input :id="id" type="number" inputmode="decimal" :min="min" :max="max" :step="step" :value="shown" :aria-label="ariaLabel" :disabled="disabled"
      :style="width ? { width } : undefined" @input="set" />
    <span v-if="unit" class="u">{{ unit }}</span>
  </span>
</template>

<style scoped>
.ni { display: inline-flex; align-items: center; max-width: 100%; background: var(--bg-3); border: 1px solid var(--line-2); border-radius: var(--radius-sm); }
.ni:focus-within { border-color: var(--accent); }
.ni.dis { opacity: .5; }
input { width: 84px; min-width: 0; border: none; background: transparent; text-align: right; padding: 4px 4px 4px 10px; font-variant-numeric: tabular-nums; }
input::-webkit-outer-spin-button, input::-webkit-inner-spin-button { -webkit-appearance: none; margin: 0; }
input[type=number] { -moz-appearance: textfield; appearance: textfield; }
.u { padding: 0 10px 0 2px; color: var(--text-3); font-size: var(--fs-sm); white-space: nowrap; }
:global(.invalid) .ni { border-color: var(--down); }
</style>
