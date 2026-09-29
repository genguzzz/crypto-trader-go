<script setup lang="ts">
import { ref } from 'vue'
import { Copy, Check } from 'lucide-vue-next'
import { copyText } from './snippets'

/** Monospace block with a copy button; `label` is shown above it. */
const props = defineProps<{ text: string; label?: string; secret?: boolean }>()
const done = ref(false)
async function copy() {
  done.value = await copyText(props.text)
  if (done.value) setTimeout(() => (done.value = false), 1500)
}
</script>

<template>
  <div class="cb">
    <div v-if="label" class="cl">{{ label }}</div>
    <div class="cw" :class="{ secret }">
      <pre>{{ text }}</pre>
      <button type="button" class="cp" :aria-label="done ? '已复制' : '复制'" :title="done ? '已复制' : '复制'" @click="copy">
        <Check v-if="done" :size="13" /><Copy v-else :size="13" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.cb { display: grid; gap: 4px; min-width: 0; }
.cl { font-size: var(--fs-xs); color: var(--text-3); }
.cw { position: relative; background: var(--bg-0); border: 1px solid var(--line); border-radius: var(--radius-sm); min-width: 0; }
.cw.secret { border-color: var(--accent); }
pre {
  margin: 0; padding: 8px 36px 8px 10px; font: 12px/1.5 ui-monospace, "SF Mono", Menlo, monospace; color: var(--text-1);
  white-space: pre-wrap; overflow-wrap: anywhere;
}
.cp {
  position: absolute; top: 4px; right: 4px; display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; padding: 0;
  background: var(--bg-2); border-color: transparent; color: var(--text-2);
}
@media (hover: hover) { .cp:hover { color: var(--text-1); border-color: var(--line-2); } }
</style>
