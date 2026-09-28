<script setup lang="ts">
import { computed } from 'vue'
/** Thin usage bar. Without an explicit tone: ≥ 100% bad, ≥ 90% warn, otherwise accent. */
const props = withDefaults(defineProps<{ value: number; max: number; tone?: '' | 'ok' | 'warn' | 'bad'; height?: number }>(), { tone: '', height: 6 })
const pct = computed(() => (props.max > 0 ? Math.min(100, (props.value / props.max) * 100) : 0))
const t = computed(() => props.tone || (pct.value >= 100 ? 'bad' : pct.value >= 90 ? 'warn' : ''))
</script>

<template>
  <div class="meter" role="meter" :aria-valuenow="value" :aria-valuemin="0" :aria-valuemax="max" :style="{ height: height + 'px' }">
    <i :class="t" :style="{ width: pct + '%' }"></i>
  </div>
</template>

<style scoped>
.meter { position: relative; width: 100%; border-radius: 999px; background: var(--bg-3); overflow: hidden; }
i { position: absolute; left: 0; top: 0; bottom: 0; border-radius: inherit; background: var(--accent); transition: width .3s ease; }
i.ok { background: var(--up); } i.warn { background: var(--warn); } i.bad { background: var(--down); }
</style>
