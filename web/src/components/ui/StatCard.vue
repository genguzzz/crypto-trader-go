<script setup lang="ts">
import type { Component } from 'vue'
import { ChevronRight } from 'lucide-vue-next'

/**
 * Generic metric card: label on top, value, sub-line. When `clickable`, the whole card is a button
 * that emits `open` (the parent decides what detail to show). Slots: value, sub.
 */
withDefaults(defineProps<{ label: string; icon?: Component; clickable?: boolean; tone?: '' | 'up' | 'down' }>(), { clickable: true, tone: '' })
const emit = defineEmits<{ open: [] }>()
</script>

<template>
  <component :is="clickable ? 'button' : 'div'" class="card" :class="{ clickable }" type="button" @click="clickable && emit('open')">
    <div class="l">
      <component :is="icon" v-if="icon" :size="13" class="ic" />
      <span>{{ label }}</span>
      <ChevronRight v-if="clickable" :size="14" class="go" />
    </div>
    <div class="v tnum" :class="tone"><slot name="value" /></div>
    <div class="s"><slot name="sub" /></div>
  </component>
</template>

<style scoped>
.card {
  all: unset; box-sizing: border-box; display: block; min-width: 0; padding: 8px 12px; border-radius: 8px;
  border: 1px solid transparent; transition: background .12s, border-color .12s; text-align: left;
}
.card.clickable { cursor: pointer; }
@media (hover: hover) { .card.clickable:hover { background: var(--bg-2); border-color: var(--line-2); } }
.card.clickable:focus-visible { outline: 2px solid var(--accent); }
.l { display: flex; align-items: center; gap: 5px; font-size: var(--fs-xs); color: var(--text-3); margin-bottom: 4px; white-space: nowrap; }
.ic { color: var(--text-3); flex: none; }
.go { margin-left: auto; color: var(--text-3); opacity: 0; transition: opacity .12s, transform .12s; }
@media (hover: hover) { .card.clickable:hover .go { opacity: 1; transform: translateX(2px); color: var(--accent); } }
@media (hover: none) { .go { opacity: .6; } }
.v { font-size: var(--fs-lg); font-weight: 600; color: var(--text-1); white-space: nowrap; }
.v.up { color: var(--up); } .v.down { color: var(--down); }
.v :deep(small) { font-size: var(--fs-xs); font-weight: 500; color: var(--text-3); }
.v :deep(.sep) { color: var(--text-3); font-weight: 400; }
.s { font-size: var(--fs-xs); color: var(--text-2); margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
