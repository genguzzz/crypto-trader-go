package realtrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
)

// target is ctg's resting limit order in an instrument and side (policy lets at most one rest).
func (s *Service) target(inst, side string) (Proposal, error) {
	var best Proposal
	for _, r := range s.restingIn(inst) {
		if r.Side == side && r.ID > best.ID {
			best = r
		}
	}
	if best.ID == 0 {
		return Proposal{}, fmt.Errorf("no resting %s limit order of ctg's in %s (it may have filled, expired or never been placed)", side, inst)
	}
	return best, nil
}

// amendPlan is what an amend changes on its target, validated against the current price.
type amendPlan struct {
	px, sl, tp   float64
	orderMin     int
	pxChanged    bool
	exitsChanged bool
	desc         string
	built        built
}

// planAmend validates new price / TP/SL / rest time for a resting order t. A zero in px, sl, tp keeps
// the order's value; orderMin 0 keeps its expiry. Only an order that has not filled at all can change
// its price or exits; an open's exits ride on the order, other kinds' exits are set after the fill.
func (s *Service) planAmend(ctx context.Context, t Proposal, px, sl, tp float64, orderMin int, mark float64) (amendPlan, error) {
	var z amendPlan
	if px <= 0 && sl <= 0 && tp <= 0 && orderMin <= 0 {
		return z, errors.New("amend needs at least one of price, sl, tp, orderExpiryMin")
	}
	if orderMin > 0 && (orderMin < minOrderMin || orderMin > s.opts.Limits.LimitMaxMin) {
		return z, fmt.Errorf("orderExpiryMin must be %d–%d minutes", minOrderMin, s.opts.Limits.LimitMaxMin)
	}
	z.px, z.sl, z.tp, z.orderMin = px, sl, tp, orderMin
	if z.px <= 0 {
		z.px = t.Px
	}
	if z.sl <= 0 {
		z.sl = t.SL
	}
	if z.tp <= 0 {
		z.tp = t.TP
	}
	spec, ok := s.opts.Spec(t.InstID)
	if !ok {
		return z, errors.New("instrument metadata unknown")
	}
	if tick := spec.TickSz; tick > 0 {
		z.px = float64(int64(z.px/tick+0.5)) * tick
	}
	z.pxChanged = z.px != t.Px
	z.exitsChanged = z.sl != t.SL || z.tp != t.TP
	if (z.pxChanged || z.exitsChanged) && t.OrdState != OrdLive {
		return z, errors.New("the order is partly filled; only its rest time can change — cancel the rest instead")
	}
	if z.exitsChanged && t.Kind != KindOpen {
		return z, errors.New("sl / tp of an add are set after it fills, those of a reduce / close do not exist; amend the price, or cancel and propose again")
	}
	if !z.pxChanged && !z.exitsChanged && orderMin <= 0 {
		return z, errors.New("nothing differs from the resting order")
	}
	if z.pxChanged {
		if err := okxorder.Resting(t.Side, t.Kind == KindReduce || t.Kind == KindClose, z.px, mark); err != nil {
			return z, err
		}
	}
	if t.Kind == KindOpen && (z.pxChanged || z.exitsChanged) {
		a := actionOf(t)
		a.Px, a.SL, a.TP = z.px, z.sl, z.tp
		b, err := s.build(ctx, a, 0, mark, okxorder.LongShortMode)
		if err != nil {
			return z, err
		}
		z.built = b
	}
	var parts []string
	if z.pxChanged {
		parts = append(parts, fmt.Sprintf("价格 %s → %s", okxorder.FormatPx(t.Px), okxorder.FormatPx(z.px)))
	}
	if z.exitsChanged {
		parts = append(parts, fmt.Sprintf("止损 %s → %s、止盈 %s → %s", okxorder.FormatPx(t.SL), okxorder.FormatPx(z.sl), okxorder.FormatPx(t.TP), okxorder.FormatPx(z.tp)))
	}
	if orderMin > 0 {
		parts = append(parts, fmt.Sprintf("挂单有效期改为 %d 分钟（从执行起算）", orderMin))
	}
	z.desc = strings.Join(parts, "；")
	return z, nil
}

// proposeOrderOp stores a cancel or amend of ctg's resting limit order in an instrument and side.
func (s *Service) proposeOrderOp(ctx context.Context, in ProposeInput) (Proposal, error) {
	t, err := s.target(in.InstID, in.Side)
	if err != nil {
		return Proposal{}, err
	}
	mark, err := s.freshQuote(in.InstID)
	if err != nil {
		return Proposal{}, err
	}
	in.OrdType = okxorder.Limit
	b := built{margin: t.MarginUSDT, lever: t.Leverage, mgnMode: t.MarginMode, sl: t.SL, tp: t.TP, px: t.Px, posID: t.PosID}
	z := okxorder.Sizing{Contracts: t.Contracts, NotionalUSDT: t.NotionalUSDT}
	if in.Kind == KindCancel {
		in.OrderMin = 0
		z.TPSL = fmt.Sprintf("撤销 #%d 的%s %s 张限价单 @%s", t.ID, kindText(t.Kind), fmtNum(t.Contracts), okxorder.FormatPx(t.Px))
	} else {
		pl, err := s.planAmend(ctx, t, in.Px, in.SL, in.TP, in.OrderMin, mark)
		if err != nil {
			return Proposal{}, err
		}
		b.px, b.sl, b.tp, in.OrderMin = pl.px, pl.sl, pl.tp, pl.orderMin
		z.TPSL = fmt.Sprintf("修改 #%d 的挂单：%s", t.ID, pl.desc)
	}
	if err := s.policy(ctx, action{Kind: in.Kind, InstID: in.InstID, Side: in.Side}, okxacct.Position{}, 0, 0, true); err != nil {
		return Proposal{}, err
	}
	return s.store(in, mark, b, z, t.ID)
}

// executeOrderOp re-validates and sends an approved cancel / amend. The resting order must still rest;
// a cancel's outcome (including a partial fill) is settled by trackOrders live, at once in a dry run.
func (s *Service) executeOrderOp(ctx context.Context, p Proposal, invalid func(string) (Proposal, error)) (Proposal, error) {
	t, err := s.get(p.TargetID)
	if err != nil || t.Status != StatusExecuted || t.InstID != p.InstID || (t.OrdState != OrdLive && t.OrdState != OrdPartial) {
		return invalid(fmt.Sprintf("the order #%d no longer rests (filled, cancelled or expired)", p.TargetID))
	}
	spec, ok := s.opts.Spec(p.InstID)
	if !ok {
		return invalid("instrument metadata unknown")
	}
	mark, err := s.freshQuote(p.InstID)
	if err != nil {
		return invalid(err.Error())
	}
	cid := "ctgp" + strconv.FormatInt(t.ID, 10)
	var o okxorder.Order
	var pl amendPlan
	empty := false
	if p.Kind == KindCancel {
		o, err = okxorder.CancelOrder(p.InstID, cid)
	} else {
		pl, err = s.planAmend(ctx, t, p.Px, p.SL, p.TP, p.OrderMin, mark)
		if err == nil {
			if !pl.pxChanged && !pl.exitsChanged {
				empty = true
			} else {
				algo, sl, tp, px := "", 0.0, 0.0, 0.0
				if pl.exitsChanged {
					algo, sl, tp = "ctga"+strconv.FormatInt(t.ID, 10), pl.sl, pl.tp
				}
				if pl.pxChanged {
					px = pl.px
				}
				o, err = okxorder.AmendOrder(p.InstID, cid, algo, px, sl, tp, spec)
			}
		}
	}
	if err != nil {
		return invalid(err.Error())
	}
	ectx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	set := map[string]any{"exec_px": mark, "tpsl": p.TPSL, "mode": string(s.Mode())}
	var res okxorder.Result
	if !empty {
		var perr error
		res, perr = s.orders.Place(ectx, o)
		reqJSON, _ := json.Marshal(res.Requests)
		respJSON := []byte{}
		if len(res.Responses) > 0 {
			respJSON, _ = json.Marshal(res.Responses)
		}
		set["request"], set["response"], set["mode"] = string(reqJSON), string(respJSON), string(res.Mode)
		if perr != nil {
			set["error"] = perr.Error()
			if ok, _ := s.transition(p.ID, []string{StatusExecuting}, StatusFailed, set, ""); ok {
				s.audit(p.ID, StatusFailed, perr.Error(), "okx")
				s.emit(StatusFailed, p.ID)
				s.queueSteer(p.ID, StatusFailed)
			}
			return s.get(p.ID)
		}
	}
	set["ord_id"] = res.OrdID
	if ok, _ := s.transition(p.ID, []string{StatusExecuting}, StatusExecuted, set, ""); !ok {
		return s.get(p.ID)
	}
	now := s.now()
	if p.Kind == KindAmend {
		upd := map[string]any{"px": pl.px, "sl": pl.sl, "tp": pl.tp, "updated": now.UnixMilli()}
		if pl.orderMin > 0 {
			upd["order_min"], upd["order_until"] = pl.orderMin, now.Add(time.Duration(pl.orderMin)*time.Minute).UnixMilli()
		}
		var sets []string
		var args []any
		for k, v := range upd {
			sets = append(sets, k+"=?")
			args = append(args, v)
		}
		s.db.Exec(`UPDATE trade_proposals SET `+strings.Join(sets, ",")+` WHERE id=?`, append(args, t.ID)...)
		s.audit(t.ID, "amend", fmt.Sprintf("提议 #%d 修改了这张挂单：%s", p.ID, pl.desc), "okx")
	}
	s.audit(p.ID, StatusExecuted, fmt.Sprintf("%s %s", modeText(string(s.Mode())), p.TPSL), "okx")
	for _, w := range res.Warnings {
		s.audit(p.ID, "warning", w, "okx")
	}
	if p.Kind == KindCancel && s.Mode() == okxorder.DryRun {
		s.orderDone(ctx, t, OrdCanceled, t.Filled, t.FillPx)
	}
	s.emit(StatusExecuted, p.ID)
	s.queueSteer(p.ID, StatusExecuted)
	return s.get(p.ID)
}
