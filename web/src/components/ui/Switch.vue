<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

/** On/off switch (Reka Switch: keyboard, ARIA). On = accent track with a dark thumb. */
defineProps<{ ariaLabel?: string; disabled?: boolean; title?: string }>()
const model = defineModel<boolean>({ required: true })
</script>

<template>
  <SwitchRoot v-model="model" class="sw" :aria-label="ariaLabel" :title="title" :disabled="disabled">
    <SwitchThumb class="sw-thumb" />
  </SwitchRoot>
</template>

<style>
.sw {
  all: unset; box-sizing: border-box; position: relative; flex: none; width: 34px; height: 20px; border-radius: 999px; cursor: pointer;
  background: var(--bg-3); border: 1px solid var(--line-2); transition: background .15s, border-color .15s;
}
.sw[data-state="checked"] { background: var(--accent); border-color: var(--accent); }
.sw[data-disabled] { opacity: .4; cursor: not-allowed; }
.sw:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.sw-thumb {
  display: block; width: 14px; height: 14px; border-radius: 50%; background: var(--text-2);
  transform: translateX(2px); transition: transform .15s, background .15s;
}
.sw[data-state="checked"] .sw-thumb { transform: translateX(16px); background: var(--on-accent); }
</style>
