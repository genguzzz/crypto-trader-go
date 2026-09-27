import { markRaw, type Component } from 'vue'
import { Gauge, BookOpenText, ChartColumn, Layers, Percent, ArrowLeftRight, Coins } from 'lucide-vue-next'
import ScoreDetail from './ScoreDetail.vue'
import OrderBookDetail from './OrderBookDetail.vue'
import TurnoverDetail from './TurnoverDetail.vue'
import OIDetail from './OIDetail.vue'
import FundingDetail from './FundingDetail.vue'
import SpotDetail from './SpotDetail.vue'
import SupplyDetail from './SupplyDetail.vue'

/**
 * Single registry for clickable metric cards → detail views. Adding a new card = add one entry
 * (key, label, icon, component taking `ctx: DetailCtx`) and render a StatCard with that key.
 */
export interface DetailDef { key: string; label: string; icon: Component; component: Component }

export const statDetails: DetailDef[] = [
  { key: 'score', label: '评分', icon: markRaw(Gauge), component: markRaw(ScoreDetail) },
  { key: 'supply', label: '市值/解锁', icon: markRaw(Coins), component: markRaw(SupplyDetail) },
  { key: 'book', label: '盘口', icon: markRaw(BookOpenText), component: markRaw(OrderBookDetail) },
  { key: 'turnover', label: '成交额', icon: markRaw(ChartColumn), component: markRaw(TurnoverDetail) },
  { key: 'oi', label: '持仓量', icon: markRaw(Layers), component: markRaw(OIDetail) },
  { key: 'funding', label: '资金费', icon: markRaw(Percent), component: markRaw(FundingDetail) },
  { key: 'spot', label: '现货/基差', icon: markRaw(ArrowLeftRight), component: markRaw(SpotDetail) },
]
export const detailIcon = (key: string) => statDetails.find(d => d.key === key)?.icon
