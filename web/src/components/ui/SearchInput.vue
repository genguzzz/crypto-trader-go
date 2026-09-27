<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { Search, X } from 'lucide-vue-next'

/** Search field with icon, clear button and "/" keyboard shortcut to focus (PC). */
const props = withDefaults(defineProps<{ placeholder?: string; shortcut?: boolean }>(), { placeholder: '搜索', shortcut: true })
const model = defineModel<string>({ required: true })
const el = ref<HTMLInputElement>()
const onKey = (e: KeyboardEvent) => {
  const t = e.target as HTMLElement
  if (props.shortcut && e.key === '/' && !/INPUT|TEXTAREA|SELECT/.test(t.tagName) && !t.isContentEditable) { e.preventDefault(); el.value?.focus() }
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <label class="si">
    <Search :size="14" class="si-ic" />
    <input ref="el" v-model="model" :placeholder="placeholder" spellcheck="false" autocomplete="off" @keydown.esc="model = ''; el?.blur()" />
    <button v-if="model" type="button" class="si-x" aria-label="清空" @click="model = ''; el?.focus()"><X :size="13" /></button>
    <kbd v-else-if="shortcut" class="si-k hide-sm">/</kbd>
  </label>
</template>

<style scoped>
.si { display: inline-flex; align-items: center; gap: 6px; height: 32px; padding: 0 8px 0 10px; min-width: 150px; background: var(--bg-0); border: 1px solid var(--line-2); border-radius: 8px; cursor: text; transition: border-color .12s, box-shadow .12s; }
.si:focus-within { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-bg); }
.si-ic { color: var(--text-3); flex: none; }
input { all: unset; flex: 1; min-width: 0; color: var(--text-1); font-size: var(--fs-md); }
input::placeholder { color: var(--text-3); }
.si-x { all: unset; display: inline-flex; padding: 2px; border-radius: 4px; color: var(--text-3); cursor: pointer; }
.si-x:hover { color: var(--text-1); background: var(--bg-3); }
.si-k { font: 500 11px ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-3); border: 1px solid var(--line-2); border-radius: 4px; padding: 0 5px; line-height: 16px; }
</style>
