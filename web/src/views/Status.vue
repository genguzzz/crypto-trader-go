<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import { time, ago, usd } from '../fmt'
const s = ref<any>(null)
let timer: number | undefined
const load = async () => { s.value = await api.status() }
onMounted(() => { load(); timer = window.setInterval(load, 3000) })
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="page" v-if="s">
    <div class="grid">
      <div class="panel">
        <h3>运行</h3>
        <table><tbody>
          <tr><td>阶段</td><td><b>{{ s.phase }}</b></td></tr>
          <tr><td>运行时长</td><td>{{ s.uptime }}</td></tr>
          <tr><td>1m 回填</td><td>{{ s.backfillDone }} / {{ s.backfillTotal }}</td></tr>
          <tr><td>最新确认分钟</td><td>{{ time(s.asOfMinute + 60000) }}（计算于 {{ ago(s.computedAt) }} 前）</td></tr>
          <tr><td>历史回放</td><td class="wrap">{{ s.replay }}</td></tr>
          <tr><td>1m 历史回补</td><td class="wrap">{{ s.history }}</td></tr>
        </tbody></table>
      </div>
      <div class="panel">
        <h3>覆盖</h3>
        <table><tbody>
          <tr><td>可交易合约</td><td>{{ s.coverage.eligible }}</td></tr>
          <tr><td>12h 窗口完整</td><td>{{ s.coverage.ready }}</td></tr>
          <tr><td>预热中</td><td>{{ s.coverage.warming }}</td></tr>
          <tr><td>数据缺口</td><td :class="s.coverage.stale ? 'down' : ''">{{ s.coverage.stale }}</td></tr>
          <tr><td>上市不足 12h</td><td>{{ s.coverage.new }}</td></tr>
          <tr><td>活跃池</td><td><b>{{ s.coverage.active }}</b> / {{ s.config.topN }}（12h &gt; {{ usd(s.config.threshold) }}）</td></tr>
        </tbody></table>
      </div>
      <div class="panel">
        <h3>OKX 连接</h3>
        <table>
          <thead><tr><th>WS</th><th>状态</th><th class="num">订阅</th><th class="num">消息</th><th class="num">重连</th><th class="num">最近</th></tr></thead>
          <tbody><tr v-for="w in s.ws" :key="w.name">
            <td>{{ w.name }}</td><td><span class="dot" :class="w.connected ? 'ok' : 'bad'"></span></td>
            <td class="num">{{ w.subscriptions }}</td><td class="num">{{ w.messages }}</td><td class="num">{{ w.reconnects }}</td><td class="num">{{ (w.lastMsgAgoMs / 1000).toFixed(1) }}s</td>
          </tr></tbody>
        </table>
        <p class="note">REST 请求 {{ s.restRequests }} · 错误 {{ s.restErrors }} · 限频 {{ s.restRateLimited }}</p>
      </div>
      <div class="panel">
        <h3>SQLite 记录（每分钟刷新）</h3>
        <table><tbody><tr v-for="(v, k) in s.db" :key="k"><td>{{ k }}</td><td class="num">{{ v?.toLocaleString() }}</td></tr></tbody></table>
        <p class="note">1m K 线与 OI 样本保留 {{ s.config.retain1mDays }} 天；1H、资金费、评分快照长期保留。</p>
      </div>
    </div>
    <div class="panel note">
      数据源：仅 OKX v5 公共接口（无凭据、无下单）。全池 candle1m 走 business WS，OI/资金费全池 + tickers 仅活跃池/自选走 public WS；
      SPOT 行情 15s、SWAP ticker 30s REST 兜底。所有时间戳按交易所 ts 记录，缺失分钟不当作 0。
    </div>
  </div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 12px; }
.wrap { white-space: normal; }
</style>
