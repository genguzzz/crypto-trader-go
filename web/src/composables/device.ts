import { onMounted, onUnmounted, ref } from 'vue'

/** PC client = desktop user agent + fine pointer with hover (mouse) + wide viewport. */
export function isDesktopClient(): boolean {
  const ua = navigator.userAgent
  const mobileUA = /Mobi|Android|iPhone|iPad|iPod|HarmonyOS|Windows Phone/i.test(ua)
  // iPadOS reports a Mac UA; touch points reveal it
  const iPadOS = /Macintosh/.test(ua) && navigator.maxTouchPoints > 1
  const mouse = window.matchMedia('(hover: hover) and (pointer: fine)').matches
  return !mobileUA && !iPadOS && mouse && window.innerWidth >= 900
}

export function useDesktop() {
  const desktop = ref(isDesktopClient())
  const on = () => (desktop.value = isDesktopClient())
  onMounted(() => window.addEventListener('resize', on))
  onUnmounted(() => window.removeEventListener('resize', on))
  return desktop
}
