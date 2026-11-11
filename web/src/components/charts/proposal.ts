import { ref, watch } from 'vue'
import { LineStyle, type IPriceLine, type ISeriesApi } from 'lightweight-charts'
import { C } from '../../theme'
import { store } from '../../store'
import { kindText, realtradeApi, type TradeProposal } from '../../realtradeApi'

/**
 * One AI trade proposal on the K-line (server: internal/realtrade), opened from its card, the
 * Telegram "查看" button (/instrument/<id>?proposal=<n>) or the app's chart (/embed/chart/<id>?proposal=<n>).
 * Entry (the reference price, or the fill once executed), stop and target as price lines; drawn only
 * on its own instrument. Owner only: without the key the server answers 401 and nothing is drawn.
 */
export function useProposalLevels(series: () => ISeriesApi<'Candlestick'> | undefined, id: () => string, proposalId: () => number | undefined) {
  const proposal = ref<TradeProposal | null>(null)
  let lines: IPriceLine[] = []

  async function refresh() {
    const pid = proposalId()
    if (!pid) { proposal.value = null; draw(); return }
    try {
      const p = await realtradeApi.get(pid)
      if (pid !== proposalId()) return
      proposal.value = p && p.instId === id() ? p : null
    } catch { /* keep what is drawn */ }
    draw()
  }

  function draw() {
    const s = series()
    if (!s) return
    for (const l of lines) s.removePriceLine(l)
    lines = []
    const p = proposal.value
    if (!p) return
    const tag = `提案 ${p.side === 'short' ? '空' : '多'}`
    const line = (price: number, color: string, title: string, style = LineStyle.Dashed, width: 1 | 2 = 1) =>
      lines.push(s.createPriceLine({ price, color, lineWidth: width, lineStyle: style, axisLabelVisible: true, title }))
    const kind = p.kind && p.kind !== 'open' ? kindText[p.kind] : '入场'
    if (p.kind !== 'adjust') line(entry(p), C.lime, `${tag} ${kind}`, p.fillPx || p.execPx ? LineStyle.Solid : LineStyle.Dashed, 2)
    if (p.sl > 0) line(p.sl, C.down, '提案 止损', LineStyle.Solid, 2)
    if (p.tp > 0) line(p.tp, C.up, '提案 止盈')
  }

  /** Levels the price scale should span (spanLevels): entry, stop and target (those it has). */
  const levels = () => (proposal.value ? [entry(proposal.value), proposal.value.sl, proposal.value.tp].filter(v => v > 0) : [])

  watch([proposalId, id], refresh)
  watch(() => store.realtradeSeq, refresh)
  return { proposal, refresh, draw, levels }
}

/** Where the order filled (or would): the fill once filled, else the limit price, else the price at execution or creation. */
export const entry = (p: TradeProposal) => p.fillPx || (p.orderType === 'limit' ? p.px : 0) || p.execPx || p.refPx
