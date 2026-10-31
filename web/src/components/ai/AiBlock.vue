<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { TrendingUp, TrendingDown, Pause, ChevronRight, ShieldAlert, Target, LogIn, Clock } from 'lucide-vue-next'
import { instOk, num, sym, type Segment } from './blocks'

/** One generative-UI block of an agent answer (schemas in ai/prompts/chat_v1.md). */
const props = defineProps<{ seg: Extract<Segment, { k: 'block' }> }>()
const emit = defineEmits<{ navigate: [] }>()
const router = useRouter()
const d = computed(() => props.seg.data ?? {})
function go(id: unknown) {
  if (!instOk(id)) return
  router.push(`/instrument/${id}`)
  emit('navigate')
}

const SIDE: Record<string, { t: string; cls: string }> = {
  long: { t: '做多', cls: 'is-up' }, short: { t: '做空', cls: 'is-down' }, wait: { t: '观望', cls: 'is-wait' },
  avoid: { t: '回避', cls: 'is-warn' }, watch: { t: '关注', cls: 'is-wait' },
}
const side = (s: unknown) => SIDE[String(s)] ?? { t: String(s ?? ''), cls: 'is-wait' }
const fmt = (v: number) => (Math.abs(v) >= 1000 ? v.toLocaleString('en-US', { maximumFractionDigits: 1 }) : Math.abs(v) >= 1 ? +v.toFixed(4) + '' : +v.toPrecision(4) + '')

// ---- plan: a price ladder from target 2 down to the stop (or up, for shorts) ----
const plan = computed(() => {
  if (props.seg.type !== 'plan') return null
  const p = d.value
  const ent = Array.isArray(p.entry) ? p.entry.map(num).filter((x: number | null): x is number => x != null) : [num(p.entry)].filter((x): x is number => x != null)
  const mid = ent.length ? ent.reduce((a: number, b: number) => a + b, 0) / ent.length : null
  const pct = (v: number | null) => (v == null || mid == null ? '' : `${v >= mid ? '+' : ''}${((v / mid - 1) * 100).toFixed(1)}%`)
  const tps = (Array.isArray(p.tp) ? p.tp : [p.tp]).map(num).filter((x: number | null): x is number => x != null)
  const stop = num(p.stop)
  const rows = [
    ...tps.map((v: number, i: number) => ({ k: 'tp', label: `止盈 ${i + 1}`, v, pct: pct(v) })),
    ...(ent.length ? [{ k: 'entry', label: '入场', v: mid!, text: ent.map(fmt).join(' – '), pct: '' }] : []),
    ...(stop != null ? [{ k: 'stop', label: '止损', v: stop, pct: pct(stop) }] : []),
  ].sort((a, b) => b.v - a.v)
  const rr = num(p.rr) ?? (mid != null && stop != null && tps[0] != null ? Math.abs(tps[0] - mid) / Math.abs(mid - stop) : null)
  return { rows, rr, lev: num(p.leverage), side: side(p.side) }
})

const kpis = computed(() => (props.seg.type === 'kpi' ? (Array.isArray(d.value) ? d.value : d.value.items) ?? [] : []))
const coins = computed(() => (props.seg.type === 'coins' ? (Array.isArray(d.value) ? d.value : d.value.items) ?? [] : []))
</script>

<template>
  <!-- streaming: the block is still being written -->
  <div v-if="seg.open" class="blk skel"><i></i><i></i><i class="short"></i></div>
  <pre v-else-if="seg.error" class="blk raw">{{ seg.raw }}</pre>

  <!-- verdict: the call, first thing in the answer -->
  <div v-else-if="seg.type === 'verdict'" class="blk verdict" :class="side(d.direction).cls">
    <div class="v-top">
      <span class="v-badge"><TrendingUp v-if="d.direction === 'long'" :size="16" /><TrendingDown v-else-if="d.direction === 'short'" :size="16" /><Pause v-else :size="16" />{{ side(d.direction).t }}</span>
      <button v-if="instOk(d.instId)" type="button" class="v-inst" @click="go(d.instId)">{{ sym(d.instId) }}<ChevronRight :size="14" /></button>
      <span v-if="d.confidence" class="v-conf">信心 {{ d.confidence }}</span>
    </div>
    <div v-if="d.title" class="v-title">{{ d.title }}</div>
    <div v-if="d.reason" class="v-reason">{{ d.reason }}</div>
  </div>

  <!-- kpi tiles -->
  <div v-else-if="seg.type === 'kpi'" class="blk kpi">
    <div v-for="(k, i) in kpis" :key="i" class="tile" :class="k.tone ? 'is-' + k.tone : ''">
      <span class="t-label">{{ k.label }}</span>
      <span class="t-value tnum">{{ k.value }}</span>
      <span v-if="k.sub" class="t-sub">{{ k.sub }}</span>
    </div>
  </div>

  <!-- trade plan -->
  <div v-else-if="seg.type === 'plan' && plan" class="blk plan" :class="plan.side.cls">
    <div class="p-head">
      <span class="p-side">{{ plan.side.t }}</span>
      <button v-if="instOk(d.instId)" type="button" class="p-inst" @click="go(d.instId)">{{ sym(d.instId) }}<ChevronRight :size="14" /></button>
      <span class="p-tags">
        <span v-if="plan.lev" class="tag">{{ plan.lev }}x</span>
        <span v-if="plan.rr" class="tag">盈亏比 {{ plan.rr.toFixed(1) }}</span>
        <span v-if="d.risk" class="tag">风险 {{ d.risk }}</span>
      </span>
    </div>
    <div class="ladder">
      <div v-for="(r, i) in plan.rows" :key="i" class="rung" :class="r.k">
        <span class="r-ic"><Target v-if="r.k === 'tp'" :size="13" /><LogIn v-else-if="r.k === 'entry'" :size="13" /><ShieldAlert v-else :size="13" /></span>
        <span class="r-label">{{ r.label }}</span>
        <span class="r-val tnum">{{ 'text' in r && r.text ? r.text : fmt(r.v) }}</span>
        <span class="r-pct tnum">{{ r.pct }}</span>
      </div>
    </div>
    <div v-if="d.invalidate || d.hold" class="p-foot">
      <div v-if="d.invalidate"><b>作废</b>{{ d.invalidate }}</div>
      <div v-if="d.hold"><Clock :size="12" /> {{ d.hold }}</div>
    </div>
  </div>

  <!-- coin list: tap a row to open it -->
  <div v-else-if="seg.type === 'coins'" class="blk coins">
    <div v-if="d.title" class="c-title">{{ d.title }}</div>
    <button v-for="(c, i) in coins" :key="i" type="button" class="c-row" :disabled="!instOk(c.instId)" @click="go(c.instId)">
      <span class="c-sym">{{ instOk(c.instId) ? sym(c.instId) : c.instId }}</span>
      <span v-if="c.side" class="c-side" :class="side(c.side).cls">{{ side(c.side).t }}</span>
      <span class="c-note">{{ c.note }}</span>
      <span v-if="c.metric" class="c-metric tnum">{{ c.metric }}</span>
      <ChevronRight v-if="instOk(c.instId)" :size="15" class="c-go" />
    </button>
  </div>

  <!-- bull vs bear -->
  <div v-else-if="seg.type === 'bullbear'" class="blk bb">
    <div class="bb-col is-up">
      <div class="bb-h"><TrendingUp :size="14" /> 多方<span v-if="d.bullScore != null" class="bb-s">{{ d.bullScore }}/10</span></div>
      <ul><li v-for="(x, i) in d.bull ?? []" :key="i">{{ x }}</li></ul>
    </div>
    <div class="bb-col is-down">
      <div class="bb-h"><TrendingDown :size="14" /> 空方<span v-if="d.bearScore != null" class="bb-s">{{ d.bearScore }}/10</span></div>
      <ul><li v-for="(x, i) in d.bear ?? []" :key="i">{{ x }}</li></ul>
    </div>
  </div>
</template>

<style scoped>
.blk { margin: 10px 0; border-radius: 14px; border: 1px solid var(--line); background: var(--bg-1); }
.is-up { --c: var(--up); --cb: rgba(37, 167, 80, .12); }
.is-down { --c: var(--down); --cb: rgba(202, 63, 100, .12); }
.is-wait { --c: #8a8a8a; --cb: rgba(255, 255, 255, .05); }
.is-warn { --c: var(--warn); --cb: rgba(247, 166, 0, .1); }

.skel { padding: 14px; display: grid; gap: 8px; }
.skel i { height: 10px; border-radius: 5px; background: linear-gradient(90deg, var(--bg-2), var(--bg-3), var(--bg-2)); background-size: 200% 100%; animation: sk 1.2s linear infinite; }
.skel i.short { width: 55%; }
@keyframes sk { from { background-position: 200% 0; } to { background-position: -200% 0; } }
.raw { padding: 10px; font-size: var(--fs-xs); white-space: pre-wrap; color: var(--text-3); overflow: auto; }

.verdict { padding: 14px 14px 12px; background: linear-gradient(135deg, var(--cb), transparent 70%); border-color: color-mix(in srgb, var(--c) 45%, transparent); }
.v-top { display: flex; align-items: center; gap: 8px; }
.v-badge { display: inline-flex; align-items: center; gap: 5px; padding: 4px 11px; border-radius: 9px; background: var(--c); color: #fff; font-weight: 700; font-size: var(--fs-lg); }
.is-wait .v-badge { background: var(--bg-3); color: var(--text-1); }
.v-inst, .p-inst { all: unset; display: inline-flex; align-items: center; gap: 2px; padding: 3px 8px; border-radius: 8px; font-weight: 700; color: var(--accent); background: var(--accent-bg); cursor: pointer; }
.v-conf { margin-left: auto; font-size: var(--fs-sm); color: var(--text-2); }
.v-title { margin-top: 10px; font-size: var(--fs-xl); font-weight: 700; line-height: 1.45; }
.v-reason { margin-top: 4px; font-size: var(--fs-md); color: var(--text-2); line-height: 1.6; }

.kpi { display: grid; grid-template-columns: repeat(auto-fill, minmax(104px, 1fr)); overflow: hidden; }
.tile { display: grid; gap: 2px; padding: 10px 11px; min-width: 0; box-shadow: 0 0 0 .5px var(--line); }
.t-label { font-size: var(--fs-xs); color: var(--text-3); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.t-value { font-size: var(--fs-xl); font-weight: 700; color: var(--text-1); }
.tile.is-up .t-value { color: var(--up); }
.tile.is-down .t-value { color: var(--down); }
.tile.is-warn .t-value { color: var(--warn); }
.t-sub { font-size: var(--fs-xs); color: var(--text-3); }

.plan { overflow: hidden; border-color: color-mix(in srgb, var(--c) 40%, transparent); }
.p-head { display: flex; align-items: center; gap: 8px; padding: 10px 12px; background: var(--cb); }
.p-side { font-weight: 800; color: var(--c); font-size: var(--fs-lg); }
.p-tags { margin-left: auto; display: flex; gap: 5px; flex-wrap: wrap; justify-content: flex-end; }
.p-tags .tag { font-size: var(--fs-xs); }
.ladder { padding: 4px 12px 6px; }
.rung { display: grid; grid-template-columns: 18px 52px 1fr auto; align-items: center; gap: 6px; padding: 7px 0; border-bottom: 1px dashed var(--line); }
.rung:last-child { border-bottom: 0; }
.r-ic { display: grid; place-items: center; }
.rung.tp .r-ic, .rung.tp .r-pct { color: var(--up); }
.rung.stop .r-ic, .rung.stop .r-pct { color: var(--down); }
.is-down .rung.tp .r-ic, .is-down .rung.tp .r-pct { color: var(--up); }
.rung.entry { background: rgba(255, 255, 255, .03); margin: 0 -12px; padding-left: 12px; padding-right: 12px; }
.rung.entry .r-ic { color: var(--accent); }
.r-label { font-size: var(--fs-sm); color: var(--text-2); }
.r-val { font-weight: 700; font-size: var(--fs-lg); color: var(--text-1); }
.r-pct { font-size: var(--fs-sm); }
.p-foot { display: grid; gap: 4px; padding: 8px 12px 10px; border-top: 1px solid var(--line); font-size: var(--fs-sm); color: var(--text-2); }
.p-foot b { margin-right: 6px; color: var(--warn); font-weight: 600; }
.p-foot div { display: flex; align-items: center; gap: 4px; }

.coins { overflow: hidden; }
.c-title { padding: 9px 12px 4px; font-size: var(--fs-sm); color: var(--text-3); }
.c-row { all: unset; box-sizing: border-box; width: 100%; display: flex; align-items: center; gap: 8px; padding: 10px 12px; border-top: 1px solid var(--line); cursor: pointer; }
.c-title + .c-row { border-top: 0; }
.c-row:active { background: var(--bg-2); }
.c-sym { flex: none; font-weight: 700; min-width: 52px; }
.c-side { flex: none; font-size: var(--fs-xs); padding: 1px 7px; border-radius: 6px; color: var(--c); background: var(--cb); font-weight: 600; }
.c-note { flex: 1; min-width: 0; font-size: var(--fs-sm); color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-metric { flex: none; font-size: var(--fs-sm); font-weight: 600; }
.c-go { flex: none; color: var(--text-4); }

.bb { display: grid; grid-template-columns: 1fr 1fr; overflow: hidden; }
.bb-col { padding: 10px 10px 8px; min-width: 0; }
.bb-col.is-up { background: linear-gradient(180deg, rgba(37, 167, 80, .08), transparent 60%); border-right: 1px solid var(--line); }
.bb-col.is-down { background: linear-gradient(180deg, rgba(202, 63, 100, .08), transparent 60%); }
.bb-h { display: flex; align-items: center; gap: 5px; font-weight: 700; color: var(--c); font-size: var(--fs-md); }
.bb-s { margin-left: auto; font-size: var(--fs-xs); color: var(--text-2); }
.bb ul { margin: 6px 0 0; padding-left: 16px; font-size: var(--fs-sm); line-height: 1.55; color: var(--text-2); }
.bb li { margin: 3px 0; }
@media (max-width: 360px) { .bb { grid-template-columns: 1fr; } .bb-col.is-up { border-right: 0; border-bottom: 1px solid var(--line); } }
</style>
