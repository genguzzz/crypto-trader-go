import { onMounted, onUnmounted, ref, type Ref } from 'vue'
import { useScrollLock } from './scrollLock'

/**
 * Full screen for a panel: the native Fullscreen API where it works (desktop, Android), otherwise —
 * iPhone Safari has no element fullscreen, and requestFullscreen may also reject — a CSS
 * pseudo-fullscreen: the element gets class `fs-pseudo` (style.css: fixed overlay over the viewport
 * with safe-area insets) and the page behind is scroll-locked. Esc or toggle() leaves either way.
 * Charts inside use autoSize, so they follow the new size by themselves.
 */
export function useFullscreen(el: Ref<HTMLElement | undefined | null>) {
  const full = ref(false)
  const pseudo = ref(false)
  useScrollLock(pseudo)
  async function enter() {
    const e = el.value as (HTMLElement & { webkitRequestFullscreen?: () => Promise<void> | void }) | null | undefined
    if (!e) return
    const req = e.requestFullscreen?.bind(e) ?? e.webkitRequestFullscreen?.bind(e)
    if (req) {
      try {
        await req()
        if (document.fullscreenElement || (document as any).webkitFullscreenElement) { full.value = true; return }
      } catch { /* not allowed for this element / browser: fall back */ }
    }
    pseudo.value = full.value = true
  }
  async function exit() {
    if (document.fullscreenElement) await document.exitFullscreen().catch(() => {})
    pseudo.value = full.value = false
  }
  const toggle = () => (full.value ? exit() : enter())
  const onChange = () => { if (!document.fullscreenElement && !(document as any).webkitFullscreenElement && !pseudo.value) full.value = false }
  const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && pseudo.value) { e.stopPropagation(); exit() } }
  onMounted(() => {
    document.addEventListener('fullscreenchange', onChange)
    document.addEventListener('webkitfullscreenchange', onChange)
    window.addEventListener('keydown', onKey, true)
  })
  onUnmounted(() => {
    document.removeEventListener('fullscreenchange', onChange)
    document.removeEventListener('webkitfullscreenchange', onChange)
    window.removeEventListener('keydown', onKey, true)
    if (full.value) exit()
  })
  return { full, pseudo, toggle, exit }
}
