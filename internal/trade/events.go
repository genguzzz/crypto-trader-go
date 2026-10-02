package trade

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Events for the interaction layer: one Chinese title line plus a few compact lines. Backtest
// accounts only report run completion (per-fill messages would flood the channel).

func fmtPx(x float64) string {
	if x == 0 || !finite(x) {
		return "0"
	}
	d := 8 - int(math.Floor(math.Log10(math.Abs(x)))) - 1
	d = max(0, min(d, 10))
	return trimNum(strconv.FormatFloat(x, 'f', d, 64))
}

func trimNum(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func fmtQty(x float64) string { return trimNum(strconv.FormatFloat(x, 'f', 6, 64)) }

func fmtUSDT(x float64) string { return trimNum(strconv.FormatFloat(x, 'f', 2, 64)) }

func fmtSigned(x float64) string {
	if x >= 0 {
		return "+" + fmtUSDT(x)
	}
	return fmtUSDT(x)
}

func sideName(posSide string) string {
	if posSide == "short" {
		return "空"
	}
	return "多"
}

func (b *book) event(kind, title, ref string, ts int64, lines ...string) Event {
	name, _ := b.name.Load().(string)
	return Event{Kind: kind, AccountID: b.acc.ID, Account: name, Mode: b.acc.Mode, Title: title, Lines: lines, Ts: ts, Ref: ref}
}

func (b *book) notify(ev Event) {
	if b.acc.Mode != ModeLive {
		return
	}
	b.s.emit(ev)
}

func (b *book) notifyFill(o *Order, f Fill, kind string) {
	if b.acc.Mode != ModeLive {
		return
	}
	var title string
	switch kind {
	case "stop":
		title = "止损触发 · 平" + sideName(o.PosSide)
	case "target":
		title = "止盈触发 · 平" + sideName(o.PosSide)
	case "liquidation":
		title = "强平 · " + sideName(o.PosSide) + "单"
	case "time":
		title = "持仓到期 · 平" + sideName(o.PosSide)
	default:
		if o.ReduceOnly {
			title = "平" + sideName(o.PosSide) + "成交"
		} else {
			title = "开" + sideName(o.PosSide) + "成交"
		}
	}
	lines := []string{fmt.Sprintf("%s %s 张 @ %s · 实例 #%d", f.InstID, fmtQty(f.Qty), fmtPx(f.Px), f.InstanceID)}
	if f.Liquidation {
		lines = append(lines, fmt.Sprintf("保证金损失 %s USDT", fmtUSDT(-f.RealizedPnl)))
	} else {
		lines = append(lines, fmt.Sprintf("手续费 %s USDT · 滑点 %.1fbp", fmtUSDT(f.Fee), f.SlippageBp))
	}
	if o.ReduceOnly && !f.Liquidation {
		lines = append(lines, fmt.Sprintf("已实现 %s USDT", fmtSigned(f.RealizedPnl-f.Fee)))
	}
	if p := b.pos[posKey{o.InstanceID, o.InstID}]; p != nil && !o.ReduceOnly {
		var x []string
		if p.StopLoss > 0 {
			x = append(x, "止损 "+fmtPx(p.StopLoss))
		}
		if p.TakeProfit > 0 {
			x = append(x, "止盈 "+fmtPx(p.TakeProfit))
		}
		x = append(x, "强平 "+fmtPx(p.LiqPx))
		lines = append(lines, strings.Join(x, " · "))
	}
	b.s.emit(b.event(kind, title, fmt.Sprintf("ord-%d", o.ID), f.Ts, lines...))
}

func actionName(a Action) string {
	switch a {
	case OpenLong:
		return "开多"
	case OpenShort:
		return "开空"
	case Close:
		return "平仓"
	case Reduce:
		return "减仓"
	}
	return string(a)
}

var outcomeNames = map[SignalOutcome]string{OutOrdered: "已下单", OutRejected: "已拒绝", OutSkippedAI: "AI 跳过", OutSkippedUs: "人工跳过",
	OutExpired: "已过期", OutRecorded: "仅记录", OutPending: "待处理"}

func (b *book) notifySignal(sg Signal) {
	kind := "signal"
	if sg.Outcome == OutRejected {
		kind = "reject"
	}
	lines := []string{fmt.Sprintf("%s %s · 实例 #%d", sg.InstID, actionName(sg.Action), sg.InstanceID)}
	if sg.Reason != "" {
		lines = append(lines, "理由："+sg.Reason)
	}
	if sg.OutcomeReason != "" {
		lines = append(lines, outcomeNames[sg.Outcome]+"："+sg.OutcomeReason)
	}
	b.notify(b.event(kind, "信号"+outcomeNames[sg.Outcome]+" · "+actionName(sg.Action)+" "+shortInst(sg.InstID),
		fmt.Sprintf("sig-%d", sg.ID), sg.RecvTs, lines...))
}

func shortInst(id string) string { return strings.TrimSuffix(id, "-USDT-SWAP") }

func signalEvent(sg Signal, acc Account, title string) Event {
	lines := []string{fmt.Sprintf("%s %s · 强度 %.2f · 实例 #%d", sg.InstID, actionName(sg.Action), sg.Strength, sg.InstanceID)}
	if sg.Price > 0 {
		x := "参考价 " + fmtPx(sg.Price)
		if sg.StopLoss > 0 {
			x += " · 止损 " + fmtPx(sg.StopLoss)
		}
		if sg.TakeProfit > 0 {
			x += " · 止盈 " + fmtPx(sg.TakeProfit)
		}
		lines = append(lines, x)
	}
	if sg.Reason != "" {
		lines = append(lines, "理由："+sg.Reason)
	}
	return Event{Kind: "signal", AccountID: acc.ID, Account: acc.Name, Mode: acc.Mode, Title: title, Lines: lines, Ts: sg.RecvTs, Ref: fmt.Sprintf("sig-%d", sg.ID)}
}

func (b *book) notifyBreaker(ts int64) {
	b.notify(b.event("breaker", "日内熔断 · 暂停开仓", "", ts,
		fmt.Sprintf("当日亏损 %s USDT（上限 %.1f%%）", fmtUSDT(-b.dayPnl()), b.acc.Limits.DailyLossPct*100),
		"今日（UTC+8）剩余时间拒绝开仓信号，平仓不受影响"))
}

func (b *book) dailySummary(key int64, ts int64) {
	if b.acc.Mode != ModeLive || b.day.start <= 0 {
		return
	}
	pnl := b.dayPnl()
	day := time.UnixMilli(key*dayMs - 8*hourMs).In(cst).Format("01-02")
	var fills int
	var fees, funding float64
	from, to := key*dayMs-8*hourMs, (key+1)*dayMs-8*hourMs
	b.s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(fee),0) FROM trade_fills WHERE account_id=? AND ts>=? AND ts<?`, b.acc.ID, from, to).Scan(&fills, &fees)
	b.s.db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM trade_ledger WHERE account_id=? AND kind='funding' AND ts>=? AND ts<?`, b.acc.ID, from, to).Scan(&funding)
	name, _ := b.name.Load().(string)
	b.notify(b.event("daily", "日报 "+day+" · "+name, "", ts,
		fmt.Sprintf("权益 %s USDT · 当日 %s（%+.2f%%）", fmtUSDT(b.equity()), fmtSigned(pnl), pnl/b.day.start*100),
		fmt.Sprintf("成交 %d 笔 · 手续费 %s · 资金费 %s", fills, fmtUSDT(fees), fmtSigned(funding)),
		fmt.Sprintf("持仓 %d 个", len(b.pos))))
}
