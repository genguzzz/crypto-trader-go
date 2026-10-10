package trade

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"time"

	"crypto-trader-go/internal/market"
)

// OnMinute is the live clock (register with market.Engine.OnConfirmed): funding settlements,
// day roll / breaker, the 1m equity sample, screener refreshes and, for every running live
// instance whose timeframe closes at this minute, one Bar event per universe instrument.
func (s *Service) OnMinute(snap *market.Snapshot) {
	if snap == nil || s.md == nil {
		return
	}
	T := snap.DecisionTs
	if T == 0 {
		T = snap.AsOfMinute + minuteMs
	}
	s.mu.Lock()
	if T <= s.lastMin {
		s.mu.Unlock()
		return
	}
	s.lastMin = T
	var rts []*instRT
	for _, ri := range s.insts {
		if !ri.b.barModel {
			rts = append(rts, ri)
		}
	}
	s.mu.Unlock()
	for _, b := range s.liveBooks() {
		s.minuteBook(b, T)
	}
	sort.Slice(rts, func(i, j int) bool { return rts[i].inst.ID < rts[j].inst.ID })
	for _, ri := range rts {
		if ri.session() == nil || ri.isPaused() {
			continue
		}
		if err := s.refreshUniverse(ri, T, false); err != nil {
			log.Printf("trade: instance %d universe: %v", ri.inst.ID, err)
		}
		s.queueBars(ri, T)
	}
	s.updateKeep()
}

// queueBars queues, for bar time T, one batch with the bars of the instance timeframe that closed
// at T (instruments with a gap are left out) and the timers that are due.
func (s *Service) queueBars(ri *instRT, T int64) {
	tf := ri.timeframe()
	if step, ok := TFMillis(tf); ok && T%step == 0 {
		var items []InstBar
		for _, id := range ri.currentUniverse() {
			bars, err := ri.b.market().Bars(id, tf, 2)
			if err != nil || len(bars) == 0 || bars[len(bars)-1].Ts != T-step {
				continue
			}
			items = append(items, InstBar{InstID: id, Bar: bars[len(bars)-1]})
		}
		if len(items) > 0 {
			// the host allows its event timeout + 10 % per instrument for a batch; the same proportion of
			// SessionTimeout (≥ the host's timeout) leaves it room to answer first
			ri.push(sessCall{"bars", func(ctx context.Context, x Session) error { return x.Bars(ctx, tf, items) },
				s.opts.SessionTimeout + time.Duration(len(items))*s.opts.SessionTimeout/10})
		}
	}
	for _, name := range sortedKeys(ri.ver.Meta.Schedule) {
		if every, ok := TFMillis(ri.ver.Meta.Schedule[name]); ok && T%every == 0 {
			ri.push(sessCall{name: "timer", fn: func(ctx context.Context, x Session) error { return x.Timer(ctx, name) }})
		}
	}
}

// minStep is the shortest clock an instance needs (its timeframe or a timer).
func minStep(inst Instance, ver StrategyVersion) int64 {
	step, _ := TFMillis(InstanceTimeframe(inst, ver))
	for _, v := range ver.Meta.Schedule {
		if x, ok := TFMillis(v); ok && (step == 0 || x < step) {
			step = x
		}
	}
	return step
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// minuteBook settles funding, rolls the day / checks the breaker and records the equity sample.
func (s *Service) minuteBook(b *book, T int64) {
	b.mu.Lock()
	now := b.market().Now()
	b.settleFunding(T, now, s.opts.FundingGrace.Milliseconds())
	b.checkBreaker(T)
	b.expire(T)
	eq := b.equity()
	b.mu.Unlock()
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO trade_equity VALUES(?,?,?)`, b.acc.ID, T, eq); err != nil {
		log.Printf("trade: equity sample: %v", err)
	}
	s.changed(b.acc.ID, "equity")
}

// settleFunding charges or credits funding for every settlement hour a position was held through.
// A missing settled rate means "no settlement at that hour" once grace has passed (live data
// can arrive a little late; backtests pass grace 0). Caller holds b.mu.
func (b *book) settleFunding(upTo, now, grace int64) {
	for _, k := range b.sortedPosKeys() {
		p := b.pos[k]
		np := *p
		for h := (p.FundedTo/hourMs + 1) * hourMs; h <= upTo; h += hourMs {
			rate, ok := b.market().FundingRate(p.InstID, h)
			if !ok {
				if now-h >= grace {
					np.FundedTo = h
					continue
				}
				break
			}
			amount := -rate * np.Qty * np.CtVal * b.mark(np.InstID, np.EntryPx) * dir(np.PosSide)
			np.FundedTo = h
			err := b.s.tx(func(tx *sql.Tx) error {
				if _, err := insertLedger(tx, LedgerEntry{AccountID: b.acc.ID, Kind: "funding", Amount: amount, InstID: np.InstID, Ts: h}); err != nil {
					return err
				}
				return savePosition(tx, &np)
			})
			if err != nil {
				log.Printf("trade: funding %s: %v", np.InstID, err)
				break
			}
			b.balance += amount
			*p = np
			inst, r, a := np.InstID, rate, amount
			b.s.enqueue(k.inst, "funding", func(ctx context.Context, x Session) error { return x.Funding(ctx, inst, r, a) })
		}
		if np.FundedTo != p.FundedTo {
			*p = np
			savePosition(b.s.db, p)
		}
	}
}

// pollPrices evaluates resting orders and liquidation for instruments with open orders or
// positions in live accounts.
func (s *Service) pollPrices(ctx context.Context) {
	byInst := map[string][]*book{}
	for _, b := range s.liveBooks() {
		b.mu.Lock()
		seen := map[string]bool{}
		for k := range b.pos {
			seen[k.id] = true
		}
		for _, o := range b.orders {
			seen[o.InstID] = true
		}
		b.mu.Unlock()
		for id := range seen {
			byInst[id] = append(byInst[id], b)
		}
	}
	for inst, books := range byInst {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		q, err := s.md.Quote(qctx, inst)
		cancel()
		if err != nil {
			continue
		}
		for _, b := range books {
			b.mu.Lock()
			need := b.needsDepth(inst, q.Last)
			b.mu.Unlock()
			qq := q
			if need {
				if dq, ok := s.md.(DepthQuoter); ok {
					dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
					if d, err := dq.QuoteDepth(dctx, inst); err == nil {
						qq = d
					}
					cancel()
				}
			}
			b.mu.Lock()
			b.onQuote(inst, qq, b.market().Now())
			b.mu.Unlock()
		}
	}
}

// updateKeep keeps instruments streaming that live accounts hold or live instances trade.
func (s *Service) updateKeep() {
	if s.md == nil {
		return
	}
	want := map[string]bool{}
	for _, b := range s.liveBooks() {
		b.mu.Lock()
		for k := range b.pos {
			want[k.id] = true
		}
		for _, o := range b.orders {
			want[o.InstID] = true
		}
		b.mu.Unlock()
	}
	s.mu.RLock()
	rts := make([]*instRT, 0, len(s.insts))
	for _, ri := range s.insts {
		if !ri.b.barModel {
			rts = append(rts, ri)
		}
	}
	s.mu.RUnlock()
	for _, ri := range rts {
		for _, id := range ri.currentUniverse() {
			want[id] = true
		}
	}
	s.mu.Lock()
	var on, off []string
	for id := range want {
		if !s.kept[id] {
			on = append(on, id)
		}
	}
	for id := range s.kept {
		if !want[id] {
			off = append(off, id)
		}
	}
	s.kept = want
	s.mu.Unlock()
	if kb, ok := s.md.(KeepBatcher); ok {
		if len(on) > 0 || len(off) > 0 {
			kb.KeepMany(on, off)
		}
		return
	}
	for _, id := range on {
		s.md.Keep(id, true)
	}
	for _, id := range off {
		s.md.Keep(id, false)
	}
}
