import { onMounted, onUnmounted, ref, shallowRef, type Ref } from 'vue'

/** Calls fn immediately and every `ms` while mounted; pauses when the tab is hidden. */
export function usePolling<T>(fn: () => Promise<T>, ms: number) {
  const data: Ref<T | null> = shallowRef<T | null>(null)
  const error = ref('')
  const loading = ref(true)
  let timer: number | undefined
  let stopped = false
  async function tick() {
    if (stopped) return
    if (!document.hidden) {
      try {
        data.value = await fn()
        error.value = ''
      } catch (e: any) {
        error.value = e.message
      } finally {
        loading.value = false
      }
    }
    timer = window.setTimeout(tick, ms)
  }
  onMounted(tick)
  onUnmounted(() => { stopped = true; clearTimeout(timer) })
  return { data, error, loading, refresh: tick }
}
