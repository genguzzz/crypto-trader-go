import { onUnmounted, reactive, watchEffect } from 'vue'

/** Customisable layout parts registered by components; the top-bar layout menu lists and resets them. */
export interface LayoutEntry { label: string; customized: boolean; reset: () => void }
export const layoutRegistry = reactive<Record<string, LayoutEntry>>({})

/** Register a layout part for the lifetime of the calling component. */
export function useLayoutEntry(key: string, label: string, customized: () => boolean, reset: () => void) {
  const stop = watchEffect(() => {
    layoutRegistry[key] = { label, customized: customized(), reset }
  })
  onUnmounted(() => { stop(); delete layoutRegistry[key] })
}
