<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useScrollLock } from '../composables/scrollLock'

/**
 * PC: dialog anchored near the top (not vertically centred), so switching content of a different
 * height never makes it jump. Touch/narrow: bottom drawer of fixed height that slides up; drag the
 * header (grab handle) down to dismiss.
 */
const props = defineProps<{ open: boolean; title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
useScrollLock(() => props.open)
const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && props.open) emit('close') }
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))

// swipe-down-to-close on the drawer header
const drag = ref(0)
let startY = 0
let dragging = false
function onDown(e: PointerEvent) {
  if (e.pointerType === 'mouse') return
  dragging = true
  startY = e.clientY
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}
function onMove(e: PointerEvent) { if (dragging) drag.value = Math.max(0, e.clientY - startY) }
function onUp() {
  if (!dragging) return
  dragging = false
  if (drag.value > 90) emit('close')
  drag.value = 0
}
</script>

<template>
  <Teleport to="body">
    <Transition name="sheet">
      <div v-if="open" class="backdrop" @click.self="emit('close')">
        <div class="dialog" :class="{ wide, dragging: drag > 0 }" role="dialog" :aria-label="title" :style="drag ? { transform: `translateY(${drag}px)` } : undefined">
          <div class="dh" @pointerdown="onDown" @pointermove="onMove" @pointerup="onUp" @pointercancel="onUp">
            <span class="grab" aria-hidden="true"></span>
            <span class="dt">{{ title }}</span>
            <button class="x" aria-label="关闭" @pointerdown.stop @click="emit('close')">✕</button>
          </div>
          <div class="db"><slot /></div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.backdrop { position: fixed; inset: 0; z-index: 100; overscroll-behavior: contain; background: rgba(0, 0, 0, .55); display: flex; align-items: flex-start; justify-content: center; padding: 8vh 16px 16px; }
.dialog { width: min(640px, 100%); max-height: min(84vh, 900px); display: flex; flex-direction: column; background: var(--bg-1); border: 1px solid var(--line-2); border-radius: 12px; box-shadow: 0 20px 60px rgba(0, 0, 0, .5); }
.dialog.wide { width: min(880px, 100%); }
.dh { position: relative; display: flex; align-items: center; justify-content: space-between; padding: 14px 18px; border-bottom: 1px solid var(--line); }
.grab { display: none; }
.dt { font-size: var(--fs-xl); font-weight: 600; }
.x { border: none; background: transparent; color: var(--text-3); font-size: 16px; padding: 2px 6px; }
@media (hover: hover) { .x:hover { color: var(--text-1); } }
.db { padding: 14px 18px 18px; overflow: auto; overscroll-behavior: contain; -webkit-overflow-scrolling: touch; }
.sheet-enter-active, .sheet-leave-active { transition: opacity .2s; }
.sheet-enter-from, .sheet-leave-to { opacity: 0; }
@media (max-width: 900px) {
  .backdrop { align-items: flex-end; padding: 0; }
  /* fixed height: switching tabs inside never resizes the drawer */
  .dialog, .dialog.wide { width: 100%; height: 88vh; height: 88dvh; max-height: none; border-radius: 16px 16px 0 0; border-bottom: none;
    padding-bottom: env(safe-area-inset-bottom); transition: transform .25s cubic-bezier(.2, .8, .2, 1); }
  .dialog.dragging { transition: none; }
  .dh { padding: 18px 16px 12px; touch-action: none; }
  .grab { display: block; position: absolute; top: 6px; left: 50%; width: 36px; height: 4px; margin-left: -18px; border-radius: 2px; background: var(--line-2); }
  .db { flex: 1; padding: 12px 16px 20px; }
  .sheet-enter-active, .sheet-leave-active { transition: opacity .25s; }
  .sheet-enter-from .dialog, .sheet-leave-to .dialog { transform: translateY(100%); }
}
</style>
