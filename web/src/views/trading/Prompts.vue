<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { Inbox } from 'lucide-vue-next'
import { interactApi, type Prompt } from '../../tradeApi'
import { refreshPrompts, store } from '../../store'
import { useTradeLive } from '../../composables/tradeLive'
import { time } from '../../fmt'
import PromptCard from '../../components/trade/PromptCard.vue'
import Loading from '../../components/ui/Loading.vue'

const prompts = ref<Prompt[] | null>(null)
const error = ref('')
const busy = ref<string | null>(null)
const done = ref<{ id: string; title: string; label: string; at: number }[]>([]) // answered in this tab

async function load() {
  try {
    const xs = await interactApi.prompts('open')
    const dl = (p: Prompt) => (typeof p.deadline === 'number' ? p.deadline : Date.parse(p.deadline))
    prompts.value = xs.slice().sort((a, b) => dl(a) - dl(b)) // most urgent first
    store.openPrompts = xs.length
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
}
useTradeLive(load, { interact: true, pollMs: 5000 })

const now = ref(Date.now())
let clock: number | undefined
onMounted(() => { clock = window.setInterval(() => (now.value = Date.now()), 1000) })
onUnmounted(() => clearInterval(clock))

async function answer(p: Prompt, key: string) {
  busy.value = p.id
  try {
    await interactApi.answer(p.id, key)
    done.value.unshift({ id: p.id, title: p.event.title, label: p.options.find(o => o.key === key)?.label || key, at: Date.now() })
    prompts.value = (prompts.value || []).filter(x => x.id !== p.id)
    refreshPrompts()
  } catch (e: any) {
    error.value = e.message
    load()
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <div class="page pr">
    <div class="ph">
      <h1>待确认</h1>
      <InfoTip>
        账户开启了<b>人工确认</b>时，符合条件的信号在下单前会停在这里，并同时推送到 Telegram（如已配置）。在任一处回答即可，另一处会同步关闭。<br />
        <b>执行</b>：按正常仓位下单（仍要经过风控）。<b>半仓</b>：仓位减半。<b>跳过</b>：不下单，信号记为“用户跳过”。<br />
        超过时限没有回答时按账户设置的“超时默认”处理。
      </InfoTip>
    </div>
    <div v-if="error" class="banner">{{ error }}</div>
    <Loading v-if="!prompts && !error" text="加载待确认" />
    <div v-else-if="prompts && !prompts.length" class="panel empty">
      <Inbox :size="22" class="muted" />
      <span class="muted">没有需要确认的信号</span>
    </div>
    <div class="list">
      <PromptCard v-for="p in prompts || []" :key="p.id" :p="p" :now="now" :busy="busy === p.id" @answer="k => answer(p, k)" />
    </div>
    <div v-if="done.length" class="panel">
      <h3>刚刚处理</h3>
      <ul class="dn">
        <li v-for="x in done" :key="x.id"><span class="muted tnum">{{ time(x.at, true) }}</span><span class="t">{{ x.title }}</span><span class="tag">{{ x.label }}</span></li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.pr { max-width: 820px !important; }
.ph { display: flex; align-items: center; gap: 6px; }
.ph h1 { margin: 0; font-size: var(--fs-2xl); font-weight: 600; }
.banner { border-radius: var(--radius-sm); }
.empty { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 36px; }
.list { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.dn { list-style: none; margin: 0; padding: 0; }
.dn li { display: flex; align-items: center; gap: 10px; padding: 6px 0; border-top: 1px solid var(--line); font-size: var(--fs-sm); }
.dn li:first-child { border-top: none; }
.dn .t { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
