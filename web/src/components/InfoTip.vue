<script setup lang="ts">
import { ref } from 'vue'
import { useDesktop } from '../composables/device'
/** ⓘ icon: hover to show on PC, tap to toggle on touch devices. Content via default slot. */
defineProps<{ label?: string }>()
const desktop = useDesktop()
const open = ref(false)
let t: number | undefined
const enter = () => { if (desktop.value) { clearTimeout(t); open.value = true } }
const leave = () => { if (desktop.value) t = window.setTimeout(() => (open.value = false), 150) }
</script>

<template>
  <span class="tip" @mouseenter="enter" @mouseleave="leave">
    <button class="i" :aria-expanded="open" :title="label" @click.stop="open = !open">ⓘ<span v-if="label" class="il">{{ label }}</span></button>
    <Teleport to="body" :disabled="desktop">
      <div v-if="open && !desktop" class="sheet-bg" @click="open = false"></div>
      <div v-if="open" class="pop" :class="{ sheet: !desktop }" @mouseenter="enter" @mouseleave="leave" @click.stop>
        <button v-if="!desktop" class="close" @click="open = false">✕</button>
        <slot />
      </div>
    </Teleport>
  </span>
</template>

<style scoped>
.tip { position: relative; display: inline-flex; }
.i { border: none; background: transparent; color: var(--text-3); padding: 0 4px; font-size: 14px; line-height: 1; display: inline-flex; align-items: center; gap: 4px; }
.i:hover, .i[aria-expanded="true"] { color: var(--accent); }
.il { font-size: var(--fs-sm); }
.pop {
  position: absolute; top: calc(100% + 8px); left: -8px; z-index: 50; width: min(520px, 80vw); max-height: 70vh; overflow: auto;
  background: var(--bg-2); border: 1px solid var(--line-2); border-radius: 10px; padding: 14px 16px; box-shadow: 0 12px 40px rgba(0, 0, 0, .5);
  font-size: var(--fs-sm); font-weight: 400; color: var(--text-2); line-height: 1.65; text-align: left; white-space: normal;
}
.sheet-bg { position: fixed; inset: 0; z-index: 99; background: rgba(0, 0, 0, .5); }
.pop.sheet { position: fixed; left: 0; right: 0; bottom: 0; top: auto; width: 100%; max-height: 80vh; z-index: 100; border-radius: 14px 14px 0 0; padding: 18px 16px 24px; }
.close { position: absolute; right: 10px; top: 8px; border: none; background: transparent; color: var(--text-3); }
</style>
