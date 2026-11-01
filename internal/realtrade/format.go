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
	}
	return "已平仓"
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

// summary is one line: side, coin, leverage, margin, exits.
func summary(p Proposal) string {
	return fmt.Sprintf("%s %s %d× 保证金 %s U · 参考 %s · 止损 %s · 止盈 %s", sideText(p.Side), coin(p.InstID), p.Leverage,
		fmtNum(p.MarginUSDT), okxorder.FormatPx(p.RefPx), okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP))
}

// cardLines are the proposal as shown in the trade bot's message (plain text; the bot escapes it).
func cardLines(p Proposal, now time.Time) []string {
	sign := map[bool]string{true: "+", false: "−"}
	long := p.Side == "long"
	lines := []string{
		fmt.Sprintf("%s %s %s · %d× 逐仓", modeText(p.Mode), sideText(p.Side), coin(p.InstID), p.Leverage),
		fmt.Sprintf("保证金 %s U · %s 张（名义 ≈ %.0f U）", fmtNum(p.MarginUSDT), fmtNum(p.Contracts), p.NotionalUSDT),
		fmt.Sprintf("入场 市价（参考 %s）", okxorder.FormatPx(p.RefPx)),
		fmt.Sprintf("止损 %s（%s%.2f%%）", okxorder.FormatPx(p.SL), sign[!long], p.SLPct),
		fmt.Sprintf("止盈 %s（%s%.2f%%）· 盈亏比 %.2f", okxorder.FormatPx(p.TP), sign[long], p.TPPct, p.RR),
		"理由：" + clip(p.Rationale, 600),
		"失效：" + clip(p.Invalidation, 200),
		"来源：" + sourceText(p),
	}
	switch {
	case p.Status == StatusPending:
		left := time.UnixMilli(p.ExpiresAt).Sub(now).Round(time.Minute)
		lines = append(lines, fmt.Sprintf("⏳ 待审核 · %s 前有效（约 %d 分钟）", time.UnixMilli(p.ExpiresAt).In(tz8).Format("15:04"), int(left.Minutes())))
	default:
		st := "状态：" + statusText[p.Status]
		if p.Status == StatusExecuted {
			st = fmt.Sprintf("状态：%s 已执行 @≈%s", modeText(p.Mode), okxorder.FormatPx(p.ExecPx))
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
	head := fmt.Sprintf("%s #%d %s %s", modeText(p.Mode), p.ID, sideText(p.Side), coin(p.InstID))
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
		if p.Mode == string(okxorder.Live) {
			return fmt.Sprintf("%s 已成交：市价 %s 张 @≈%s，止损 %s / 止盈 %s 已挂 · ordId %s", head, fmtNum(p.Contracts),
				okxorder.FormatPx(p.ExecPx), okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP), p.OrdID)
		}
		return fmt.Sprintf("%s 将会下单（未发送）：市价 %s 张 @≈%s，止损 %s / 止盈 %s，逐仓 %d×", head, fmtNum(p.Contracts),
			okxorder.FormatPx(p.ExecPx), okxorder.FormatPx(p.SL), okxorder.FormatPx(p.TP), p.Leverage)
	case "closed":
		return fmt.Sprintf("%s %s %s · %+.2f U", head, closeText(p.CloseKind), okxorder.FormatPx(p.ClosePx), p.PnlUSDT)
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
