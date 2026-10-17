<script setup lang="ts">
import { ref } from 'vue'
import OIChart from './charts/OIChart.vue'
import FundingChart from './charts/FundingChart.vue'

const props = defineProps<{ id: string }>()
const oiN = ref(0)
const fN = ref(0)
void props
</script>

<template>
  <div class="grid">
    <div class="panel">
      <h3>持仓量 OI（币本位，7 天） <InfoTip>
        <b>口径</b>：oiCcy = 以标的币数量计的未平仓量。用币数而不是美元，是为了排除“价格上涨导致美元持仓变大”的假象。<br />
        <b>数据</b>：OKX rubik 1H 历史 + 近 8 小时 5m + 本机每分钟录制；图上每 5 分钟取最后一个样本（当前 {{ oiN }} 个点）。<br />
        <b>注意</b>：OI 是多空双方共同的未平仓合约，OI 增加只说明“新开仓多于平仓”，<b>不能</b>说明是多头还是空头在加仓。
      </InfoTip></h3>
      <OIChart :id="id" @loaded="n => (oiN = n)" />
    </div>
    <div class="panel">
      <h3>资金费（已结算，近 14 天） <InfoTip>
        每根柱是一次<b>已结算</b>的资金费率（单次，%）。正值 = 多头付给空头，负值 = 空头付给多头。<br />
        OKX 不同合约结算间隔不同（4h 或 8h），跨币比较请看事实面板里的“资金费/小时”（当前 {{ fN }} 次记录）。
      </InfoTip></h3>
      <FundingChart :id="id" @loaded="r => (fN = r.length)" />
    </div>
  </div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
@media (max-width: 900px) { .grid { grid-template-columns: 1fr; } }
@container (max-width: 640px) { .grid { grid-template-columns: 1fr; } } /* narrow workbench side panel */
</style>
