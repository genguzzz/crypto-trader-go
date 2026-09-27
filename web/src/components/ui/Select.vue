<script setup lang="ts" generic="T extends string | number">
import { computed, type Component } from 'vue'
import { SelectRoot, SelectTrigger, SelectValue, SelectPortal, SelectContent, SelectViewport, SelectItem, SelectItemText, SelectItemIndicator, SelectGroup, SelectLabel, SelectIcon } from 'reka-ui'
import { ChevronDown, Check } from 'lucide-vue-next'

/** Styled select on Reka UI: keyboard/typeahead, portal popover, optional descriptions, groups and badges. */
export interface SelOption<V> { value: V; label: string; desc?: string; group?: string; badge?: string }
const props = withDefaults(defineProps<{ options: SelOption<T>[]; icon?: Component; placeholder?: string; size?: 'sm' | 'md'; width?: string; ariaLabel?: string }>(), { size: 'md' })
const model = defineModel<T>({ required: true })
const groups = computed(() => {
  const m = new Map<string, SelOption<T>[]>()
  for (const o of props.options) { const g = o.group || ''; if (!m.has(g)) m.set(g, []); m.get(g)!.push(o) }
  return [...m.entries()]
})
const current = computed(() => props.options.find(o => o.value === model.value))
</script>

<template>
  <SelectRoot v-model="model as any">
    <SelectTrigger class="sel-trigger" :class="size" :style="width ? { width } : {}" :aria-label="ariaLabel">
      <component :is="icon" v-if="icon" :size="14" class="lead" />
      <SelectValue :placeholder="placeholder" class="val">{{ current?.label }}</SelectValue>
      <span v-if="current?.badge" class="badge">{{ current.badge }}</span>
      <SelectIcon as-child><ChevronDown :size="14" class="chev" /></SelectIcon>
    </SelectTrigger>
    <SelectPortal>
      <SelectContent class="sel-content" position="popper" :side-offset="6" align="start">
        <SelectViewport class="sel-vp">
          <SelectGroup v-for="[g, opts] in groups" :key="g">
            <SelectLabel v-if="g" class="sel-group">{{ g }}</SelectLabel>
            <SelectItem v-for="o in opts" :key="String(o.value)" :value="o.value as any" class="sel-item">
              <div class="txt">
                <SelectItemText>{{ o.label }}</SelectItemText>
                <span v-if="o.badge" class="badge">{{ o.badge }}</span>
                <div v-if="o.desc" class="desc">{{ o.desc }}</div>
              </div>
              <SelectItemIndicator class="ind"><Check :size="14" /></SelectItemIndicator>
            </SelectItem>
          </SelectGroup>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>

<style>
.sel-trigger {
  all: unset; box-sizing: border-box; display: inline-flex; align-items: center; gap: 8px; height: 32px; padding: 0 10px; min-width: 120px; max-width: 100%;
  background: var(--bg-2); border: 1px solid var(--line-2); border-radius: 8px; color: var(--text-1); font-size: var(--fs-md); cursor: pointer;
  transition: border-color .12s, background .12s;
}
.sel-trigger.sm { height: 26px; font-size: var(--fs-sm); padding: 0 8px; }
@media (hover: hover) { .sel-trigger:hover { border-color: var(--text-3); } }
.sel-trigger[data-state="open"], .sel-trigger:focus-visible { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
.sel-trigger .lead { color: var(--text-3); flex: none; }
.sel-trigger .val { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.sel-trigger .chev { color: var(--text-3); flex: none; transition: transform .15s; }
.sel-trigger[data-state="open"] .chev { transform: rotate(180deg); }
.sel-trigger .badge, .sel-item .badge { font-size: 10px; padding: 1px 5px; border-radius: 4px; background: var(--warn-bg); color: var(--warn); flex: none; }
.sel-content {
  z-index: 130; min-width: var(--reka-select-trigger-width); max-width: min(420px, 92vw); max-height: min(360px, var(--reka-select-content-available-height));
  background: var(--bg-2); border: 1px solid var(--line-2); border-radius: 10px; box-shadow: 0 16px 48px rgba(0, 0, 0, .5); overflow: hidden;
}
.sel-vp { padding: 4px; }
.sel-group { padding: 6px 10px 2px; font-size: var(--fs-xs); color: var(--text-3); }
.sel-item { all: unset; box-sizing: border-box; display: flex; align-items: center; gap: 10px; width: 100%; padding: 7px 10px; border-radius: 6px; cursor: pointer; color: var(--text-1); font-size: var(--fs-md); }
.sel-item[data-highlighted] { background: var(--bg-3); }
.sel-item[data-state="checked"] { color: var(--accent); }
.sel-item .txt { flex: 1; min-width: 0; }
.sel-item .desc { font-size: var(--fs-xs); color: var(--text-3); white-space: normal; margin-top: 1px; }
.sel-item .ind { color: var(--accent); display: inline-flex; }
</style>
