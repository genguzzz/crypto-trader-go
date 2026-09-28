<script setup lang="ts">
/**
 * Loading indicator. `overlay` covers the parent (which must be position: relative) and keeps
 * the previous content visible underneath; otherwise it renders inline as a block. It appears
 * after a short delay so fast loads don't flash.
 */
withDefaults(defineProps<{ show?: boolean; text?: string; overlay?: boolean }>(), { show: true, text: '', overlay: false })
</script>

<template>
  <Transition name="ld">
    <div v-if="show" class="ld" :class="{ overlay }" role="status" :aria-label="text || '加载中'">
      <span class="spin"><i></i><i></i><i></i></span>
      <span v-if="text" class="t">{{ text }}</span>
    </div>
  </Transition>
</template>

<style scoped>
.ld { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 24px 0; color: var(--text-3); font-size: var(--fs-sm); }
.ld.overlay { position: absolute; inset: 0; z-index: 6; padding: 0; background: rgba(0, 0, 0, .28); pointer-events: none; }
/* three bars pulsing like a tiny volume histogram */
.spin { display: inline-flex; align-items: flex-end; gap: 3px; height: 16px; }
.spin i { width: 3px; height: 100%; border-radius: 1px; background: var(--accent); animation: bar 0.9s ease-in-out infinite; }
.spin i:nth-child(2) { animation-delay: .15s; } .spin i:nth-child(3) { animation-delay: .3s; }
@keyframes bar { 0%, 100% { transform: scaleY(.3); opacity: .5; } 50% { transform: scaleY(1); opacity: 1; } }
.spin i { transform-origin: bottom; }
.ld-enter-active { transition: opacity .2s ease .15s; } /* delay: fast loads never show it */
.ld-leave-active { transition: opacity .15s ease; }
.ld-enter-from, .ld-leave-to { opacity: 0; }
@media (prefers-reduced-motion: reduce) { .spin i { animation: none; opacity: .8; } }
</style>
