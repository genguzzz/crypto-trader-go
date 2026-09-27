<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useScrollLock } from '../composables/scrollLock'
const props = defineProps<{ open: boolean; title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
useScrollLock(() => props.open)
const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && props.open) emit('close') }
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="backdrop" @click.self="emit('close')">
        <div class="dialog" :class="{ wide }" role="dialog" :aria-label="title">
          <div class="dh">
            <span class="dt">{{ title }}</span>
            <button class="x" aria-label="关闭" @click="emit('close')">✕</button>
          </div>
          <div class="db"><slot /></div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.backdrop { position: fixed; inset: 0; z-index: 100; overscroll-behavior: contain; background: rgba(0, 0, 0, .55); display: flex; align-items: center; justify-content: center; padding: 16px; }
.dialog { width: min(640px, 100%); max-height: min(86vh, 900px); display: flex; flex-direction: column; background: var(--bg-1); border: 1px solid var(--line-2); border-radius: 12px; box-shadow: 0 20px 60px rgba(0, 0, 0, .5); }
.dialog.wide { width: min(880px, 100%); }
.dh { display: flex; align-items: center; justify-content: space-between; padding: 14px 18px; border-bottom: 1px solid var(--line); }
.dt { font-size: var(--fs-xl); font-weight: 600; }
.x { border: none; background: transparent; color: var(--text-3); font-size: 16px; padding: 2px 6px; }
.x:hover { color: var(--text-1); }
.db { padding: 14px 18px 18px; overflow: auto; overscroll-behavior: contain; -webkit-overflow-scrolling: touch; }
.fade-enter-active, .fade-leave-active { transition: opacity .15s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
@media (max-width: 900px) {
  .backdrop { align-items: flex-end; padding: 0; }
  .dialog, .dialog.wide { width: 100%; max-height: 88vh; border-radius: 14px 14px 0 0; }
}
</style>
