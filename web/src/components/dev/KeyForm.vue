<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { KeyRound, Timer, TriangleAlert } from 'lucide-vue-next'
import { api, type ApiKey, type KeyKind } from '../../api'
import Modal from '../Modal.vue'
import CopyBlock from './CopyBlock.vue'
import { claudeCodeSnippet, curlSnippet, mcpJsonSnippet, ttlLabel } from './snippets'

/**
 * Create a key / token, or edit an existing one's name and limits. After creation the secret is
 * shown once (with ready-to-paste snippets); it cannot be retrieved again.
 */
const props = defineProps<{ open: boolean; edit: ApiKey | null; maxRate: number; maxBurst: number }>()
const emit = defineEmits<{ close: []; saved: [ApiKey]; created: [ApiKey & { secret: string }] }>()

const kind = ref<KeyKind>('key')
const name = ref('')
const rate = ref(60)
const burst = ref<number | ''>('')
const ttl = ref(0)
const err = ref('')
const saving = ref(false)
const created = ref<(ApiKey & { secret: string }) | null>(null)

const ttlOptions = computed(() => (kind.value === 'token' ? [1, 24, 168, 720] : [0, 168, 720, 2160, 8760]).map(h => ({ value: h, label: ttlLabel(h) })))
const defaultBurst = computed(() => Math.max(1, Math.min(rate.value || 1, Math.max(5, Math.floor((rate.value || 0) / 3)), props.maxBurst)))

watch(() => props.open, o => {
  if (!o) return
  err.value = ''
  created.value = null
  const k = props.edit
  kind.value = k?.kind ?? 'key'
  name.value = k?.name ?? ''
  rate.value = k?.ratePerMin ?? 60
  burst.value = k ? k.burst : ''
  ttl.value = 0
})
watch(kind, k => { if (!props.edit) ttl.value = k === 'token' ? 24 : 0 })

const errors = computed(() => {
  const e: Record<string, string> = {}
  if (!name.value.trim()) e.name = '请输入名称'
  if (!Number.isInteger(rate.value) || rate.value < 1 || rate.value > props.maxRate) e.rate = `1–${props.maxRate} 的整数`
  if (burst.value !== '' && (!Number.isInteger(burst.value) || burst.value < 1 || burst.value > props.maxBurst)) e.burst = `1–${props.maxBurst} 的整数`
  return e
})
const valid = computed(() => !Object.keys(errors.value).length)

async function save() {
  if (!valid.value) return
  saving.value = true
  err.value = ''
  try {
    const b = burst.value === '' ? defaultBurst.value : burst.value
    if (props.edit) {
      emit('saved', await api.updateKey(props.edit.id, { name: name.value.trim(), ratePerMin: rate.value, burst: b }))
    } else {
      created.value = await api.createKey({ kind: kind.value, name: name.value.trim(), ratePerMin: rate.value, burst: b, ttlHours: ttl.value })
      emit('created', created.value)
    }
  } catch (e: any) {
    err.value = e.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Modal :open="open" :title="created ? '已创建：请立即复制' : edit ? '编辑访问凭证' : '新建访问凭证'" @close="emit('close')">
    <div v-if="created" class="kf">
      <div class="warn-box"><TriangleAlert :size="15" /><span>密钥<b>只显示这一次</b>，服务器只保存哈希，关闭后无法再查看；遗失请吊销后重建。</span></div>
      <CopyBlock :text="created.secret" :label="`${created.kind === 'token' ? 'Token' : 'API Key'} · ${created.name}`" secret />
      <CopyBlock :text="curlSnippet(created.secret)" label="REST 调用示例" />
      <CopyBlock :text="claudeCodeSnippet(created.secret)" label="接入 Claude Code（MCP）" />
      <CopyBlock :text="mcpJsonSnippet(created.secret)" label="通用 MCP 配置（JSON）" />
      <div class="acts"><button class="primary" @click="emit('close')">我已保存</button></div>
    </div>

    <div v-else class="kf">
      <div v-if="!edit" class="f">
        <span>类型</span>
        <Segmented v-model="kind" aria-label="类型" :options="[{ value: 'key', label: 'API Key', icon: KeyRound }, { value: 'token', label: 'Token', icon: Timer }]" />
        <small class="muted">{{ kind === 'key' ? '长期凭证，适合常驻 Agent / 服务；可选过期时间。' : '短期凭证，必须设置有效期（最长 30 天），适合临时分析或分享给他人。' }}</small>
      </div>
      <label class="f" :class="{ invalid: errors.name && name !== '' }">
        <span>名称</span>
        <input v-model="name" maxlength="64" placeholder="例如：research-agent" />
      </label>
      <label class="f">
        <span>频率上限（次 / 分钟）</span>
        <span class="inl"><input type="number" min="1" :max="maxRate" step="10" v-model.number="rate" />
          <Segmented v-model="rate" size="sm" aria-label="频率预设" :options="[30, 60, 120, 300, 600].map(v => ({ value: v, label: String(v) }))" /></span>
        <small v-if="errors.rate" class="err">{{ errors.rate }}</small>
      </label>
      <label class="f">
        <span>突发容量（令牌）</span>
        <span class="inl"><input type="number" min="1" :max="maxBurst" v-model.number="burst" :placeholder="String(defaultBurst)" />
          <small class="muted">留空 = {{ defaultBurst }}；空闲时最多可连续调用这么多次，之后按 {{ rate }}/分钟 匀速恢复</small></span>
        <small v-if="errors.burst" class="err">{{ errors.burst }}</small>
      </label>
      <div v-if="!edit" class="f">
        <span>有效期</span>
        <Segmented v-model="ttl" size="sm" aria-label="有效期" :options="ttlOptions" />
      </div>
      <div class="acts">
        <button class="primary" :disabled="saving || !valid" @click="save">{{ saving ? '保存中…' : edit ? '保存' : '创建' }}</button>
        <button @click="emit('close')">取消</button>
        <span v-if="err" class="err">{{ err }}</span>
      </div>
    </div>
  </Modal>
</template>

<style scoped>
.kf { display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
.f { display: grid; grid-template-columns: minmax(0, 1fr); justify-items: start; gap: 6px; min-width: 0; }
.f > input, .f > .inl { justify-self: stretch; }
.f > span:first-child { color: var(--text-3); font-size: var(--fs-sm); }
.f small { font-size: var(--fs-xs); line-height: 1.5; }
.inl { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-width: 0; }
input[type=number] { width: 110px; }
.acts { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding-top: 4px; }
.err { color: var(--down); font-size: var(--fs-sm); }
.warn-box { display: flex; gap: 8px; align-items: flex-start; padding: 10px 12px; border-radius: var(--radius-sm); background: var(--warn-bg); color: var(--warn); font-size: var(--fs-sm); line-height: 1.5; }
.warn-box svg { flex: none; margin-top: 2px; }
</style>
