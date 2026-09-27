<script setup lang="ts">
import type { Component } from 'vue'
import { Toggle } from 'reka-ui'

/** On/off filter pill (Reka Toggle): icon + label + optional count; pressed = accent fill. */
defineProps<{ label: string; icon?: Component; count?: number | string; title?: string }>()
const model = defineModel<boolean>({ required: true })
</script>

<template>
  <Toggle v-model="model" class="fchip" :title="title">
    <component :is="icon" v-if="icon" :size="13" class="fc-ic" />
    <span>{{ label }}</span>
    <span v-if="count !== undefined" class="fc-n">{{ count }}</span>
  </Toggle>
</template>

<style>
.fchip {
  all: unset; box-sizing: border-box; display: inline-flex; align-items: center; gap: 6px; height: 28px; padding: 0 10px; border-radius: 999px;
  border: 1px dashed var(--line-2); color: var(--text-2); font-size: var(--fs-sm); cursor: pointer; user-select: none; white-space: nowrap;
  transition: background .12s, color .12s, border-color .12s;
}
@media (hover: hover) { .fchip:hover { color: var(--text-1); border-color: var(--text-3); } }
.fchip:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.fchip[data-state="on"] { border-style: solid; border-color: rgba(76, 141, 255, .55); background: var(--accent-bg); color: var(--text-1); }
.fchip .fc-ic { color: var(--text-3); } .fchip[data-state="on"] .fc-ic { color: var(--accent); }
.fchip .fc-n { font-size: var(--fs-xs); font-variant-numeric: tabular-nums; color: var(--text-3); background: var(--bg-3); border-radius: 999px; padding: 0 6px; line-height: 16px; }
.fchip[data-state="on"] .fc-n { color: var(--text-1); background: rgba(76, 141, 255, .25); }
</style>
