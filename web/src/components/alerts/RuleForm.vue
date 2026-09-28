<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Send, Webhook } from 'lucide-vue-next'
import { api, type AlertChannels, type AlertRule, type AlertType } from '../../api'
import { store, ruleOptions } from '../../store'
import Modal from '../Modal.vue'
import FilterChip from '../ui/FilterChip.vue'
import Switch from '../ui/Switch.vue'
import { alertTypes } from './meta'

/** Create / edit one monitoring rule in a Modal (bottom drawer on phones). */
const props = defineProps<{ open: boolean; rule: AlertRule | null; channels: AlertChannels | null }>()
const emit = defineEmits<{ close: []; saved: [AlertRule] }>()

type Draft = Omit<AlertRule, 'id' | 'createdAt' | 'updatedAt' | 'summary'> & { id?: number }
const draft = ref<Draft>(blank())
const paramsByType = ref<Record<string, Record<string, any>>>({})
const err = ref('')
const saving = ref(false)

function blank(): Draft {
  const p = alertTypes[0].defaults()
  return { name: '', enabled: true, type: 'price_move', params: p, scope: 'active', instId: '', cooldownMin: 60, channels: { telegram: true, webhook: false } }
}
watch(() => props.open, o => {
  if (!o) return
  err.value = ''
  draft.value = props.rule ? JSON.parse(JSON.stringify(props.rule)) : blank()
  paramsByType.value = { [draft.value.type]: draft.value.params }
  if (draft.value.type === 'score' && !draft.value.params.ruleId) draft.value.params.ruleId = store.ruleId
})

// switching type keeps each type's params, so flipping back and forth loses nothing
const type = computed({
  get: () => draft.value.type,
  set: (t: AlertType) => {
    paramsByType.value[draft.value.type] = draft.value.params
    const p = paramsByType.value[t] || alertTypes.find(x => x.key === t)!.defaults()
    if (t === 'score' && !p.ruleId) p.ruleId = store.ruleId || store.meta?.rules[0]?.id || ''
    draft.value.type = t
    draft.value.params = p
  },
})
const p = computed(() => draft.value.params)
const dirOpts = (up: string, down: string) => [{ value: 'both', label: '双向' }, { value: 'up', label: up, tone: 'up' as const }, { value: 'down', label: down, tone: 'down' as const }]

const cooldownPresets = [0, 15, 60, 240, 1440].map(v => ({ value: v, label: v === 0 ? '无' : v < 60 ? `${v}m` : `${v / 60}h` }))

async function save() {
  saving.value = true
  err.value = ''
  try {
    const d = draft.value
    if (!d.name.trim()) d.name = autoName()
    emit('saved', await api.saveAlertRule(d as Partial<AlertRule>))
  } catch (e: any) {
    err.value = e.message
  } finally {
    saving.value = false
  }
}
function autoName() {
  const x = p.value
  if (type.value === 'price_move') return `${x.window} 异动 ${x.pct}%`
  if (type.value === 'score') return `${store.meta?.rules.find(r => r.id === x.ruleId)?.name || x.ruleId} ${x.side === 'long' ? '多' : '空'} ≥ ${x.min}`
  return `资金费 ${x.pctPerHour}%/h`
}
</script>

<template>
  <Modal :open="open" :title="rule ? '编辑监控规则' : '新建监控规则'" @close="emit('close')">
    <div class="rf">
      <label class="f">
        <span>名称</span>
        <input v-model="draft.name" maxlength="40" :placeholder="autoName()" />
      </label>

      <div class="f">
        <span>类型</span>
        <Segmented v-model="type" aria-label="类型" :options="alertTypes.map(t => ({ value: t.key, label: t.label, icon: t.icon }))" />
      </div>

      <template v-if="type === 'price_move'">
        <div class="f"><span>窗口</span>
          <Segmented v-model="p.window" size="sm" aria-label="窗口" :options="['15m', '1h', '4h', '24h'].map(v => ({ value: v, label: v }))" /></div>
        <div class="f"><span>方向</span><Segmented v-model="p.dir" size="sm" aria-label="方向" :options="dirOpts('涨', '跌')" /></div>
        <label class="f"><span>涨跌幅 ≥（%）</span>
          <span class="inl"><input type="number" min="0.1" max="100" step="0.5" v-model.number="p.pct" />
            <Segmented v-model="p.pct" size="sm" aria-label="阈值预设" :options="[2, 3, 5, 10].map(v => ({ value: v, label: v + '%' }))" /></span></label>
      </template>

      <template v-else-if="type === 'score'">
        <div class="f"><span>评分规则</span><USelect v-model="p.ruleId" :options="ruleOptions()" width="100%" aria-label="评分规则" /></div>
        <div class="f"><span>方向</span>
          <Segmented v-model="p.side" size="sm" aria-label="方向" :options="[{ value: 'long', label: '多头分', tone: 'up' }, { value: 'short', label: '空头分', tone: 'down' }]" /></div>
        <label class="f"><span>分数 ≥</span>
          <span class="inl"><input type="number" min="1" max="100" step="1" v-model.number="p.min" />
            <Segmented v-model="p.min" size="sm" aria-label="阈值预设" :options="[70, 80, 90].map(v => ({ value: v, label: String(v) }))" /></span></label>
      </template>

      <template v-else>
        <div class="f"><span>来源</span>
          <Segmented v-model="p.source" size="sm" aria-label="来源" :options="[{ value: 'settled', label: '已结算' }, { value: 'predicted', label: '预测' }]" /></div>
        <div class="f"><span>方向</span><Segmented v-model="p.dir" size="sm" aria-label="方向" :options="dirOpts('正费率', '负费率')" /></div>
        <label class="f"><span>每小时资金费 ≥（%）</span>
          <span class="inl"><input type="number" min="0.001" max="1" step="0.005" v-model.number="p.pctPerHour" />
            <Segmented v-model="p.pctPerHour" size="sm" aria-label="阈值预设" :options="[0.005, 0.01, 0.02, 0.05].map(v => ({ value: v, label: v + '%' }))" /></span></label>
      </template>

      <div class="f"><span>范围</span>
        <Segmented v-model="draft.scope" size="sm" aria-label="范围" :options="[{ value: 'active', label: '活跃池' }, { value: 'watch', label: '自选' }, { value: 'inst', label: '单个合约' }]" /></div>
      <label v-if="draft.scope === 'inst'" class="f"><span>合约</span>
        <input v-model="draft.instId" placeholder="BTC-USDT-SWAP" autocapitalize="characters" spellcheck="false" /></label>

      <label class="f"><span>冷却（分钟）</span>
        <span class="inl"><input type="number" min="0" max="10080" step="5" v-model.number="draft.cooldownMin" />
          <Segmented v-model="draft.cooldownMin" size="sm" aria-label="冷却预设" :options="cooldownPresets" /></span></label>

      <div class="f"><span>推送到</span>
        <span class="inl">
          <FilterChip v-model="draft.channels.telegram" label="Telegram" :icon="Send" />
          <FilterChip v-model="draft.channels.webhook" label="Webhook" :icon="Webhook" />
          <span v-if="(draft.channels.telegram && !channels?.telegram.configured) || (draft.channels.webhook && !channels?.webhook.configured)" class="tag warn">所选渠道未配置，只记入历史</span>
        </span>
      </div>

      <div class="f row"><span>启用</span><Switch v-model="draft.enabled" aria-label="启用" /></div>

      <div class="acts">
        <button class="primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
        <button @click="emit('close')">取消</button>
        <span v-if="err" class="err">{{ err }}</span>
      </div>
    </div>
  </Modal>
</template>

<style scoped>
.rf { display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
.f { display: grid; grid-template-columns: minmax(0, 1fr); justify-items: start; gap: 6px; align-content: start; min-width: 0; }
.f > input:not([type=number]), .f > .inl { justify-self: stretch; }
.f > .seg { max-width: 100%; overflow-x: auto; }
.f > span:first-child { color: var(--text-3); font-size: var(--fs-sm); }
.f.row { display: flex; align-items: center; gap: 12px; }
.inl { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }
input[type=number] { width: 110px; }
.acts { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding-top: 4px; }
.err { color: var(--down); font-size: var(--fs-sm); }
</style>
