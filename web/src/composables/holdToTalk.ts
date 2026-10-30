import { ref } from 'vue'
import { useRecorder } from './recorder'
import { transcribe } from './ai'

/**
 * Tap vs press-and-hold on one button: a tap (released before HOLD_MS) calls onTap; holding records
 * voice until release, then transcribes it (Groq) and calls onText. Sliding up by CANCEL_PX while
 * holding arms "release to cancel".
 */
const HOLD_MS = 260
const CANCEL_PX = 70

export function useHoldToTalk(opts: { onTap?: () => void; onText: (text: string) => void; onError?: (msg: string) => void }) {
  const rec = useRecorder()
  const phase = ref<'idle' | 'arming' | 'rec' | 'busy'>('idle')
  const cancelZone = ref(false)
  let timer = 0
  let startY = 0
  let pid = -1

  function down(e: PointerEvent) {
    if (phase.value === 'busy' || e.button > 0) return
    pid = e.pointerId
    try { (e.currentTarget as HTMLElement).setPointerCapture(pid) } catch { /* synthetic or already released pointer */ }
    startY = e.clientY
    cancelZone.value = false
    phase.value = 'arming'
    timer = window.setTimeout(async () => {
      timer = 0
      if (phase.value !== 'arming') return
      phase.value = 'rec'
      navigator.vibrate?.(12)
      try {
        await rec.start()
        if (phase.value !== 'rec') rec.cancel() // released while the mic was opening
      } catch (err: any) {
        phase.value = 'idle'
        opts.onError?.(err?.name === 'NotAllowedError' ? '没有麦克风权限' : err?.message || String(err))
      }
    }, HOLD_MS)
  }

  function move(e: PointerEvent) {
    if (e.pointerId !== pid || phase.value !== 'rec') return
    cancelZone.value = startY - e.clientY > CANCEL_PX
  }

  async function up(e: PointerEvent) {
    if (e.pointerId !== pid) return
    pid = -1
    if (timer) {
      clearTimeout(timer)
      timer = 0
      phase.value = 'idle'
      opts.onTap?.()
      return
    }
    if (phase.value !== 'rec') return
    if (cancelZone.value) {
      rec.cancel()
      phase.value = 'idle'
      cancelZone.value = false
      return
    }
    phase.value = 'busy'
    try {
      const blob = await rec.stop()
      if (!blob) { opts.onError?.('说话时间太短'); return }
      const text = await transcribe(blob)
      if (text.trim()) opts.onText(text.trim())
      else opts.onError?.('没有识别到语音')
    } catch (err: any) {
      opts.onError?.(err?.message || String(err))
    } finally {
      phase.value = 'idle'
    }
  }

  function cancel() {
    if (timer) clearTimeout(timer)
    timer = 0
    pid = -1
    if (phase.value === 'rec') rec.cancel()
    if (phase.value !== 'busy') phase.value = 'idle'
    cancelZone.value = false
  }

  const handlers = { pointerdown: down, pointermove: move, pointerup: up, pointercancel: cancel, contextmenu: (e: Event) => e.preventDefault() }
  return { phase, cancelZone, levels: rec.levels, level: rec.level, handlers }
}
