package realtrade

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/okxorder"
)

// ---------- following resting orders and AI positions (every followEvery) ----------

// follow settles resting limit orders (filled, partly filled, expired → cancelled) and detects AI
// positions that closed: a dry run when the last price crosses the position's SL or TP (P&L estimated
// before fees), live when the position is gone from the account (P&L from OKX's positions history).
// It holds execMu: bookkeeping never interleaves with an execution.
func (s *Service) follow(ctx context.Context) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.trackOrders(ctx)
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM trade_proposals WHERE kind='open' AND status='executed' AND closed_at=0`)
	if err != nil {
		s.logf("realtrade: follow: %v", err)
		return
	}
	var ps []Proposal
	for rows.Next() {
		if p, err := scan(rows); err == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	for _, p := range ps {
		if p.CurContracts <= 0 {
			continue // a limit open that has not filled yet
		}
		if p.Mode == string(okxorder.DryRun) {
			s.followDry(ctx, p)
		} else {
			s.followLive(ctx, p)
		}
	}
}

// trackOrders follows every resting limit order: a dry run fills it when the last price reaches the
// limit; live reads its state from OKX. Past its expiry the rest is cancelled.
func (s *Service) trackOrders(ctx context.Context) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM trade_proposals WHERE status='executed' AND ord_state IN ('live','partially_filled')`)
	if err != nil {
		return
	}
	var ps []Proposal
	for rows.Next() {
		if p, err := scan(rows); err == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	now := s.now()
	for _, p := range ps {
		due := now.UnixMilli() >= p.OrderUntil
		if p.Mode == string(okxorder.DryRun) {
			px, _, ok := s.opts.Quote(p.InstID)
			buy := (p.Side == "long") == (p.Kind == KindOpen || p.Kind == KindAdd)
			switch {
			case ok && px > 0 && ((buy && px <= p.Px) || (!buy && px >= p.Px)):
				s.orderDone(ctx, p, OrdFilled, p.Contracts, p.Px)
			case due:
				s.audit(p.ID, "cancel", "限价单到期未成交，已撤单（模拟）", "system")
				s.orderDone(ctx, p, OrdCanceled, 0, 0)
			}
			continue
		}
		st, filled, avg, found, err := s.orderStatus(ctx, p)
		switch {
		case err != nil:
			s.logf("realtrade: order #%d: %v", p.ID, err)
		case found && st == OrdFilled:
			s.orderDone(ctx, p, OrdFilled, filled, avg)
		case found && strings.HasSuffix(st, "canceled"):
			s.orderDone(ctx, p, OrdCanceled, filled, avg)
		case found:
			if filled > p.Filled {
				s.db.Exec(`UPDATE trade_proposals SET ord_state=?, filled=?, fill_px=?, updated=? WHERE id=?`, OrdPartial, filled, avg, now.UnixMilli(), p.ID)
				s.audit(p.ID, "partial", fmt.Sprintf("部分成交 %s/%s 张 @%s", fmtNum(filled), fmtNum(p.Contracts), okxorder.FormatPx(avg)), "okx")
				s.emit("partial", p.ID)
			}
			if due {
				s.cancelResting(ctx, p, "限价单到期，撤销未成交部分")
			}
		case due && now.Sub(time.UnixMilli(p.OrderUntil)) > lostOrder:
			s.audit(p.ID, "warning", "OKX 上查不到这张限价单（未挂单也无历史），按已撤单处理，请在 OKX 核对", "system")
			s.orderDone(ctx, p, OrdCanceled, p.Filled, p.FillPx)
		}
	}
}

// orderStatus reads ctg's order (by client id) from OKX: pending orders first, then the order history.
func (s *Service) orderStatus(ctx context.Context, p Proposal) (state string, filled, avg float64, found bool, err error) {
	a := s.account()
	if a == nil {
		return "", 0, 0, false, fmt.Errorf("OKX account not configured")
	}
	cid := "ctgp" + strconv.FormatInt(p.ID, 10)
	for _, path := range []string{"/api/v5/trade/orders-pending", "/api/v5/trade/orders-history"} {
		v, err := a.Read(ctx, path, url.Values{"instType": {"SWAP"}, "instId": {p.InstID}, "limit": {"100"}})
		if err != nil {
			return "", 0, 0, false, err
		}
		rows, _ := v.([]any)
		for _, r := range rows {
			m, _ := r.(map[string]any)
			str := func(k string) string { x, _ := m[k].(string); return x }
			if str("clOrdId") != cid {
				continue
			}
			f, _ := strconv.ParseFloat(str("accFillSz"), 64)
			px, _ := strconv.ParseFloat(str("avgPx"), 64)
			return str("state"), f, px, true, nil
		}
	}
	return "", 0, 0, false, nil
}

// cancelResting cancels one of ctg's resting orders by its client id. Live, OKX's answer is settled by
// trackOrders; a dry-run order is settled at once.
func (s *Service) cancelResting(ctx context.Context, p Proposal, why string) {
	if p.Mode == string(okxorder.DryRun) {
		s.audit(p.ID, "cancel", why+"（模拟）", "system")
		s.orderDone(ctx, p, OrdCanceled, p.Filled, p.FillPx)
		return
	}
	o, err := okxorder.CancelOrder(p.InstID, "ctgp"+strconv.FormatInt(p.ID, 10))
	if err != nil {
		s.logf("realtrade: cancel #%d: %v", p.ID, err)
		return
	}
	if _, err := s.orders.Place(ctx, o); err != nil {
		s.audit(p.ID, "warning", "撤单失败（下一轮再查）："+err.Error(), "okx")
		return
	}
	s.audit(p.ID, "cancel", why, "system")
}

// orderDone settles a resting order (first caller wins): the filled part is booked onto its position,
// an open that never filled frees its slot, a full close by limit cancels ctg's TP/SL, and the chat is told.
func (s *Service) orderDone(ctx context.Context, p Proposal, state string, filled, avg float64) {
	now := s.now().UnixMilli()
	res, err := s.db.Exec(`UPDATE trade_proposals SET ord_state=?, filled=?, fill_px=?, filled_at=?, updated=? WHERE id=? AND ord_state IN ('live','partially_filled')`,
		state, filled, avg, now, now, p.ID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	p, _ = s.get(p.ID)
	if state == OrdFilled {
		s.audit(p.ID, OrdFilled, fmt.Sprintf("限价单已成交 %s 张 @%s", fmtNum(filled), okxorder.FormatPx(avg)), "okx")
	} else {
		s.audit(p.ID, OrdCanceled, fmt.Sprintf("限价单已撤单，成交 %s/%s 张", fmtNum(filled), fmtNum(p.Contracts)), "system")
	}
	if filled > 0 {
		s.applyFill(ctx, p, filled, avg)
	} else if p.Kind == KindOpen {
		s.db.Exec(`UPDATE trade_proposals SET closed_at=?, close_kind='unfilled', updated=? WHERE id=? AND closed_at=0`, now, now, p.ID)
	}
	if p.Kind == KindClose && state == OrdFilled && p.algoIDs != "" && p.Mode == string(okxorder.Live) {
		if o, err := okxorder.CancelAlgos(p.InstID, strings.Split(p.algoIDs, ",")); err == nil {
			if _, err := s.orders.Place(ctx, o); err != nil {
				s.audit(p.ID, "warning", "平仓后撤销止盈止损失败（OKX 可能已自动撤销）："+err.Error(), "okx")
			}
		}
	}
	s.emit(state, p.ID)
	s.queueSteer(p.ID, state)
}

// applyFill books a fill: the proposal's own fill (market orders) and what it does to its AI position —
// an open starts it, an add grows it at the new average with the new TP/SL, a reduce shrinks it (a dry
// run books the estimated P&L), a close ends it (a dry run at once; live once OKX shows it gone), an
// adjust moves its TP/SL. The owner's own positions (posId 0) are not booked.
func (s *Service) applyFill(ctx context.Context, p Proposal, filled, px float64) {
	now := s.now().UnixMilli()
	if p.OrdState == "" {
		s.db.Exec(`UPDATE trade_proposals SET ord_state=?, filled=?, fill_px=?, filled_at=? WHERE id=?`, OrdFilled, filled, px, now, p.ID)
	}
	tpsl := "ctgt" + strconv.FormatInt(p.ID, 10) // the whole-position TP/SL this proposal placed …
	if p.Kind == KindOpen {
		s.db.Exec(`UPDATE trade_proposals SET pos_id=id, cur_sz=?, cur_avg=?, cur_sl=sl, cur_tp=tp, cur_algo=? WHERE id=?`,
			filled, px, "ctga"+strconv.FormatInt(p.ID, 10), p.ID)
		return
	}
	if p.PosID <= 0 {
		return
	}
	r, err := s.get(p.PosID)
	if err != nil || r.ClosedAt > 0 {
		return
	}
	if strings.HasPrefix(r.curAlgo, "ctgt") {
		tpsl = r.curAlgo // … or the one it amended
	}
	spec, _ := s.opts.Spec(p.InstID)
	dir := 1.0
	if r.Side == "short" {
		dir = -1
	}
	switch p.Kind {
	case KindAdd:
		sz := r.CurContracts + filled
		avg := (r.CurContracts*r.CurAvgPx + filled*px) / sz
		s.db.Exec(`UPDATE trade_proposals SET cur_sz=?, cur_avg=?, cur_sl=?, cur_tp=?, cur_algo=?, updated=? WHERE id=?`, sz, avg, p.SL, p.TP, tpsl, now, r.ID)
	case KindAdjust:
		s.db.Exec(`UPDATE trade_proposals SET cur_sl=?, cur_tp=?, cur_algo=?, updated=? WHERE id=?`, p.SL, p.TP, tpsl, now, r.ID)
	case KindReduce: // ctg's TP/SL now covers the whole position
		part := (px - r.CurAvgPx) * dir * filled * spec.CtVal
		s.db.Exec(`UPDATE trade_proposals SET cur_sz=?, realized=realized+?, cur_algo=?, updated=? WHERE id=?`, math.Max(r.CurContracts-filled, 0), part, tpsl, now, r.ID)
	case KindClose:
		if r.Mode == string(okxorder.DryRun) {
			s.closed(ctx, r, "manual", px, r.realized+(px-r.CurAvgPx)*dir*r.CurContracts*spec.CtVal, "估算，未含手续费")
		}
	}
}

func (s *Service) followDry(ctx context.Context, p Proposal) {
	px, _, ok := s.opts.Quote(p.InstID)
	if !ok || px <= 0 {
		return
	}
	long := p.Side == "long"
	kind, at := "", 0.0
	switch {
	case p.CurSL > 0 && ((long && px <= p.CurSL) || (!long && px >= p.CurSL)):
		kind, at = "sl", p.CurSL
	case p.CurTP > 0 && ((long && px >= p.CurTP) || (!long && px <= p.CurTP)):
		kind, at = "tp", p.CurTP
	default:
		return
	}
	spec, _ := s.opts.Spec(p.InstID)
	dir := 1.0
	if !long {
		dir = -1
	}
	s.closed(ctx, p, kind, at, p.realized+(at-p.CurAvgPx)*dir*p.CurContracts*spec.CtVal, "估算，未含手续费")
}

func (s *Service) followLive(ctx context.Context, p Proposal) {
	a := s.account()
	since := max(p.StatusAt, p.FilledAt)
	if a == nil || s.now().Sub(time.UnixMilli(since)) < closeSettle {
		return
	}
	snap, err := a.Snapshot(ctx, p.InstID)
	if err != nil {
		return
	}
	for _, x := range snap.Positions {
		if x.Side == p.Side {
			delete(s.missing, p.ID)
			return
		}
	}
	if _, ok := s.missing[p.ID]; !ok {
		s.missing[p.ID] = s.now()
	}
	v, err := a.Read(ctx, "/api/v5/account/positions-history", url.Values{"instType": {"SWAP"}, "instId": {p.InstID}, "limit": {"20"}})
	rows, _ := v.([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		str := func(k string) string { x, _ := m[k].(string); return x }
		ut, _ := strconv.ParseInt(str("uTime"), 10, 64)
		dir := str("direction")
		if ut < since || (dir != "" && dir != p.Side) {
			continue
		}
		pnl, _ := strconv.ParseFloat(str("realizedPnl"), 64)
		cpx, _ := strconv.ParseFloat(str("closeAvgPx"), 64)
		kind := "other"
		if t := str("type"); t != "3" && t != "4" && t != "5" && cpx > 0 && p.CurSL > 0 && p.CurTP > 0 {
			kind = "tp"
			if math.Abs(cpx-p.CurSL) < math.Abs(cpx-p.CurTP) {
				kind = "sl"
			}
		}
		delete(s.missing, p.ID)
		s.closed(ctx, p, kind, cpx, pnl, "OKX 已实现盈亏")
		return
	}
	if err == nil && s.now().Sub(s.missing[p.ID]) > closeWaitHist { // gone, but no history row: close without P&L
		delete(s.missing, p.ID)
		s.closed(ctx, p, "other", 0, 0, "仓位已不在，未查到平仓记录")
	}
}

// closed records an AI position's end (first caller wins), cancels ctg's orders still resting on it
// (an add must never open a new position after a stop-out), cancels the signals linked to it and
// tells its chat (a post-trade review).
func (s *Service) closed(ctx context.Context, p Proposal, kind string, px, pnl float64, note string) {
	res, err := s.db.Exec(`UPDATE trade_proposals SET closed_at=?, close_kind=?, close_px=?, pnl=?, cur_sz=0, updated=? WHERE id=? AND closed_at=0`,
		s.now().UnixMilli(), kind, px, pnl, s.now().UnixMilli(), p.ID)
	if err != nil {
		s.logf("realtrade: close %d: %v", p.ID, err)
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	s.audit(p.ID, "closed", fmt.Sprintf("%s %s %+.2f USDT（%s）", closeText(kind), okxorder.FormatPx(px), pnl, note), "system")
	for _, r := range s.restingIn(p.InstID) {
		if r.PosID == p.ID && r.ID != p.ID {
			s.cancelResting(ctx, r, fmt.Sprintf("仓位 #%d 已平，撤销这张挂单", p.ID))
		}
	}
	s.mu.Lock()
	f := s.onClosed
	s.mu.Unlock()
	if f != nil {
		if ids := f(p.ID, fmt.Sprintf("仓位 #%d 已平仓（%s），关联信号自动取消", p.ID, closeText(kind))); len(ids) > 0 {
			s.audit(p.ID, "signals", "已自动取消关联信号 "+idList(ids), "system")
		}
	}
	s.emit("closed", p.ID)
	s.queueSteer(p.ID, "closed")
}

func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "#" + strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, " ")
}
