<script setup lang="ts">
import { computed } from 'vue'
import Modal from '../Modal.vue'
import { statDetails } from './registry'
import type { DetailCtx } from './types'

/** Generic host for metric details: PC = centered wide dialog, mobile = bottom sheet (via Modal); tabs switch views. */
const props = defineProps<{ active: string | null; ctx: DetailCtx; title: string }>()
const emit = defineEmits<{ 'update:active': [string | null] }>()
const def = computed(() => statDetails.find(d => d.key === props.active))
</script>

<template>
  <Modal :open="!!active" :title="title" wide @close="emit('update:active', null)">
    <nav class="tabs" role="tablist">
      <button v-for="d in statDetails" :key="d.key" role="tab" :aria-selected="d.key === active" :class="{ on: d.key === active }" @click="emit('update:active', d.key)">
        <component :is="d.icon" :size="14" /> {{ d.label }}
      </button>
    </nav>
    <div class="body">
      <component :is="def.component" v-if="def" :key="def.key" :ctx="ctx" />
    </div>
  </Modal>
</template>

<style scoped>
.tabs { display: flex; gap: 4px; margin: -4px 0 14px; overflow-x: auto; scrollbar-width: none; border-bottom: 1px solid var(--line); }
.tabs button { display: inline-flex; align-items: center; gap: 6px; border: none; border-bottom: 2px solid transparent; border-radius: 0; background: transparent; color: var(--text-3); padding: 6px 10px; white-space: nowrap; font-weight: 500; }
@media (hover: hover) { .tabs button:hover { color: var(--text-1); } }
.tabs button.on { color: var(--text-1); border-bottom-color: var(--accent); }
.body { min-height: 320px; }
</style>
