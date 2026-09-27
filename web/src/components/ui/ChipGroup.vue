<script setup lang="ts" generic="T extends string | number">
import type { Component } from 'vue'
import { ToggleGroupRoot, ToggleGroupItem } from 'reka-ui'

/** Multi-select filter pills sharing FilterChip styling (Reka ToggleGroup type="multiple"). */
export interface ChipOption<V> { value: V; label: string; icon?: Component; count?: number }
defineProps<{ options: ChipOption<T>[]; ariaLabel?: string; atLeastOne?: boolean }>()
const model = defineModel<T[]>({ required: true })
</script>

<template>
  <ToggleGroupRoot type="multiple" :model-value="model as any" class="cg" :aria-label="ariaLabel"
    @update:model-value="(v: any) => { if (!(atLeastOne && (!v || v.length === 0))) model = v }">
    <ToggleGroupItem v-for="o in options" :key="String(o.value)" :value="o.value as any" class="fchip">
      <component :is="o.icon" v-if="o.icon" :size="13" class="fc-ic" />
      <span>{{ o.label }}</span>
      <span v-if="o.count !== undefined" class="fc-n">{{ o.count }}</span>
    </ToggleGroupItem>
  </ToggleGroupRoot>
</template>

<style scoped>.cg { display: inline-flex; gap: 6px; flex-wrap: wrap; }</style>
