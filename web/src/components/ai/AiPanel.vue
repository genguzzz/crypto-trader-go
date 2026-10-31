<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import { X, History, Brain, Plus, SquarePen, ArrowUp, Square, Mic, CircleAlert, Copy, Trash2, Sparkles, ChartCandlestick, ChevronRight } from 'lucide-vue-next'
import { ai, send, retry, resume, stop, newChat, openChat, loadChats, deleteChat, textOf, setModel, AI_MODELS, notes, loadNotes, addNote, deleteNote, type Msg, type Part } from '../../composables/ai'
import { useTapToTalk } from '../../composables/holdToTalk'
import AiSteps from './AiSteps.vue'
import AiBlock from './AiBlock.vue'
import { segments } from './blocks'
import { useDesktop } from '../../composables/device'
import { useRouter } from 'vue-router'

/** AI chat: bottom sheet on phones (drag the header down to close), side panel on desktop. */
const desktop = useDesktop()
const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
// Links to app paths ([SOL](/instrument/SOL-USDT-SWAP)) become in-app buttons; others open outside.
const APP_PATH = /^\/(instrument\/[A-Z0-9-]+|rules(\/[\w-]+)?|alerts|trading(\/[\w/-]+)?|status|settings|help\/[\w/-]+)?$/
const defaultLink = md.renderer.rules.link_open
md.renderer.rules.link_open = (t, i, o, e, s) => {
  const href = String(t[i].attrGet('href') || '')
  if (APP_PATH.test(href)) {
    t[i].attrSet('class', href.startsWith('/instrument/') ? 'app-link inst' : 'app-link')
    t[i].attrSet('data-route', href)
  } else {
    t[i].attrSet('target', '_blank')
    t[i].attrSet('rel', 'noopener')
  }
  return defaultLink ? defaultLink(t, i, o, e, s) : s.renderToken(t, i, o)
}
const router = useRouter()
function onBlockNav() { if (!desktop.value) ai.open = false }
function onMdClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest<HTMLAnchorElement>('a.app-link')
  if (!a) return
  e.preventDefault()
  router.push(a.dataset.route!)
  if (!desktop.value) ai.open = false // phone: show the page; the orb brings the chat back
}

const input = ref('')
const ta = ref<HTMLTextAreaElement>()
const scroller = ref<HTMLElement>()
const showHistory = ref(false)
const toast = ref('')
let toastT = 0
function flash(m: string) { toast.value = m; clearTimeout(toastT); toastT = window.setTimeout(() => (toast.value = ''), 2600) }

const sym = computed(() => ai.instId.split('-')[0])
const ONE_CLICK = '一键分析当前合约：完整的多空分析、风险和交易计划'
const suggestions = computed(() => ai.instId
  ? [`${sym.value} 资金费和 OI 最近有什么变化？`, `${sym.value} 上方和下方的清算密集区在哪？`, `${sym.value} 最近有什么新闻或利空？`]
  : ['今天哪些币逼空风险高、别空？', '大盘怎么样？BTC 和 ETH 的资金费与 OI', '监控池里哪些币多头最拥挤？'])

function submit(text = input.value) {
  if (!text.trim() || ai.running) return
  send(text)
  input.value = ''
  nextTick(autosize)
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && desktop.value) { e.preventDefault(); submit() }
}
function autosize() {
  const el = ta.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(140, el.scrollHeight) + 'px'
}
const talk = useTapToTalk({ onText: t => submit(t), onError: flash })
watch(() => ai.open, o => { if (!o) talk.cancel() })

// ---- follow the stream when the reader is near the bottom ----
let stick = true
function onScroll() {
  const el = scroller.value
  if (el) stick = el.scrollHeight - el.scrollTop - el.clientHeight < 80
}
watch(() => [ai.msgs.length, ai.msgs[ai.msgs.length - 1]?.parts.length, textOf(ai.msgs[ai.msgs.length - 1] || { role: 'user', parts: [] }).length], () => {
  if (stick) nextTick(() => scroller.value?.scrollTo({ top: scroller.value.scrollHeight }))
})
watch(() => ai.chatId, () => { stick = true })
watch(() => ai.open, o => {
  if (!o) return
  ai.unread = false
  stick = true
  resume() // reopen the chat left on screen / catch up with an answer that finished meanwhile
  nextTick(() => scroller.value?.scrollTo({ top: scroller.value!.scrollHeight }))
})

// ---- history ----
const showNotes = ref(false)
const noteInput = ref('')
async function toggleNotes() {
  showNotes.value = !showNotes.value
  showHistory.value = false
  if (showNotes.value) await loadNotes().catch(e => flash(e.message))
}
async function saveNote() {
  const t = noteInput.value.trim()
  if (!t) return
  try { await addNote(t); noteInput.value = '' } catch (e: any) { flash(e.message) }
}
async function removeNote(id: number) { await deleteNote(id).catch(e => flash(e.message)) }
const noteAgo = (t: number) => new Date(t).toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' })

async function toggleHistory() {
  showNotes.value = false
  showHistory.value = !showHistory.value
  if (showHistory.value) await loadChats().catch(e => flash(e.message))
}
async function pick(id: number) {
  showHistory.value = false
  await openChat(id).catch(e => flash(e.message))
}
async function remove(id: number) { await deleteChat(id).catch(e => flash(e.message)) }
function fresh() { newChat(); showHistory.value = false; showNotes.value = false; nextTick(() => ta.value?.focus()) }
const ago = (t: number) => {
  const s = (Date.now() - t) / 1000
  return s < 60 ? '刚刚' : s < 3600 ? `${Math.floor(s / 60)} 分钟前` : s < 86400 ? `${Math.floor(s / 3600)} 小时前` : `${Math.floor(s / 86400)} 天前`
}

// ---- message parts: consecutive tool calls form one step group ----
type Group = { k: 'text'; v: string } | { k: 'tools'; items: Extract<Part, { t: 'tool' }>[] }
function groups(m: Msg): Group[] {
  const out: Group[] = []
  for (const p of m.parts) {
    if (p.t === 'text') { if (p.v.trim()) out.push({ k: 'text', v: p.v }) }
    else {
      const g = out[out.length - 1]
      if (g?.k === 'tools') g.items.push(p)
      else out.push({ k: 'tools', items: [p] })
    }
  }
  return out
}
async function copy(m: Msg) {
  try { await navigator.clipboard.writeText(textOf(m)); flash('已复制') } catch { flash('复制失败') }
}

// ---- phone: freeze the page behind the sheet (iOS scrolls the body through overflow:hidden, so pin it) ----
let savedY = 0
function lockPage(on: boolean) {
  const b = document.body
  if (on) {
    savedY = window.scrollY
    Object.assign(b.style, { position: 'fixed', top: `-${savedY}px`, left: '0', right: '0', overflow: 'hidden' })
  } else if (b.style.position === 'fixed') {
    Object.assign(b.style, { position: '', top: '', left: '', right: '', overflow: '' })
    window.scrollTo(0, savedY)
  }
}
watch(() => ai.open && !desktop.value, lockPage, { immediate: true })
onUnmounted(() => lockPage(false))

// ---- "still working" cues: a thinking line when nothing visibly moves, a caret while text streams ----
const now = ref(Date.now())
let tick = 0
watch(() => ai.running, r => {
  clearInterval(tick)
  if (r) tick = window.setInterval(() => (now.value = Date.now()), 400)
}, { immediate: true })
onUnmounted(() => clearInterval(tick))
function phaseOf(m: Msg): 'thinking' | 'typing' | 'tool' | null {
  if (!m.live) return null
  const p = m.parts[m.parts.length - 1]
  if (p?.t === 'tool' && p.ok == null) return 'tool'
  if (p?.t === 'text' && now.value - ai.lastDelta < 1500) return 'typing'
  return 'thinking'
}

// the orb opens the sheet on pointerup; the click that follows lands on the backdrop and must not close it
const bdDown = ref(false)

// ---- drag the sheet header down to close (phone) ----
const dragY = ref(0)
let y0 = -1
function dragStart(e: PointerEvent) {
  if (desktop.value) return
  y0 = e.clientY
  try { (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId) } catch { /* ignore */ }
}
function dragMove(e: PointerEvent) { if (y0 >= 0) dragY.value = Math.max(0, e.clientY - y0) }
function dragEnd() { if (y0 < 0) return; y0 = -1; if (dragY.value > 110) ai.open = false; dragY.value = 0 }
</script>

<template>
  <Teleport to="body">
    <Transition name="fade"><div v-if="ai.open && !desktop" class="ai-backdrop" @touchmove.prevent @pointerdown="bdDown = true" @click="bdDown && (ai.open = false); bdDown = false"></div></Transition>
    <Transition :name="desktop ? 'side' : 'sheet'">
      <section v-if="ai.open" class="ai-panel" :class="{ desk: desktop }" :style="dragY ? { transform: `translateY(${dragY}px)`, transition: 'none' } : undefined" aria-label="AI 研究助手">
        <header class="hd" @pointerdown="dragStart" @pointermove="dragMove" @pointerup="dragEnd" @pointercancel="dragEnd">
          <i v-if="!desktop" class="grab"></i>
          <span class="ttl"><span class="mini-orb"></span>{{ showNotes ? '偏好与记忆' : showHistory ? '历史对话' : ai.title || 'AI 研究助手' }}</span>
          <span v-if="!showHistory && !showNotes" class="models" role="radiogroup" aria-label="模型" @pointerdown.stop>
            <button v-for="o in AI_MODELS" :key="o.value" type="button" role="radio" :aria-checked="ai.model === o.value" :class="{ on: ai.model === o.value }" @click="setModel(o.value)">{{ o.label }}</button>
          </span>
          <span class="acts" @pointerdown.stop>
            <button type="button" class="ib" :class="{ on: showNotes }" title="偏好与记忆" aria-label="偏好与记忆" @click="toggleNotes"><Brain :size="17" /></button>
            <button type="button" class="ib" :class="{ on: showHistory }" title="历史对话" aria-label="历史对话" @click="toggleHistory"><History :size="17" /></button>
            <button type="button" class="ib" title="新对话" aria-label="新对话" @click="fresh"><SquarePen :size="17" /></button>
            <button type="button" class="ib" title="关闭" aria-label="关闭" @click="ai.open = false"><X :size="18" /></button>
          </span>
        </header>

        <div v-if="showNotes" class="body hist">
          <p class="muted note-tip">AI 每次回答都会参考这些偏好；在对话里说"记住…/我喜欢…"也会自动加到这里。</p>
          <div class="note-add">
            <input v-model="noteInput" placeholder="例如：杠杆偏好，多空都用 8 倍" maxlength="300" @keydown.enter="saveNote" />
            <button type="button" class="ib on" aria-label="添加" @click="saveNote"><Plus :size="17" /></button>
          </div>
          <p v-if="!notes.list.length" class="muted empty-h">还没有记忆</p>
          <div v-for="n in notes.list" :key="n.id" class="hrow nrow">
            <div class="hmain"><span class="ntext">{{ n.text }}</span><span class="hmeta">{{ n.by === 'ai' ? 'AI 记录' : '手动添加' }} · {{ noteAgo(n.ts) }}</span></div>
            <button type="button" class="ib del" aria-label="删除" @click="removeNote(n.id)"><Trash2 :size="15" /></button>
          </div>
        </div>

        <div v-else-if="showHistory" class="body hist">
          <p v-if="!ai.chats.length" class="muted empty-h">还没有对话</p>
          <div v-for="c in ai.chats" :key="c.id" class="hrow" :class="{ cur: c.id === ai.chatId }" @click="pick(c.id)">
            <div class="hmain"><span class="htitle">{{ c.title }}</span>
              <span class="hmeta">{{ ago(c.updated) }}<template v-if="c.instId"> · {{ c.instId.split('-')[0] }}</template><template v-if="c.costUsd"> · ${{ c.costUsd.toFixed(2) }}</template><span v-if="c.running" class="live-tag">进行中</span></span></div>
            <button type="button" class="ib del" aria-label="删除" @click.stop="remove(c.id)"><Trash2 :size="15" /></button>
          </div>
        </div>

        <div v-else ref="scroller" class="body" @scroll.passive="onScroll">
          <div v-if="!ai.msgs.length" class="hello">
            <div class="big-orb"><Sparkles :size="26" /></div>
            <h4>想看点什么？</h4>
            <p class="muted">我能查本站录制的 K 线、持仓、资金费、订单流、清算图，联网搜新闻，在沙箱里跑计算。按住麦克风说话也行。</p>
            <button v-if="ai.instId" type="button" class="oneclick" @click="submit(ONE_CLICK)">
              <ChartCandlestick :size="18" /><span><b>一键分析 {{ sym }}</b><small>多空辩论 · 风险 · 交易计划</small></span><ChevronRight :size="18" />
            </button>
            <div class="sugs"><button v-for="s in suggestions" :key="s" type="button" class="sug" @click="submit(s)">{{ s }}</button></div>
          </div>

          <template v-for="(m, mi) in ai.msgs" :key="m.id ?? 'm' + mi">
            <div v-if="m.role === 'user'" class="u"><div class="ubub">{{ textOf(m) }}</div></div>
            <div v-else class="a">
              <template v-for="(g, gi) in groups(m)" :key="gi">
                <AiSteps v-if="g.k === 'tools'" :items="g.items" :live="!!m.live" />
                <template v-else>
                  <template v-for="(sg, si) in segments(g.v, !!m.live)" :key="sg.key">
                    <div v-if="sg.k === 'md'" class="md" :class="{ typing: gi === groups(m).length - 1 && si === segments(g.v, !!m.live).length - 1 && phaseOf(m) === 'typing' }" @click="onMdClick" v-html="md.render(sg.text)"></div>
                    <AiBlock v-else :seg="sg" @navigate="onBlockNav" />
                  </template>
                </template>
              </template>
              <div v-if="phaseOf(m) === 'thinking'" class="thinking"><i class="tdot"></i><i class="tdot"></i><i class="tdot"></i><span class="tlabel">{{ m.parts.length ? '整理思路' : '思考中' }}</span></div>
              <div v-if="m.error" class="err"><CircleAlert :size="14" /> {{ m.error }}</div>
              <div v-if="!m.live && textOf(m)" class="foot">
                <button type="button" class="ib sm" aria-label="复制" @click="copy(m)"><Copy :size="13" /></button>
                <span v-if="m.latencyMs" class="muted">{{ (m.latencyMs / 1000).toFixed(0) }}s</span>
                <span v-if="m.costUsd" class="muted">· ${{ m.costUsd.toFixed(3) }}</span>
              </div>
            </div>
          </template>
          <p v-if="ai.reconnecting" class="net-bar"><span class="spin"></span>连接断了，回答仍在服务器上继续，正在重新连接…</p>
          <div v-else-if="!ai.running && ai.msgs.length && (ai.interrupted || ai.msgs[ai.msgs.length - 1].error)" class="retry">
            <span>{{ ai.interrupted ? '这次回答被中断了（服务重启或网络问题）' : '回答没有完成' }}</span>
            <button type="button" @click="retry">重试</button>
          </div>
        </div>

        <footer v-if="!showHistory && !showNotes" class="ft" @touchmove="e => (e.target as HTMLElement).tagName !== 'TEXTAREA' && e.preventDefault()">
          <button v-if="ai.instId && ai.msgs.length" type="button" class="ctx" :disabled="ai.running" @click="submit(ONE_CLICK)"><ChartCandlestick :size="13" /> 一键分析 {{ sym }}</button>
          <div class="composer">
            <textarea ref="ta" v-model="input" rows="1" :placeholder="talk.phase.value === 'rec' ? '正在听…点 ↑ 发送' : talk.phase.value === 'busy' ? '识别中…' : '问点什么…'" @input="autosize" @keydown="onKey"></textarea>
            <button v-if="talk.phase.value === 'rec'" type="button" class="mic x" aria-label="取消录音" @click="talk.cancel"><X :size="16" /></button>
            <button type="button" class="mic" :class="{ rec: talk.phase.value === 'rec', busy: talk.phase.value === 'busy' }" :aria-label="talk.phase.value === 'rec' ? '结束并发送' : '语音输入'" @click="talk.toggle">
              <span v-if="talk.phase.value === 'busy'" class="spin"></span><ArrowUp v-else-if="talk.phase.value === 'rec'" :size="18" /><Mic v-else :size="18" />
            </button>
            <button v-if="ai.running" type="button" class="go stop" aria-label="停止" @click="stop"><Square :size="14" /></button>
            <button v-else type="button" class="go" :disabled="!input.trim()" aria-label="发送" @click="submit()"><ArrowUp :size="18" /></button>
          </div>
          <div v-if="talk.phase.value === 'rec'" class="wave"><i v-for="(v, i) in talk.levels.value" :key="i" :style="{ height: 3 + v * 22 + 'px' }"></i></div>
        </footer>
        <Transition name="fade"><div v-if="toast" class="toast">{{ toast }}</div></Transition>
      </section>
    </Transition>
  </Teleport>
</template>

<style scoped>
.ai-backdrop { position: fixed; inset: 0; z-index: 40; touch-action: none; overscroll-behavior: contain; background: rgba(0, 0, 0, .55); backdrop-filter: blur(2px); }
.ai-panel { position: fixed; z-index: 41; overscroll-behavior: contain; left: 0; right: 0; bottom: 0; height: min(88vh, calc(100vh - env(safe-area-inset-top) - 24px));
  display: flex; flex-direction: column; background: linear-gradient(180deg, #111410 0%, #0b0b0b 140px); border: 1px solid var(--line-2); border-bottom: 0;
  border-radius: 18px 18px 0 0; box-shadow: 0 -20px 60px rgba(0, 0, 0, .6), 0 -1px 0 rgba(188, 255, 47, .12); transition: transform .3s cubic-bezier(.2, .9, .3, 1); }
.ai-panel.desk { left: auto; top: 0; width: 460px; height: 100vh; border-radius: 0; border-bottom: 1px solid var(--line-2); border-right: 0;
  box-shadow: -20px 0 60px rgba(0, 0, 0, .5); }
.hd { position: relative; display: flex; align-items: center; gap: 8px; padding: 14px 10px 10px 16px; touch-action: none; }
.grab { position: absolute; top: 6px; left: 50%; width: 38px; height: 4px; margin-left: -19px; border-radius: 2px; background: var(--line-2); }
.ttl { flex: 1; min-width: 0; display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: var(--fs-lg); overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.mini-orb { flex: none; width: 14px; height: 14px; border-radius: 50%; background: conic-gradient(#bcff2f, #2fe0ff, #9b7df0, #ff5fa2, #bcff2f); box-shadow: 0 0 10px rgba(188, 255, 47, .5); animation: spin 4s linear infinite; }
.acts { display: flex; gap: 2px; }
.models { flex: none; display: flex; padding: 2px; border-radius: 9px; background: var(--bg-2); border: 1px solid var(--line-2); }
.models button { all: unset; padding: 3px 9px; border-radius: 7px; font-size: var(--fs-xs); color: var(--text-3); cursor: pointer; }
.models button.on { background: var(--bg-3); color: var(--text-1); box-shadow: inset 0 0 0 1px rgba(188, 255, 47, .35); }
.ib { all: unset; box-sizing: border-box; display: grid; place-items: center; width: 34px; height: 34px; border-radius: 8px; color: var(--text-2); cursor: pointer; }
.ib.sm { width: 26px; height: 26px; }
.ib.on { color: var(--accent); background: var(--accent-bg); }
.ib:active { background: var(--bg-3); }
@media (hover: hover) { .ib:hover { color: var(--text-1); background: var(--bg-2); } }

.body { flex: 1; overflow-y: auto; overscroll-behavior: contain; padding: 6px 16px 16px; -webkit-overflow-scrolling: touch; }
.hello { display: grid; justify-items: center; text-align: center; gap: 10px; padding: 26px 4px 8px; }
.hello h4 { margin: 4px 0 0; font-size: var(--fs-xl); }
.hello p { margin: 0; font-size: var(--fs-sm); line-height: 1.6; max-width: 340px; }
.big-orb { position: relative; display: grid; place-items: center; width: 64px; height: 64px; border-radius: 50%; color: #f4ffe0;
  background: radial-gradient(circle at 50% 60%, #1a1f17, #050505 70%); box-shadow: 0 0 0 2px #0b0b0b, 0 0 30px rgba(188, 255, 47, .25); }
.big-orb::before { content: ''; position: absolute; inset: -3px; z-index: -1; border-radius: 50%; background: conic-gradient(#bcff2f, #2fe0ff, #9b7df0, #ff5fa2, #f5c542, #bcff2f); animation: spin 5s linear infinite; }
.oneclick { all: unset; box-sizing: border-box; width: 100%; max-width: 380px; margin-top: 8px; display: flex; align-items: center; gap: 12px; padding: 14px 14px; border-radius: 14px; cursor: pointer; text-align: left;
  background: linear-gradient(120deg, rgba(188, 255, 47, .16), rgba(47, 224, 255, .1) 50%, rgba(155, 125, 240, .14)); border: 1px solid rgba(188, 255, 47, .35); color: var(--text-1); }
.oneclick span { flex: 1; display: grid; }
.oneclick small { color: var(--text-3); font-size: var(--fs-xs); }
.oneclick:active { transform: scale(.98); }
.sugs { display: grid; gap: 8px; width: 100%; max-width: 380px; margin-top: 4px; }
.sug { all: unset; box-sizing: border-box; padding: 10px 12px; border-radius: 10px; border: 1px solid var(--line-2); background: var(--bg-1); font-size: var(--fs-md); color: var(--text-2); cursor: pointer; text-align: left; }
.sug:active { background: var(--bg-3); }

.u { display: flex; justify-content: flex-end; margin: 14px 0 6px; }
.ubub { max-width: 84%; padding: 9px 13px; border-radius: 16px 16px 4px 16px; background: #22281a; border: 1px solid rgba(188, 255, 47, .18); white-space: pre-wrap; word-break: break-word; font-size: var(--fs-lg); }
.a { margin: 8px 0 4px; }
.md { font-size: var(--fs-lg); line-height: 1.7; word-break: break-word; }
.md :deep(p) { margin: 6px 0; }
.md :deep(h1), .md :deep(h2), .md :deep(h3) { font-size: var(--fs-xl); margin: 14px 0 6px; }
.md :deep(h4) { font-size: var(--fs-lg); margin: 12px 0 4px; }
.md :deep(ul), .md :deep(ol) { margin: 4px 0; padding-left: 20px; }
.md :deep(li) { margin: 2px 0; }
.md :deep(strong) { color: #fff; }
.md :deep(code) { padding: 1px 5px; border-radius: 4px; background: var(--bg-3); font-size: .9em; }
.md :deep(pre) { overflow-x: auto; padding: 10px; border-radius: 8px; background: var(--bg-2); }
.md :deep(pre code) { padding: 0; background: none; }
.md :deep(table) { display: block; overflow-x: auto; border-collapse: collapse; font-size: var(--fs-sm); margin: 8px 0; }
.md :deep(th), .md :deep(td) { padding: 4px 8px; border: 1px solid var(--line-2); white-space: nowrap; }
.md :deep(blockquote) { margin: 6px 0; padding-left: 10px; border-left: 3px solid var(--line-2); color: var(--text-2); }
.md :deep(a.app-link) { display: inline-flex; align-items: center; gap: 3px; margin: 1px 2px; padding: 1px 9px; border-radius: 12px; font-weight: 600; font-size: .92em;
  color: var(--accent); background: var(--accent-bg); border: 1px solid rgba(188, 255, 47, .3); text-decoration: none; white-space: nowrap; vertical-align: baseline; }
.md :deep(a.app-link)::after { content: '›'; opacity: .7; }
.md :deep(a.app-link:active) { background: rgba(188, 255, 47, .22); }
.md :deep(hr) { border: 0; border-top: 1px solid var(--line-2); margin: 12px 0; }
.thinking { display: flex; align-items: center; gap: 5px; padding: 10px 2px; }
.tdot { width: 7px; height: 7px; border-radius: 50%; background: var(--accent); animation: bounce 1.2s infinite ease-in-out; }
.tdot:nth-child(2) { animation-delay: .15s; background: #2fe0ff; }
.tdot:nth-child(3) { animation-delay: .3s; background: #9b7df0; }
.tlabel { margin-left: 6px; font-size: var(--fs-sm); background: linear-gradient(90deg, var(--text-3) 30%, var(--text-1) 50%, var(--text-3) 70%); background-size: 200% 100%;
  -webkit-background-clip: text; background-clip: text; color: transparent; animation: shine 1.6s linear infinite; }
.md.typing :deep(> :last-child)::after { content: ''; display: inline-block; width: 7px; height: 1.05em; margin-left: 2px; vertical-align: -2px; background: var(--accent); animation: blink 1s steps(2) infinite; }
@keyframes shine { from { background-position: 100% 0; } to { background-position: -100% 0; } }
@keyframes blink { 50% { opacity: 0; } }
.err { display: flex; align-items: center; gap: 6px; margin: 6px 0; color: var(--warn); font-size: var(--fs-sm); }
.net-bar { display: flex; align-items: center; gap: 8px; margin: 10px 0; padding: 8px 12px; border-radius: 10px; background: var(--bg-2); color: var(--text-2); font-size: var(--fs-sm); }
.retry { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin: 10px 0; padding: 8px 8px 8px 12px; border-radius: 10px; border: 1px solid rgba(247, 166, 0, .35); background: rgba(247, 166, 0, .06); font-size: var(--fs-sm); color: var(--text-2); }
.retry button { all: unset; padding: 6px 14px; border-radius: 8px; background: var(--accent); color: #000; font-weight: 600; cursor: pointer; }
.foot { display: flex; align-items: center; gap: 6px; font-size: var(--fs-xs); margin-top: 2px; }

.hist { padding-top: 4px; }
.empty-h { text-align: center; margin-top: 40px; }
.hrow { display: flex; align-items: center; gap: 8px; padding: 10px 6px 10px 10px; border-radius: 10px; cursor: pointer; }
.hrow.cur { background: var(--accent-bg); }
.hrow:active { background: var(--bg-2); }
.hmain { flex: 1; min-width: 0; display: grid; }
.htitle { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hmeta { font-size: var(--fs-xs); color: var(--text-3); }
.live-tag { margin-left: 6px; color: var(--accent); }
.del { color: var(--text-4); }
.note-tip { font-size: var(--fs-xs); margin: 4px 4px 10px; line-height: 1.6; }
.note-add { display: flex; gap: 6px; margin-bottom: 8px; }
.note-add input { flex: 1; min-width: 0; height: 38px; border-radius: 10px; font-size: 16px; }
.nrow { cursor: default; align-items: flex-start; }
.ntext { white-space: normal; line-height: 1.5; }

.ft { padding: 8px 12px calc(10px + env(safe-area-inset-bottom)); border-top: 1px solid var(--line); background: rgba(10, 10, 10, .9); }
.ctx { all: unset; display: inline-flex; align-items: center; gap: 5px; margin: 0 0 8px 2px; padding: 4px 10px; border-radius: 14px; font-size: var(--fs-xs); color: var(--accent); background: var(--accent-bg); border: 1px solid rgba(188, 255, 47, .3); cursor: pointer; }
.ctx:disabled { opacity: .4; }
.composer { display: flex; align-items: flex-end; gap: 6px; padding: 5px 5px 5px 12px; border-radius: 22px; background: var(--bg-2); border: 1px solid var(--line-2); }
.composer:focus-within { border-color: rgba(188, 255, 47, .5); }
textarea { flex: 1; min-height: 24px; max-height: 140px; padding: 6px 0; resize: none; border: 0; outline: 0; background: none; color: var(--text-1); font: inherit; font-size: 16px; line-height: 1.45; }
.mic, .go { all: unset; box-sizing: border-box; flex: none; display: grid; place-items: center; width: 36px; height: 36px; border-radius: 50%; cursor: pointer; touch-action: none; -webkit-user-select: none; user-select: none; -webkit-touch-callout: none; }
.mic { color: var(--text-2); background: var(--bg-3); transition: transform .2s, background .2s; }
.mic.rec { transform: scale(1.18); color: #000; background: conic-gradient(#bcff2f, #2fe0ff, #9b7df0, #bcff2f); box-shadow: 0 0 18px rgba(188, 255, 47, .5); }
.mic.x { color: var(--text-2); background: var(--bg-3); }
.go { color: #000; background: var(--accent); }
.go:disabled { background: var(--bg-3); color: var(--text-4); }
.go.stop { background: var(--text-1); }
.wave { display: flex; justify-content: center; align-items: center; gap: 3px; height: 26px; margin-top: 6px; }
.wave i { width: 3px; border-radius: 2px; background: linear-gradient(180deg, #bcff2f, #2fe0ff); transition: height .08s linear; }
.spin { width: 13px; height: 13px; border-radius: 50%; border: 2px solid var(--line-2); border-top-color: var(--accent); animation: spin .8s linear infinite; }
.toast { position: absolute; left: 50%; bottom: 96px; transform: translateX(-50%); padding: 8px 14px; border-radius: 16px; background: rgba(30, 30, 30, .95); border: 1px solid var(--line-2); font-size: var(--fs-sm); pointer-events: none; }

.sheet-enter-active, .sheet-leave-active { transition: transform .38s cubic-bezier(.2, .9, .3, 1); }
.sheet-enter-from, .sheet-leave-to { transform: translateY(100%); }
.side-enter-active, .side-leave-active { transition: transform .3s cubic-bezier(.2, .9, .3, 1); }
.side-enter-from, .side-leave-to { transform: translateX(100%); }
.fade-enter-active, .fade-leave-active { transition: opacity .2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
@keyframes spin { to { transform: rotate(360deg); } }
@keyframes bounce { 0%, 80%, 100% { transform: scale(.5); opacity: .4; } 40% { transform: scale(1); opacity: 1; } }
@keyframes pop { from { opacity: 0; transform: translateY(4px); } }
</style>
