package trade

import (
	"fmt"
	"math"
)

// sizeIn is everything sizing needs, so it can be tested without a book.
type sizeIn struct {
	Equity, Cash float64
	InstMargin   float64 // margin already used in this instrument (whole account)
	Entry, Stop  float64
	Long         bool
	Strength     float64 // 0..1
	Scale        float64 // AI / human size scale 0..1
	Lever        float64 // requested leverage (0 = account cap)
	Spec         Spec
	Limits       Limits
	MMR          float64
}

type sizeOut struct {
	Qty, Lever, Margin float64
}

// liqBuffer keeps the liquidation price at least this fraction of the stop distance beyond the stop.
const liqBuffer = 0.25

// size computes the order quantity: qty = equity × RiskPct × strength × scale / (|entry − stop| × ctVal),
// then caps it by available cash, the per-instrument margin cap (MaxInstPct) and rounds down to the
// lot. Leverage is capped by the account and the contract, and lowered so the liquidation price
// stays beyond the stop. Without a stop the margin is equity × MaxInstPct × strength × scale.
func size(in sizeIn) (sizeOut, error) {
	if in.Entry <= 0 || in.Spec.CtVal <= 0 {
		return sizeOut{}, fmt.Errorf("缺少价格或合约面值")
	}
	lev := in.Lever
	if lev <= 0 || lev > in.Limits.MaxLeverage {
		lev = in.Limits.MaxLeverage
	}
	if in.Spec.MaxLever > 0 {
		lev = math.Min(lev, in.Spec.MaxLever)
	}
	strength, scale := clamp01(in.Strength, 1), clamp01(in.Scale, 1)
	var qty float64
	if in.Stop > 0 {
		dist := math.Abs(in.Entry - in.Stop)
		if (in.Long && in.Stop >= in.Entry) || (!in.Long && in.Stop <= in.Entry) {
			return sizeOut{}, fmt.Errorf("止损价 %s 位于开仓价 %s 的错误一侧", fmtPx(in.Stop), fmtPx(in.Entry))
		}
		if bound := levForStop(in.Entry, in.Stop, dist, in.Long, in.MMR); bound > 0 {
			lev = math.Min(lev, bound)
		}
		qty = in.Equity * in.Limits.RiskPct * strength * scale / (dist * in.Spec.CtVal)
	} else {
		lev = math.Max(1, lev)
		qty = in.Equity * in.Limits.MaxInstPct * strength * scale * lev / (in.Entry * in.Spec.CtVal)
	}
	lev = math.Floor(lev*100) / 100
	if lev < 1 && in.Long {
		lev = 1 // a 1x long cannot be liquidated above zero
	}
	if lev < 1 {
		return sizeOut{}, fmt.Errorf("止损距离过大：1 倍杠杆下强平价也在止损之前")
	}
	maxMargin := math.Min(in.Cash*0.98, in.Equity*in.Limits.MaxInstPct-in.InstMargin)
	if maxMargin <= 0 {
		return sizeOut{}, fmt.Errorf("可用保证金不足或已达单合约上限 %.0f%%", in.Limits.MaxInstPct*100)
	}
	if capQty := maxMargin * lev / (in.Entry * in.Spec.CtVal); qty > capQty {
		qty = capQty
	}
	lot := in.Spec.LotSz
	if lot <= 0 {
		lot = in.Spec.MinSz
	}
	qty = floorStep(qty, lot)
	if qty <= 0 || qty < in.Spec.MinSz-qtyEps {
		return sizeOut{}, fmt.Errorf("按风险计算的数量 %s 张低于最小下单量 %s 张", fmtQty(qty), fmtQty(in.Spec.MinSz))
	}
	return sizeOut{Qty: qty, Lever: lev, Margin: qty * in.Spec.CtVal * in.Entry / lev}, nil
}

// levForStop is the highest leverage whose liquidation price stays liqBuffer × stop distance
// beyond the stop (0 = no bound).
func levForStop(entry, stop, dist float64, long bool, mmr float64) float64 {
	if long {
		s := stop - liqBuffer*dist
		d := 1 - s*(1-mmr)/entry
		if d <= 0 {
			return 0
		}
		return 1 / d
	}
	s := stop + liqBuffer*dist
	d := s*(1+mmr)/entry - 1
	if d <= 0 {
		return 0
	}
	return 1 / d
}

func clamp01(x, def float64) float64 {
	if x <= 0 || !finite(x) {
		return def
	}
	return math.Min(x, 1)
}

// checkOpen applies the account rules to an opening signal (caller holds b.mu). ref is the
// reference entry price.
func (b *book) checkOpen(sg Signal, ref float64, now int64) error {
	l := b.acc.Limits
	if b.checkBreaker(now) {
		return fmt.Errorf("日内熔断：今日（UTC+8）不再开仓")
	}
	if t, ok := b.lastClose[sg.InstID]; ok && l.CooldownMin > 0 && now-t < int64(l.CooldownMin)*minuteMs {
		return fmt.Errorf("冷却中：%s 平仓后 %d 分钟内不再开仓", shortInst(sg.InstID), l.CooldownMin)
	}
	if l.RequireStop && sg.StopLoss <= 0 {
		return fmt.Errorf("账户要求开仓信号带止损")
	}
	long := sg.Action == OpenLong
	if sg.TakeProfit > 0 && ((long && sg.TakeProfit <= ref) || (!long && sg.TakeProfit >= ref)) {
		return fmt.Errorf("止盈价 %s 位于参考价 %s 的错误一侧", fmtPx(sg.TakeProfit), fmtPx(ref))
	}
	k := posKey{sg.InstanceID, sg.InstID}
	if p := b.pos[k]; p != nil {
		if (p.PosSide == "long") != long {
			return fmt.Errorf("已有反向持仓，请先平仓")
		}
	} else {
		for _, p := range b.pos {
			if p.InstID == sg.InstID && (p.PosSide == "long") != long {
				return fmt.Errorf("账户内另一实例持有反向仓位")
			}
		}
		if l.MaxPositions > 0 && len(b.pos) >= l.MaxPositions {
			return fmt.Errorf("持仓数已达上限 %d", l.MaxPositions)
		}
	}
	return nil
}

// checkBreaker rolls the UTC+8 day and trips the daily loss breaker; it reports whether opening
// is halted. Caller holds b.mu.
func (b *book) checkBreaker(now int64) bool {
	b.rollDay(now)
	l := b.acc.Limits
	if !b.day.halted && l.DailyLossPct > 0 && b.day.start > 0 && b.dayPnl() <= -l.DailyLossPct*b.day.start {
		b.day.halted = true
		saveDayState(b.s.db, b.acc.ID, b.day)
		b.notifyBreaker(now)
		b.s.changed(b.acc.ID, "account")
	}
	return b.day.halted
}

func (b *book) rollDay(now int64) {
	k := dayKey(now)
	if b.day.key == k {
		return
	}
	if b.day.key != 0 {
		b.dailySummary(b.day.key, now)
	}
	b.day = dayState{key: k, start: b.equity()}
	saveDayState(b.s.db, b.acc.ID, b.day)
}
