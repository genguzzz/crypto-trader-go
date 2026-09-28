<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Tone } from './storage'

/**
 * Section navigation with scroll-spy. PC: sticky vertical list beside the content; narrow screens:
 * sticky horizontal chip bar under the top bar. Each item may carry a health dot.
 * Works with either scroller (PC scrolls `#app > main`, phones scroll the document) by listening
 * to scroll events in the capture phase and measuring section positions against the viewport.
 */
export interface NavItem { id: string; label: string; tone?: Tone | '' }
const props = defineProps<{ items: NavItem[] }>()
const active = ref(props.items[0]?.id ?? '')
const bar = ref<HTMLElement | null>(null)
let lockUntil = 0

function scrollerOf(el: HTMLElement): HTMLElement {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const o = getComputedStyle(p).overflowY
    if ((o === 'auto' || o === 'scroll') && p.scrollHeight > p.clientHeight) return p
  }
  return document.scrollingElement as HTMLElement
}

function spy() {
  if (Date.now() < lockUntil) return
  const els = props.items.map(i => document.getElementById(i.id)).filter(Boolean) as HTMLElement[]
  if (!els.length) return
  const sc = scrollerOf(els[0])
  const top = sc === document.scrollingElement ? 0 : sc.getBoundingClientRect().top
  const line = top + (window.innerWidth <= 900 ? 120 : 80)
  let cur = els[0].id
  for (const el of els) if (el.getBoundingClientRect().top <= line) cur = el.id
  if (sc.scrollTop + sc.clientHeight >= sc.scrollHeight - 4) cur = els[els.length - 1].id // at the bottom: last one wins
  active.value = cur
}

function go(id: string) {
  const el = document.getElementById(id)
  if (!el) return
  active.value = id
  lockUntil = Date.now() + 800 // don't let the smooth scroll flicker the highlight
  el.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// keep the active chip visible in the horizontal bar
watch(active, id => nextTick(() => {
  const b = bar.value
  const c = b?.querySelector<HTMLElement>(`[data-id="${id}"]`)
  if (b && c && b.scrollWidth > b.clientWidth) b.scrollTo({ left: c.offsetLeft - 16, behavior: 'smooth' })
}))

let raf = 0
const onScroll = () => { cancelAnimationFrame(raf); raf = requestAnimationFrame(spy) }
onMounted(() => { document.addEventListener('scroll', onScroll, true); window.addEventListener('resize', onScroll); spy() })
onUnmounted(() => { document.removeEventListener('scroll', onScroll, true); window.removeEventListener('resize', onScroll) })
defineExpose({ go })
</script>

<template>
  <nav ref="bar" class="snav" aria-label="页面分区">
    <button v-for="i in items" :key="i.id" type="button" class="si" :class="{ on: active === i.id }" :data-id="i.id"
      :aria-current="active === i.id ? 'location' : undefined" @click="go(i.id)">
      <span>{{ i.label }}</span>
      <span v-if="i.tone && i.tone !== 'ok'" class="dot" :class="i.tone"></span>
    </button>
  </nav>
</template>

<style scoped>
.snav { display: flex; flex-direction: column; gap: 2px; position: sticky; top: 16px; align-self: start; }
.si {
  all: unset; box-sizing: border-box; display: flex; align-items: center; justify-content: space-between; gap: 8px; cursor: pointer;
  height: 32px; padding: 0 12px; border-radius: 8px; color: var(--text-3); font-size: var(--fs-md); font-weight: 500;
  border-left: 2px solid transparent; transition: color .12s, background .12s;
}
@media (hover: hover) { .si:hover { color: var(--text-1); background: var(--bg-1); } }
.si.on { color: var(--text-1); background: var(--bg-1); border-left-color: var(--accent); }
.si:focus-visible { outline: 2px solid var(--accent); }

@media (max-width: 900px) {
  .snav {
    flex-direction: row; top: 48px; z-index: 5; gap: 6px; overflow-x: auto; scrollbar-width: none;
    margin: 0 -16px; padding: 8px 16px; background: var(--bg-0); border-bottom: 1px solid var(--line);
  }
  .snav::-webkit-scrollbar { display: none; }
  .si { flex: none; height: 28px; padding: 0 12px; border-radius: 14px; border: 1px solid var(--line-2); font-size: var(--fs-sm); }
  .si.on { border-color: var(--accent); background: var(--accent-bg); color: var(--text-1); }
}
</style>
