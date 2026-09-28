<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { Plus, Pencil, Trash2, Send, Webhook, BellRing } from 'lucide-vue-next'
import { api, type AlertChannels, type AlertEvent, type AlertRule } from '../api'
import { store } from '../store'
import { time, ago } from '../fmt'
import { useBusy } from '../composables/busy'
import Loading from '../components/ui/Loading.vue'
import Switch from '../components/ui/Switch.vue'
import RuleForm from '../components/alerts/RuleForm.vue'
import ChannelsCard from '../components/alerts/ChannelsCard.vue'
import { typeMeta, scopeLabel, cooldownLabel, deliveryTag, channelName } from '../components/alerts/meta'

const rules = ref<AlertRule[] | null>(null)
const history = ref<AlertEvent[] | null>(null)
const channels = ref<AlertChannels | null>(null)
const error = ref('')
const { busy, track } = useBusy()

const load = track(async () => {
  try {
    const [r, h] = await Promise.all([api.alertRules(), api.alertHistory(100)])
    rules.value = r.rules
    history.value = h
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
})
onMounted(() => {
  load()
  api.alertChannels().then(c => (channels.value = c)).catch(e => (error.value = e.message))
})
// rules are evaluated once per confirmed minute: refresh with each new snapshot, and as a fallback
watch(() => store.snap?.asOfMinute, (m, old) => { if (m && old && m !== old) load() })
const timer = window.setInterval(() => { if (!document.hidden) load() }, 60_000)
onUnmounted(() => clearInterval(timer))

const formOpen = ref(false)
const editing = ref<AlertRule | null>(null)
function openForm(r: AlertRule | null) { editing.value = r; formOpen.value = true }
function onSaved(r: AlertRule) {
  formOpen.value = false
  const list = rules.value || []
  const i = list.findIndex(x => x.id === r.id)
  rules.value = i >= 0 ? list.map(x => (x.id === r.id ? r : x)) : [...list, r]
}

const toggling = ref<number | null>(null)
async function toggle(r: AlertRule, on: boolean) {
  toggling.value = r.id
  try {
    onSaved(await api.saveAlertRule({ ...r, enabled: on }))
  } catch (e: any) {
    error.value = e.message
  } finally {
    toggling.value = null
  }
}
async function remove(r: AlertRule) {
  if (!confirm(`删除监控规则「${r.name}」？（历史记录保留）`)) return
  try {
    await api.deleteAlertRule(r.id)
    rules.value = (rules.value || []).filter(x => x.id !== r.id)
  } catch (e: any) {
    error.value = e.message
  }
}
const shown = ref(30)
const deliveries = (ev: AlertEvent) => Object.entries(ev.delivery || {}).map(([ch, v]) => ({ ch, v, ...deliveryTag(v) }))
</script>

<template>
  <div class="page alerts">
    <div class="banner" v-if="error">{{ error }}</div>
    <div class="cols">
        <div class="panel rules-p">
          <div class="ph">
            <h3>监控规则 <InfoTip>
              <b>何时检查</b>：每个已收盘分钟确认后，用当时的事实面板与规则分数检查一次（盘中 10 秒预览不触发）。<br />
              <b>触发方式</b>：条件从“不满足”变为“满足”的那一分钟触发一次（边沿触发），持续满足不重复提醒；回落后再次满足会再触发。<br />
              <b>冷却</b>：同一规则、同一合约两次提醒的最短间隔；冷却期内的新触发被忽略。<br />
              <b>数据缺失</b>：缺少所需字段（如新币、数据缺口）时跳过，不算“不满足”。规则分数只对活跃池与自选计算。<br />
              <b>第一分钟</b>：新建/修改规则或服务重启后的第一分钟只记录状态、不发提醒，已经满足条件的合约要等回落后再次满足才触发（避免一次性提醒整个池子）。想确认推送是否通，用右侧“发送测试”。<br /><br />
              <b>价格异动</b>：窗口内涨跌幅（收盘价，简单收益）。<b>规则分数</b>：所选评分规则的多头/空头分（未验证）。<b>资金费</b>：已结算资金费或当前预测资金费，按每小时换算（4h/8h 结算已折算）。
            </InfoTip></h3>
            <button class="primary add" @click="openForm(null)"><Plus :size="14" />新建</button>
          </div>

          <Loading v-if="!rules" text="加载规则" />
          <div v-else-if="!rules.length" class="empty">
            <BellRing :size="20" class="muted" />
            <span class="muted">还没有监控规则</span>
          </div>
          <ul v-else class="rules">
            <li v-for="r in rules" :key="r.id" class="rule" :class="{ off: !r.enabled }">
              <Switch :model-value="r.enabled" :disabled="toggling === r.id" :aria-label="`启用 ${r.name}`" @update:model-value="v => toggle(r, v)" />
              <div class="rm">
                <div class="rn">
                  <component :is="typeMeta(r.type).icon" :size="14" class="ti" />
                  <b>{{ r.name }}</b>
                </div>
                <div class="rs">{{ r.summary }}</div>
                <div v-if="r.error" class="rerr">{{ r.error }}</div>
              </div>
              <div class="meta">
                <span class="tag">{{ scopeLabel(r) }}</span>
                <span class="tag" title="冷却">{{ cooldownLabel(r.cooldownMin) }}</span>
                <Send v-if="r.channels.telegram" :size="13" class="chi" :class="{ warn: !channels?.telegram.configured }" :title="channels?.telegram.configured ? 'Telegram' : 'Telegram（未配置）'" />
                <Webhook v-if="r.channels.webhook" :size="13" class="chi" :class="{ warn: !channels?.webhook.configured }" :title="channels?.webhook.configured ? 'Webhook' : 'Webhook（未配置）'" />
              </div>
              <div class="last">
                <span class="lbl">最近触发</span>
                <span class="tnum" :title="r.lastFired ? time(r.lastFired, true) : ''">{{ r.lastFired ? ago(r.lastFired) + '前' : '—' }}</span>
              </div>
              <div class="acts">
                <button class="icon" :aria-label="`编辑 ${r.name}`" @click="openForm(r)"><Pencil :size="14" /></button>
                <button class="icon" :aria-label="`删除 ${r.name}`" @click="remove(r)"><Trash2 :size="14" /></button>
              </div>
            </li>
          </ul>
        </div>

        <div class="panel hist">
          <h3>最近触发 <InfoTip>最近 100 条（服务器保留 30 天、最多 5000 条）。投递状态：已送达 / 发送中 / 失败（悬停或点按看原因）。每个已收盘分钟自动刷新。</InfoTip></h3>
          <div class="rel">
            <Loading v-if="!history" text="加载历史" />
            <p v-else-if="!history.length" class="muted empty-line">暂无触发记录</p>
            <ul v-else class="evs">
              <li v-for="ev in history.slice(0, shown)" :key="ev.id" class="ev">
                <span class="et tnum">{{ time(ev.ts, true) }}</span>
                <RouterLink :to="`/?sel=${ev.instId}`" class="ei">{{ ev.instId.replace('-USDT-SWAP', '') }}</RouterLink>
                <span class="em">{{ ev.message.replace(ev.instId + ' ', '') }}<small class="muted"> · {{ ev.ruleName }}</small></span>
                <span class="ed">
                  <span v-for="d in deliveries(ev)" :key="d.ch" class="tag" :class="d.cls" :title="`${channelName[d.ch] || d.ch}：${d.v}`">
                    <Send v-if="d.ch === 'telegram'" :size="11" /><Webhook v-else :size="11" />{{ d.t }}
                  </span>
                  <span v-if="!deliveries(ev).length" class="muted small">仅记录</span>
                </span>
              </li>
            </ul>
            <Loading overlay :show="busy && !!history" />
          </div>
          <button v-if="history && history.length > shown" class="more" @click="shown += 50">显示更多（共 {{ history.length }} 条）</button>
        </div>

      <aside class="side">
        <ChannelsCard :channels="channels" @update="c => (channels = c)" />
      </aside>
    </div>

    <RuleForm :open="formOpen" :rule="editing" :channels="channels" @close="formOpen = false" @saved="onSaved" />
  </div>
</template>

<style scoped>
/* phones: rules → channels → history; PC: channels in a right column */
.cols { display: grid; grid-template-columns: minmax(0, 1fr); grid-template-areas: "rules" "side" "hist"; gap: 12px; align-items: start; }
.rules-p { grid-area: rules; } .hist { grid-area: hist; } .side { grid-area: side; }
@media (min-width: 1100px) {
  .cols { grid-template-columns: minmax(0, 1fr) 360px; grid-template-areas: "rules side" "hist side"; grid-template-rows: auto 1fr; }
}
.more { display: block; margin: 8px auto 0; background: transparent; color: var(--text-2); }
.banner { border-radius: var(--radius-sm); }
.ph { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.ph h3 { margin: 0; }
.add { display: inline-flex; align-items: center; gap: 4px; height: 28px; }

.empty { display: flex; align-items: center; justify-content: center; gap: 8px; padding: 28px 0; }
.rules { list-style: none; margin: 0; padding: 0; }
.rule {
  display: grid; grid-template-columns: auto minmax(0, 1fr) auto 88px auto; align-items: center; gap: 14px;
  padding: 10px 4px; border-top: 1px solid var(--line);
}
.rule:first-child { border-top: none; }
.rule.off .rm, .rule.off .meta, .rule.off .last { opacity: .5; }
.rn { display: flex; align-items: center; gap: 6px; min-width: 0; }
.rn b { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ti { color: var(--text-3); flex: none; }
.rs { color: var(--text-2); font-size: var(--fs-sm); margin-top: 2px; }
.rerr { color: var(--warn); font-size: var(--fs-xs); margin-top: 2px; }
.meta { display: flex; align-items: center; gap: 6px; }
.chi { color: var(--text-2); }
.chi.warn { color: var(--warn); }
.last { display: grid; justify-items: end; line-height: 1.3; }
.last .lbl { font-size: var(--fs-xs); color: var(--text-3); }
.last span:last-child { color: var(--text-2); font-size: var(--fs-sm); }
.acts { display: flex; gap: 4px; }
button.icon { display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 0; background: transparent; border-color: transparent; color: var(--text-3); }
@media (hover: hover) { button.icon:hover { color: var(--text-1); border-color: var(--line-2); } }

.rel { position: relative; min-height: 48px; }
.empty-line { margin: 8px 0; font-size: var(--fs-sm); }
.evs { list-style: none; margin: 0; padding: 0; }
.ev { display: grid; grid-template-columns: 76px 72px minmax(0, 1fr) auto; gap: 10px; align-items: baseline; padding: 7px 0; border-top: 1px solid var(--line); }
.ev:first-child { border-top: none; }
.et { color: var(--text-3); font-size: var(--fs-xs); }
.ei { color: var(--text-1); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.em { color: var(--text-2); font-size: var(--fs-sm); min-width: 0; overflow-wrap: anywhere; }
.ed { display: flex; gap: 4px; justify-content: flex-end; flex-wrap: wrap; }
.small { font-size: var(--fs-xs); }

@media (max-width: 700px) {
  .rule { grid-template-columns: auto minmax(0, 1fr) auto; grid-template-areas: "sw main acts" ". meta last"; row-gap: 6px; gap: 10px; }
  .rule > :first-child { grid-area: sw; }
  .rm { grid-area: main; }
  .acts { grid-area: acts; }
  .meta { grid-area: meta; flex-wrap: wrap; }
  .last { grid-area: last; grid-auto-flow: column; gap: 6px; align-items: baseline; }
  .ev { grid-template-columns: minmax(0, auto) minmax(0, 1fr) auto; grid-template-areas: "i t d" "m m m"; row-gap: 2px; }
  .et { grid-area: t; } .ei { grid-area: i; } .em { grid-area: m; } .ed { grid-area: d; }
}
</style>
