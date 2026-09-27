<script setup lang="ts">
import OrderBookView from '../market/OrderBookView.vue'
import type { DetailCtx } from './types'
defineProps<{ ctx: DetailCtx }>()
</script>

<template>
  <div>
    <div class="hint">
      <InfoTip label="怎么看">
        <b>数据</b>：OKX 永续实时盘口快照（每侧最多 400 档，每秒刷新），金额按 价格 × 张数 × 合约面值 换算成 <b>USDT</b>。<br />
        <b>深度图</b>：横轴是相对中间价的偏离（%），纵轴是从中间价到该价位的累计挂单金额。左绿 = 买盘，右红 = 卖盘。<br />
        <b>悬浮/拖动</b>：把鼠标（手机上手指）放到任意位置，例如 +2%，显示“用市价买单把价格推到 +2% 需要吃掉多少 USDT、成交均价和平均滑点”；负方向同理是卖出压低。<br />
        <b>速查表</b>：常用偏离下的所需资金；“+”表示超出已获取的 400 档，实际更多。<br />
        盘口会被撤单/新挂单快速改变，数字只代表此刻的挂单，不含隐藏单和冰山单。
      </InfoTip>
    </div>
    <OrderBookView :id="ctx.id" />
  </div>
</template>

<style scoped>.hint { margin: -4px 0 8px; }</style>
