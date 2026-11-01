package realtrade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
)

// After the owner approved a proposal, its chat is told what happened — executed (a market fill, a
// limit placed, TP/SL moved), a limit filled or cancelled, failed, invalidated — and when its AI
// position closed. The message is steered into the chat (aigate.SteerChat): it starts a turn in an idle
// chat and is injected into a running one, so the agent verifies the fill and the TP/SL on OKX and
// leaves the next signals in place. A chat that cannot take it now (its turn is finishing, the agent is
// down) keeps it queued (trade_steers): retried when a turn ends and every few seconds, failed after 2 h.

// Steerer delivers a message into a chat now; ErrSteerLater when the chat cannot take it yet.
type Steerer func(chat int64, message string) error

// ErrSteerLater is what a Steerer returns to have the message retried.
var ErrSteerLater = errors.New("the chat cannot take the message yet")

const steerTimeout = 2 * time.Hour

// SetSteerer wires the chat agent (aigate.SteerChat with the trade role).
func (s *Service) SetSteerer(f Steerer) {
	s.mu.Lock()
	s.steerer = f
	s.mu.Unlock()
}

// SetSignals wires internal/signals: brief lists the active signals linked to a position (or the chat's
// on that instrument) for the messages; closed cancels a closed position's signals and returns their ids.
func (s *Service) SetSignals(brief func(chat int64, instID string, posID int64) string, closed func(posID int64, reason string) []int64) {
	s.mu.Lock()
	s.sigBrief, s.onClosed = brief, closed
	s.mu.Unlock()
}

// ChatDone: a chat turn ended; messages waiting for it may go now.
func (s *Service) ChatDone(int64) { go s.deliverSteers(context.Background()) }

// queueSteer records a message for the proposal's chat and tries to deliver it. A live market
// execution waits Options.Settle so the fill and its TP/SL show in the account snapshot.
func (s *Service) queueSteer(id int64, kind string) {
	p, err := s.get(id)
	if err != nil || p.ChatID <= 0 {
		return
	}
	now := s.now()
	due := now
	if p.Mode == string(okxorder.Live) && (kind == StatusExecuted || kind == OrdFilled) && s.opts.Settle > 0 {
		due = now.Add(s.opts.Settle)
	}
	if _, err := s.db.Exec(`INSERT INTO trade_steers(proposal_id, chat_id, kind, ts, due, state) VALUES(?,?,?,?,?,'queued')`,
		id, p.ChatID, kind, now.UnixMilli(), due.UnixMilli()); err != nil {
		s.logf("realtrade: steer %d: %v", id, err)
		return
	}
	go func() {
		if d := due.Sub(now); d > 0 {
			time.Sleep(d)
		}
		s.deliverSteers(context.Background())
	}()
}

// deliverSteers sends the queued messages that are due, oldest first; one pass at a time.
func (s *Service) deliverSteers(ctx context.Context) {
	s.mu.Lock()
	f := s.steerer
	s.mu.Unlock()
	if f == nil {
		return
	}
	s.steerMu.Lock()
	defer s.steerMu.Unlock()
	now := s.now()
	rows, err := s.db.Query(`SELECT id, proposal_id, chat_id, kind, ts FROM trade_steers WHERE state='queued' AND due<=? ORDER BY id`, now.UnixMilli())
	if err != nil {
		return
	}
	type job struct {
		id, prop, chat, ts int64
		kind               string
	}
	var jobs []job
	for rows.Next() {
		var j job
		rows.Scan(&j.id, &j.prop, &j.chat, &j.kind, &j.ts)
		jobs = append(jobs, j)
	}
	rows.Close()
	busy := map[int64]bool{} // keep a chat's messages in order: after one has to wait, the later ones wait too
	for _, j := range jobs {
		if busy[j.chat] {
			continue
		}
		p, err := s.get(j.prop)
		if err != nil {
			s.db.Exec(`UPDATE trade_steers SET state='failed', error=? WHERE id=?`, err.Error(), j.id)
			continue
		}
		msg := s.steerText(ctx, p, j.kind)
		err = f(j.chat, msg)
		switch {
		case err == nil:
			s.db.Exec(`UPDATE trade_steers SET state='delivered', message=?, tries=tries+1, done_at=?, error='' WHERE id=?`, msg, s.now().UnixMilli(), j.id)
		case errors.Is(err, ErrSteerLater) && now.Sub(time.UnixMilli(j.ts)) < steerTimeout:
			busy[j.chat] = true
			s.db.Exec(`UPDATE trade_steers SET tries=tries+1, error=? WHERE id=?`, err.Error(), j.id)
		default:
			s.db.Exec(`UPDATE trade_steers SET state='failed', message=?, tries=tries+1, error=?, done_at=? WHERE id=?`, msg, err.Error(), s.now().UnixMilli(), j.id)
			s.logf("realtrade: steer #%d to chat %d: %v", j.prop, j.chat, err)
		}
	}
}

// steerText is the message the chat gets: what happened, the position now (OKX, or the dry-run
// simulation) with its TP/SL, every action on it so far, its signals, and what to do next.
func (s *Service) steerText(ctx context.Context, p Proposal, kind string) string {
	var b strings.Builder
	dry := ""
	if p.Mode == string(okxorder.DryRun) {
		dry = "[DRY RUN] "
	}
	what := kindText(p.Kind) + " " + posText(p.InstID, p.Side)
	order := fmt.Sprintf("%s %s 张", ordText(p), fmtNum(p.Contracts))
	switch kind {
	case StatusExecuted:
		switch {
		case p.Kind == KindAdjust, p.Kind == KindCancel, p.Kind == KindAmend:
			fmt.Fprintf(&b, "✅ %s#%d 已执行 · %s：%s", dry, p.ID, what, p.TPSL)
		case p.OrdType == okxorder.Limit:
			fmt.Fprintf(&b, "📌 %s#%d 限价单已挂出 · %s · %s，%s前未成交的部分自动撤单", dry, p.ID, what, order,
				time.UnixMilli(p.OrderUntil).In(tz8).Format("01-02 15:04"))
		default:
			fmt.Fprintf(&b, "✅ %s#%d 已成交 · %s · 市价 %s 张 @≈%s", dry, p.ID, what, fmtNum(p.Contracts), okxorder.FormatPx(p.ExecPx))
		}
	case OrdFilled:
		fmt.Fprintf(&b, "✅ %s#%d 限价单已成交 · %s · %s 张 @%s", dry, p.ID, what, fmtNum(p.Filled), okxorder.FormatPx(p.FillPx))
	case OrdCanceled:
		if p.Filled > 0 {
			fmt.Fprintf(&b, "⚠️ %s#%d 限价单部分成交 %s/%s 张 @%s，剩余已撤单 · %s", dry, p.ID, fmtNum(p.Filled), fmtNum(p.Contracts), okxorder.FormatPx(p.FillPx), what)
		} else {
			fmt.Fprintf(&b, "⌛ %s#%d 限价单未成交，已撤单 · %s @%s", dry, p.ID, what, okxorder.FormatPx(p.Px))
		}
	case StatusFailed:
		fmt.Fprintf(&b, "❌ %s#%d 执行失败 · %s：%s", dry, p.ID, what, clip(p.Error, 300))
	case StatusRejected:
		fmt.Fprintf(&b, "🚫 %s#%d 主人拒绝了这个提议，没有下单 · %s · %s", dry, p.ID, what, summary(p))
	case StatusExpired:
		fmt.Fprintf(&b, "⌛ %s#%d 超时未审核，提议已过期，没有下单 · %s · %s", dry, p.ID, what, summary(p))
	case StatusInvalidated:
		fmt.Fprintf(&b, "⚠️ %s#%d 主人已同意，但复核未通过，没有下单 · %s：%s", dry, p.ID, what, clip(p.Error, 300))
	case "closed":
		fmt.Fprintf(&b, "📕 %s仓位 #%d 已结束 · %s %s · %s %s · %+.2f U", dry, p.ID, sideText(p.Side), coin(p.InstID), closeText(p.CloseKind),
			okxorder.FormatPx(p.ClosePx), p.PnlUSDT)
	}
	if kind == StatusExecuted && p.Kind != KindAdjust && p.OrdType != okxorder.Limit || kind == OrdFilled {
		fmt.Fprintf(&b, " · %s %d×", mgnText(p.MarginMode), p.Leverage)
	}
	b.WriteString("\n")
	if p.TPSL != "" && (kind == StatusExecuted || kind == OrdFilled) && p.Kind != KindAdjust && p.Kind != KindCancel && p.Kind != KindAmend {
		b.WriteString("止盈止损：" + p.TPSL + "\n")
	}
	if strings.HasPrefix(p.Error, "警告") {
		b.WriteString(clip(p.Error, 300) + "\n")
	}
	root := p.PosID
	if p.Kind == KindOpen {
		root = p.ID
	}
	b.WriteString(s.PositionBrief(ctx, p.ChatID, p.InstID, p.Side, root))
	b.WriteString("\n")
	switch kind {
	case StatusExecuted, OrdFilled:
		if p.OrdType == okxorder.Limit && kind == StatusExecuted {
			b.WriteString("请确认挂单价位和到期时间符合计划；成交或撤单时你会再收到消息。需要盯的价位用 subscribe_signal（带 tradeId）。")
		} else {
			b.WriteString("请用 get_positions 核对 OKX 上的仓位和止盈止损（张数、价格、是否覆盖整个仓位）。然后为这个仓位留好下一步的信号" +
				"（例如移到保本的价位、部分止盈位、失效位；subscribe_signal 带 tradeId），过时的信号取消。需要调整就提议" +
				"（补仓 / 减仓 / 平仓 / 调整止盈止损，都要主人审核）。")
		}
	case OrdCanceled:
		b.WriteString("请说明这个价位是否还值得等：值得就重新提议或订阅信号（带 tradeId），否则说明原因并取消相关信号。")
	case StatusFailed:
		b.WriteString("请用 get_positions 核对 OKX 上的实际状态（可能部分请求已生效），告诉主人发生了什么；原因没排除前不要重复提议同一笔。")
	case StatusRejected:
		b.WriteString("主人没有给原因：不要原样再提；简短问他想改什么（价位、止损、仓位大小、还是不想做），或带着新证据重新提议。过时的信号取消。")
	case StatusExpired:
		b.WriteString("请用最新数据判断这个计划现在还成立吗：成立就按当前价重新提议（有效期可以放长），不成立就说明原因并取消相关信号。")
	case StatusInvalidated:
		b.WriteString("请用最新数据判断计划是否还成立：成立就按新价格重新提议，否则说明原因。")
	case "closed":
		b.WriteString("这个仓位已结束，关联信号已自动取消。用两三句复盘：计划与结果、哪里做对或做错；有值得长期记住的经验就 memory_save。")
	}
	b.WriteString("\n（这是交易执行后的自动消息，主人此刻不一定在看。）")
	return b.String()
}

// PositionBrief is the compact state of a position for the agent: on OKX (or the dry-run simulation)
// its size, average, liquidation and TP/SL; every action on it (root = its open, 0 = the owner's own);
// and its signals.
func (s *Service) PositionBrief(ctx context.Context, chat int64, inst, side string, root int64) string {
	var b strings.Builder
	var pos okxacct.Position
	found := false
	if r, err := s.get(root); err == nil && root > 0 && r.Mode == string(okxorder.DryRun) {
		if r.ClosedAt == 0 && r.CurContracts > 0 {
			pos, found = simulated(r), true
		}
	} else if a := s.account(); a != nil {
		if snap, err := a.Snapshot(ctx, inst); err == nil {
			for _, x := range snap.Positions {
				if x.Side == side {
					pos, found = x, true
				}
			}
		} else {
			b.WriteString("仓位：读取 OKX 失败（" + clip(err.Error(), 120) + "）\n")
		}
	}
	if found {
		b.WriteString(positionLine(pos) + "\n")
	} else if !strings.Contains(b.String(), "失败") {
		fmt.Fprintf(&b, "仓位：%s %s 当前没有持仓\n", coin(inst), sideText(side))
	}
	if root > 0 {
		if ps, err := s.List(ListQuery{PosID: root, Limit: 20}); err == nil && len(ps) > 0 {
			b.WriteString("此仓位的操作：")
			for i := len(ps) - 1; i >= 0; i-- {
				q := ps[i]
				fmt.Fprintf(&b, "\n· #%d %s %s %s", q.ID, time.UnixMilli(q.Created).In(tz8).Format("01-02 15:04"), shortAction(q), proposalState(q))
			}
			b.WriteString("\n")
		}
	}
	s.mu.Lock()
	f := s.sigBrief
	s.mu.Unlock()
	if f != nil {
		if sig := f(chat, inst, root); sig != "" {
			b.WriteString("此仓位的信号：" + sig + "\n")
		} else {
			b.WriteString("此仓位的信号：无\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// WakeContext is the position block a signal wake-up carries: the AI positions (and ctg's resting
// orders) in the subscription's instruments, or the one it is linked to.
func (s *Service) WakeContext(chat int64, insts []string, posID int64) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var parts []string
	seen := map[int64]bool{}
	if posID > 0 {
		if r, err := s.get(posID); err == nil {
			seen[posID] = true
			parts = append(parts, fmt.Sprintf("关联仓位 #%d：\n%s", posID, s.PositionBrief(ctx, chat, r.InstID, r.Side, posID)))
		}
	}
	for _, inst := range insts {
		for _, side := range []string{"long", "short"} {
			if r, ok := s.root(inst, side); ok && !seen[r.ID] {
				seen[r.ID] = true
				parts = append(parts, fmt.Sprintf("AI 仓位 #%d：\n%s", r.ID, s.PositionBrief(ctx, chat, inst, side, r.ID)))
			}
		}
		for _, r := range s.restingIn(inst) {
			if !seen[r.ID] && r.PosID == 0 {
				parts = append(parts, fmt.Sprintf("挂单中：#%d %s", r.ID, shortAction(r)))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func positionLine(x okxacct.Position) string {
	line := fmt.Sprintf("仓位：%s %s %s 张 · 均价 %s · %s %s×", coin(x.InstID), sideText(x.Side), fmtNum(x.Contracts), okxorder.FormatPx(x.AvgPx),
		mgnText(x.MgnMode), fmtNum(x.Lever))
	if x.LiqPx > 0 {
		line += " · 强平 " + okxorder.FormatPx(x.LiqPx)
	}
	if x.MarkPx > 0 {
		line += fmt.Sprintf(" · 标记价 %s · 未实现 %+.2f U", okxorder.FormatPx(x.MarkPx), x.Upl)
	}
	legs := func(name string, ts []okxacct.Trigger) string {
		if len(ts) == 0 {
			return name + " 无"
		}
		var out []string
		for _, t := range ts {
			cover := "整仓"
			if !t.Full {
				cover = fmtNum(t.Contracts) + " 张"
			}
			who := ""
			if okxorder.Owned(t.ClientID) {
				who = "ctg "
			}
			out = append(out, fmt.Sprintf("%s%s（%s%s）", name, okxorder.FormatPx(t.Px), who, cover))
		}
		return strings.Join(out, "、")
	}
	return line + "\nOKX 上的止盈止损：" + legs("止损 ", x.SL) + "；" + legs("止盈 ", x.TP)
}

func shortAction(q Proposal) string {
	s := kindText(q.Kind) + " " + ordText(q)
	switch q.Kind {
	case KindOpen, KindAdd:
		s += fmt.Sprintf(" %s 张 · 止损 %s 止盈 %s", fmtNum(q.Contracts), okxorder.FormatPx(q.SL), okxorder.FormatPx(q.TP))
	case KindReduce:
		s += fmt.Sprintf(" %.0f%%（%s 张）", q.Fraction*100, fmtNum(q.Contracts))
	case KindClose:
		s += " 全部"
	case KindCancel, KindAmend:
		s = kindText(q.Kind) + " " + q.TPSL
	case KindAdjust:
		s = kindText(q.Kind) + fmt.Sprintf(" 止损 %s 止盈 %s", okxorder.FormatPx(q.SL), okxorder.FormatPx(q.TP))
	}
	return s
}

func proposalState(q Proposal) string {
	st := statusText[q.Status]
	switch {
	case q.Status == StatusExecuted && q.OrdState == OrdLive:
		st = "挂单中"
	case q.Status == StatusExecuted && q.OrdState == OrdPartial:
		st = fmt.Sprintf("挂单中，已成交 %s/%s", fmtNum(q.Filled), fmtNum(q.Contracts))
	case q.Status == StatusExecuted && q.OrdState == OrdCanceled:
		st = fmt.Sprintf("已撤单（成交 %s/%s）", fmtNum(q.Filled), fmtNum(q.Contracts))
	case q.Status == StatusExecuted && q.FillPx > 0 && q.Kind != KindAdjust:
		st = "已成交 @" + okxorder.FormatPx(q.FillPx)
	}
	if q.ClosedAt > 0 && q.Kind == KindOpen && q.CloseKind != "unfilled" {
		st += fmt.Sprintf("；仓位已结束 %s %+.2f U", closeText(q.CloseKind), q.PnlUSDT)
	}
	return st
}
