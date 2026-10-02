<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { tradeApi, type Account, type Limits } from '../../tradeApi'
import Modal from '../Modal.vue'
import SettingRow from '../settings/SettingRow.vue'
import LimitsForm from './LimitsForm.vue'
import { limitErrors } from './meta'

/** Rename an account and edit its hard limits (applies to the next signal). */
const props = defineProps<{ open: boolean; account: Account }>()
const emit = defineEmits<{ close: []; saved: [Account] }>()
const name = ref('')
const limits = ref<Limits>({ ...props.account.limits })
const err = ref('')
const saving = ref(false)
watch(() => props.open, o => { if (o) { name.value = props.account.name; limits.value = { ...props.account.limits }; err.value = '' } })

const invalid = computed(() => !name.value.trim() || Object.keys(limitErrors(limits.value)).length > 0)
const changed = computed(() => name.value.trim() !== props.account.name || JSON.stringify(limits.value) !== JSON.stringify(props.account.limits))
async function save() {
  if (invalid.value) return
  saving.value = true
  try {
    emit('saved', await tradeApi.patchAccount(props.account.id, { name: name.value.trim(), limits: limits.value }))
  } catch (e: any) {
    err.value = e.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Modal :open="open" title="账户设置" wide @close="emit('close')">
    <SettingRow label="账户名称" for-id="as-name" :changed="name.trim() !== account.name" :error="name.trim() ? '' : '请填写名称'">
      <input id="as-name" v-model="name" maxlength="40" class="txt" />
    </SettingRow>
    <LimitsForm v-model="limits" :base="account.limits" />
    <div class="ft">
      <span class="small" :class="err ? 'down' : 'muted'">{{ err || '修改从下一个信号开始生效，不影响已有持仓' }}</span>
      <span class="acts">
        <button type="button" @click="emit('close')">取消</button>
        <button type="button" class="primary" :disabled="saving || invalid || !changed" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </span>
    </div>
  </Modal>
</template>

<style scoped>
.txt { width: 240px; max-width: 100%; height: 32px; }
.ft { position: sticky; bottom: -18px; display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-top: 8px; padding: 12px 0 4px; background: var(--bg-1); border-top: 1px solid var(--line); }
.acts { display: inline-flex; gap: 8px; margin-left: auto; }
.acts button { height: 32px; padding: 0 16px; }
button:disabled { opacity: .45; cursor: default; }
.small { font-size: var(--fs-sm); }
</style>
