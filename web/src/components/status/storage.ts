/** Shared formatting for the storage report (retention.go) and the status page. */
export const bytes = (b?: number) =>
  b == null ? '—' : b >= 1 << 30 ? (b / (1 << 30)).toFixed(2) + ' GB' : (b / (1 << 20)).toFixed(0) + ' MB'

export const tableLabel: Record<string, string> = {
  candles_1m: '1m K 线', oi_samples: '持仓量样本', candles_1h: '1H K 线', funding_settled: '资金费（已结算）', funding_obs: '资金费（盘中观测）',
  orderflow_1m: '订单流（逐笔 1m / 足迹）', taker_volume: '主动买卖量（OKX 统计）', other: '规则 / 评分快照 / 其他',
  funding_samples: '资金费样本', score_snapshots: '评分快照',
}

/** Storage tiers, high → low priority; report ages are indexed none, base, viewed, pool, watch. */
export const tierCols: [number, string][] = [[4, '自选'], [3, '活跃池'], [2, '最近查看'], [1, '全池录制'], [0, '未跟踪']]

/** Compact count: 1,581,359 → 158.1万. */
export function count(n?: number | null) {
  if (n == null) return '—'
  if (n >= 1e8) return (n / 1e8).toFixed(2) + '亿'
  if (n >= 1e4) return (n / 1e4).toFixed(1) + '万'
  return n.toLocaleString()
}

export type Tone = 'ok' | 'warn' | 'bad'

/** GET /api/v1/status (internal/market/queries.go Status). */
export interface WSStatus { name: string; connected: boolean; subscriptions: number; messages: number; reconnects: number; lastMsgAgoMs: number; subErrors: number }
export interface SystemStatus {
  phase: string; uptime: string; backfillDone: number; backfillTotal: number
  restRequests: number; restErrors: number; restRateLimited: number; ws: WSStatus[] | null
  coverage: { eligible: number; ready: number; warming: number; stale: number; new: number; active: number }
  asOfMinute: number; computedAt: number; replay: string; history: string; hourHistory?: string
  db: Record<string, number> | null
  config: { threshold: number; topN: number; storageCapMB: number; replayHours: number }
  mem?: { heapAllocMB: number; heapSysMB: number; sysMB: number; goroutines: number; numGC: number; insts: number; flowDecodeErrors: number }
}
