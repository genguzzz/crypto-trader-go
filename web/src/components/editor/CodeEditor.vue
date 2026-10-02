<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection, dropCursor, rectangularSelection, crosshairCursor, highlightSpecialChars } from '@codemirror/view'
import { Compartment, EditorState } from '@codemirror/state'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { bracketMatching, foldGutter, foldKeymap, indentOnInput, indentUnit, syntaxHighlighting, HighlightStyle } from '@codemirror/language'
import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import { lintGutter, setDiagnostics, type Diagnostic as CmDiagnostic } from '@codemirror/lint'
import { python, pythonLanguage } from '@codemirror/lang-python'
import { tags as t } from '@lezer/highlight'
import { vim, Vim } from '@replit/codemirror-vim'
import type { Diagnostic } from '../../tradeApi'
import { sdkCompletions } from './completions'

/**
 * Python editor on CodeMirror 6: highlighting, indentation, folding, search, SDK autocompletion,
 * diagnostics from the server check (lines/columns as ctg.check reports them) and an optional vim
 * mode. Read-only shows the code greyed and unselectable for editing.
 */
const props = defineProps<{ readonly?: boolean; vim?: boolean; diagnostics?: Diagnostic[] }>()
const code = defineModel<string>({ required: true })
const emit = defineEmits<{ save: [] }>()

const host = ref<HTMLDivElement>()
let view: EditorView | null = null
const vimC = new Compartment(), roC = new Compartment()

const theme = EditorView.theme({
  '&': { height: '100%', fontSize: '13px', backgroundColor: 'var(--bg-0)', color: 'var(--text-1)' },
  '.cm-scroller': { fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace', lineHeight: '1.55' },
  '.cm-content': { caretColor: 'var(--accent)', padding: '8px 0' },
  '.cm-gutters': { backgroundColor: 'var(--bg-0)', color: 'var(--text-3)', border: 'none', borderRight: '1px solid var(--line)' },
  '.cm-activeLine': { backgroundColor: 'rgba(255,255,255,.035)' },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(255,255,255,.05)', color: 'var(--text-2)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'rgba(90,140,255,.32) !important' },
  '.cm-cursor': { borderLeftColor: 'var(--accent)' },
  '.cm-fat-cursor': { background: 'var(--accent) !important', color: 'var(--bg-0) !important' },
  '&:not(.cm-focused) .cm-fat-cursor': { background: 'none !important', outline: '1px solid var(--accent)' },
  '.cm-tooltip': { backgroundColor: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: '6px', color: 'var(--text-1)' },
  '.cm-tooltip-autocomplete ul li[aria-selected]': { backgroundColor: 'var(--bg-3)', color: 'var(--text-1)' },
  '.cm-completionDetail': { color: 'var(--text-3)', fontStyle: 'normal', marginLeft: '8px' },
  '.cm-completionInfo': { maxWidth: '420px', whiteSpace: 'pre-wrap', fontSize: '12px', lineHeight: '1.5' },
  '.cm-panels': { backgroundColor: 'var(--bg-2)', color: 'var(--text-1)' },
  '.cm-panels input, .cm-panels button': { fontSize: '12px' },
  '.cm-foldPlaceholder': { backgroundColor: 'var(--bg-3)', border: 'none', color: 'var(--text-2)' },
  '.cm-diagnostic-error': { borderLeft: '3px solid var(--down)' },
  '.cm-diagnostic-warning': { borderLeft: '3px solid var(--warn)' },
  '.cm-vim-panel': { padding: '2px 8px', fontFamily: 'ui-monospace, Menlo, monospace', fontSize: '12px' },
}, { dark: true })

const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.controlKeyword, t.definitionKeyword, t.moduleKeyword, t.operatorKeyword], color: '#c792ea' },
  { tag: [t.string, t.special(t.string)], color: '#c3e88d' },
  { tag: [t.number, t.bool, t.null], color: '#f78c6c' },
  { tag: t.comment, color: '#7f8a99', fontStyle: 'italic' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: '#82aaff' },
  { tag: [t.definition(t.variableName), t.definition(t.function(t.variableName))], color: '#82aaff' },
  { tag: t.className, color: '#ffcb6b' },
  { tag: [t.propertyName, t.attributeName], color: '#89ddff' },
  { tag: t.self, color: '#f07178' },
  { tag: [t.operator, t.punctuation], color: '#a6accd' },
  { tag: t.invalid, color: 'var(--down)' },
])

function cmDiagnostics(state: EditorState, ds: Diagnostic[] = []): CmDiagnostic[] {
  const doc = state.doc
  return ds.map(d => {
    const line = doc.line(Math.min(Math.max(d.line, 1), doc.lines))
    const from = Math.min(line.from + Math.max(d.col, 0), line.to)
    let to = line.to
    if (d.endLine && d.endLine >= d.line) {
      const el = doc.line(Math.min(d.endLine, doc.lines))
      to = Math.min(el.from + (d.endCol ?? 0), el.to)
    }
    if (to <= from) to = Math.min(from + 1, doc.length)
    return { from, to, severity: d.severity === 'warning' ? 'warning' : 'error', message: d.message }
  })
}

onMounted(() => {
  Vim.defineEx('write', 'w', () => emit('save')) // :w saves
  view = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: code.value,
      extensions: [
        vimC.of(props.vim ? vim() : []), // first, so vim's keys win
        lineNumbers(), highlightActiveLineGutter(), highlightSpecialChars(), history(), foldGutter(), drawSelection(), dropCursor(),
        EditorState.allowMultipleSelections.of(true), indentOnInput(), bracketMatching(), closeBrackets(),
        rectangularSelection(), crosshairCursor(), highlightActiveLine(), highlightSelectionMatches(),
        indentUnit.of('    '), EditorState.tabSize.of(4),
        python(), syntaxHighlighting(highlight), theme, lintGutter(), EditorView.lineWrapping,
        pythonLanguage.data.of({ autocomplete: sdkCompletions }), autocompletion({ activateOnTyping: true }),
        keymap.of([
          { key: 'Mod-s', run: () => { emit('save'); return true } },
          ...closeBracketsKeymap, ...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap, ...completionKeymap, indentWithTab,
        ]),
        roC.of([EditorState.readOnly.of(!!props.readonly), EditorView.editable.of(!props.readonly)]),
        EditorView.updateListener.of(u => { if (u.docChanged) code.value = u.state.doc.toString() }),
      ],
    }),
  })
  view.dispatch(setDiagnostics(view.state, cmDiagnostics(view.state, props.diagnostics)))
})
onBeforeUnmount(() => { view?.destroy(); view = null })

watch(() => props.vim, v => view?.dispatch({ effects: vimC.reconfigure(v ? vim() : []) }))
watch(() => props.readonly, r => view?.dispatch({ effects: roC.reconfigure([EditorState.readOnly.of(!!r), EditorView.editable.of(!r)]) }))
watch(() => props.diagnostics, ds => { if (view) view.dispatch(setDiagnostics(view.state, cmDiagnostics(view.state, ds))) })
watch(code, v => { // external replace (load, template)
  if (view && v !== view.state.doc.toString()) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } })
})

/** Moves the cursor to a 1-based line and focuses the editor. */
function goto(line: number, col = 0) {
  if (!view) return
  const l = view.state.doc.line(Math.min(Math.max(line, 1), view.state.doc.lines))
  view.dispatch({ selection: { anchor: Math.min(l.from + col, l.to) }, scrollIntoView: true })
  view.focus()
}
defineExpose({ goto })
</script>

<template>
  <div ref="host" class="ce" :class="{ ro: readonly }"></div>
</template>

<style scoped>
.ce { height: 100%; min-height: 0; overflow: hidden; border: 1px solid var(--line); border-radius: 8px; }
.ce.ro { opacity: .82; }
.ce :deep(.cm-editor) { height: 100%; }
.ce :deep(.cm-editor.cm-focused) { outline: none; }
.ce.ro :deep(.cm-content) { color: var(--text-2); }
</style>
