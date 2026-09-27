<script setup lang="ts">
import { ref } from 'vue'
import { PopoverRoot, PopoverTrigger, PopoverPortal, PopoverContent } from 'reka-ui'
import { useDesktop } from '../composables/device'
import { useScrollLock } from '../composables/scrollLock'

/**
 * ⓘ icon. PC: hover opens a Reka popover (portal + collision detection, so it never overflows the
 * viewport, e.g. at the top-right corner). Touch: tap opens a bottom sheet. Content via default slot.
 */
defineProps<{ label?: string }>()
const desktop = useDesktop()
const open = ref(false)
useScrollLock(() => open.value && !desktop.value)
let t: number | undefined
const enter = () => { if (desktop.value) { clearTimeout(t); open.value = true } }
const leave = () => { if (desktop.value) t = window.setTimeout(() => (open.value = false), 150) }
</script>

<template>
  <span class="tip">
    <PopoverRoot v-if="desktop" v-model:open="open">
      <PopoverTrigger as-child>
        <button class="i" :title="label" @mouseenter="enter" @mouseleave="leave" @click.stop>ⓘ<span v-if="label" class="il">{{ label }}</span></button>
      </PopoverTrigger>
      <PopoverPortal>
        <PopoverContent class="tip-pop" side="bottom" align="start" :side-offset="8" :collision-padding="12" @mouseenter="enter" @mouseleave="leave" @open-auto-focus.prevent>
          <slot />
        </PopoverContent>
      </PopoverPortal>
    </PopoverRoot>
    <template v-else>
      <button class="i" :aria-expanded="open" :title="label" @click.stop="open = !open">ⓘ<span v-if="label" class="il">{{ label }}</span></button>
      <Teleport to="body">
        <div v-if="open" class="tip-sheet-bg" @click="open = false"></div>
        <div v-if="open" class="tip-pop sheet" @click.stop>
          <button class="tip-close" @click="open = false">✕</button>
          <slot />
        </div>
      </Teleport>
    </template>
  </span>
</template>

<style scoped>
.tip { position: relative; display: inline-flex; }
.i { border: none; background: transparent; color: var(--text-3); padding: 0 4px; font-size: 14px; line-height: 1; display: inline-flex; align-items: center; gap: 4px; }
.i[data-state="open"], .i[aria-expanded="true"] { color: var(--accent); }
@media (hover: hover) { .i:hover { color: var(--accent); } }
.il { font-size: var(--fs-sm); }
</style>

<style>
.tip-pop {
  z-index: 140; width: min(520px, calc(100vw - 24px)); max-height: min(70vh, var(--reka-popover-content-available-height, 70vh)); overflow: auto;
  background: var(--bg-2); border: 1px solid var(--line-2); border-radius: 10px; padding: 14px 16px; box-shadow: 0 12px 40px rgba(0, 0, 0, .5);
  font-size: var(--fs-sm); font-weight: 400; color: var(--text-2); line-height: 1.65; text-align: left; white-space: normal; letter-spacing: normal;
}
.tip-sheet-bg { position: fixed; inset: 0; z-index: 139; background: rgba(0, 0, 0, .5); }
.tip-pop.sheet { overscroll-behavior: contain; position: fixed; left: 0; right: 0; bottom: 0; width: 100%; max-height: 80vh; border-radius: 14px 14px 0 0; padding: 18px 16px 24px; }
.tip-close { position: absolute; right: 10px; top: 8px; border: none; background: transparent; color: var(--text-3); }
</style>
