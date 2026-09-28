<script setup lang="ts">
import type { Component } from 'vue'
/** Settings group card: icon + title (+ ⓘ via the `tip` slot, + right-aligned `aside`), rows in the default slot. */
defineProps<{ title: string; icon?: Component; id?: string }>()
</script>

<template>
  <section :id="id" class="ss">
    <header>
      <component :is="icon" v-if="icon" :size="16" class="ic" />
      <h2>{{ title }}</h2>
      <InfoTip v-if="$slots.tip"><slot name="tip" /></InfoTip>
      <div class="aside"><slot name="aside" /></div>
    </header>
    <div class="body"><slot /></div>
    <footer v-if="$slots.footer"><slot name="footer" /></footer>
  </section>
</template>

<style scoped>
.ss { background: var(--bg-1); border: 1px solid var(--line); border-radius: var(--radius); scroll-margin-top: 16px; min-width: 0; }
header { display: flex; align-items: center; gap: 8px; padding: 14px 20px 4px; }
.ic { color: var(--text-3); flex: none; }
h2 { margin: 0; font-size: var(--fs-lg); font-weight: 600; color: var(--text-1); }
.aside { margin-left: auto; display: flex; align-items: center; gap: 8px; font-size: var(--fs-sm); }
.body { padding: 0 20px; }
footer { border-top: 1px solid var(--line); padding: 12px 20px; }
@media (max-width: 900px) {
  .ss { scroll-margin-top: 60px; }
  header { padding: 14px 16px 4px; }
  .body { padding: 0 16px; }
  footer { padding: 12px 16px; }
}
</style>
