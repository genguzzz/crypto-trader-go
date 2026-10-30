import { ref } from 'vue'
import { isNative } from '../native'

/**
 * Press-and-hold voice capture: MediaRecorder for the audio (audio/mp4 on iOS, webm elsewhere) and
 * an AnalyserNode for a live level (0–1) that drives the waveform. In the iOS app the microphone
 * needs NSMicrophoneUsageDescription in Info.plist (IPA ≥ the build that added it).
 */
export function useRecorder() {
  const level = ref(0)
  const levels = ref<number[]>(Array(24).fill(0)) // recent levels, newest last (waveform bars)
  const active = ref(false)
  let rec: MediaRecorder | null = null
  let stream: MediaStream | null = null
  let ctx: AudioContext | null = null
  let raf = 0
  let chunks: Blob[] = []
  let startedAt = 0

  async function start() {
    if (active.value) return
    if (isNative && !__NATIVE_MIC__) throw new Error('语音需要安装新版 App（全能签里更新 OKX Helper）')
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') throw new Error('此设备不支持录音（App 需要更新到含麦克风权限的版本）')
    stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } })
    const type = ['audio/mp4', 'audio/webm;codecs=opus', 'audio/webm'].find(t => MediaRecorder.isTypeSupported?.(t))
    rec = new MediaRecorder(stream, type ? { mimeType: type } : undefined)
    chunks = []
    rec.ondataavailable = e => { if (e.data.size) chunks.push(e.data) }
    rec.start(250)
    startedAt = performance.now()
    active.value = true
    try {
      ctx = new AudioContext()
      const src = ctx.createMediaStreamSource(stream)
      const an = ctx.createAnalyser()
      an.fftSize = 512
      src.connect(an)
      const buf = new Uint8Array(an.fftSize)
      let frame = 0
      const tick = () => {
        an.getByteTimeDomainData(buf)
        let sum = 0
        for (const v of buf) sum += ((v - 128) / 128) ** 2
        const rms = Math.sqrt(sum / buf.length)
        level.value = Math.min(1, level.value * 0.6 + Math.min(1, rms * 4) * 0.4)
        if (++frame % 3 === 0) levels.value = [...levels.value.slice(1), level.value]
        raf = requestAnimationFrame(tick)
      }
      tick()
    } catch { /* no level meter: recording still works */ }
  }

  function cleanup() {
    cancelAnimationFrame(raf)
    stream?.getTracks().forEach(t => t.stop())
    ctx?.close().catch(() => {})
    stream = null
    ctx = null
    active.value = false
    level.value = 0
    levels.value = Array(24).fill(0)
  }

  /** Stop and return the recording (null when shorter than 0.5 s). */
  function stop(): Promise<Blob | null> {
    return new Promise(res => {
      const r = rec
      rec = null
      if (!r || r.state === 'inactive') { cleanup(); return res(null) }
      const long = performance.now() - startedAt >= 500
      r.onstop = () => {
        const blob = new Blob(chunks, { type: r.mimeType || 'audio/webm' })
        cleanup()
        res(long && blob.size > 0 ? blob : null)
      }
      r.stop()
    })
  }

  function cancel() {
    const r = rec
    rec = null
    if (r && r.state !== 'inactive') { r.onstop = null; r.stop() }
    cleanup()
  }

  return { level, levels, active, start, stop, cancel }
}
