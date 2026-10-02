<script setup lang="ts">
import { computed } from 'vue'
import { SDK } from '../../components/editor/completions'

/** API reference generated from the Python SDK (web/src/docs/sdk.json, see strategies/ctg/sdkdoc.py). */
const groups: { key: string; title: string; note: string }[] = [
  { key: 'Strategy', title: 'Strategy：策略类', note: '类属性与可重写的回调（handler）。' },
  { key: 'params', title: '参数类型', note: '写成类属性，实例表单按它们生成。' },
  { key: 'Signal', title: 'Signal：信号', note: '回调返回的东西（或 ctx.submit 发出）。' },
  { key: 'ctx', title: 'ctx：数据与工具', note: '每个回调的第一个参数。' },
  { key: 'data', title: '数据对象', note: 'Bar / Bars / Series 的字段。' },
  { key: 'ta', title: 'ta：指标', note: 'numpy 数组进，等长数组出；预热期为 NaN。' },
  { key: 'portfolio', title: '组合工具', note: '多币种策略的观察名单和名额分配。' },
]
const by = computed(() => groups.map(g => ({ ...g, items: SDK.filter(e => e.group === g.key) })))
const kind: Record<string, string> = { attr: '属性', handler: '回调', method: '方法', function: '函数', class: '类' }
</script>

<template>
  <div class="md api">
    <h1>API 参考</h1>
    <p>本页由 SDK 源码自动生成（签名和英文说明来自代码），和编辑器的自动补全是同一份数据。</p>
    <section v-for="g in by" :key="g.key">
      <h2 :id="g.key">{{ g.title }}</h2>
      <p class="note">{{ g.note }}</p>
      <dl>
        <template v-for="e in g.items" :key="e.group + e.name">
          <dt><code>{{ e.group === 'ctx' && e.kind !== 'class' ? 'ctx.' : e.group === 'Signal' ? 'Signal.' : e.group === 'ta' ? 'ta.' : '' }}{{ e.name }}<span class="sig">{{ e.kind === 'attr' ? '' : e.sig }}</span></code><span class="k">{{ kind[e.kind] || e.kind }}</span></dt>
          <dd>{{ e.doc || '—' }}</dd>
        </template>
      </dl>
    </section>
  </div>
</template>

<style scoped>
dl { margin: 0; display: grid; gap: 2px; }
dt { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; margin-top: 10px; }
dt code { font-weight: 600; overflow-wrap: anywhere; }
.sig { font-weight: 400; color: var(--text-3); }
.k { font-size: var(--fs-xs); color: var(--text-3); border: 1px solid var(--line-2); border-radius: 4px; padding: 0 4px; }
dd { margin: 2px 0 0 0; white-space: pre-wrap; color: var(--text-2); font-size: var(--fs-sm); }
.note { color: var(--text-3); }
</style>
