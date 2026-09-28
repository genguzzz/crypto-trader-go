import { ref } from 'vue'

/** `busy` is true while any call of a tracked async function is in flight (drives <Loading>). */
export function useBusy() {
  const busy = ref(false)
  let n = 0
  const track = <A extends unknown[], R>(f: (...a: A) => Promise<R>) => async (...a: A): Promise<R> => {
    n++
    busy.value = true
    try { return await f(...a) } finally { if (--n === 0) busy.value = false }
  }
  return { busy, track }
}
