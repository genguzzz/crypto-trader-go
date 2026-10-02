<script setup lang="ts">
import type { ExecMode, StrategyVersion, Universe } from '../../tradeApi'
import UniversePicker from './UniversePicker.vue'
import ParamsForm from './ParamsForm.vue'
import { execLabel } from './meta'

/** Configuration of one strategy instance: exec mode, universe, parameters. */
defineProps<{ version: StrategyVersion; aiReady?: boolean }>()
const params = defineModel<Record<string, number>>('params', { required: true })
const universe = defineModel<Universe>('universe', { required: true })
const exec = defineModel<ExecMode>('exec', { required: true })
const execOpts = (Object.keys(execLabel) as ExecMode[]).map(k => ({ value: k, label: execLabel[k].t }))
</script>

<template>
  <div class="if">
    <section>
      <div class="sh">执行方式
        <InfoTip>
          <b>自动</b>：信号直接经过账户风控与仓位计算后下单。<br />
          <b>AI 审核</b>：先把信号、持仓、行情摘要交给 AI，AI 决定执行或跳过（可缩小仓位、调整止损止盈），再经风控。AI 不可用或超时按“跳过”处理。<br />
          <b>只记录</b>：只记录信号和它的去向，从不下单，用来观察新策略。<br />
          如果账户开启了人工确认，确认发生在 AI 审核之后、风控之前。
        </InfoTip>
      </div>
      <Segmented v-model="exec" size="sm" aria-label="执行方式" :options="execOpts" />
      <div class="hint">{{ execLabel[exec].d }}<span v-if="exec === 'ai' && aiReady === false" class="warn"> · AI 尚未授权，信号会被跳过（设置 → AI 授权）</span></div>
    </section>
    <section>
      <div class="sh">交易标的
        <InfoTip>
          <b>固定列表</b>：只交易选中的合约。<br />
          <b>规则筛选</b>：按评分规则在监控池里取分数最高的前 N 个，按“重新筛选”间隔更新；被移出的合约不再开新仓，已有持仓照常由策略管理。分数未经验证，只是排序依据。
        </InfoTip>
      </div>
      <UniversePicker v-model="universe" />
    </section>
    <section>
      <div class="sh">参数 <span class="muted">· {{ version.timeframe }} 周期</span></div>
      <ParamsForm v-model="params" :specs="version.params" />
    </section>
  </div>
</template>

<style scoped>
.if { display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; min-width: 0; }
section { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; min-width: 0; }
section > .seg { justify-self: start; }
.sh { display: flex; align-items: center; gap: 4px; font-size: var(--fs-sm); font-weight: 600; color: var(--text-1); }
.sh .muted { font-weight: 400; }
.hint { font-size: var(--fs-xs); color: var(--text-3); }
.warn { color: var(--warn); }
</style>
