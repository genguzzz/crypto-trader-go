<script setup lang="ts" generic="T extends string | number">
import type { Component } from 'vue'
import { ToggleGroupRoot, ToggleGroupItem } from 'reka-ui'

/**
 * Segmented control (single choice) on Reka UI ToggleGroup: keyboard arrows, roving focus, ARIA.
 * The active item is a raised pill inside a sunken track — no overlapping borders to clip.
 */
export interface SegOption<V> { value: V; label?: string; icon?: Component; title?: string; tone?: 'up' | 'down' }
const props = withDefaults(defineProps<{ options: SegOption<T>[]; size?: 'sm' | 'md'; ariaLabel?: string }>(), { size: 'md' })
const model = defineModel<T>({ required: true })
function onUpdate(v: any) { if (v !== undefined && v !== null && v !== '') model.value = v as T } // never deselect
void props
</script>

<template>
  <ToggleGroupRoot type="single" :model-value="model as any" class="seg" :class="size" :aria-label="ariaLabel" @update:model-value="onUpdate">
    <ToggleGroupItem v-for="o in options" :key="String(o.value)" :value="o.value as any" class="seg-i" :class="o.tone" :title="o.title">
      <component :is="o.icon" v-if="o.icon" :size="size === 'sm' ? 13 : 14" />
      <span v-if="o.label">{{ o.label }}</span>
    </ToggleGroupItem>
  </ToggleGroupRoot>
</template>

<style scoped>
.seg { display: inline-flex; gap: 2px; padding: 2px; background: var(--bg-0); border: 1px solid var(--line); border-radius: 8px; }
.seg-i {
  all: unset; box-sizing: border-box; display: inline-flex; align-items: center; justify-content: center; gap: 5px; cursor: pointer;
  height: 26px; padding: 0 10px; border-radius: 6px; color: var(--text-3); font-size: var(--fs-sm); font-weight: 500; white-space: nowrap;
  transition: color .12s, background .12s, box-shadow .12s;
}
.sm .seg-i { height: 22px; padding: 0 8px; font-size: var(--fs-xs); }
@media (hover: hover) { .seg-i:hover { color: var(--text-1); } }
.seg-i[data-state="on"] { color: var(--text-1); background: var(--bg-3); box-shadow: 0 1px 0 rgba(255, 255, 255, .04) inset, 0 1px 3px rgba(0, 0, 0, .4); }
.seg-i.up[data-state="on"] { color: var(--up); } .seg-i.down[data-state="on"] { color: var(--down); }
.seg-i:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
</style>
