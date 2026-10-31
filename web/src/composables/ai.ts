import { reactive } from 'vue'
import { askAdminKey, clearAdminKey, getAdminKey } from './adminKey'

/**
 * AI research chat (internal/aigate/chat.go): one global conversation state shared by the floating
 * orb and the chat panel. A turn streams as Server-Sent Events over a POST; the run continues on the
 * server when the stream drops, and `open()` re-attaches to a running chat.
 */

export type Part =
  | { t: 'text'; v: string }
  | { t: 'tool'; id: string; name: string; label: string; input: string; ok: boolean | null; sub?: boolean; args?: string; output?: string }
export interface Msg { id?: number; role: 'user' | 'assistant'; parts: Part[]; error?: string; costUsd?: number; latencyMs?: number; live?: boolean }
export interface ChatSummary { id: number; title: string; instId?: string; updated: number; costUsd: number; running: boolean }

export const ai = reactive({
  open: false,
  chatId: 0,
  title: '',
  msgs: [] as Msg[],
  running: false,
  chats: [] as ChatSummary[],
  error: '',
  /** set when an answer finished while the panel was closed (orb dot) */
  unread: false,
  /** the stream dropped and we are polling the server back */
  reconnecting: false,
  /** the chat's last question has no answer and nothing runs (server restarted): offer retry */
  interrupted: false,
  /** instrument the owner is looking at (route /instrument/:id) */
  instId: '',
  /** model for the next turn (remembered on this device) */
  model: localStorage.getItem('ctg.aiModel') || 'claude-sonnet-5',
})
export const AI_MODELS = [{ value: 'claude-sonnet-5', label: 'Sonnet' }, { value: 'claude-opus-5-5', label: 'Opus' }]
export function setModel(m: string) { ai.model = m; localStorage.setItem('ctg.aiModel', m) }

let abort: AbortController | null = null

/** fetch with the owner key; a 401 admin_required asks for the key once and retries. */
async function kfetch(path: string, init: RequestInit = {}): Promise<Response> {
  const send = (key: string) => fetch(path, { ...init, headers: { ...(init.headers || {}), ...(key ? { 'X-CTG-Admin': key } : {}) } })
  let r = await send(getAdminKey())
  if (r.status === 401) {
    const out = await r.clone().json().catch(() => ({}))
    if (out?.code === 'admin_required') {
      const key = await askAdminKey(getAdminKey() ? '密钥不正确，请重新输入' : '')
      if (!key) throw new Error('需要管理员密钥')
      r = await send(key)
      if (r.status === 401) clearAdminKey()
    }
  }
  return r
}

async function errorOf(r: Response) {
  const out = await r.json().catch(() => ({}))
  return new Error(out?.error || `HTTP ${r.status}`)
}

function last(): Msg {
  let m = ai.msgs[ai.msgs.length - 1]
  if (!m || m.role !== 'assistant' || !m.live) {
    m = { role: 'assistant', parts: [], live: true }
    ai.msgs.push(m)
    m = ai.msgs[ai.msgs.length - 1] // the reactive proxy
  }
  return m
}

function apply(ev: any) {
  switch (ev.type) {
    case 'chat':
      ai.chatId = ev.chatId
      return
    case 'text': {
      const m = last(), p = m.parts[m.parts.length - 1]
      if (p && p.t === 'text') p.v += ev.delta
      else m.parts.push({ t: 'text', v: ev.delta })
      return
    }
    case 'text_start':
      last().parts.push({ t: 'text', v: '' })
      return
    case 'tool': {
      const m = last()
      if (!m.parts.some(p => p.t === 'tool' && p.id === ev.id)) m.parts.push({ t: 'tool', id: ev.id, name: ev.name, label: ev.label, input: '', ok: null, sub: ev.sub })
      return
    }
    case 'tool_input':
    case 'tool_done': {
      const p = last().parts.find(p => p.t === 'tool' && p.id === ev.id) as Extract<Part, { t: 'tool' }> | undefined
      if (!p) return
      if (ev.type === 'tool_input') {
        p.input = ev.input
        if (ev.args) p.args = ev.args
        if (ev.label) p.label = ev.label
      } else {
        p.ok = ev.ok
        if (ev.output != null) p.output = ev.output
      }
      return
    }
    case 'done': {
      const m = last()
      m.live = false
      m.costUsd = ev.cost
      m.latencyMs = ev.latencyMs
      m.parts.forEach(p => { if (p.t === 'tool' && p.ok == null) p.ok = true })
      return
    }
    case 'error': {
      const m = last()
      m.live = false
      m.error = ev.error
      return
    }
  }
}

/** Read an SSE response body and apply its events until it ends. */
async function pump(r: Response, onEvent: (ev: any) => void, onData: () => void) {
  const reader = r.body!.getReader()
  const dec = new TextDecoder()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    onData()
    buf += dec.decode(value, { stream: true })
    let i
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i)
      buf = buf.slice(i + 2)
      for (const line of block.split('\n')) {
        if (!line.startsWith('data: ')) continue
        let ev: any
        try { ev = JSON.parse(line.slice(6)) } catch { continue }
        onEvent(ev)
        apply(ev)
      }
    }
  }
}

// ---- connection: a turn runs on the server; the app only watches it and can always pick it up again ----
const CHAT_KEY = 'ctg.aiChat' // the chat on screen, reopened after an app restart
const PENDING_KEY = 'ctg.aiPending' // a chat whose answer we have not seen finish yet
const STALE_MS = 40_000 // the server pings every 15 s: this much silence means the stream is dead
const saved = (k: string) => Number(localStorage.getItem(k) || 0)
function setChat(id: number) { ai.chatId = id; id ? localStorage.setItem(CHAT_KEY, String(id)) : localStorage.removeItem(CHAT_KEY) }
function setPending(id: number) { id ? localStorage.setItem(PENDING_KEY, String(id)) : localStorage.removeItem(PENDING_KEY) }

/** Stream one response; resolves true when the turn ended (done/error event), false when the stream dropped. */
async function stream(open: (signal: AbortSignal) => Promise<Response>): Promise<boolean> {
  abort?.abort()
  const ac = new AbortController()
  abort = ac
  ai.running = true
  ai.error = ''
  let last = Date.now(), ended = false
  const dog = window.setInterval(() => { if (Date.now() - last > STALE_MS) ac.abort() }, 5000)
  const onEvent = (ev: any) => {
    last = Date.now()
    if (ev.type === 'chat') setChat(ev.chatId)
    if (ev.type === 'done' || ev.type === 'error') {
      ended = true
      if (!ai.open) ai.unread = true
    }
    if (ev.type === 'idle') ended = true // re-attached to a turn that had already finished
  }
  try {
    const r = await open(ac.signal)
    if (!r.ok) throw await errorOf(r)
    await pump(r, onEvent, () => (last = Date.now()))
  } catch (e: any) {
    if (abort !== ac) return true // replaced by a newer stream (new chat, reopened chat)
    if (!ac.signal.aborted || !ended) ai.error = ac.signal.aborted ? '连接超时' : e.message || String(e)
  } finally {
    clearInterval(dog)
    if (abort === ac) {
      abort = null
      ai.running = false
    }
  }
  return ended
}

export async function send(text: string, retry = false) {
  text = text.trim()
  if ((!text && !retry) || ai.running) return
  if (retry) {
    const m = ai.msgs[ai.msgs.length - 1]
    if (m?.role === 'assistant') ai.msgs.pop() // the failed answer is replaced
  } else {
    ai.msgs.push({ role: 'user', parts: [{ t: 'text', v: text }] })
    if (!ai.chatId) ai.title = text.slice(0, 40)
  }
  ai.interrupted = false
  const body = JSON.stringify({ chatId: ai.chatId || 0, message: text, instId: ai.instId, model: ai.model, retry })
  const ended = await stream(signal => kfetch('/api/v1/ai/chat', { method: 'POST', body, headers: { 'Content-Type': 'application/json' }, signal }))
  if (ai.chatId) setPending(ended ? 0 : ai.chatId)
  if (!ended && ai.chatId) await reconnect()
}

/** Re-run the last question after an error or an interrupted answer. */
export const retry = () => send('', true)

export async function stop() {
  if (!ai.chatId) return
  await kfetch(`/api/v1/ai/chats/${ai.chatId}/stop`, { method: 'POST' }).catch(() => {})
}

interface StoredMsg { id: number; role: 'user' | 'assistant'; content: string; steps: any[]; costUsd?: number; latencyMs?: number; error?: string }
function fromStored(m: StoredMsg): Msg {
  const parts: Part[] = Array.isArray(m.steps) && m.steps.length ? m.steps.map((s: any) => (s.t === 'text' ? { t: 'text', v: s.v } : { ...s, t: 'tool' })) : m.content ? [{ t: 'text', v: m.content }] : []
  return { id: m.id, role: m.role, parts, error: m.error, costUsd: m.costUsd, latencyMs: m.latencyMs }
}

/** Load a chat from the server; when it is still running, follow its live events. */
export async function openChat(id: number) {
  abort?.abort()
  abort = null
  const r = await fetch(`/api/v1/ai/chats/${id}`)
  if (!r.ok) {
    if (r.status === 404 && id === ai.chatId) newChat()
    throw await errorOf(r)
  }
  const c = await r.json()
  setChat(c.id)
  ai.title = c.title
  ai.msgs = (c.messages as StoredMsg[]).map(fromStored)
  ai.interrupted = !!c.interrupted
  ai.error = ''
  ai.reconnecting = false
  if (c.running) {
    setPending(c.id)
    ai.msgs.push({ role: 'assistant', parts: [], live: true }) // the stream replays this turn from its start
    const ended = await stream(signal => fetch(`/api/v1/ai/chats/${c.id}/stream`, { signal }))
    if (!ended) return reconnect()
    return openChat(c.id) // load the stored answer (complete, with every step's result)
  }
  if (saved(PENDING_KEY) === c.id) {
    setPending(0)
    if (!ai.open) ai.unread = true
  }
}

let reconnecting: Promise<void> | null = null
/** The stream dropped (background, network, server restart): poll back with backoff until the turn is over. */
function reconnect(): Promise<void> {
  if (reconnecting) return reconnecting
  const id = ai.chatId
  ai.reconnecting = true
  reconnecting = (async () => {
    for (let i = 0; i < 8 && ai.chatId === id; i++) {
      await new Promise(r => setTimeout(r, Math.min(15_000, 1000 * 2 ** i)))
      if (document.visibilityState === 'hidden' || ai.chatId !== id) break // resume() picks it up on return
      try { reconnecting = null; await openChat(id); return } catch { /* offline: try again */ }
    }
  })().finally(() => { reconnecting = null; if (ai.chatId === id) ai.reconnecting = false })
  return reconnecting
}

/** App back in front / network back / panel opened: catch up with the chat on screen or the one left running. */
export async function resume() {
  if (ai.running && abort) return // a live stream is still flowing
  const id = ai.chatId || saved(CHAT_KEY)
  if (!id) return
  const pending = saved(PENDING_KEY) === id
  if (!ai.chatId && !pending && !ai.open) return // nothing in flight: load lazily when the panel opens
  await openChat(id).catch(() => {})
}

if (typeof window !== 'undefined') {
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') resume() })
  window.addEventListener('online', () => resume())
  window.addEventListener('pageshow', () => resume())
}

export function newChat() {
  abort?.abort()
  abort = null
  setChat(0)
  setPending(0)
  ai.title = ''
  ai.msgs = []
  ai.error = ''
  ai.running = false
  ai.interrupted = false
  ai.reconnecting = false
}

export async function loadChats() {
  const r = await fetch('/api/v1/ai/chats')
  if (r.ok) ai.chats = await r.json()
}

export async function deleteChat(id: number) {
  const r = await kfetch(`/api/v1/ai/chats/${id}`, { method: 'DELETE' })
  if (!r.ok) throw await errorOf(r)
  ai.chats = ai.chats.filter(c => c.id !== id)
  if (ai.chatId === id) newChat()
}

export async function transcribe(blob: Blob): Promise<string> {
  const fd = new FormData()
  const ext = blob.type.includes('mp4') ? 'm4a' : blob.type.includes('ogg') ? 'ogg' : 'webm'
  fd.append('audio', blob, `voice.${ext}`)
  const r = await kfetch('/api/v1/ai/transcribe', { method: 'POST', body: fd })
  if (!r.ok) throw await errorOf(r)
  return (await r.json()).text || ''
}

/** Plain-text of a message (copy button). */
export const textOf = (m: Msg) => m.parts.filter(p => p.t === 'text').map(p => (p as { v: string }).v).join('\n\n')

export interface Note { id: number; ts: number; text: string; by: 'ai' | 'owner' }
export const notes = reactive({ list: [] as Note[] })
export async function loadNotes() {
  const r = await fetch('/api/v1/ai/notes')
  if (r.ok) notes.list = await r.json()
}
export async function addNote(text: string) {
  const r = await kfetch('/api/v1/ai/notes', { method: 'POST', body: JSON.stringify({ text }), headers: { 'Content-Type': 'application/json' } })
  if (!r.ok) throw await errorOf(r)
  await loadNotes()
}
export async function deleteNote(id: number) {
  const r = await kfetch(`/api/v1/ai/notes/${id}`, { method: 'DELETE' })
  if (!r.ok) throw await errorOf(r)
  notes.list = notes.list.filter(n => n.id !== id)
}
