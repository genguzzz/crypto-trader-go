package autopilot

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/realtrade"
	"crypto-trader-go/internal/signals"
)

// The brief is the message a run delivers to the desk's chat. It is self-contained: the agent's
// session may be fresh (ResetSession), so everything the desk must remember travels here — what
// fired, what it holds and has proposed, what it decided recently and how that turned out, the
// lessons it keeps, and what is expected of this run.

const (
	briefMax       = 7600 // runes; the chat takes 8000
	briefSignals   = 12
	briefDecisions = 8
	briefMemory    = 14
	briefReviews   = 3
)

// InboxItem is one coin of a scan's fire, waiting for (or delivered by) a run.
type InboxItem struct {
	ID       int64   `json:"id"`
	Ts       int64   `json:"ts"`
	FireID   int64   `json:"fireId,omitempty"`
	SubID    int64   `json:"subId,omitempty"`
	Strategy string  `json:"strategy"`
	Label    string  `json:"label"`
	InstID   string  `json:"instId"`
	Px       float64 `json:"px,omitempty"`
	Value    float64 `json:"value,omitempty"`
	Text     string  `json:"text"`
	Status   string  `json:"status"`
	RunID    int64   `json:"runId,omitempty"`
}

func (s *Service) inbox(where string, args ...any) []InboxItem {
	rows, err := s.db.Query(`SELECT id, ts, fire_id, sub_id, strategy, label, inst_id, px, value, text, status, run_id FROM desk_inbox WHERE `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []InboxItem{}
	for rows.Next() {
		var x InboxItem
		if rows.Scan(&x.ID, &x.Ts, &x.FireID, &x.SubID, &x.Strategy, &x.Label, &x.InstID, &x.Px, &x.Value, &x.Text, &x.Status, &x.RunID) == nil {
			out = append(out, x)
		}
	}
	return out
}

func (s *Service) pendingInbox() []InboxItem {
	return s.inbox(`status=? ORDER BY id LIMIT 200`, InboxPending)
}

func triggerLabel(trigger string, items []InboxItem) string {
	switch trigger {
	case TriggerSchedule:
		return "定时巡检"
	case TriggerManual:
		return "手动运行"
	}
	var labels []string
	for _, it := range items {
		if !slices.Contains(labels, it.Label) {
			labels = append(labels, it.Label)
		}
	}
	if len(labels) > 2 {
		labels = append(labels[:2], fmt.Sprintf("等 %d 类", len(labels)))
	}
	return "信号 · " + strings.Join(labels, "、")
}

type briefInput struct {
	RunID      int64
	Trigger    string
	Now        time.Time
	Items      []InboxItem
	Insts      []string
	Chat       int64
	Note       string
	NewSession bool
}

func coin(inst string) string { return strings.TrimSuffix(inst, "-USDT-SWAP") }

// nextFunding is the next 00:00 / 08:00 / 16:00 UTC+8 (the 8 h settlement most contracts follow).
func nextFunding(now time.Time) time.Time {
	t := now.In(tz8)
	h := (t.Hour()/8 + 1) * 8
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz8).Add(time.Duration(h) * time.Hour)
}

func durText(d time.Duration) string {
	m := int(d.Minutes() + 0.5)
	if m < 60 {
		return fmt.Sprintf("%d 分钟", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d 小时", m/60)
	}
	return fmt.Sprintf("%d 小时 %d 分", m/60, m%60)
}

// brief renders the run's message.
func (s *Service) brief(cfg Config, in briefInput) string {
	var b strings.Builder
	now := in.Now
	fmt.Fprintf(&b, "🤖 交易台 #%d · %s（%s UTC+8）\n", in.RunID, triggerLabel(in.Trigger, in.Items), now.In(tz8).Format("01-02 15:04"))
	nf := nextFunding(now)
	fmt.Fprintf(&b, "下次 8 小时资金费结算 %s（还有 %s）", nf.Format("15:04"), durText(nf.Sub(now)))
	if s.opts.TradeMode != nil {
		if m := s.opts.TradeMode(); m != "live" {
			b.WriteString(" · 交易模式：模拟（dry run）")
		} else {
			b.WriteString(" · 交易模式：实盘")
		}
	}
	b.WriteString("\n")
	if in.NewSession {
		b.WriteString("（这是一段新的上下文：之前的对话不在你的记忆里，以下面的决策日志和长期记忆为准；需要细节用 desk_recall 或 read_chat。）\n")
	}
	if cfg.Focus != "" {
		b.WriteString("\n【主人的长期指示】" + cfg.Focus + "\n")
	}
	b.WriteString(riskText(cfg.Risk))
	if in.Note != "" {
		b.WriteString("\n【主人这次的要求】" + clip(in.Note, 500) + "\n")
	}

	if len(in.Items) > 0 {
		fmt.Fprintf(&b, "\n【本次信号】自上次运行以来 %d 条，涉及 %d 个币：\n", len(in.Items), len(in.Insts))
		// newest first per coin, strongest strategies grouped by coin so the agent reads one line per coin
		byInst := map[string][]InboxItem{}
		for _, it := range in.Items {
			byInst[it.InstID] = append(byInst[it.InstID], it)
		}
		shown := 0
		hints := []string{}
		for _, inst := range in.Insts {
			if shown >= briefSignals {
				fmt.Fprintf(&b, "· 另有 %d 个币未列出\n", len(in.Insts)-shown)
				break
			}
			shown++
			its := byInst[inst]
			parts := make([]string, 0, len(its))
			for _, it := range its[:min(len(its), 3)] {
				parts = append(parts, fmt.Sprintf("[%s] %s（%s）", it.Label, strings.TrimPrefix(it.Text, coin(inst)+" "), time.UnixMilli(it.Ts).In(tz8).Format("15:04")))
				if st, ok := cfg.strategy(it.Strategy); ok && st.Hint != "" && !slices.Contains(hints, it.Label+"："+st.Hint) {
					hints = append(hints, it.Label+"："+st.Hint)
				}
			}
			line := "· " + coin(inst) + "：" + strings.Join(parts, "；")
			if px, ts, ok := s.mkt.Quote(inst); ok && now.Sub(time.UnixMilli(ts)) < 2*time.Minute {
				line += "，现价 " + fmtPx(px)
			}
			b.WriteString(line + "\n")
		}
		for _, h := range hints[:min(len(hints), 4)] {
			b.WriteString("  ↳ " + h + "\n")
		}
	} else if in.Trigger == TriggerSchedule {
		b.WriteString("\n【本次信号】自上次运行以来没有策略信号触发。\n")
	}

	// positions and resting orders: the coins that fired plus everything the desk has open
	insts := append([]string(nil), in.Insts...)
	open, _ := s.trades.List(realtrade.ListQuery{Status: "open", ChatID: in.Chat, Limit: 20})
	executed, _ := s.trades.List(realtrade.ListQuery{Status: realtrade.StatusExecuted, ChatID: in.Chat, Limit: 50})
	for _, p := range executed {
		if p.Kind == realtrade.KindOpen && p.ClosedAt == 0 && !slices.Contains(insts, p.InstID) {
			insts = append(insts, p.InstID)
		}
	}
	if blk := s.trades.WakeContext(in.Chat, insts, 0); blk != "" {
		b.WriteString("\n【AI 仓位与挂单】\n" + clip(blk, 1500) + "\n")
	} else {
		b.WriteString("\n【AI 仓位与挂单】无。\n")
	}
	var pend []string
	for _, p := range open {
		if p.Status == realtrade.StatusPending {
			what := actionText[p.Kind]
			if what == "" {
				what = p.Kind // cancel / amend of a resting order
			}
			pend = append(pend, fmt.Sprintf("· #%d %s %s%s，等待主人批准（%s 前有效）", p.ID, coin(p.InstID), what, sideText[p.Side],
				time.UnixMilli(p.ExpiresAt).In(tz8).Format("15:04")))
		}
	}
	if len(pend) > 0 {
		b.WriteString("\n【待主人审核的提议】\n" + strings.Join(pend, "\n") + "\n")
	}

	// what the desk's agent is waiting for: a fresh context would otherwise subscribe to it again
	if subs, _ := s.scans.List(signals.ListQuery{Status: signals.StatusActive, ChatID: in.Chat, Limit: 10}); len(subs) > 0 {
		b.WriteString("\n【你在等的信号】（触发时会单独唤醒你；不要重复订阅，过时的用 unsubscribe_signal 取消）\n")
		for _, x := range subs {
			line := fmt.Sprintf("· #%d %s（%s 到期）", x.ID, x.Desc, time.UnixMilli(x.ExpiresAt).In(tz8).Format("01-02 15:04"))
			if x.TradeID > 0 {
				line += fmt.Sprintf(" · 管理仓位 #%d", x.TradeID)
			}
			if x.Intent != "" {
				line += "：" + clip(x.Intent, 80)
			}
			b.WriteString(line + "\n")
		}
	}
	if ds, _ := s.Journal(JournalQuery{Limit: briefDecisions}); len(ds) > 0 {
		b.WriteString("\n【最近决策】（新 → 旧；括号里是之后的价格变化，按你的方向计正负）\n")
		for _, d := range ds {
			b.WriteString("· " + d.line() + "\n")
		}
	}
	if due := s.reviewsDue(briefReviews); len(due) > 0 {
		b.WriteString("\n【待复盘】结果已出、还没写结论的决策：\n")
		for _, d := range due {
			b.WriteString("· " + d.line() + "\n")
		}
	}
	if ms := s.memoryFor(in.Insts, briefMemory); len(ms) > 0 {
		b.WriteString("\n【长期记忆】\n")
		for _, m := range ms {
			b.WriteString("· " + m.line() + "\n")
		}
	}
	// the instructions must never be cut off: the context above them gives way
	var body strings.Builder
	body.WriteString(b.String())
	b.Reset()
	fmt.Fprintf(&b, "\n【预算】过去 24 小时信号运行 %d/%d 次（含本轮）；本轮最多 %d 笔提议。\n", s.signalRuns(now), cfg.Signals.MaxRunsDay, cfg.MaxProposals)

	b.WriteString("\n【这一轮要做的】\n")
	switch {
	case len(in.Items) > 0 && in.Trigger != TriggerSchedule:
		b.WriteString("1. 只看上面触发信号的币（多的话挑最值得的两三个）和已有仓位；用最新数据核对，信号里的数字是触发那一刻的。\n")
	case len(in.Items) > 0:
		b.WriteString("1. 先看大盘（BTC、ETH）和已有仓位，再看上面触发信号的币，最后扫一眼监控池里最突出的机会。\n")
	default:
		b.WriteString("1. 先看大盘（BTC、ETH）和已有仓位，再扫监控池里此刻最突出的一两个机会；没有就明确说没有。\n")
	}
	b.WriteString("2. 每个认真看过的币都用 desk_journal 记一笔决策（open / add / reduce / close / adjust / hold / watch / skip），写清方向、把握、" +
		"一句话理由、关键价位和失效条件——包括决定不做的，这是以后复盘的依据。\n" +
		"3. 要交易只能 propose_trade（主人批准后才执行），并把 proposalId 写进那笔 desk_journal。" + proposeRule(cfg.Risk) + "\n" +
		"   已有仓位和挂单同样要管：改止盈止损用 adjust，减仓 / 平仓用 reduce / close，撤掉或修改自己的限价挂单用 cancel / amend——都通过 propose_trade 交给主人批准（他在手表上确认）；该动就提，不要只在文字里建议。\n" +
		"4. 还差一步的用 subscribe_signal 盯具体条件；【待复盘】里的用 desk_review 写结论；能复用的经验用 desk_remember 存成长期记忆（每轮都会带给你，宁少勿滥）。\n" +
		"5. 回答从简：先一句结论，再每个币一两行。\n" +
		"（这是自动触发的交易台运行，主人此刻不一定在看。你不能下单，只能提交待审核的提议。）")
	tail := b.String()
	return clip(body.String(), briefMax-len([]rune(tail))) + tail
}

// riskText is the owner's sizing and loss tolerance in the agent's terms (units stated: USDT margin,
// % of margin, % of price).
func riskText(r RiskConfig) string {
	pricePct := r.MaxLossPct / float64(r.Leverage)
	style := "偏稳健"
	if r.Style == "aggressive" {
		style = "略偏激进：风险和收益并存，位置好、有一定把握的机会可以尝试，不必等到十拿九稳"
	}
	return fmt.Sprintf("\n【主人的风险风格】%s。\n"+
		"· 仓位：一笔通常保证金约 %s U、%d×（名义约 %s U）。\n"+
		"· 止损：最多亏保证金的 %s%%（%d× 下约 %s%% 的价格距离，约 %s U）；按结构放在失效位外，结构止损更远就降杠杆或减保证金，不要硬放宽。\n"+
		"· 扛单：位置好、逻辑没被破坏时可以扛住浮亏，不必一回撤就砍；可以在更好的价格补仓（add），同一个币含补仓的保证金合计不超过 %s U，"+
		"补仓后按新均价重算止损，整体亏损仍不超过整体保证金的 %s%%。逻辑被破坏（失效条件触发）就认错，不补仓摊平。\n",
		style, fmtNum(r.MarginUSDT), r.Leverage, fmtNum(r.MarginUSDT*float64(r.Leverage)),
		fmtNum(r.MaxLossPct), r.Leverage, fmtNum(math.Round(pricePct*100)/100), fmtNum(r.MarginUSDT*r.MaxLossPct/100), fmtNum(r.MaxMarginUSDT), fmtNum(r.MaxLossPct))
}

// proposeRule is the bar a trade has to clear before it is proposed.
func proposeRule(r RiskConfig) string {
	if r.Style == "aggressive" {
		return "位置好（靠近支撑/阻力、回踩确认、盈亏比 ≥ 1.5）且有中等把握就可以提议，不要因为不够完美就只记 watch；把握一般的用较小保证金或限价单在更好的位置挂。"
	}
	return "没有中等以上把握就不提议。"
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

var sideText = map[string]string{"long": "多", "short": "空"}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func fmtPx(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v >= 1000:
		return fmt.Sprintf("%.1f", v)
	case v >= 1:
		return fmt.Sprintf("%.4g", v)
	}
	return fmt.Sprintf("%.4g", v)
}

func joinIDs(ids []string) string { return strings.Join(ids, ",") }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
