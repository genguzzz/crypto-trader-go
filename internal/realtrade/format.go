package realtrade

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/okxorder"
)

// Chinese texts for the owner (Telegram, audit trail).

var statusText = map[string]string{StatusPending: "待审核", StatusApproved: "已同意", StatusExecuting: "执行中", StatusExecuted: "已执行",
	StatusFailed: "失败", StatusRejected: "已拒绝", StatusExpired: "已过期", StatusInvalidated: "已失效"}

func sideText(side string) string {
	if side == "short" {
		return "做空"
	}
	return "做多"
}

// posText names a position: "BTC 多单".
func posText(inst, side string) string {
	if side == "short" {
		return coin(inst) + " 空单"
	}
	return coin(inst) + " 多单"
}

func modeText(mode string) string {
	if mode == string(okxorder.Live) {
		return "[实盘]"
	}
	return "[DRY RUN]"
}

func closeText(kind string) string {
	switch kind {
	case "tp":
		return "止盈平仓"
	case "sl":
		return "止损平仓"
	case "manual":
		return "主动平仓"
	case "unfilled":
		return "限价单未成交"
	}
	return "已平仓"
}

var kindTexts = map[string]string{KindOpen: "开仓", KindAdd: "补仓", KindReduce: "减仓", KindClose: "平仓", KindAdjust: "调整止盈止损", KindCancel: "撤销挂单", KindAmend: "修改挂单"}

func kindText(kind string) string {
	if t := kindTexts[kind]; t != "" {
		return t
	}
	return "开仓"
}

func mgnText(mode string) string {
	if mode == okxorder.Cross {
		return "全仓"
	}
	return "逐仓"
}

// ordText is the order type: 市价, or 限价 @price.
func ordText(p Proposal) string {
	if p.OrdType == okxorder.Limit {
		return "限价 @" + okxorder.FormatPx(p.Px)
	}
	return "市价"
}

// orderState is a limit order's state on OKX (empty for a market order or before execution).
func orderState(p Proposal) string {
	switch p.OrdState {
	case OrdLive:
		return fmt.Sprintf("挂单中 · %s 前未成交自动撤单", time.UnixMilli(p.OrderUntil).In(tz8).Format("01-02 15:04"))
	case OrdPartial:
		return fmt.Sprintf("挂单中 · 已成交 %s/%s 张", fmtNum(p.Filled), fmtNum(p.Contracts))
	case OrdCanceled:
		return fmt.Sprintf("已撤单 · 成交 %s/%s 张", fmtNum(p.Filled), fmtNum(p.Contracts))
	case OrdFilled:
		if p.OrdType == okxorder.Limit {
			return fmt.Sprintf("已成交 %s 张 @%s", fmtNum(p.Filled), okxorder.FormatPx(p.FillPx))
		}
	}
	return ""
}

func coin(inst string) string { return strings.Split(inst, "-")[0] }

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func sourceText(p Proposal) string {
	switch p.Source {
	case SourceSignal, SourceSchedule:
		return "信号 · " + p.Trigger
	}
	if p.ChatID > 0 {
		return "AI 对话 #" + strconv.FormatInt(p.ChatID, 10)
	}
	return "AI 对话"
}

// summary is one line: kind, side, coin, the order and its exits.
func summary(p Proposal) string {
	head := fmt.Sprintf("%s %s %s · %s", kindText(p.Kind), sideText(p.Side), coin(p.InstID), ordText(p))
	switch p.Kind {
	case KindCancel, KindAmend:
		return fmt.Sprintf("%s %s %s · %s", kindText(p.Kind), sideText(p.Side), coin(p.InstID), p.TPSL)
	case KindReduce:
		return fmt.Sprintf("%s · %.0f%%（%s 张）· 参考 %s", head, p.Fraction*100, fmtNum(p.Contracts), okxorder.FormatPx(p.RefPx))
	case KindClose:
		return fmt.Sprintf("%s · 全部 %s 张 · 参考 %s", head, fmtNum(p.Contracts), okxorder.FormatPx(p.RefPx))
	case KindAdjust:
		return fmt.Sprintf("%s %s %s · 止损 %s · 止盈 %s", kindText(p.Kind), sideText(p.Side), coin(p.InstID), okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP))
	}
	return fmt.Sprintf("%s · %d× %s 保证金 %s U · 参考 %s · 止损 %s · 止盈 %s", head, p.Leverage, mgnText(p.MarginMode),
		fmtNum(p.MarginUSDT), okxorder.FormatPx(p.RefPx), okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP))
}

// confirmText is the order the owner is asked to confirm, in one sentence.
func confirmText(p Proposal) string {
	switch p.Kind {
	case KindCancel, KindAmend:
		return fmt.Sprintf("确认%s %s %s？%s", kindText(p.Kind), coin(p.InstID), sideText(p.Side), p.TPSL)
	case KindReduce:
		return fmt.Sprintf("确认以%s减仓 %s %s %.0f%%（%s 张）？", ordText(p), coin(p.InstID), sideText(p.Side), p.Fraction*100, fmtNum(p.Contracts))
	case KindClose:
		return fmt.Sprintf("确认以%s平掉 %s %s 全部 %s 张？", ordText(p), coin(p.InstID), sideText(p.Side), fmtNum(p.Contracts))
	case KindAdjust:
		return fmt.Sprintf("确认把 %s %s 的止损改为 %s、止盈改为 %s？", coin(p.InstID), sideText(p.Side), okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP))
	}
	return fmt.Sprintf("确认以%s%s%s %s，保证金 %s U，%d× %s？", ordText(p), kindText(p.Kind), sideText(p.Side), coin(p.InstID), fmtNum(p.MarginUSDT),
		p.Leverage, mgnText(p.MarginMode))
}

// cardLines are the proposal as shown in the trade bot's message (plain text; the bot escapes it).
func cardLines(p Proposal, now time.Time) []string {
	sign := map[bool]string{true: "+", false: "−"}
	long := p.Side == "long"
	lines := []string{fmt.Sprintf("%s %s · %s %s · %d× %s", modeText(p.Mode), kindText(p.Kind), sideText(p.Side), coin(p.InstID), p.Leverage, mgnText(p.MarginMode))}
	entry := fmt.Sprintf("入场 市价（参考 %s）", okxorder.FormatPx(p.RefPx))
	if p.OrdType == okxorder.Limit {
		entry = fmt.Sprintf("入场 限价 %s（现价 %s，挂单 %d 分钟）", okxorder.FormatPx(p.Px), okxorder.FormatPx(p.RefPx), p.OrderMin)
	}
	switch p.Kind {
	case KindCancel, KindAmend:
		return []string{fmt.Sprintf("%s %s · %s %s", modeText(p.Mode), kindText(p.Kind), sideText(p.Side), coin(p.InstID)), p.TPSL,
			fmt.Sprintf("现价 %s", okxorder.FormatPx(p.RefPx))}
	case KindOpen, KindAdd:
		lines = append(lines, fmt.Sprintf("保证金 %s U · %s 张（名义 ≈ %.0f U）", fmtNum(p.MarginUSDT), fmtNum(p.Contracts), p.NotionalUSDT), entry)
		if p.Kind == KindAdd {
			lines = append(lines, fmt.Sprintf("现有仓位 %s 张 @%s → 补后 %s 张 · 均价 ≈ %s", fmtNum(p.PosContracts), okxorder.FormatPx(p.PosAvgPx),
				fmtNum(p.PosContracts+p.Contracts), okxorder.FormatPx(p.NewAvgPx)))
		}
	case KindReduce:
		lines = append(lines, fmt.Sprintf("减仓 %.0f%% = %s 张（持仓 %s 张）· %s（参考 %s）", p.Fraction*100, fmtNum(p.Contracts), fmtNum(p.PosContracts),
			ordText(p), okxorder.FormatPx(p.RefPx)))
	case KindClose:
		lines = append(lines, fmt.Sprintf("平仓 全部 %s 张 · %s（参考 %s）", fmtNum(p.Contracts), ordText(p), okxorder.FormatPx(p.RefPx)))
	case KindAdjust:
		lines = append(lines, fmt.Sprintf("仓位 %s 张 · 现价 %s", fmtNum(p.PosContracts), okxorder.FormatPx(p.RefPx)))
	}
	if p.SL > 0 && p.TP > 0 {
		lines = append(lines, fmt.Sprintf("止损 %s（%s%.2f%%）", okxorder.FormatPx(p.SL), sign[!long], p.SLPct))
		tp := fmt.Sprintf("止盈 %s（%s%.2f%%）", okxorder.FormatPx(p.TP), sign[long], p.TPPct)
		if p.Kind != KindAdjust {
			tp += fmt.Sprintf(" · 盈亏比 %.2f", p.RR)
		}
		lines = append(lines, tp)
	}
	if p.LiqPx > 0 {
		lines = append(lines, "预估强平 ≈ "+okxorder.FormatPx(p.LiqPx))
	}
	if p.TPSL != "" {
		lines = append(lines, "止盈止损："+p.TPSL)
	}
	if p.Note != "" {
		lines = append(lines, "注意："+p.Note)
	}
	lines = append(lines, "理由："+clip(p.Rationale, 600))
	if p.Invalidation != "" {
		lines = append(lines, "失效："+clip(p.Invalidation, 200))
	}
	lines = append(lines, "来源："+sourceText(p))
	switch {
	case p.Status == StatusPending:
		left := time.UnixMilli(p.ExpiresAt).Sub(now).Round(time.Minute)
		lines = append(lines, fmt.Sprintf("⏳ 待审核 · %s 前有效（约 %d 分钟）", time.UnixMilli(p.ExpiresAt).In(tz8).Format("15:04"), int(left.Minutes())))
	default:
		st := "状态：" + statusText[p.Status]
		if p.Status == StatusExecuted {
			st = fmt.Sprintf("状态：%s 已执行 @≈%s", modeText(p.Mode), okxorder.FormatPx(p.ExecPx))
			if os := orderState(p); os != "" {
				st = fmt.Sprintf("状态：%s %s", modeText(p.Mode), os)
			}
			if p.OrdID != "" {
				st += " · ordId " + p.OrdID
			}
		}
		if p.By != "" && p.By != "system" {
			st += " · " + byText(p)
		}
		lines = append(lines, st)
		if p.Error != "" {
			lines = append(lines, "原因："+clip(p.Error, 300))
		}
		if p.ClosedAt > 0 {
			lines = append(lines, fmt.Sprintf("%s %s · %+.2f U", closeText(p.CloseKind), okxorder.FormatPx(p.ClosePx), p.PnlUSDT))
		}
	}
	return lines
}

func byText(p Proposal) string {
	switch p.Via {
	case "timeout":
		return "超时"
	case "telegram":
		return "经 Telegram"
	case "app":
		return "经 App"
	case "web":
		return "经网页"
	}
	return p.Via
}

// eventLine is the short push for a state change (the card itself is edited in place).
func eventLine(kind string, p Proposal) string {
	head := fmt.Sprintf("%s #%d %s %s", modeText(p.Mode), p.ID, kindText(p.Kind), posText(p.InstID, p.Side))
	switch kind {
	case StatusApproved:
		return head + " 已同意（" + byText(p) + "），正在复核价格并执行…"
	case StatusRejected:
		return head + " 已拒绝"
	case StatusExpired:
		return head + " 超时未审核，已过期（不会执行）"
	case StatusInvalidated:
		return head + " 复核未通过，未下单：" + clip(p.Error, 300)
	case StatusFailed:
		return head + " 下单失败：" + clip(p.Error, 300)
	case StatusExecuted:
		did := fmt.Sprintf("%s %s 张 @≈%s", ordText(p), fmtNum(p.Contracts), okxorder.FormatPx(p.ExecPx))
		switch {
		case p.Kind == KindAdjust, p.Kind == KindCancel, p.Kind == KindAmend:
			did = p.TPSL
		case p.OrdType == okxorder.Limit:
			did = fmt.Sprintf("%s %s 张已挂出（%s）", ordText(p), fmtNum(p.Contracts), orderState(p))
		case p.SL > 0 && p.TP > 0:
			did += fmt.Sprintf("，止损 %s / 止盈 %s", okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP))
		}
		if p.Mode == string(okxorder.Live) {
			if p.OrdID != "" {
				did += " · ordId " + p.OrdID
			}
			return head + " 已执行：" + did
		}
		return head + " 将会执行（未发送）：" + did
	case OrdFilled:
		return fmt.Sprintf("%s 限价单已成交 %s 张 @%s", head, fmtNum(p.Filled), okxorder.FormatPx(p.FillPx))
	case OrdCanceled:
		return fmt.Sprintf("%s 限价单已撤单（成交 %s/%s 张）", head, fmtNum(p.Filled), fmtNum(p.Contracts))
	case "partial":
		return fmt.Sprintf("%s 限价单部分成交 %s/%s 张", head, fmtNum(p.Filled), fmtNum(p.Contracts))
	case "closed":
		return fmt.Sprintf("%s 仓位%s %s · %+.2f U", head, closeText(p.CloseKind), okxorder.FormatPx(p.ClosePx), p.PnlUSDT)
	}
	return head + " " + kind
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
