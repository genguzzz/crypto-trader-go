package signals

import (
	"fmt"
	"strings"
	"time"
)

// Delivery is the record of one firing — the contract between the evaluator and whoever receives
// it. The wake-up message the agent reads, the push and the API's fire history are all rendered
// from it, and it is stored with the fire (signal_fires.payload, JSON). Version 1:
//
//	{"v":1, "subId":7, "fireId":31, "ts":1793600000000, "type":"pool", "desc":"监控池任一币 15 分钟内涨 ≥ 3%",
//	 "fireNo":2, "maxFires":10, "final":false, "expiresAt":…, "cooldownMin":60,
//	 "hits":[{"instId":"ENA-USDT-SWAP","px":0.1912,"value":3.42,"text":"ENA 15 分钟内 +3.42%（0.1849 → 0.1912）"}], "more":0,
//	 "matched":"…", "intent":"…", "subscriber":"chat", "chatId":12, "tradeId":0, "planId":0}
//
// hits: for a pool scan the members that turned true (largest |value| first, at most 10; more = the
// rest); for any other type one entry per instrument of the condition with its price then. value is
// the scan's measurement (percent for move / range, the field's own unit for field), 0 otherwise.
type Delivery struct {
	V           int    `json:"v"`
	SubID       int64  `json:"subId"`
	FireID      int64  `json:"fireId,omitempty"`
	Ts          int64  `json:"ts"`
	Type        string `json:"type"`
	Metric      string `json:"metric,omitempty"` // pool: move | range | field
	Desc        string `json:"desc"`
	FireNo      int    `json:"fireNo"`
	MaxFires    int    `json:"maxFires"`
	Final       bool   `json:"final"`
	ExpiresAt   int64  `json:"expiresAt"`
	CooldownMin int    `json:"cooldownMin"`
	Hits        []Hit  `json:"hits"`
	More        int    `json:"more,omitempty"`
	Matched     string `json:"matched,omitempty"`
	Intent      string `json:"intent,omitempty"`
	Subscriber  string `json:"subscriber"` // chat | owner
	ChatID      int64  `json:"chatId,omitempty"`
	TradeID     int64  `json:"tradeId,omitempty"`
	PlanID      int64  `json:"planId,omitempty"`
}

// DeliveryVersion is the current Delivery shape.
const DeliveryVersion = 1

// Hit is one instrument in a delivery.
type Hit struct {
	InstID string  `json:"instId"`
	Px     float64 `json:"px,omitempty"`
	Value  float64 `json:"value,omitempty"`
	Text   string  `json:"text,omitempty"`
}

// delivery builds the record of a firing (FireNo already counted).
func delivery(sub Subscription, hits []Hit, matched string, now time.Time) Delivery {
	d := Delivery{V: DeliveryVersion, SubID: sub.ID, Ts: now.UnixMilli(), Type: sub.Spec.Type, Metric: sub.Spec.Metric, Desc: sub.Desc, FireNo: sub.FireCount,
		MaxFires: sub.MaxFires, Final: sub.FireCount >= sub.MaxFires, ExpiresAt: sub.ExpiresAt, CooldownMin: sub.CooldownMin,
		Matched: matched, Intent: sub.Intent, Subscriber: sub.Kind, TradeID: sub.TradeID, PlanID: sub.PlanID}
	if sub.Kind == KindChat {
		d.ChatID = sub.ChatID
	}
	if len(hits) > poolMaxHits {
		d.More = len(hits) - poolMaxHits
		hits = hits[:poolMaxHits]
	}
	d.Hits = hits
	if d.Hits == nil {
		d.Hits = []Hit{}
	}
	return d
}

// px is the price shown with the delivery (its first hit's).
func (d Delivery) px() (string, float64) {
	if len(d.Hits) == 0 {
		return "", 0
	}
	return d.Hits[0].InstID, d.Hits[0].Px
}

// wakeText is what the subscriber's agent reads: the signal, what matched, its own plan, and what to do.
// ctxBlock is the positions block (may be empty); plan the linked plan (may be nil).
func (d Delivery) wakeText(ctxBlock string, plan *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ 信号 #%d 触发：%s\n", d.SubID, d.Desc)
	fmt.Fprintf(&b, "时间：%s（UTC+8）", time.UnixMilli(d.Ts).In(tz8).Format("01-02 15:04:05"))
	if inst, px := d.px(); px > 0 && d.Type != "pool" {
		fmt.Fprintf(&b, " · %s 现价 %s", coin(inst), fmtPx(px))
	}
	b.WriteString("\n")
	if d.Type == "pool" {
		fmt.Fprintf(&b, "触发的币（%d 个，按幅度排序）：\n", len(d.Hits)+d.More)
		for _, h := range d.Hits {
			b.WriteString("· " + h.Text)
			if h.Px > 0 && !strings.Contains(h.Text, "现价") {
				b.WriteString("，现价 " + fmtPx(h.Px))
			}
			b.WriteString("\n")
		}
		if d.More > 0 {
			fmt.Fprintf(&b, "（另有 %d 个未列出）\n", d.More)
		}
	} else if d.Matched != "" {
		b.WriteString("触发值：" + d.Matched + "\n")
	}
	if d.Intent != "" {
		b.WriteString("你订阅时的计划：" + d.Intent + "\n")
	}
	if ctxBlock != "" {
		b.WriteString(ctxBlock + "\n")
	}
	if p := plan; p != nil {
		fmt.Fprintf(&b, "关联计划 #%d：%s %s · 入场 %s–%s · 止损 %s · 止盈 %s", p.ID, map[bool]string{true: "做多", false: "做空"}[p.Side == "long"],
			coin(p.InstID), fmtPx(p.EntryLo), fmtPx(p.EntryHi), fmtPx(p.Stop), fmtPx(p.TP1))
		if p.TP2 > 0 {
			b.WriteString(" / " + fmtPx(p.TP2))
		}
		if p.Invalidate != "" {
			b.WriteString(" · 失效：" + p.Invalidate)
		}
		b.WriteString("（当前状态 " + p.Status + "）\n")
	}
	if !d.Final {
		fmt.Fprintf(&b, "这是第 %d/%d 次触发；订阅仍有效（冷却 %d 分钟，%s 到期）。\n", d.FireNo, d.MaxFires, d.CooldownMin,
			time.UnixMilli(d.ExpiresAt).In(tz8).Format("01-02 15:04"))
	} else {
		fmt.Fprintf(&b, "这是第 %d/%d 次触发；订阅已结束。\n", d.FireNo, d.MaxFires)
	}
	if d.Type == "pool" {
		b.WriteString("\n这是全池扫描信号：只关心上面列出的币。先挑最值得看的一两个，用最新数据（K 线、成交、盘口，有仓位先 get_positions）确认这次异动的性质，然后三选一：\n" +
			"1. 有可做的机会 → 用 propose_trade 提交（主人审核后才执行）；\n" +
			"2. 还差一步 → 对那个币用 subscribe_signal 订阅具体条件，并说明为什么；\n" +
			"3. 不值得做 → 一句话说明原因即可；扫描订阅会继续运行，除非它已经没用了（那就 unsubscribe_signal）。\n")
	} else {
		b.WriteString("\n请先用最新数据（有仓位就先 get_positions）重新检查，不要沿用订阅时的数字，然后三选一：\n" +
			"1. 计划仍然成立 → 用 propose_trade 提交对应的操作（开仓 / 补仓 / 减仓 / 平仓 / 调整止盈止损，主人审核后才执行）；\n" +
			"2. 还差一步 → 用 subscribe_signal 订阅调整后的条件（管理仓位的带 tradeId），并说明为什么；\n" +
			"3. 已经不成立 → 说明原因；若这个订阅仍有效且不再需要，用 unsubscribe_signal 取消。\n")
	}
	b.WriteString("（这是自动触发的信号会话，主人此刻不一定在看。你只能用 propose_trade 提交待主人审核的提议，不能下单；" +
		"把握不够就不要提议，直接说明理由（按主人的风险风格判断：位置好、有中等把握即可）。最多提议一笔。）")
	return b.String()
}

// pushText is the short note for the owner's phone (the wake-up state is added by the caller).
func (d Delivery) pushText(subscriber string) string {
	text := fmt.Sprintf("⚡ 信号 #%d 触发：%s", d.SubID, d.Desc)
	if d.Type == "pool" {
		parts := make([]string, 0, 5)
		for _, h := range d.Hits[:min(4, len(d.Hits))] {
			p := coin(h.InstID)
			switch {
			case d.Metric == "range":
				p += fmt.Sprintf(" 振幅 %.2f%%", h.Value)
			case d.Metric == "move":
				p += fmt.Sprintf(" %+.2f%%", h.Value)
			case h.Text != "":
				p = h.Text
			}
			parts = append(parts, p)
		}
		if len(d.Hits)+d.More > len(parts) {
			parts = append(parts, fmt.Sprintf("等 %d 个", len(d.Hits)+d.More))
		}
		text += "\n" + strings.Join(parts, "、")
	} else if inst, px := d.px(); px > 0 {
		text += fmt.Sprintf("（%s 现价 %s）", coin(inst), fmtPx(px))
	}
	return text + "\n订阅者：" + subscriber
}
