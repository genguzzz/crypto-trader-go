import type { Component } from 'vue'
import { TrendingUp, Gauge, Percent } from 'lucide-vue-next'
import type { AlertRule, AlertType } from '../../api'

/** Per-type presentation and default params (the backend validates and normalises). */
export const alertTypes: { key: AlertType; label: string; icon: Component; defaults: () => Record<string, any> }[] = [
  { key: 'price_move', label: '价格异动', icon: TrendingUp, defaults: () => ({ window: '1h', pct: 3, dir: 'both' }) },
  { key: 'score', label: '规则分数', icon: Gauge, defaults: () => ({ ruleId: '', side: 'long', min: 80 }) },
  { key: 'funding', label: '资金费', icon: Percent, defaults: () => ({ source: 'settled', pctPerHour: 0.01, dir: 'both' }) },
]
export const typeMeta = (k: AlertType) => alertTypes.find(t => t.key === k) || alertTypes[0]

export function scopeLabel(r: Pick<AlertRule, 'scope' | 'instId'>) {
  return r.scope === 'active' ? '活跃池' : r.scope === 'watch' ? '自选' : r.instId?.replace('-USDT-SWAP', '') || '—'
}

export function cooldownLabel(m: number) {
  if (!m) return '无冷却'
  if (m < 60) return `${m} 分钟`
  return m % 60 === 0 ? `${m / 60} 小时` : `${(m / 60).toFixed(1)} 小时`
}

/** Delivery status for one channel → tag class + short text. */
export function deliveryTag(v: string) {
  if (v === 'ok') return { cls: 'good', t: '已送达' }
  if (v === 'pending') return { cls: '', t: '发送中' }
  if (v.endsWith('未配置')) return { cls: 'warn', t: '未配置' }
  return { cls: 'bad', t: '失败' }
}
export const channelName: Record<string, string> = { telegram: 'Telegram', webhook: 'Webhook' }
