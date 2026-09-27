<script setup lang="ts">
import { ref, watch } from 'vue'
import { useDesktop } from '../composables/device'

/**
 * Renders named slots as a vertical stack of panels. On PC clients each panel gets a drag handle;
 * dragging with the mouse reorders panels and the order is saved in localStorage.
 * Touch/mobile clients get the default order without handles.
 */
const props = defineProps<{ storageKey: string; ids: string[]; labels?: Record<string, string> }>()
const desktop = useDesktop()

function load(): string[] {
  try {
    const saved: string[] = JSON.parse(localStorage.getItem(props.storageKey) || '[]')
    const known = saved.filter(id => props.ids.includes(id))
    return [...known, ...props.ids.filter(id => !known.includes(id))] // new panels appended
  } catch {
    return [...props.ids]
  }
}
const order = ref<string[]>(load())
watch(order, v => localStorage.setItem(props.storageKey, JSON.stringify(v)), { deep: true })
const customized = () => order.value.join() !== props.ids.join()
function reset() { order.value = [...props.ids] }

const dragging = ref<string | null>(null)
const armed = ref<string | null>(null) // handle pressed → panel becomes draggable
const target = ref<{ id: string; after: boolean } | null>(null)

function onDragStart(e: DragEvent, id: string) {
  if (armed.value !== id) { e.preventDefault(); return }
  dragging.value = id
  e.dataTransfer?.setData('text/plain', id)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function onDragOver(e: DragEvent, id: string) {
  if (!dragging.value) return
  e.preventDefault()
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
  target.value = { id, after: e.clientY > r.top + r.height / 2 }
}
function onDrop(e: DragEvent) {
  e.preventDefault()
  const from = dragging.value, t = target.value
  if (from && t && from !== t.id) {
    const next = order.value.filter(x => x !== from)
    const i = next.indexOf(t.id) + (t.after ? 1 : 0)
    next.splice(i, 0, from)
    order.value = next
  }
  end()
}
function end() { dragging.value = null; armed.value = null; target.value = null }
function move(id: string, d: number) {
  const i = order.value.indexOf(id), j = i + d
  if (j < 0 || j >= order.value.length) return
  const next = [...order.value]
  ;[next[i], next[j]] = [next[j], next[i]]
  order.value = next
}
</script>

<template>
  <div class="stack">
    <div v-if="desktop && customized()" class="layoutbar">
      <span class="note">已按你的拖拽顺序排列</span>
      <button class="small" @click="reset">恢复默认布局</button>
    </div>
    <div
      v-for="id in order"
      :key="id"
      class="slot"
      :class="{
        dragging: dragging === id,
        'drop-before': target?.id === id && !target.after && dragging !== id,
        'drop-after': target?.id === id && target.after && dragging !== id,
      }"
      :draggable="desktop && armed === id"
      @dragstart="onDragStart($event, id)"
      @dragover="onDragOver($event, id)"
      @drop="onDrop"
      @dragend="end"
    >
      <div v-if="desktop" class="handle" :title="`按住拖动「${labels?.[id] || id}」面板；双击上移一位`"
           @mousedown="armed = id" @mouseup="armed = null" @dblclick="move(id, -1)">
        <span class="grip">⋮⋮</span>
      </div>
      <slot :name="id" />
    </div>
  </div>
</template>

<style scoped>
.stack { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
.slot { position: relative; min-width: 0; border-radius: 8px; transition: opacity 0.15s; }
.slot.dragging { opacity: 0.35; }
.slot.drop-before::before, .slot.drop-after::after {
  content: ''; position: absolute; left: 0; right: 0; height: 3px; background: var(--accent); border-radius: 2px; z-index: 5;
}
.slot.drop-before::before { top: -8px; }
.slot.drop-after::after { bottom: -8px; }
.handle {
  position: absolute; top: 0; bottom: 0; left: -11px; width: 10px; z-index: 4; display: flex; align-items: flex-start;
  justify-content: center; padding-top: 10px; cursor: grab; user-select: none; border-radius: 4px; color: var(--muted); opacity: 0.25;
}
.slot:hover .handle { opacity: 1; background: var(--panel2); }
.handle:active { cursor: grabbing; }
.grip { font-size: 10px; line-height: 1; writing-mode: vertical-rl; letter-spacing: -2px; }
.layoutbar { display: flex; justify-content: flex-end; align-items: center; gap: 8px; margin-bottom: -4px; }
.small { font-size: 12px; padding: 1px 8px; }
</style>
