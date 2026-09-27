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
        <h3>OKX 连接 <InfoTip>
          仅使用 OKX v5 <b>公共</b>接口（无凭据、无下单）。<br />
          <b>business</b> WS：全池 1m K 线；<b>public</b> WS：全池 OI 与资金费，盘口报价只订阅活跃池和自选（全池订阅约 40GB/天，没必要）。<br />
          REST：现货行情 15 秒、永续 ticker 30 秒兜底，历史回填与补洞走独立限频。时间戳按交易所时间记录，缺失分钟不当 0。
        </InfoTip></h3>
        <table>
          <thead><tr><th>WS</th><th>状态</th><th class="num">订阅</th><th class="num">消息</th><th class="num">重连</th><th class="num">最近</th></tr></thead>
          <tbody><tr v-for="w in s.ws" :key="w.name">
            <td>{{ w.name }}</td><td><span class="dot" :class="w.connected ? 'ok' : 'bad'"></span></td>
            <td class="num">{{ w.subscriptions }}</td><td class="num">{{ w.messages }}</td><td class="num">{{ w.reconnects }}</td><td class="num">{{ (w.lastMsgAgoMs / 1000).toFixed(1) }}s</td>
          </tr></tbody>
        </table>
        <p class="note">REST 请求 {{ s.restRequests }} · 错误 {{ s.restErrors }} · 限频 {{ s.restRateLimited }}</p>
        <p class="note" v-if="s.mem">内存 堆 {{ s.mem.heapAllocMB }} MB · 进程 {{ s.mem.sysMB }} MB · 协程 {{ s.mem.goroutines }} · 内存中合约 {{ s.mem.insts }}</p>
      </div>
      <div class="panel">
        <h3>SQLite 记录 <InfoTip>
          行数每分钟刷新一次。各类数据按合约分层保留，总大小受存储上限（{{ s.config.storageCapMB }} MB）约束，规则与占用见“设置 → 数据存储”；评分快照、规则长期保留。<br />
          全池 1m 约 60 MB/天。
        </InfoTip></h3>
        <table><tbody><tr v-for="(v, k) in s.db" :key="k"><td>{{ k }}</td><td class="num">{{ v?.toLocaleString() }}</td></tr></tbody></table>
      </div>
    </div>
  </div>
</template>

<style scoped>
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 12px; }
.wrap { white-space: normal; }
</style>
