import type { Completion, CompletionContext, CompletionResult } from '@codemirror/autocomplete'
import sdk from '../../docs/sdk.json'

/**
 * Autocompletion for the strategy SDK, from web/src/docs/sdk.json (generated from the Python SDK by
 * strategies/ctg/sdkdoc.py): members after `ctx.`, `Signal.`, `ta.`, the handlers to override after
 * `def `, and the SDK's top-level names.
 */
export interface SdkEntry { group: string; name: string; kind: string; sig: string; doc: string }
export const SDK = sdk as SdkEntry[]

const typeOf: Record<string, Completion['type']> = { method: 'method', function: 'function', class: 'class', attr: 'property', handler: 'method' }
const opt = (e: SdkEntry, apply?: string): Completion => ({
  label: e.name.includes('.') ? e.name.split('.').pop()! : e.name,
  type: typeOf[e.kind] ?? 'variable',
  detail: e.kind === 'attr' ? '' : e.sig,
  info: e.doc || undefined,
  apply,
  boost: e.kind === 'handler' ? 2 : 0,
})
const byGroup = (g: string) => SDK.filter(e => e.group === g).map(e => opt(e))
const members: Record<string, Completion[]> = {
  ctx: byGroup('ctx'),
  Signal: byGroup('Signal'),
  ta: byGroup('ta'),
  self: SDK.filter(e => e.group === 'Strategy' && e.kind === 'method').map(e => opt(e)),
  bar: 'inst tf ts o h l c vol volq'.split(' ').map(label => ({ label, type: 'property' })),
  b: 'ts o h l c vol volq'.split(' ').map(label => ({ label, type: 'property' })),
}
const handlers = SDK.filter(e => e.kind === 'handler').map(e => opt(e, `${e.name}${e.sig.replace(/^\(/, '(self, ').replace('(self, )', '(self)')}:`))
const topLevel: Completion[] = [
  ...SDK.filter(e => e.group === 'params' || e.name === 'Watchlist' || e.name === 'pick').map(e => opt(e)),
  { label: 'Signal', type: 'class', info: '信号：Signal.open / open_long / open_short / close / reduce' },
  { label: 'Strategy', type: 'class', info: '策略基类' },
  { label: 'ta', type: 'namespace', info: '指标：ema rsi atr …' },
  ...SDK.filter(e => e.group === 'Strategy' && e.kind === 'attr').map(e => ({ ...opt(e), apply: `${e.name} = ` })),
]

export function sdkCompletions(cx: CompletionContext): CompletionResult | null {
  const member = cx.matchBefore(/\b(\w+)\.\w*$/)
  if (member) {
    const obj = /^(\w+)\./.exec(member.text)![1]
    const opts = members[obj]
    if (!opts) return null
    return { from: member.from + obj.length + 1, options: opts, validFor: /^\w*$/ }
  }
  const def = cx.matchBefore(/\bdef\s+\w*$/)
  if (def) return { from: def.to - (/\w*$/.exec(def.text)?.[0].length ?? 0), options: handlers, validFor: /^\w*$/ }
  const word = cx.matchBefore(/\w+$/)
  if (!word && !cx.explicit) return null
  return { from: word ? word.from : cx.pos, options: topLevel, validFor: /^\w*$/ }
}
