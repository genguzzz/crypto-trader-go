package trade

import (
	"fmt"
	"strconv"
	"strings"
)

// Commands answers chat commands (internal/interact.Commands) in plain Chinese text.
type Commands struct{ S *Service }

func (c Commands) Status() string {
	var b strings.Builder
	n := 0
	for _, a := range c.S.Accounts() {
		if a.Archived || a.Mode != ModeLive {
			continue
		}
		n++
		halt := ""
		if a.Halted {
			halt = " · 已熔断"
		}
		fmt.Fprintf(&b, "%s：权益 %.2f USDT，今日 %+.2f，持仓 %d，策略 %d%s\n", a.Name, a.Equity, a.DayPnl, a.OpenCount, a.Instances, halt)
	}
	if n == 0 {
		return "没有实时模拟账户"
	}
	return strings.TrimSpace(b.String())
}

func (c Commands) Positions() string {
	var b strings.Builder
	for _, a := range c.S.Accounts() {
		if a.Archived || a.Mode != ModeLive {
			continue
		}
		d, err := c.S.AccountDetail(a.ID)
		if err != nil {
			continue
		}
		for _, p := range d.Positions {
			fmt.Fprintf(&b, "%s %s %s %g 张 @ %g，浮盈 %+.2f，止损 %g\n", a.Name, p.InstID, p.PosSide, p.Qty, p.EntryPx, p.UnrealizedPnl, p.StopLoss)
		}
	}
	if b.Len() == 0 {
		return "没有持仓"
	}
	return strings.TrimSpace(b.String())
}

func (c Commands) Pause(arg string) string  { return c.act(arg, "pause", "已暂停") }
func (c Commands) Resume(arg string) string { return c.act(arg, "resume", "已恢复") }

// act resolves an instance by numeric id or by (case-insensitive) name among live accounts.
func (c Commands) act(arg, action, done string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "用法：/" + action + " <实例 ID 或名称>"
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		id = 0
		for _, a := range c.S.Accounts() {
			if a.Archived || a.Mode != ModeLive {
				continue
			}
			insts, _ := c.S.Instances(a.ID)
			for _, in := range insts {
				if strings.EqualFold(in.Name, arg) {
					if id != 0 {
						return "名称不唯一，请用实例 ID"
					}
					id = in.ID
				}
			}
		}
		if id == 0 {
			return "找不到实例 " + arg
		}
	}
	in, err := c.S.InstanceAction(id, action)
	if err != nil {
		return "失败：" + err.Error()
	}
	return fmt.Sprintf("%s #%d %s", in.Name, in.ID, done)
}
