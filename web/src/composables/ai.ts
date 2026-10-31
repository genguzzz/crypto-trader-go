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
  /** set by the orb when an answer finished while the panel was closed */
  unread: false,
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
async function pump(r: Response) {
  const reader = r.body!.getReader()
  const dec = new TextDecoder()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    let i
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i)
      buf = buf.slice(i + 2)
      for (const line of block.split('\n')) {
        if (!line.startsWith('data: ')) continue
        try { apply(JSON.parse(line.slice(6))) } catch { /* partial or keep-alive */ }
      }
    }
  }
}

async function stream(open: () => Promise<Response>) {
  abort?.abort()
  const ac = new AbortController()
  abort = ac
  ai.running = true
  ai.error = ''
  try {
    const r = await open()
    if (!r.ok) throw await errorOf(r)
    await pump(r)
  } catch (e: any) {
    if (ac.signal.aborted) return
    // the stream dropped (app in background, network): the run goes on server-side, pick it up again
    ai.error = e.message || String(e)
  } finally {
    if (abort === ac) {
      abort = null
      ai.running = false
      const m = ai.msgs[ai.msgs.length - 1]
      if (m?.live && !ai.error) m.live = false
      if (!ai.open) ai.unread = true
    }
  }
}

export async function send(text: string) {
  text = text.trim()
  if (!text || ai.running) return
  ai.msgs.push({ role: 'user', parts: [{ t: 'text', v: text }] })
  if (!ai.chatId) ai.title = text.slice(0, 40)
  const body = JSON.stringify({ chatId: ai.chatId || 0, message: text, instId: ai.instId, model: ai.model })
  await stream(() => kfetch('/api/v1/ai/chat', { method: 'POST', body, headers: { 'Content-Type': 'application/json' }, signal: abort!.signal }))
  if (ai.error && ai.chatId) await reattach() // dropped mid-answer: reload what was saved / keep following
}

export async function stop() {
  if (!ai.chatId) return
  await kfetch(`/api/v1/ai/chats/${ai.chatId}/stop`, { method: 'POST' }).catch(() => {})
}

interface StoredMsg { id: number; role: 'user' | 'assistant'; content: string; steps: any[]; costUsd?: number; latencyMs?: number; error?: string }
function fromStored(m: StoredMsg): Msg {
  const parts: Part[] = Array.isArray(m.steps) && m.steps.length ? m.steps.map((s: any) => (s.t === 'text' ? { t: 'text', v: s.v } : { ...s, t: 'tool' })) : m.content ? [{ t: 'text', v: m.content }] : []
  return { id: m.id, role: m.role, parts, error: m.error, costUsd: m.costUsd, latencyMs: m.latencyMs }
}

/** Load a chat; when it is still running, follow its live events. */
export async function openChat(id: number) {
  abort?.abort()
  const r = await fetch(`/api/v1/ai/chats/${id}`)
  if (!r.ok) throw await errorOf(r)
  const c = await r.json()
  ai.chatId = c.id
  ai.title = c.title
  ai.msgs = (c.messages as StoredMsg[]).map(fromStored)
  ai.error = ''
  if (c.running) await follow()
}

async function follow() {
  ai.msgs.push({ role: 'assistant', parts: [], live: true }) // the stream replays this turn's events from the start
  await stream(() => fetch(`/api/v1/ai/chats/${ai.chatId}/stream`, { signal: abort!.signal }))
}

async function reattach() {
  const id = ai.chatId
  await new Promise(r => setTimeout(r, 1500))
  if (ai.chatId === id) await openChat(id).catch(() => {})
}

export function newChat() {
  abort?.abort()
  ai.chatId = 0
  ai.title = ''
  ai.msgs = []
  ai.error = ''
  ai.running = false
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
