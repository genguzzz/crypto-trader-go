package trade

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

const defaultTTL = 5 * minuteMs

// Submit validates and persists a strategy signal and returns its id. Live signals are then
// processed asynchronously (AI review, confirmation, risk, sizing, orders); backtest signals are
// processed before Submit returns so the replayer stays in lockstep.
func (s *Service) Submit(ctx context.Context, sg Signal) (int64, error) {
	switch sg.Action {
	case OpenLong, OpenShort, Close, Reduce:
	default:
		return 0, fmt.Errorf("unknown action %q", sg.Action)
	}
	if sg.InstID == "" {
		return 0, fmt.Errorf("instId required")
	}
	for _, x := range []float64{sg.Strength, sg.StopLoss, sg.TakeProfit, sg.MaxSlippageBp, sg.Leverage, sg.ReduceFrac, sg.Price} {
		if !finite(x) || x < 0 {
			return 0, fmt.Errorf("numeric fields must be finite and non-negative")
		}
	}
	ri := s.instRT(sg.InstanceID)
	var inst Instance
	var b *book
	if ri != nil {
		inst, b = ri.instance(), ri.b
	} else {
		in, err := s.loadInstance(sg.InstanceID)
		if err != nil {
			return 0, err
		}
		inst = in
		if b, err = s.book(in.AccountID); err != nil {
			return 0, err
		}
	}
	now := b.market().Now()
	sg.ID, sg.AccountID, sg.RecvTs, sg.ConfigVersion = 0, b.acc.ID, now, inst.ConfigVersion
	sg.Outcome, sg.OutcomeReason, sg.AIDecisionID = OutPending, "", 0
	if sg.Ts <= 0 {
		sg.Ts = now
	}
	sg.Strength = clamp01(sg.Strength, 1)
	if sg.TTLms <= 0 {
		sg.TTLms = defaultTTL
	}
	if sg.MaxSlippageBp <= 0 {
		sg.MaxSlippageBp = b.snapshot().Limits.SlippageBp
	}
	if sg.Price <= 0 {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if q, err := b.market().Quote(qctx, sg.InstID); err == nil {
			sg.Price = refPrice(q)
		}
		cancel()
	}
	reason := ""
	spec, specOK := b.market().Spec(sg.InstID)
	if specOK {
		sg.StopLoss, sg.TakeProfit = roundTick(sg.StopLoss, spec.TickSz), roundTick(sg.TakeProfit, spec.TickSz)
	}
	switch {
	case ri == nil || inst.State != StRunning:
		reason = fmt.Sprintf("实例未运行（%s）", inst.State)
	case !specOK:
		reason = "合约不存在或已下线"
	case sg.Action == Reduce && (sg.ReduceFrac <= 0 || sg.ReduceFrac > 1):
		reason = "减仓比例 reduceFrac 需在 (0, 1]"
	}
	if err := insertSignal(s.db, &sg); err != nil {
		return 0, err
	}
	s.changed(b.acc.ID, "signal")
	if reason != "" {
		s.finish(b, sg, OutRejected, reason)
		return sg.ID, nil
	}
	if b.barModel {
		s.process(ctx, b, inst, sg)
	} else {
		go s.process(s.baseCtx(), b, inst, sg)
	}
	return sg.ID, nil
}

// finish records a signal's outcome, reports it and tells the strategy.
func (s *Service) finish(b *book, sg Signal, out SignalOutcome, reason string) {
	sg.Outcome, sg.OutcomeReason = out, reason
	if err := updateSignal(s.db, sg); err != nil {
		reason += "（记录失败：" + err.Error() + "）"
	}
	b.notifySignal(sg)
	s.enqueue(sg.InstanceID, "signal_result", func(ctx context.Context, x Session) error {
		return x.SignalResult(ctx, sg.ID, out, reason)
	})
	s.changed(b.acc.ID, "signal")
}

func needsConfirm(m ConfirmMode, opening bool) bool {
	return m == ConfirmAll || (m == ConfirmOpen && opening)
}

// process runs the pipeline: TTL → exec mode → pending guard → AI → confirmation → risk, sizing, orders.
func (s *Service) process(ctx context.Context, b *book, inst Instance, sg Signal) {
	expired := func() bool { return b.market().Now() > sg.Ts+sg.TTLms }
	if expired() {
		s.finish(b, sg, OutExpired, "信号已超过有效期")
		return
	}
	if inst.Exec == ExecRecord {
		s.finish(b, sg, OutRecorded, "仅记录模式")
		return
	}
	k := posKey{sg.InstanceID, sg.InstID}
	b.mu.Lock()
	if id := b.pending[k]; id != 0 {
		b.mu.Unlock()
		s.finish(b, sg, OutRejected, fmt.Sprintf("该合约已有待处理信号 #%d", id))
		return
	}
	b.pending[k] = sg.ID
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.pending[k] == sg.ID {
			delete(b.pending, k)
		}
		b.mu.Unlock()
	}()
	opening := sg.Action == OpenLong || sg.Action == OpenShort
	scale, note := 1.0, ""
	if inst.Exec == ExecAI && opening && !b.barModel {
		d, out, reason := s.review(ctx, b, inst, &sg)
		if out != "" {
			s.finish(b, sg, out, reason)
			return
		}
		scale, note = clamp01(d.SizeScale, 1), d.Rationale
	}
	if !b.barModel && needsConfirm(b.snapshot().Limits.Confirm, opening) {
		if _, inter, _ := s.deps(); inter != nil {
			choice, by := s.confirm(ctx, b, inter, sg, note)
			switch choice {
			case "execute":
			case "half":
				scale *= 0.5
			default:
				s.finish(b, sg, OutSkippedUs, "人工跳过（"+by+"）")
				return
			}
		}
	}
	if expired() {
		s.finish(b, sg, OutExpired, "等待审核/确认期间超过有效期")
		return
	}
	out, reason := s.execute(ctx, b, sg, scale)
	s.finish(b, sg, out, reason)
}

// review asks the AI layer; a non-empty outcome ends the pipeline.
func (s *Service) review(ctx context.Context, b *book, inst Instance, sg *Signal) (Decision, SignalOutcome, string) {
	_, _, rev := s.deps()
	if rev == nil || !rev.Available() {
		return Decision{}, OutSkippedAI, "AI unavailable"
	}
	rctx, cancel := context.WithDeadline(ctx, time.UnixMilli(sg.Ts+sg.TTLms))
	defer cancel()
	d, err := rev.Review(rctx, s.reviewRequest(b, inst, *sg))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || rctx.Err() != nil {
			return d, OutExpired, "AI 审核超时"
		}
		return d, OutSkippedAI, "AI 错误：" + err.Error()
	}
	sg.AIDecisionID = d.ID
	if d.Action != "execute" {
		r := d.Rationale
		if r == "" {
			r = "AI 判断跳过"
		}
		return d, OutSkippedAI, r
	}
	if d.StopLoss > 0 {
		sg.StopLoss = d.StopLoss
	}
	if d.TakeProfit > 0 {
		sg.TakeProfit = d.TakeProfit
	}
	return d, "", ""
}

func (s *Service) reviewRequest(b *book, inst Instance, sg Signal) ReviewRequest {
	b.mu.Lock()
	acc, pos := b.view(), b.positionsSorted(0)
	b.mu.Unlock()
	ver, _ := s.version(inst.VersionID)
	ver.Code = ""
	market := map[string]any{}
	if v, ok := b.market().Factors(sg.InstID); ok {
		market["factors"] = v
	}
	if ver.Timeframe != "" {
		if bars, err := b.market().Bars(sg.InstID, ver.Timeframe, 24); err == nil && len(bars) > 0 {
			closes := make([]float64, len(bars))
			for i, x := range bars {
				closes[i] = x.C
			}
			market["closes"] = closes
			market["timeframe"] = ver.Timeframe
		}
	}
	var recent []DecisionSummary
	rows, err := s.db.Query(`SELECT ts, inst_id, action, outcome, outcome_reason FROM trade_signals WHERE instance_id=? AND ai_decision_id>0 ORDER BY id DESC LIMIT 10`, inst.ID)
	if err == nil {
		for rows.Next() {
			var d DecisionSummary
			var out, reason string
			if rows.Scan(&d.Ts, &d.InstID, &d.Action, &out, &reason) == nil {
				d.Decision, d.Result = out, reason
				recent = append(recent, d)
			}
		}
		rows.Close()
	}
	return ReviewRequest{Signal: sg, Account: acc, Instance: inst, Strategy: ver, Positions: pos, Market: market, Recent: recent}
}

// confirm asks the owner; on timeout or error the account's ConfirmDefault applies.
func (s *Service) confirm(ctx context.Context, b *book, inter Interactor, sg Signal, aiNote string) (choice, by string) {
	acc := b.snapshot()
	l := acc.Limits
	timeout := l.ConfirmTimeoutS
	if timeout <= 0 {
		timeout = 120
	}
	deadline := time.UnixMilli(min(b.market().Now()+int64(timeout)*1000, sg.Ts+sg.TTLms))
	def := "skip"
	if l.ConfirmDefault == "execute" {
		def = "execute"
	}
	ev := signalEvent(sg, acc, "待确认 · "+actionName(sg.Action)+" "+shortInst(sg.InstID))
	if est := s.previewSize(b, sg); est != "" {
		ev.Lines = append(ev.Lines, est)
	}
	if aiNote != "" {
		ev.Lines = append(ev.Lines, "AI："+aiNote)
	}
	ev.Lines = append(ev.Lines, fmt.Sprintf("%s 前未确认则%s", deadline.In(cst).Format("15:04:05"), map[string]string{"skip": "跳过", "execute": "执行"}[def]))
	p := Prompt{ID: fmt.Sprintf("sig-%d", sg.ID), Event: ev, Deadline: deadline, Default: def,
		Options: []PromptOption{{Key: "execute", Label: "执行"}, {Key: "half", Label: "半仓"}, {Key: "skip", Label: "跳过"}}}
	cctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	choice, by, err := inter.Ask(cctx, p)
	if err != nil || choice == "" {
		return def, "timeout"
	}
	return choice, by
}

// execute applies risk and sizing and places the order group (caller does not hold b.mu).
func (s *Service) execute(ctx context.Context, b *book, sg Signal, scale float64) (SignalOutcome, string) {
	spec, ok := b.market().Spec(sg.InstID)
	if !ok {
		return OutRejected, "合约不存在或已下线"
	}
	q, err := s.execQuote(ctx, b, sg.InstID)
	if err != nil {
		return OutRejected, "无行情：" + err.Error()
	}
	ref := refPrice(q)
	if b.barModel {
		ref = q.Last
	}
	if ref <= 0 {
		return OutRejected, "无有效价格"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.market().Now()
	k := posKey{sg.InstanceID, sg.InstID}
	bp := sg.MaxSlippageBp
	switch sg.Action {
	case OpenLong, OpenShort:
		if err := b.checkOpen(sg, ref, now); err != nil {
			return OutRejected, err.Error()
		}
		long := sg.Action == OpenLong
		existing := b.pos[k]
		lev := sg.Leverage
		if existing != nil {
			lev = existing.Leverage
		}
		out, err := size(sizeIn{Equity: b.equity(), Cash: b.cash(), InstMargin: b.instMargin(sg.InstID), Entry: ref, Stop: sg.StopLoss, Long: long,
			Strength: sg.Strength, Scale: scale, Lever: lev, Spec: spec, Limits: b.acc.Limits, MMR: s.opts.MaintMarginRate})
		if err != nil {
			return OutRejected, err.Error()
		}
		if existing != nil && out.Lever < existing.Leverage-0.005 {
			return OutRejected, fmt.Sprintf("加仓止损过远：需要低于现有 %.2fx 的杠杆", existing.Leverage)
		}
		posSide := "short"
		if long {
			posSide = "long"
		}
		g := fmt.Sprintf("g%d", sg.ID)
		b.groups[g] = groupSpec{stop: sg.StopLoss, target: sg.TakeProfit, signal: sg}
		o := &Order{InstanceID: sg.InstanceID, SignalID: sg.ID, InstID: sg.InstID, Side: sideOf(posSide, true), PosSide: posSide, Type: OrdMarket,
			Qty: out.Qty, Price: ref * (1 + sgn(long)*bp/1e4), Leverage: out.Lever, OCOGroup: g, State: OsLive, CreatedAt: now}
		if err := b.addOrder(s.db, o); err != nil {
			return OutRejected, "下单失败：" + err.Error()
		}
		s.changed(b.acc.ID, "order")
		detail := fmt.Sprintf("%s 张 · %.2fx · 保证金约 %s USDT", fmtQty(out.Qty), out.Lever, fmtUSDT(out.Margin))
		if !b.barModel {
			b.execLive(o, q, true, "fill", now)
			if o.Filled <= 0 {
				detail += "；未成交：" + o.Reason
			}
		}
		return OutOrdered, detail
	default:
		p := b.pos[k]
		if p == nil {
			return OutRejected, "无持仓"
		}
		if len(b.ordersFor(k, func(o *Order) bool { return o.ReduceOnly && o.Type == OrdMarket })) > 0 {
			return OutRejected, "已有待成交的平仓单"
		}
		qty := p.Qty
		if sg.Action == Reduce {
			lot := spec.LotSz
			if lot <= 0 {
				lot = spec.MinSz
			}
			qty = math.Min(p.Qty, floorStep(p.Qty*sg.ReduceFrac, lot))
			if qty <= 0 || qty < spec.MinSz-qtyEps {
				return OutRejected, "减仓数量低于最小下单量"
			}
		}
		buy := p.PosSide == "short"
		o := &Order{InstanceID: sg.InstanceID, SignalID: sg.ID, InstID: sg.InstID, Side: sideOf(p.PosSide, false), PosSide: p.PosSide, Type: OrdMarket,
			Qty: qty, Price: ref * (1 + sgn(buy)*bp/1e4), ReduceOnly: true, Leverage: p.Leverage, State: OsLive, CreatedAt: now}
		if err := b.addOrder(s.db, o); err != nil {
			return OutRejected, "下单失败：" + err.Error()
		}
		s.changed(b.acc.ID, "order")
		detail := fmt.Sprintf("%s 张", fmtQty(qty))
		if !b.barModel {
			b.execLive(o, q, true, "fill", now)
			if o.Filled <= 0 {
				detail += "；未成交：" + o.Reason
			}
		}
		return OutOrdered, detail
	}
}

// execQuote fetches the quote used for execution (with depth when the live source offers it).
func (s *Service) execQuote(ctx context.Context, b *book, inst string) (Quote, error) {
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if dq, ok := b.market().(DepthQuoter); ok && !b.barModel {
		if q, err := dq.QuoteDepth(qctx, inst); err == nil {
			return q, nil
		}
	}
	return b.market().Quote(qctx, inst)
}

// ClosePositions market-closes one instrument's positions (or all) of a live account.
func (s *Service) ClosePositions(ctx context.Context, accountID int64, instID string) (int, error) {
	b, err := s.book(accountID)
	if err != nil {
		return 0, err
	}
	if b.acc.Mode != ModeLive {
		return 0, errBad("只有实时模拟账户支持手动平仓")
	}
	b.mu.Lock()
	insts := map[string]bool{}
	for k := range b.pos {
		if instID == "" || k.id == instID {
			insts[k.id] = true
		}
	}
	b.mu.Unlock()
	if len(insts) == 0 {
		return 0, errBad("没有可平的持仓")
	}
	n := 0
	for inst := range insts {
		q, err := s.execQuote(ctx, b, inst)
		if err != nil {
			continue
		}
		b.mu.Lock()
		now := b.market().Now()
		for _, k := range b.instPositions(inst) {
			p := b.pos[k]
			buy := p.PosSide == "short"
			ref := refPrice(q)
			o := &Order{InstanceID: k.inst, InstID: inst, Side: sideOf(p.PosSide, false), PosSide: p.PosSide, Type: OrdMarket, Qty: p.Qty,
				Price: ref * (1 + sgn(buy)*b.acc.Limits.SlippageBp/1e4), ReduceOnly: true, Leverage: p.Leverage, State: OsLive, Reason: "手动平仓", CreatedAt: now}
			if b.addOrder(s.db, o) != nil {
				continue
			}
			b.execLive(o, q, true, "fill", now)
			if o.Filled > 0 {
				n++
			}
		}
		b.mu.Unlock()
	}
	s.changed(accountID, "order")
	return n, nil
}

// previewSize sizes an opening signal at its reference price without placing anything, so a
// confirmation shows what "执行" would do.
func (s *Service) previewSize(b *book, sg Signal) string {
	if (sg.Action != OpenLong && sg.Action != OpenShort) || sg.Price <= 0 {
		return ""
	}
	spec, ok := b.market().Spec(sg.InstID)
	if !ok {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	lev := sg.Leverage
	if p := b.pos[posKey{sg.InstanceID, sg.InstID}]; p != nil {
		lev = p.Leverage
	}
	out, err := size(sizeIn{Equity: b.equity(), Cash: b.cash(), InstMargin: b.instMargin(sg.InstID), Entry: sg.Price, Stop: sg.StopLoss,
		Long: sg.Action == OpenLong, Strength: sg.Strength, Scale: 1, Lever: lev, Spec: spec, Limits: b.acc.Limits, MMR: s.opts.MaintMarginRate})
	if err != nil {
		return "预估：" + err.Error()
	}
	return fmt.Sprintf("预估 %s 张 ≈ %s USDT · %.2fx · 保证金约 %s USDT", fmtQty(out.Qty), fmtUSDT(out.Qty*spec.CtVal*sg.Price), out.Lever, fmtUSDT(out.Margin))
}

func roundTick(px, tick float64) float64 {
	if tick <= 0 || px <= 0 {
		return px
	}
	return math.Round(math.Round(px/tick)*tick*1e10) / 1e10
}
