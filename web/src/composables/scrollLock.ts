import { onUnmounted, watch, type Ref } from 'vue'

/**
 * Freeze the page behind an overlay while `active` is true (mobile: the window is the scroller, so a
 * swipe on a modal/sheet would otherwise scroll the page underneath). Pins <body> with position:fixed
 * — overflow:hidden alone is ignored by iOS Safari — and restores the scroll offset on release.
 * Ref-counted so nested overlays (an InfoTip sheet inside a Modal) don't unlock early.
 */
let locks = 0
let savedY = 0

function lock() {
  if (locks++ > 0) return
  savedY = window.scrollY
  const s = document.body.style
  s.position = 'fixed'; s.top = `-${savedY}px`; s.left = '0'; s.right = '0'; s.overflow = 'hidden'
}

function unlock() {
  if (locks === 0 || --locks > 0) return
  const s = document.body.style
  s.position = ''; s.top = ''; s.left = ''; s.right = ''; s.overflow = ''
  window.scrollTo(0, savedY)
}

export function useScrollLock(active: Ref<boolean> | (() => boolean)) {
  let held = false
  const set = (on: boolean) => { if (on === held) return; held = on; on ? lock() : unlock() }
  watch(active, v => set(!!v), { immediate: true })
  onUnmounted(() => set(false))
}
