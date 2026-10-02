<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch, type Component } from 'vue'
import { useRouter } from 'vue-router'
import { BookOpen, ChevronLeft, ChevronRight } from 'lucide-vue-next'
import { tradeApi } from '../tradeApi'
import ApiRef from './help/ApiRef.vue'

/**
 * Help center: markdown articles from src/docs/<section>/NN-slug.md (compiled to components at build
 * time) plus the generated API reference. Internal links stay in the app; strategy examples get an
 * 「在编辑器中打开」 button that creates a user strategy from the snippet.
 */
const props = defineProps<{ section: string; slug?: string }>()
const router = useRouter()

interface Article { slug: string; title: string; summary: string; comp: Component }
const mods = import.meta.glob('../docs/strategy/*.md', { eager: true }) as Record<string, { default: Component; title?: string; summary?: string }>
const articles: Article[] = Object.entries(mods)
  .sort(([a], [b]) => a.localeCompare(b))
  .map(([path, m]) => ({
    slug: path.split('/').pop()!.replace(/^\d+-/, '').replace(/\.md$/, ''),
    title: m.title || path, summary: m.summary || '', comp: m.default,
  }))
articles.push({ slug: 'api', title: 'API 参考', summary: '所有类、方法、字段（从 SDK 源码生成）', comp: ApiRef })

const current = computed(() => articles.find(a => a.slug === props.slug) ?? null)
const idx = computed(() => (current.value ? articles.indexOf(current.value) : -1))
const prev = computed(() => (idx.value > 0 ? articles[idx.value - 1] : null))
const next = computed(() => (idx.value >= 0 && idx.value < articles.length - 1 ? articles[idx.value + 1] : null))
const pick = computed({ get: () => props.slug || '', set: (s: string) => router.push(`/help/strategy/${s}`) })
watch(() => props.section, s => { if (s !== 'strategy') router.replace('/help/strategy') }, { immediate: true })

const body = ref<HTMLElement>()
function onClick(e: MouseEvent) {
  const a = (e.target as Element).closest('a')
  const href = a?.getAttribute('href')
  if (!href || e.metaKey || e.ctrlKey || a?.target === '_blank') return
  if (href.startsWith('/') && !href.startsWith('//')) { e.preventDefault(); router.push(href) }
  else if (/^https?:/.test(href)) { a!.target = '_blank'; a!.rel = 'noopener' }
}
const opening = ref(false)
async function decorate() {
  await nextTick()
  for (const pre of body.value?.querySelectorAll('pre') ?? []) {
    const text = pre.textContent || ''
    if (pre.dataset.dec || !/class \w+\((\w+\.)?Strategy\)/.test(text) || !/^from ctg import/m.test(text)) continue
    pre.dataset.dec = '1'
    const b = document.createElement('button')
    b.type = 'button'
    b.className = 'open-ed'
    b.textContent = '在编辑器中打开'
    b.onclick = async () => {
      if (opening.value) return
      opening.value = true
      try { const s = await tradeApi.createSource({ code: text.trimEnd() + '\n' }); router.push(`/trading/strategies/edit/${s.id}`) } catch (e: any) { alert(e.message) } finally { opening.value = false }
    }
    pre.appendChild(b)
  }
}
onMounted(decorate)
watch(() => props.slug, () => { decorate(); window.scrollTo({ top: 0 }) })
</script>

<template>
  <div class="page help">
    <aside class="toc">
      <RouterLink to="/help/strategy" class="toc-h"><BookOpen :size="15" />策略文档</RouterLink>
      <nav>
        <RouterLink v-for="(a, i) in articles" :key="a.slug" :to="`/help/strategy/${a.slug}`" class="ti" :class="{ on: a.slug === slug }">
          <span class="n tnum">{{ a.slug === 'api' ? '' : i + 1 }}</span>{{ a.title }}
        </RouterLink>
      </nav>
    </aside>
    <div v-if="slug" class="toc-m">
      <USelect v-model="pick" :options="articles.map((a, i) => ({ value: a.slug, label: (a.slug === 'api' ? '' : `${i + 1}. `) + a.title }))" placeholder="选择文章" aria-label="文章" />
    </div>

    <main ref="body" class="content" @click="onClick">
      <template v-if="current">
        <component :is="current.comp" :key="current.slug" />
        <nav class="pn">
          <RouterLink v-if="prev" :to="`/help/strategy/${prev.slug}`" class="pv"><ChevronLeft :size="14" /><span><small>上一篇</small>{{ prev.title }}</span></RouterLink>
          <span class="sp"></span>
          <RouterLink v-if="next" :to="`/help/strategy/${next.slug}`" class="nx"><span><small>下一篇</small>{{ next.title }}</span><ChevronRight :size="14" /></RouterLink>
        </nav>
      </template>
      <div v-else class="md">
        <h1>策略文档</h1>
        <p>从这里学会写自己的交易策略：怎么声明参数、读数据、发信号、做空、做多币种组合、发通知，以及为什么策略没有出信号。
          想直接动手：<RouterLink to="/trading/strategies">策略库</RouterLink> → 新建策略。</p>
        <ol class="cards">
          <li v-for="a in articles" :key="a.slug"><RouterLink :to="`/help/strategy/${a.slug}`"><b>{{ a.title }}</b><span>{{ a.summary }}</span></RouterLink></li>
        </ol>
      </div>
    </main>
  </div>
</template>

<style scoped>
.help { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 24px; max-width: 1180px; margin: 0 auto; padding: 16px; align-items: start; }
.toc { position: sticky; top: 12px; display: grid; gap: 6px; }
.toc-h { display: flex; align-items: center; gap: 6px; font-weight: 600; color: var(--text-1); text-decoration: none !important; padding: 4px 8px; }
.toc nav { display: grid; gap: 1px; }
.ti { display: flex; gap: 8px; padding: 5px 8px; border-radius: 6px; font-size: var(--fs-sm); color: var(--text-2); text-decoration: none !important; }
.ti .n { width: 16px; flex: none; color: var(--text-3); text-align: right; }
.ti.on { background: var(--bg-3); color: var(--text-1); }
@media (hover: hover) { .ti:hover { color: var(--text-1); } }
.toc-m { display: none; }
.content { min-width: 0; }
.pn { display: flex; gap: 12px; margin: 32px 0 8px; padding-top: 16px; border-top: 1px solid var(--line); }
.pn a { display: inline-flex; align-items: center; gap: 6px; padding: 8px 12px; border: 1px solid var(--line-2); border-radius: 8px; text-decoration: none !important; color: var(--text-1); }
.pn span { display: grid; }
.pn small { color: var(--text-3); font-size: var(--fs-xs); }
.nx span { text-align: right; }
.sp { flex: 1; }
.cards { list-style: none; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr)); gap: 10px; }
.cards a { display: grid; gap: 4px; padding: 12px 14px; height: 100%; box-sizing: border-box; border: 1px solid var(--line-2); border-radius: 8px; text-decoration: none !important; }
.cards b { color: var(--text-1); }
.cards span { color: var(--text-2); font-size: var(--fs-sm); }
@media (hover: hover) { .cards a:hover { border-color: var(--accent); } }
@media (max-width: 900px) {
  .help { grid-template-columns: minmax(0, 1fr); padding: 12px; gap: 12px; }
  .toc { display: none; }
  .toc-m { display: block; }
}
</style>

<style>
/* markdown articles (unplugin-vue-markdown wraps each in .md) */
.md { color: var(--text-1); font-size: 15px; line-height: 1.75; overflow-wrap: anywhere; }
.md h1 { font-size: 26px; margin: 0 0 12px; line-height: 1.3; }
.md h2 { font-size: 20px; margin: 32px 0 10px; padding-bottom: 6px; border-bottom: 1px solid var(--line); line-height: 1.35; }
.md h3 { font-size: 16px; margin: 24px 0 8px; }
.md p, .md ul, .md ol { margin: 10px 0; }
.md ul, .md ol { padding-left: 1.4em; }
.md li { margin: 3px 0; }
.md a { color: var(--accent); }
.md blockquote { margin: 12px 0; padding: 8px 14px; border-left: 3px solid var(--accent); background: var(--bg-2); color: var(--text-2); border-radius: 0 6px 6px 0; }
.md blockquote p { margin: 4px 0; }
.md code { font: 0.88em ui-monospace, "SF Mono", Menlo, monospace; background: var(--bg-3); padding: 1px 5px; border-radius: 4px; }
.md pre { position: relative; margin: 12px 0; padding: 12px 14px; border: 1px solid var(--line); border-radius: 8px; overflow: auto; font-size: 13px; line-height: 1.6; background: var(--bg-0) !important; }
.md pre code { background: none; padding: 0; font-size: inherit; }
.md table { border-collapse: collapse; margin: 12px 0; display: block; overflow-x: auto; max-width: 100%; font-size: 14px; }
.md th, .md td { border: 1px solid var(--line-2); padding: 6px 10px; text-align: left; vertical-align: top; }
.md th { background: var(--bg-2); font-weight: 600; white-space: nowrap; }
.md hr { border: none; border-top: 1px solid var(--line); margin: 24px 0; }
.md .open-ed { position: absolute; top: 6px; right: 6px; height: 24px; padding: 0 8px; font-size: 12px; border-radius: 6px; border: 1px solid var(--line-2); background: var(--bg-2); color: var(--text-2); cursor: pointer; opacity: .85; }
@media (hover: hover) { .md .open-ed:hover { color: var(--accent); border-color: var(--accent); opacity: 1; } }
@media (max-width: 900px) { .md { font-size: 14.5px; } .md pre { font-size: 12px; } }
</style>
