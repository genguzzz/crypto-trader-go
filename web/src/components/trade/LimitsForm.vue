<script setup lang="ts">
import { computed } from 'vue'
import type { Limits } from '../../tradeApi'
import SettingRow from '../settings/SettingRow.vue'
import NumInput from '../ui/NumInput.vue'
import Switch from '../ui/Switch.vue'
import RiskSlider from './RiskSlider.vue'
import { limitErrors } from './meta'

/** Account hard limits (wizard step 2 and "修改限额"). Percent fields are stored as fractions. */
const l = defineModel<Limits>({ required: true })
const props = defineProps<{ base?: Limits }>() // saved values: marks changed rows when editing
const e = computed(() => limitErrors(l.value))
const ch = (k: keyof Limits) => !!props.base && props.base[k] !== l.value[k]
</script>

<template>
  <div class="lf">
    <div class="grp">仓位与风险
      <InfoTip>
        <b>账户硬规则</b>：策略和 AI 都不能绕过。每个信号先过这些规则，再按风险计算仓位。<br />
        <b>单笔风险</b>：信号强度为 1 时，止损被打到亏掉的权益比例，可设 1%–500%（滑杆为对数刻度）。超过 100% 意味着一次止损可能亏掉超过全部权益；实际仓位仍受杠杆和单合约上限约束。仓位 = 权益 × 单笔风险 × 强度 ÷ (|入场 − 止损| × 合约面值)，再受杠杆、单合约上限约束，按最小下单量向下取整。<br />
        <b>单合约上限</b>：一个合约占用的保证金不超过权益的这个比例（逐仓）。<br />
        <b>日亏损熔断</b>：当天（UTC+8）已实现 + 未实现亏损达到权益的这个比例后，当天不再接受开仓信号，次日自动恢复；平仓、止损不受影响。<br />
        <b>冷却</b>：某合约平仓后多少分钟内不再开同一合约。
      </InfoTip>
    </div>
    <SettingRow label="最大持仓数" desc="同时持有的合约个数" for-id="l-mp" :changed="ch('maxPositions')" :error="e.maxPositions">
      <NumInput id="l-mp" v-model="l.maxPositions" unit="个" :min="1" :max="100" :step="1" />
    </SettingRow>
    <SettingRow label="最大杠杆" desc="逐仓杠杆上限，信号请求更高时按此截断" for-id="l-lv" :changed="ch('maxLeverage')" :error="e.maxLeverage">
      <div class="line">
        <Segmented :model-value="l.maxLeverage" size="sm" aria-label="杠杆预设" :options="[2, 3, 5, 10].map(v => ({ value: v, label: v + 'x' }))" @update:model-value="v => (l.maxLeverage = v)" />
        <NumInput id="l-lv" v-model="l.maxLeverage" unit="x" :min="1" :max="100" width="56px" />
      </div>
    </SettingRow>
    <SettingRow label="单笔风险" for-id="l-rk" :changed="ch('riskPct')" :error="e.riskPct">
      <template #desc>止损触发时亏掉的权益比例（强度 1），1%–500%<span v-if="l.riskPct > 1" class="warn"> · 超过 100%：一次止损亏掉的超过全部权益</span></template>
      <RiskSlider id="l-rk" v-model="l.riskPct" />
    </SettingRow>
    <SettingRow label="单合约上限" desc="一个合约的保证金占权益比例" for-id="l-mi" :changed="ch('maxInstPct')" :error="e.maxInstPct">
      <NumInput id="l-mi" v-model="l.maxInstPct" unit="%" :scale="100" :min="1" :max="100" />
    </SettingRow>
    <SettingRow label="日亏损熔断" desc="当日亏损达到权益比例后停止开仓，次日恢复；0 = 不熔断" for-id="l-dl" :changed="ch('dailyLossPct')" :error="e.dailyLossPct">
      <NumInput id="l-dl" v-model="l.dailyLossPct" unit="%" :scale="100" :min="0" :max="100" />
    </SettingRow>
    <SettingRow label="冷却时间" desc="平仓后多久才能再开同一合约" for-id="l-cd" :changed="ch('cooldownMin')" :error="e.cooldownMin">
      <NumInput id="l-cd" v-model="l.cooldownMin" unit="分钟" :min="0" :step="1" />
    </SettingRow>
    <SettingRow label="必须带止损" desc="开仓信号没有止损价时直接拒绝" :changed="ch('requireStop')">
      <Switch v-model="l.requireStop" aria-label="必须带止损" />
    </SettingRow>

    <div class="grp">成本
      <InfoTip>
        <b>滑点容忍</b>：市价入场的最差成交价 = 中间价 ×（1 ± bp/10000），盘口吃不到的部分取消。止损、止盈触发后按市价成交，不受此限制。<br />
        <b>手续费</b>：按成交额收取，默认 OKX 普通用户档 taker 5 bp / maker 2 bp。资金费按真实结算记录收取。
      </InfoTip>
    </div>
    <SettingRow label="滑点容忍" desc="市价入场允许偏离中间价的幅度" for-id="l-sl" :changed="ch('slippageBp')" :error="e.slippageBp">
      <NumInput id="l-sl" v-model="l.slippageBp" unit="bp" :min="0" :max="500" />
    </SettingRow>
    <SettingRow label="手续费" desc="taker / maker" :changed="ch('takerFeeBp') || ch('makerFeeBp')" :error="e.takerFeeBp || e.makerFeeBp">
      <div class="line">
        <NumInput v-model="l.takerFeeBp" unit="bp" aria-label="taker 手续费" width="56px" />
        <NumInput v-model="l.makerFeeBp" unit="bp" aria-label="maker 手续费" width="56px" />
      </div>
    </SettingRow>

    <div class="grp">人工确认
      <InfoTip>
        需要确认的信号会通过交互通道（Telegram 与本页“待确认”）推送给你，选择执行、半仓或跳过。<br />
        <b>超时</b>：在设定时间内没有回应，按“超时默认”处理。信号自身也有有效期（TTL），超过即作废。<br />
        回测账户不会等待人工确认。
      </InfoTip>
    </div>
    <SettingRow label="需要确认" desc="哪些信号在下单前要你确认" :changed="ch('confirm')">
      <Segmented v-model="l.confirm" size="sm" aria-label="人工确认" :options="[{ value: 'off', label: '关闭' }, { value: 'open', label: '仅开仓' }, { value: 'all', label: '全部' }]" />
    </SettingRow>
    <template v-if="l.confirm !== 'off'">
      <SettingRow label="确认超时" for-id="l-to" :changed="ch('confirmTimeoutS')" :error="e.confirmTimeoutS">
        <NumInput id="l-to" v-model="l.confirmTimeoutS" unit="秒" :min="10" :max="3600" :step="10" />
      </SettingRow>
      <SettingRow label="超时默认" desc="无人回应时的处理" :changed="ch('confirmDefault')">
        <Segmented v-model="l.confirmDefault" size="sm" aria-label="超时默认" :options="[{ value: 'skip', label: '跳过' }, { value: 'execute', label: '执行' }]" />
      </SettingRow>
    </template>
  </div>
</template>

<style scoped>
.lf { display: grid; grid-template-columns: minmax(0, 1fr); }
.grp { display: flex; align-items: center; gap: 4px; margin-top: 14px; padding-bottom: 2px; font-size: var(--fs-xs); font-weight: 600; color: var(--text-3); letter-spacing: .3px; border-bottom: 1px solid var(--line); }
.grp:first-child { margin-top: 0; }
.warn { color: var(--warn); }
.line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.lf :deep(.sr) { padding: 12px 0; }
@media (max-width: 700px) { .line { justify-content: flex-start; } }
</style>
