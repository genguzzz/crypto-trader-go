<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import ProposalCard from './ProposalCard.vue'
import Switch from '../ui/Switch.vue'
import Loading from '../ui/Loading.vue'
import InfoTip from '../InfoTip.vue'
import { ago } from '../../fmt'
import { store } from '../../store'
import { rt, refreshPending, remember } from '../../composables/realtrade'
import { realtradeApi, type RtConfig, type RtTrigger } from '../../realtradeApi'

/**
 * The AI's real-trade proposals inside the AI panel (owner only): pending ones as full cards to
 * answer, recent ones of any status as expandable rows, plus the execution mode and the triggers
 * that start headless agent runs (each can be switched on/off).
 */
const recent = ref<number[]>([])
const cfg = ref<RtConfig | null>(null)
const loading = ref(true)
const err = ref('')

async function loadRecent() {
  try {
    const d = await realtradeApi.list({ status: 'all', limit: 30 })
    if (!d) return
    d.proposals.forEach(remember)
    recent.value = d.proposals.filter(p => p.status !== 'pending').map(p => p.id)
  } catch (e: any) { err.value = e.message }
}
async function loadConfig() {
  try { cfg.value = await realtradeApi.config() } catch (e: any) { err.value = e.message }
}
onMounted(async () => {
  await Promise.all([refreshPending(), loadRecent(), loadConfig()])
  loading.value = false
})
watch(() => store.realtradeSeq, loadRecent)

const saving = ref('')
async function toggle(t: RtTrigger, on: boolean) {
  if (!cfg.value || saving.value) return
  const before = t.enabled
  t.enabled = on
  saving.value = t.id
  try {
    cfg.value = await realtradeApi.saveTriggers(cfg.value.triggers)
    err.value = ''
  } catch (e: any) {
    t.enabled = before
    err.value = e.message
  } finally {
    saving.value = ''
  }
}
const trigDesc = (t: RtTrigger) =>
  (t.kind === 'schedule' ? `每 ${t.everyMin ?? '?'} 分钟` : `告警规则 ${t.ruleIds?.length ? t.ruleIds.map(r => '#' + r).join(' ') : '全部'}`) + ` · 冷却 ${t.cooldownMin} 分钟`
const tgText = (s: string) => ({ ok: '已连接', disabled: '未启用', starting: '连接中', stopped: '已停止', error: '出错' } as Record<string, string>)[s] || s
</script>

<template>
  <div class="pl">
    <div class="mode">
      <span v-if="(cfg?.mode || rt.mode) === 'live'" class="tag bad">实盘</span>
      <span v-else class="tag warn">DRY RUN</span>
      <span class="muted">{{ (cfg?.mode || rt.mode) === 'live' ? '同意后会在 OKX 真实下单' : '同意后只生成并记录下单请求，不会真实下单' }}</span>
      <InfoTip v-if="cfg">
        <b>流程</b>：AI 在对话或信号触发的自动分析里提出交易（市价开仓 + 止盈止损，逐仓）。你在 Telegram、网页或 App 任一处先答复的为准；过期未答复自动作废。<br />
        <b>服务器限制</b>：杠杆 ≤ {{ cfg.limits.maxLeverage }}×，单笔保证金 ≤ {{ cfg.limits.maxMarginUsdt }} U，同时最多 {{ cfg.limits.maxOpen }} 笔，当日亏损 ≥ {{ cfg.limits.dailyLossUsdt }} U 停止，盈亏比 ≥ {{ cfg.limits.minRR }}，成交前价格偏离参考价超过 {{ cfg.limits.maxDriftPct }}% 作废，{{ cfg.limits.expiryMin }} 分钟内未答复过期。<br />
        <b>模式</b>：DRY RUN 时只生成并保存发给 OKX 的请求（可在卡片“记录”里看到），不会发送；切换实盘由服务器运维设置。
      </InfoTip>
      <span class="sp"></span>
      <span v-if="cfg" class="muted tg" :title="cfg.telegram.lastError || ''">Telegram {{ cfg.telegram.enabled ? tgText(cfg.telegram.state) : '未启用' }}</span>
    </div>
    <div v-if="err" class="down errl">{{ err }}</div>
    <Loading v-if="loading" />
    <template v-else>
      <h4>待审核 <span class="muted tnum">{{ rt.pending.length }}</span></h4>
      <p v-if="!rt.pending.length" class="muted empty">没有待审核的交易</p>
      <ProposalCard v-for="p in rt.pending" :key="p.id" :id="p.id" />

      <h4>最近</h4>
      <p v-if="!recent.length" class="muted empty">还没有记录</p>
      <ProposalCard v-for="id in recent" :key="id" :id="id" compact />

      <template v-if="cfg?.triggers?.length">
        <h4>自动触发 <InfoTip>到点或告警命中时，服务器让 AI 在后台跑一次分析（标题为“信号 · 触发器名”的对话），它可以提出交易，同样要你同意。关掉即不再触发。</InfoTip></h4>
        <div v-for="t in cfg.triggers" :key="t.id" class="trig">
          <div class="tmain">
            <span class="tl">{{ t.label || t.id }}</span>
            <span class="muted">{{ trigDesc(t) }}<template v-if="t.lastFiredAt"> · 上次 {{ ago(t.lastFiredAt) }}前</template></span>
            <span v-if="t.lastError" class="down terr">{{ t.lastError }}</span>
          </div>
          <Switch :model-value="t.enabled" :disabled="saving === t.id" :aria-label="`${t.label} 开关`" @update:model-value="v => toggle(t, v)" />
        </div>
      </template>
    </template>
  </div>
</template>

<style scoped>
.pl { display: grid; grid-template-columns: minmax(0, 1fr); gap: 6px; }
.mode { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; font-size: var(--fs-xs); padding: 4px 2px; }
.sp { flex: 1; }
.tg { white-space: nowrap; }
h4 { display: flex; align-items: center; gap: 6px; margin: 12px 2px 2px; font-size: var(--fs-md); font-weight: 600; }
.empty { margin: 4px 2px; font-size: var(--fs-sm); }
.errl { font-size: var(--fs-sm); }
.trig { display: flex; align-items: center; gap: 10px; padding: 8px 10px; border-radius: var(--radius-sm); background: var(--bg-1); border: 1px solid var(--line); }
.tmain { flex: 1; min-width: 0; display: grid; font-size: var(--fs-xs); }
.tl { font-size: var(--fs-md); color: var(--text-1); }
.terr { word-break: break-word; }
</style>
