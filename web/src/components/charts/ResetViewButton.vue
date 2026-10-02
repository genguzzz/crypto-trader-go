<script setup lang="ts">
import { LocateFixed } from 'lucide-vue-next'

/** Bottom-right ◎ of a chart: back to the default range with auto-scaled prices. `right` = price-axis width + gap. */
withDefaults(defineProps<{ right?: number; bottom?: number; title?: string }>(), { right: 70, bottom: 34, title: '复位：默认范围、自动缩放' })
const emit = defineEmits<{ reset: [] }>()
</script>

<template>
  <button type="button" class="reset" :title="title" aria-label="重置视图" :style="{ right: right + 'px', bottom: bottom + 'px' }" @click.stop="emit('reset')"><LocateFixed :size="15" /></button>
</template>

<style scoped>
.reset { all: unset; box-sizing: border-box; position: absolute; z-index: 4; display: grid; place-items: center; width: 28px; height: 28px; border-radius: 6px;
  background: rgba(14, 14, 14, .85); border: 1px solid var(--line-2); color: var(--text-2); cursor: pointer; }
@media (hover: hover) { .reset:hover { color: var(--text-1); border-color: var(--accent); } }
.reset:active { background: var(--bg-3); }
.reset:focus-visible { outline: 2px solid var(--accent); }
</style>
