<script setup lang="ts">
/** Compact row of key numbers used at the top of detail views. */
export interface Kpi { label: string; value: string; sub?: string; tone?: '' | 'up' | 'down' }
defineProps<{ items: Kpi[] }>()
</script>

<template>
  <div class="kpis">
    <div v-for="k in items" :key="k.label" class="kpi">
      <div class="l">{{ k.label }}</div>
      <div class="v tnum" :class="k.tone">{{ k.value }}</div>
      <div v-if="k.sub" class="s">{{ k.sub }}</div>
    </div>
  </div>
</template>

<style scoped>
.kpis { display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); gap: 10px; }
.kpi { background: var(--bg-2); border-radius: 8px; padding: 8px 12px; min-width: 0; }
.l { font-size: var(--fs-xs); color: var(--text-3); }
.v { font-size: var(--fs-xl); font-weight: 600; color: var(--text-1); white-space: nowrap; }
.v.up { color: var(--up); } .v.down { color: var(--down); }
.s { font-size: var(--fs-xs); color: var(--text-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
