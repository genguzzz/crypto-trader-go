package trade

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// DepthQuoter is optionally implemented by live MarketData: a quote that includes order-book depth
// (Quote itself may be a cheap ticker view used for polling).
type DepthQuoter interface {
	QuoteDepth(ctx context.Context, instID string) (Quote, error)
}

// barSpreadBp is the assumed full spread in the bar fill model (no historical book data).
const barSpreadBp = 2.0

const qtyEps = 1e-9

func sideOf(posSide string, opening bool) string {
	if (posSide == "long") == opening {
		return "buy"
	}
	return "sell"
}

// ---------- order bookkeeping (caller holds b.mu) ----------

func (b *book) addOrder(x execer, o *Order) error {
	o.AccountID = b.acc.ID
	if o.UpdatedAt == 0 {
		o.UpdatedAt = o.CreatedAt
	}
	if err := insertOrder(x, o); err != nil {
		return err
	}
	if o.State == OsTrigger || o.State == OsLive || o.State == OsPartial {
		b.orders[o.ID] = o
	}
	return nil
}

func (b *book) cancel(x execer, o *Order, reason string, now int64) error {
	c := *o
	c.State, c.Reason, c.UpdatedAt = OsCancelled, reason, now
	if err := updateOrder(x, c); err != nil {
		return err
	}
	*o = c
	delete(b.orders, o.ID)
	return nil
}

// ordersFor returns open orders of one position (instance + instrument), oldest first.
func (b *book) ordersFor(k posKey, pred func(*Order) bool) []*Order {
	var out []*Order
	for _, o := range b.orders {
		if o.InstanceID == k.inst && o.InstID == k.id && (pred == nil || pred(o)) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func isProtect(o *Order) bool { return o.ReduceOnly && (o.Type == OrdStop || o.Type == OrdTarget) }

// ---------- fills ----------

// fillReq describes one execution of an order.
type fillReq struct {
	qty, px float64
	maker   bool
	liq     bool
	slipBp  float64
	ts      int64
}

// fill applies one execution: fill row, ledger entries, order and position update commit in one
// transaction; memory follows the commit. Follow-ups (OCO, protection orders, notifications)
// are done by afterFill.
func (b *book) fill(o *Order, r fillReq) (Fill, bool, error) {
	k := posKey{o.InstanceID, o.InstID}
	old := b.pos[k]
	spec, _ := b.market().Spec(o.InstID)
	ct := spec.CtVal
	if old != nil {
		ct = old.CtVal
	}
	if ct <= 0 {
		return Fill{}, false, fmt.Errorf("缺少合约面值")
	}
	var np posState
	realized, closes := 0.0, false
	if !o.ReduceOnly {
		if old != nil {
			np = *old
		} else {
			np = posState{Position: Position{AccountID: b.acc.ID, InstanceID: o.InstanceID, InstID: o.InstID, PosSide: o.PosSide, CtVal: ct,
				Leverage: o.Leverage, OpenedAt: r.ts}, FundedTo: r.ts}
		}
		total := np.Qty + r.qty
		np.EntryPx = (np.EntryPx*np.Qty + r.px*r.qty) / total
		np.Margin += r.qty * ct * r.px / np.Leverage
		np.Qty = total
	} else {
		if old == nil || old.Qty <= qtyEps {
			return Fill{}, false, fmt.Errorf("无持仓")
		}
		r.qty = math.Min(r.qty, old.Qty)
		np = *old
		released := old.Margin * r.qty / old.Qty
		if r.liq {
			realized = -released
		} else {
			realized = (r.px - old.EntryPx) * r.qty * ct * dir(old.PosSide)
		}
		np.Qty -= r.qty
		np.Margin -= released
		closes = np.Qty <= qtyEps+spec.LotSz*1e-6
	}
	if !closes {
		np.LiqPx = liqPrice(np.PosSide, np.EntryPx, np.Margin, np.Qty, ct, b.s.opts.MaintMarginRate)
	}
	feeBp := b.acc.Limits.TakerFeeBp
	if r.maker {
		feeBp = b.acc.Limits.MakerFeeBp
	}
	fee := r.qty * ct * r.px * feeBp / 1e4
	if r.liq {
		fee = 0
	}
	f := Fill{OrderID: o.ID, AccountID: b.acc.ID, InstanceID: o.InstanceID, InstID: o.InstID, Side: o.Side, PosSide: o.PosSide,
		Qty: r.qty, Px: r.px, Fee: fee, SlippageBp: round(r.slipBp, 3), RealizedPnl: realized, Liquidation: r.liq, Ts: r.ts}
	no := *o
	no.AvgPx = (o.AvgPx*o.Filled + r.px*r.qty) / (o.Filled + r.qty)
	no.Filled += r.qty
	no.UpdatedAt = r.ts
	if no.Filled >= no.Qty-qtyEps {
		no.State = OsFilled
	} else {
		no.State = OsPartial
	}
	err := b.s.tx(func(tx *sql.Tx) error {
		if err := insertFill(tx, &f, ct, o.ReduceOnly, closes); err != nil {
			return err
		}
		if fee > 0 {
			if _, err := insertLedger(tx, LedgerEntry{AccountID: b.acc.ID, Kind: "fee", Amount: -fee, InstID: o.InstID, RefID: f.ID, Ts: r.ts}); err != nil {
				return err
			}
		}
		if r.liq {
			if _, err := insertLedger(tx, LedgerEntry{AccountID: b.acc.ID, Kind: "liquidation", Amount: realized, InstID: o.InstID, RefID: f.ID, Ts: r.ts}); err != nil {
				return err
			}
		} else if realized != 0 {
			if _, err := insertLedger(tx, LedgerEntry{AccountID: b.acc.ID, Kind: "pnl", Amount: realized, InstID: o.InstID, RefID: f.ID, Ts: r.ts}); err != nil {
				return err
			}
		}
		if err := updateOrder(tx, no); err != nil {
			return err
		}
		if closes {
			return deletePosition(tx, b.acc.ID, o.InstanceID, o.InstID)
		}
		return savePosition(tx, &np)
	})
	if err != nil {
		return Fill{}, false, err
	}
	b.balance += realized - fee
	*o = no
	if o.State == OsFilled {
		delete(b.orders, o.ID)
	}
	if closes {
		delete(b.pos, k)
		b.lastClose[o.InstID] = r.ts
	} else {
		b.pos[k] = &np
	}
	if b.marks[o.InstID] <= 0 {
		b.marks[o.InstID] = r.px
	}
	return f, closes, nil
}

// afterFill keeps protection orders consistent with the position and reports the fill.
func (b *book) afterFill(o *Order, f Fill, closes bool, kind string) {
	k := posKey{o.InstanceID, o.InstID}
	now := f.Ts
	switch {
	case closes:
		for _, x := range b.ordersFor(k, func(x *Order) bool { return x.ReduceOnly }) {
			b.cancel(b.s.db, x, "持仓已平", now)
		}
		b.s.recordAIOutcome(o.InstanceID, o.InstID, f, kind)
		defer b.s.reopenAfter(b, o, f) // after this fill is reported, so the strategy sees close then open
	case o.ReduceOnly:
		p := b.pos[k]
		for _, x := range b.ordersFor(k, isProtect) {
			if x.Qty-x.Filled > p.Qty+qtyEps {
				x.Qty = x.Filled + p.Qty
				x.UpdatedAt = now
				updateOrder(b.s.db, *x)
			}
		}
	default:
		b.protect(o, now)
	}
	b.notifyFill(o, f, kind)
	f.Kind, f.Closed = kind, closes
	b.s.enqueue(o.InstanceID, "fill", func(ctx context.Context, s Session) error { return s.Fill(ctx, f) })
	b.s.changed(b.acc.ID, "fill")
}

// protect (re)creates the stop and target of a position after an entry fill: they cover the
// whole position and form one OCO group; earlier protection orders are cancelled.
func (b *book) protect(entry *Order, now int64) {
	k := posKey{entry.InstanceID, entry.InstID}
	p := b.pos[k]
	if p == nil {
		return
	}
	stop, target := p.StopLoss, p.TakeProfit
	np := *p
	if g, ok := b.groups[entry.OCOGroup]; ok {
		if g.stop > 0 {
			stop = g.stop
		}
		if g.target > 0 {
			target = g.target
		}
		if g.trail > 0 {
			np.Trail = g.trail
		}
		if g.limitMs > 0 && np.Expire == 0 {
			np.Expire = p.OpenedAt + g.limitMs
		}
	}
	if np.Trail > 0 && np.Best == 0 {
		np.Best = p.EntryPx
	}
	if entry.State == OsFilled || entry.State == OsCancelled {
		delete(b.groups, entry.OCOGroup)
	}
	for _, x := range b.ordersFor(k, isProtect) {
		b.cancel(b.s.db, x, "被新的止损/止盈替换", now)
	}
	side := sideOf(p.PosSide, false)
	np.StopLoss, np.TakeProfit, np.Group = 0, 0, entry.OCOGroup
	for _, x := range []struct {
		t  OrderType
		px float64
	}{{OrdStop, stop}, {OrdTarget, target}} {
		if x.px <= 0 {
			continue
		}
		o := &Order{InstanceID: entry.InstanceID, SignalID: entry.SignalID, InstID: entry.InstID, Side: side, PosSide: p.PosSide, Type: x.t,
			Qty: p.Qty, TriggerPx: x.px, ReduceOnly: true, OCOGroup: entry.OCOGroup, Leverage: p.Leverage, State: OsTrigger, CreatedAt: now}
		if err := b.addOrder(b.s.db, o); err != nil {
			continue
		}
		if x.t == OrdStop {
			np.StopLoss = x.px
		} else {
			np.TakeProfit = x.px
		}
	}
	if savePosition(b.s.db, &np) == nil {
		b.pos[k] = &np
	}
	b.trailStop(k, np.Best, np.Best, now) // a trailing distance tighter than the stop applies at once
}

// ---------- live execution ----------

// execLive executes a market order (IOC with its worst price when capped; triggered stops and
// targets are uncapped) against a quote, walking the book when depth is available.
func (b *book) execLive(o *Order, q Quote, capped bool, kind string, now int64) {
	spec, _ := b.market().Spec(o.InstID)
	buy := o.Side == "buy"
	ref := refPrice(q)
	want := o.Qty - o.Filled
	if o.ReduceOnly {
		if p := b.pos[posKey{o.InstanceID, o.InstID}]; p == nil {
			b.cancel(b.s.db, o, "无持仓", now)
			return
		} else {
			want = math.Min(want, p.Qty)
		}
	}
	worst := 0.0
	if capped {
		worst = o.Price
	}
	qty, notional := 0.0, 0.0
	levels := q.Asks
	if !buy {
		levels = q.Bids
	}
	if len(levels) > 0 {
		for _, l := range levels {
			if qty >= want-qtyEps {
				break
			}
			if worst > 0 && ((buy && l[0] > worst) || (!buy && l[0] < worst)) {
				break
			}
			take := math.Min(l[1], want-qty)
			qty += take
			notional += take * l[0]
		}
		if !capped && qty < want-qtyEps { // book exhausted: the rest at the last level
			last := levels[len(levels)-1][0]
			notional += (want - qty) * last
			qty = want
		}
	} else if q.Last > 0 {
		half := 0.0
		if q.Bid > 0 && q.Ask >= q.Bid {
			half = (q.Ask - q.Bid) / 2
		}
		px := snapTick(q.Last+(math.Max(half, spec.TickSz)+spec.TickSz)*sgn(buy), spec.TickSz, buy)
		if worst <= 0 || (buy && px <= worst) || (!buy && px >= worst) {
			qty, notional = want, want*px
		}
	}
	if qty <= qtyEps {
		reason := "无行情，无法成交"
		if capped {
			reason = fmt.Sprintf("无满足滑点上限（%s）的对手价", fmtPx(worst))
		}
		b.cancel(b.s.db, o, reason, now)
		if !o.ReduceOnly {
			delete(b.groups, o.OCOGroup)
		}
		return
	}
	px := notional / qty
	slip := 0.0
	if ref > 0 {
		slip = (px - ref) / ref * 1e4 * sgn(buy)
	}
	f, closes, err := b.fill(o, fillReq{qty: qty, px: px, slipBp: slip, ts: now})
	if err != nil {
		b.cancel(b.s.db, o, "成交失败："+err.Error(), now)
		return
	}
	if o.State == OsPartial { // IOC: the remainder is cancelled
		b.cancel(b.s.db, o, "IOC 剩余撤销（滑点上限内深度不足）", now)
	}
	b.afterFill(o, f, closes, kind)
}

func sgn(buy bool) float64 {
	if buy {
		return 1
	}
	return -1
}

// onQuote evaluates resting orders and liquidation of one instrument on a live price tick.
func (b *book) onQuote(inst string, q Quote, now int64) {
	mark := q.Mark
	if mark <= 0 {
		mark = q.Last
	}
	if mark <= 0 {
		return
	}
	b.marks[inst] = mark
	last := q.Last
	if last <= 0 {
		last = mark
	}
	for _, o := range b.instOrders(inst, func(o *Order) bool { return o.Type == OrdLimit }) {
		if (o.Side == "buy" && last < o.Price) || (o.Side == "sell" && last > o.Price) {
			b.execLimit(o, o.Price, now)
		}
	}
	for _, k := range b.instPositions(inst) {
		p := b.pos[k]
		if p == nil {
			continue
		}
		long := p.PosSide == "long"
		if o := b.triggered(k, last, last, last); o != nil {
			kind := "stop"
			if o.Type == OrdTarget {
				kind = "target"
			}
			b.execLive(o, q, false, kind, now)
			continue
		}
		if p.LiqPx > 0 && ((long && mark <= p.LiqPx) || (!long && mark >= p.LiqPx)) {
			b.liquidate(k, p.LiqPx, now)
			continue
		}
		b.trailStop(k, last, last, now)
	}
}

// trailStop moves a trailing stop behind the best price seen in [lo, hi] (caller holds b.mu).
// The stop only ever tightens; a position without a stop order gets one.
func (b *book) trailStop(k posKey, lo, hi float64, now int64) {
	p := b.pos[k]
	if p == nil || p.Trail <= 0 {
		return
	}
	long := p.PosSide == "long"
	np := *p
	if long {
		np.Best = math.Max(np.Best, hi)
	} else if np.Best <= 0 || lo < np.Best {
		np.Best = lo
	}
	spec, _ := b.market().Spec(k.id)
	want := roundTick(np.Best-dir(p.PosSide)*p.Trail, spec.TickSz)
	var stop *Order
	for _, o := range b.ordersFor(k, isProtect) {
		if o.Type == OrdStop {
			stop = o
			break
		}
	}
	moved := false
	switch {
	case want <= 0:
	case stop == nil:
		o := &Order{InstanceID: k.inst, InstID: k.id, Side: sideOf(p.PosSide, false), PosSide: p.PosSide, Type: OrdStop, Qty: p.Qty,
			TriggerPx: want, ReduceOnly: true, OCOGroup: p.Group, Leverage: p.Leverage, State: OsTrigger, Reason: "追踪止损", CreatedAt: now}
		moved = b.addOrder(b.s.db, o) == nil
	case (long && want > stop.TriggerPx) || (!long && want < stop.TriggerPx):
		stop.TriggerPx, stop.UpdatedAt, stop.Reason = want, now, "追踪止损"
		moved = updateOrder(b.s.db, *stop) == nil
	}
	if moved {
		np.StopLoss = want
	}
	if moved || np.Best != p.Best {
		if savePosition(b.s.db, &np) == nil {
			b.pos[k] = &np
		}
		if moved {
			b.s.changed(b.acc.ID, "order")
		}
	}
}

// expire market-closes positions whose time limit passed (caller holds b.mu). Live orders fill
// against the current quote; backtest orders at the next bar open.
func (b *book) expire(now int64) {
	for _, k := range b.sortedPosKeys() {
		p := b.pos[k]
		if p.Expire <= 0 || now < p.Expire {
			continue
		}
		if len(b.ordersFor(k, func(o *Order) bool { return o.ReduceOnly && o.Type == OrdMarket })) > 0 {
			continue
		}
		o := &Order{InstanceID: k.inst, InstID: k.id, Side: sideOf(p.PosSide, false), PosSide: p.PosSide, Type: OrdMarket, Qty: p.Qty,
			ReduceOnly: true, Leverage: p.Leverage, State: OsLive, Reason: reasonExpired, CreatedAt: now}
		if b.addOrder(b.s.db, o) != nil {
			continue
		}
		b.s.changed(b.acc.ID, "order")
		if !b.barModel {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			q, err := b.market().Quote(ctx, k.id)
			cancel()
			if err == nil {
				b.execLive(o, q, false, "time", now)
			}
		}
	}
}

// reasonExpired marks the closing order of a position whose time limit passed.
const reasonExpired = "持仓到期"

// needsDepth reports whether a quote would trigger a stop or target (execution then fetches depth).
func (b *book) needsDepth(inst string, last float64) bool {
	for _, k := range b.instPositions(inst) {
		if b.triggered(k, last, last, last) != nil {
			return true
		}
	}
	return false
}

// triggered returns the protection order of a position that fires for a price range [lo, hi]
// (open o for gaps); the stop wins when both are inside the range.
func (b *book) triggered(k posKey, open, lo, hi float64) *Order {
	p := b.pos[k]
	if p == nil {
		return nil
	}
	long := p.PosSide == "long"
	var stop, target *Order
	for _, o := range b.ordersFor(k, isProtect) {
		if o.Type == OrdStop && stop == nil {
			stop = o
		} else if o.Type == OrdTarget && target == nil {
			target = o
		}
	}
	hitStop := stop != nil && ((long && lo <= stop.TriggerPx) || (!long && hi >= stop.TriggerPx))
	hitTarget := target != nil && ((long && hi >= target.TriggerPx) || (!long && lo <= target.TriggerPx))
	if hitStop && hitTarget && target != nil { // a gap through the target at the open fills the target first
		if (long && open >= target.TriggerPx) || (!long && open <= target.TriggerPx) {
			return target
		}
	}
	if hitStop {
		return stop
	}
	if hitTarget {
		return target
	}
	return nil
}

func (b *book) instOrders(inst string, pred func(*Order) bool) []*Order {
	var out []*Order
	for _, o := range b.orders {
		if o.InstID == inst && (pred == nil || pred(o)) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *book) instPositions(inst string) []posKey {
	var out []posKey
	for k := range b.pos {
		if k.id == inst {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].inst < out[j].inst })
	return out
}

func (b *book) execLimit(o *Order, px float64, now int64) {
	f, closes, err := b.fill(o, fillReq{qty: o.Qty - o.Filled, px: px, maker: true, ts: now})
	if err != nil {
		b.cancel(b.s.db, o, "成交失败："+err.Error(), now)
		return
	}
	b.afterFill(o, f, closes, "fill")
}

// liquidate closes the whole position at its liquidation price; its margin is lost.
func (b *book) liquidate(k posKey, px float64, now int64) {
	p := b.pos[k]
	if p == nil {
		return
	}
	for _, x := range b.ordersFor(k, func(x *Order) bool { return x.ReduceOnly }) {
		b.cancel(b.s.db, x, "强平", now)
	}
	o := &Order{InstanceID: k.inst, InstID: k.id, Side: sideOf(p.PosSide, false), PosSide: p.PosSide, Type: OrdMarket, Qty: p.Qty,
		Price: px, ReduceOnly: true, Leverage: p.Leverage, State: OsLive, Reason: "强平", CreatedAt: now}
	if err := b.addOrder(b.s.db, o); err != nil {
		return
	}
	f, closes, err := b.fill(o, fillReq{qty: p.Qty, px: px, liq: true, ts: now})
	if err != nil {
		b.cancel(b.s.db, o, "强平失败："+err.Error(), now)
		return
	}
	b.afterFill(o, f, closes, "liquidation")
}

// ---------- bar model (backtest) ----------

// processBar runs one base bar of an instrument: pending market orders fill at the open
// (± half spread + 1 tick, cancelled beyond their worst price), limits fill when the range trades
// through, then stops/targets/liquidation use the bar's high/low with the stop first.
func (b *book) processBar(inst string, bar Bar) {
	spec, _ := b.market().Spec(inst)
	half := bar.O * barSpreadBp / 2 / 1e4
	for _, o := range b.instOrders(inst, func(o *Order) bool { return o.Type == OrdMarket && o.State == OsLive }) {
		buy := o.Side == "buy"
		px := snapTick(bar.O+(half+spec.TickSz)*sgn(buy), spec.TickSz, buy)
		if o.Price > 0 && ((buy && px > o.Price) || (!buy && px < o.Price)) {
			b.cancel(b.s.db, o, fmt.Sprintf("开盘价超出滑点上限（%s）", fmtPx(o.Price)), bar.Ts)
			delete(b.groups, o.OCOGroup)
			continue
		}
		want := o.Qty - o.Filled
		if o.ReduceOnly {
			p := b.pos[posKey{o.InstanceID, o.InstID}]
			if p == nil {
				b.cancel(b.s.db, o, "无持仓", bar.Ts)
				continue
			}
			want = math.Min(want, p.Qty)
		}
		ref := bar.O
		f, closes, err := b.fill(o, fillReq{qty: want, px: px, slipBp: (px - ref) / ref * 1e4 * sgn(buy), ts: bar.Ts})
		if err != nil {
			b.cancel(b.s.db, o, "成交失败："+err.Error(), bar.Ts)
			continue
		}
		kind := "fill"
		if o.Reason == reasonExpired {
			kind = "time"
		}
		b.afterFill(o, f, closes, kind)
	}
	for _, o := range b.instOrders(inst, func(o *Order) bool { return o.Type == OrdLimit }) {
		switch {
		case o.Side == "buy" && bar.L < o.Price:
			b.execLimit(o, math.Min(o.Price, bar.O), bar.Ts)
		case o.Side == "sell" && bar.H > o.Price:
			b.execLimit(o, math.Max(o.Price, bar.O), bar.Ts)
		}
	}
	for _, k := range b.instPositions(inst) {
		p := b.pos[k]
		if p == nil {
			continue
		}
		long := p.PosSide == "long"
		liq := p.LiqPx
		if liq > 0 && ((long && bar.O <= liq) || (!long && bar.O >= liq)) {
			b.liquidate(k, liq, bar.Ts)
			continue
		}
		if o := b.triggered(k, bar.O, bar.L, bar.H); o != nil {
			// fill at the trigger, or at the open when the bar gapped through it
			px := o.TriggerPx
			buy := o.Side == "buy"
			gap := (buy && bar.O > px) || (!buy && bar.O < px) // stop gapped through
			if o.Type == OrdTarget {
				gap = (buy && bar.O < px) || (!buy && bar.O > px)
			}
			if gap {
				px = bar.O
			}
			fpx := snapTick(px+(half+spec.TickSz)*sgn(buy), spec.TickSz, buy)
			kind := "stop"
			if o.Type == OrdTarget {
				kind = "target"
			}
			f, closes, err := b.fill(o, fillReq{qty: math.Min(o.Qty-o.Filled, p.Qty), px: fpx, slipBp: (fpx - px) / px * 1e4 * sgn(buy), ts: bar.Ts})
			if err != nil {
				continue
			}
			b.afterFill(o, f, closes, kind)
			continue
		}
		if liq > 0 && ((long && bar.L <= liq) || (!long && bar.H >= liq)) {
			b.liquidate(k, liq, bar.Ts)
			continue
		}
		b.trailStop(k, bar.L, bar.H, bar.Ts) // moves from the next bar on: the path inside this one is unknown
	}
	b.marks[inst] = bar.C
}

// snapTick rounds a simulated fill price to the instrument tick, against the taker (buys up,
// sells down), so modelled prices are prices the exchange could print.
func snapTick(px, tick float64, buy bool) float64 {
	if tick <= 0 || px <= 0 {
		return px
	}
	n := px / tick
	if buy {
		n = math.Ceil(n - 1e-9)
	} else {
		n = math.Floor(n + 1e-9)
	}
	return math.Round(n*tick*1e10) / 1e10
}
