<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Sparkles, Mic, X } from 'lucide-vue-next'
import { ai, send, resume } from '../../composables/ai'
import { useHoldToTalk } from '../../composables/holdToTalk'

/**
 * Floating AI orb centred above the phone tab bar (bottom centre on desktop). Tap opens the chat panel;
 * press and hold talks: a capsule with a live waveform shows above it, slide up to cancel, release
 * to transcribe and send.
 */
const toast = ref('')
let toastT = 0
function flash(msg: string) {
  toast.value = msg
  clearTimeout(toastT)
  toastT = window.setTimeout(() => (toast.value = ''), 2600)
}
const { phase, cancelZone, levels, level, handlers } = useHoldToTalk({
  onTap: () => { ai.open = true; ai.unread = false },
  onText: t => { ai.open = true; ai.unread = false; send(t) },
  onError: flash,
})
const rec = computed(() => phase.value === 'rec')
// an answer that finished while the panel was closed (or the app was away): say so once
watch(() => ai.unread, u => { if (u) flash('AI 回答好了，点按钮查看') })
onMounted(() => resume()) // app start: pick up a question left running
const style = computed(() => ({ '--lv': String(level.value) }))
</script>

<template>
  <div class="ai-orb-wrap" :class="{ hidden: ai.open }">
    <button type="button" class="orb" :class="{ rec, busy: phase === 'busy', run: ai.running, cancel: rec && cancelZone, arming: phase === 'arming' }" :style="style"
      aria-label="AI 助手：点按打开，长按说话" v-on="handlers">
      <span class="glow"></span>
      <span class="ring"></span>
      <span class="core">
        <Mic v-if="rec" :size="22" />
        <Sparkles v-else :size="22" />
      </span>
      <span v-if="ai.running" class="sat"></span>
      <span v-if="ai.unread && !ai.running" class="dot"></span>
      <span v-if="rec" class="ripple r1"></span><span v-if="rec" class="ripple r2"></span>
    </button>
  </div>
  <Transition name="cap">
    <div v-if="rec || phase === 'busy'" class="capsule" :class="{ cancel: cancelZone }" role="status">
      <template v-if="rec">
        <div class="wave"><i v-for="(v, i) in levels" :key="i" :style="{ height: 4 + v * 30 + 'px' }"></i></div>
        <span class="hint"><X v-if="cancelZone" :size="13" />{{ cancelZone ? '松开取消' : '松开发送 · 上滑取消' }}</span>
      </template>
      <span v-else class="hint"><span class="spin"></span>识别中…</span>
    </div>
  </Transition>
  <Transition name="cap"><div v-if="toast" class="capsule toast">{{ toast }}</div></Transition>
</template>

<style scoped>
.ai-orb-wrap { position: fixed; left: 50%; margin-left: -27px; bottom: 26px; z-index: 30; transition: opacity .2s, transform .25s; }
.ai-orb-wrap.hidden { opacity: 0; transform: scale(.6); pointer-events: none; }
@media (max-width: 900px) { .ai-orb-wrap { bottom: calc(66px + env(safe-area-inset-bottom)); } }

.orb { all: unset; position: relative; display: grid; place-items: center; width: 54px; height: 54px; border-radius: 50%; cursor: pointer;
  touch-action: none; user-select: none; -webkit-user-select: none; -webkit-touch-callout: none; -webkit-tap-highlight-color: transparent;
  transition: transform .28s cubic-bezier(.3, 1.6, .5, 1); }
.orb:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.orb.arming { transform: scale(.92); }
.orb.rec { transform: scale(calc(1.22 + var(--lv) * .18)); transition: transform .08s linear; }

/* rotating aurora ring + blurred glow of the same gradient */
.ring, .glow { position: absolute; inset: 0; border-radius: 50%;
  background: conic-gradient(from 0deg, #bcff2f, #2fe0ff, #9b7df0, #ff5fa2, #f5c542, #bcff2f); animation: spin 6s linear infinite; }
.glow { inset: -6px; filter: blur(14px); opacity: .45; animation: breathe 3.2s ease-in-out infinite; }
.orb.rec .ring { animation-duration: 1.4s; }
.orb.rec .glow { animation: none; }
.orb.rec .glow { opacity: calc(.55 + var(--lv) * .45); inset: calc(-8px - var(--lv) * 10px); }
.orb.busy .ring, .orb.run .ring { animation-duration: 1.8s; }
.orb.cancel .ring, .orb.cancel .glow { background: conic-gradient(#ca3f64, #ff7a59, #ca3f64); }

.core { position: absolute; inset: 3px; border-radius: 50%; display: grid; place-items: center; color: #f4ffe0;
  background: radial-gradient(circle at 32% 28%, rgba(255, 255, 255, .22), transparent 42%), radial-gradient(circle at 50% 60%, #1a1f17, #050505 72%);
  box-shadow: inset 0 0 12px rgba(188, 255, 47, .18), inset 0 -6px 14px rgba(155, 125, 240, .18); }
.core :deep(svg) { filter: drop-shadow(0 0 6px rgba(188, 255, 47, .55)); }
.orb.busy .core::after { content: ''; position: absolute; inset: 0; border-radius: 50%;
  background: linear-gradient(110deg, transparent 30%, rgba(255, 255, 255, .18) 50%, transparent 70%); background-size: 220% 100%; animation: shimmer 1.1s linear infinite; }

/* agent running: a satellite circles the orb */
.sat { position: absolute; inset: -5px; border-radius: 50%; animation: spin 1.6s linear infinite; pointer-events: none; }
.sat::before { content: ''; position: absolute; top: -1px; left: 50%; width: 7px; height: 7px; margin-left: -3.5px; border-radius: 50%; background: #bcff2f; box-shadow: 0 0 8px #bcff2f; }
.dot { position: absolute; top: 2px; right: 2px; width: 11px; height: 11px; border-radius: 50%; background: var(--warn); border: 2px solid #000; }

.ripple { position: absolute; inset: 0; border-radius: 50%; border: 2px solid rgba(188, 255, 47, .55); animation: ripple 1.6s ease-out infinite; pointer-events: none; }
.ripple.r2 { animation-delay: .8s; }
.orb.cancel .ripple { border-color: rgba(202, 63, 100, .6); }

.capsule { position: fixed; left: 50%; bottom: calc(170px + env(safe-area-inset-bottom)); transform: translateX(-50%); z-index: 31;
  display: grid; justify-items: center; gap: 8px; min-width: 210px; padding: 14px 20px 12px; border-radius: 22px;
  background: rgba(16, 18, 14, .88); backdrop-filter: blur(16px); -webkit-backdrop-filter: blur(16px);
  border: 1px solid rgba(188, 255, 47, .25); box-shadow: 0 10px 40px rgba(0, 0, 0, .6), 0 0 30px rgba(188, 255, 47, .12); pointer-events: none; }
.capsule.cancel { border-color: rgba(202, 63, 100, .55); box-shadow: 0 10px 40px rgba(0, 0, 0, .6), 0 0 30px rgba(202, 63, 100, .25); }
.capsule.toast { padding: 10px 16px; min-width: 0; font-size: var(--fs-sm); color: var(--text-1); border-color: var(--line-2); }
.wave { display: flex; align-items: center; gap: 3px; height: 36px; }
.wave i { width: 4px; border-radius: 2px; background: linear-gradient(180deg, #bcff2f, #2fe0ff); transition: height .08s linear; }
.capsule.cancel .wave i { background: #ca3f64; }
.hint { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-sm); color: var(--text-2); }
.capsule.cancel .hint { color: #ff8aa5; }
.spin { width: 12px; height: 12px; border-radius: 50%; border: 2px solid var(--line-2); border-top-color: var(--accent); animation: spin .8s linear infinite; }

.cap-enter-active, .cap-leave-active { transition: opacity .18s, transform .25s cubic-bezier(.3, 1.4, .5, 1); }
.cap-enter-from, .cap-leave-to { opacity: 0; transform: translate(-50%, 14px) scale(.9); }

@keyframes spin { to { transform: rotate(360deg); } }
@keyframes breathe { 0%, 100% { opacity: .3; transform: scale(.94); } 50% { opacity: .62; transform: scale(1.08); } }
@keyframes ripple { from { transform: scale(1); opacity: .9; } to { transform: scale(2.1); opacity: 0; } }
@keyframes shimmer { from { background-position: 120% 0; } to { background-position: -120% 0; } }
@media (prefers-reduced-motion: reduce) { .ring, .glow, .sat, .ripple { animation: none !important; } }
</style>
