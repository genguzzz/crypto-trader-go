package trade

import (
	"strings"
	"testing"
)

const xInst = "X-USDT-SWAP"

// testBook returns the book of a fresh live (or backtest) account with deposit.
func testBook(t *testing.T, mode Mode, deposit float64, limits Limits) (*Service, *fakeMD, *book) {
	t.Helper()
	md := newFakeMD(t0)
	s, _ := newSvc(t, md)
	in := AccountInput{Name: "t", Mode: mode, Deposit: deposit, Limits: &limits}
	if mode == ModeBacktest {
		in.From, in.To = t0-10*dayMs, t0-dayMs
	}
	a := mustAccount(t, s, in)
	b, _ := s.book(a.ID)
	return s, md, b
}

// openPos fills an entry directly and attaches its stop/target. Caller does not hold b.mu.
func openPos(t *testing.T, b *book, inst, posSide string, qty, px, lev, stop, target float64) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.market().Now()
	g := "gt-" + inst
	b.groups[g] = groupSpec{stop: stop, target: target}
	o := &Order{InstanceID: 1, InstID: inst, Side: sideOf(posSide, true), PosSide: posSide, Type: OrdMarket, Qty: qty, Leverage: lev, OCOGroup: g, State: OsLive, CreatedAt: now}
	if err := b.addOrder(b.s.db, o); err != nil {
		t.Fatal(err)
	}
	f, closes, err := b.fill(o, fillReq{qty: qty, px: px, ts: now})
	if err != nil {
		t.Fatal(err)
	}
	b.afterFill(o, f, closes, "fill")
}

func ledgerSum(s *Service, acct int64, kind string) float64 {
	var v float64
	s.db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM trade_ledger WHERE account_id=? AND kind=?`, acct, kind).Scan(&v)
	return v
}

func TestOCOTargetCancelsStop(t *testing.T) {
	_, md, b := testBook(t, ModeLive, 10000, DefaultLimits())
	openPos(t, b, btc, "long", 1, 100000, 5, 99000, 102000)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.orders) != 2 {
		t.Fatalf("want stop+target, got %d open orders", len(b.orders))
	}
	for _, o := range b.orders {
		if !o.ReduceOnly || o.Qty != 1 || o.OCOGroup != "gt-"+btc || o.State != OsTrigger {
			t.Fatalf("protection order %+v", o)
		}
	}
	q := btcQuote(101000)
	b.onQuote(btc, q, md.Now()) // nothing triggers
	if len(b.orders) != 2 || len(b.pos) != 1 {
		t.Fatal("triggered too early")
	}
	q = btcQuote(102000.5)
	b.onQuote(btc, q, md.Now())
	if len(b.pos) != 0 || len(b.orders) != 0 {
		t.Fatalf("position %v orders %v after target", b.pos, b.orders)
	}
	var stopState, reason string
	b.s.db.QueryRow(`SELECT state, reason FROM trade_orders WHERE type='stop'`).Scan(&stopState, &reason)
	if stopState != string(OsCancelled) || reason != "持仓已平" {
		t.Fatalf("stop %s %q", stopState, reason)
	}
	// target sells into the bid (102000.4): realized (102000.4 − 100000) × 1 × 0.01
	if got := ledgerSum(b.s, b.acc.ID, "pnl"); !approx(got, 20.004, 1e-6) {
		t.Fatalf("pnl %v", got)
	}
}

func TestIOCSlippage(t *testing.T) {
	cases := []struct {
		name      string
		q         Quote
		bp        float64
		qty       float64
		filled    float64
		avg       float64
		state     OrderState
		reasonHas string
	}{
		{"walks the book up to the worst price, rest cancelled",
			Quote{Last: 100, Bid: 99.9, Ask: 100, Asks: [][2]float64{{100, 1}, {100.05, 1}, {100.5, 10}}}, 10, 3, 1, 100, OsCancelled, "IOC"},
		{"wider tolerance takes two levels",
			Quote{Last: 100, Bid: 99.9, Ask: 100, Asks: [][2]float64{{100, 1}, {100.05, 1}, {100.5, 10}}}, 20, 3, 2, 100.025, OsCancelled, "IOC"},
		{"enough depth fills completely",
			Quote{Last: 100, Bid: 99.9, Ask: 100, Asks: [][2]float64{{100, 5}}}, 10, 3, 3, 100, OsFilled, ""},
		{"no depth: last + half spread + tick beyond the cap",
			Quote{Last: 100, Bid: 99.9, Ask: 100.1}, 10, 3, 0, 0, OsCancelled, "滑点上限"},
		{"no depth within the cap",
			Quote{Last: 100, Bid: 99.9, Ask: 100.1}, 20, 3, 3, 100.11, OsFilled, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, md, b := testBook(t, ModeLive, 10000, DefaultLimits())
			b.mu.Lock()
			defer b.mu.Unlock()
			ref := refPrice(c.q)
			o := &Order{InstanceID: 1, InstID: xInst, Side: "buy", PosSide: "long", Type: OrdMarket, Qty: c.qty, Price: ref * (1 + c.bp/1e4),
				Leverage: 5, OCOGroup: "g", State: OsLive, CreatedAt: md.Now()}
			b.addOrder(b.s.db, o)
			b.execLive(o, c.q, true, "fill", md.Now())
			if !approx(o.Filled, c.filled, 1e-9) || !approx(o.AvgPx, c.avg, 1e-9) || o.State != c.state || !strings.Contains(o.Reason, c.reasonHas) {
				t.Fatalf("order %+v", o)
			}
			p := b.pos[posKey{1, xInst}]
			if c.filled == 0 {
				if p != nil {
					t.Fatal("position without fill")
				}
				return
			}
			if p == nil || !approx(p.Qty, c.filled, 1e-9) {
				t.Fatalf("position %+v", p)
			}
		})
	}
}

func TestLimitThroughFill(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		_, md, b := testBook(t, ModeLive, 10000, DefaultLimits())
		b.mu.Lock()
		defer b.mu.Unlock()
		o := &Order{InstanceID: 1, InstID: xInst, Side: "buy", PosSide: "long", Type: OrdLimit, Qty: 2, Price: 99, Leverage: 5, State: OsLive, CreatedAt: md.Now()}
		b.addOrder(b.s.db, o)
		b.onQuote(xInst, Quote{Last: 99, Mark: 99}, md.Now())
		if o.Filled != 0 {
			t.Fatal("touch is not a trade-through")
		}
		b.onQuote(xInst, Quote{Last: 98.99, Mark: 98.99}, md.Now())
		if o.State != OsFilled || o.AvgPx != 99 {
			t.Fatalf("order %+v", o)
		}
		if fee := -ledgerSum(b.s, b.acc.ID, "fee"); !approx(fee, 2*99*2/1e4, 1e-9) {
			t.Fatalf("maker fee %v", fee)
		}
	})
	t.Run("bar", func(t *testing.T) {
		for _, c := range []struct {
			bar  Bar
			fill float64
		}{
			{Bar{Ts: t0, O: 100, H: 101, L: 99, C: 100}, 0},
			{Bar{Ts: t0, O: 100, H: 101, L: 98.5, C: 100}, 99},
			{Bar{Ts: t0, O: 98, H: 101, L: 97, C: 100}, 98},
		} {
			_, md, b := testBook(t, ModeBacktest, 10000, DefaultLimits())
			b.mu.Lock()
			o := &Order{InstanceID: 1, InstID: xInst, Side: "buy", PosSide: "long", Type: OrdLimit, Qty: 2, Price: 99, Leverage: 5, State: OsLive, CreatedAt: md.Now()}
			b.addOrder(b.s.db, o)
			b.processBar(xInst, c.bar)
			b.mu.Unlock()
			if o.AvgPx != c.fill {
				t.Fatalf("bar %+v: filled at %v, want %v", c.bar, o.AvgPx, c.fill)
			}
		}
	})
}

func TestBarStopPriority(t *testing.T) {
	half := func(o float64) float64 { return o*barSpreadBp/2/1e4 + 0.01 } // half spread + 1 tick (X tick 0.01)
	cases := []struct {
		name    string
		side    string
		stop    float64
		target  float64
		bar     Bar
		kind    OrderType
		px      float64
		noFills bool
	}{
		{"both inside: stop first", "long", 95, 105, Bar{O: 100, H: 106, L: 94, C: 100}, OrdStop, 95 - half(100), false},
		{"target only", "long", 95, 105, Bar{O: 100, H: 106, L: 96, C: 105}, OrdTarget, 105 - half(100), false},
		{"gap below stop fills at the open", "long", 95, 105, Bar{O: 93, H: 94, L: 92, C: 93}, OrdStop, snapTick(93-half(93), 0.01, false), false},
		{"gap above target fills at the open", "long", 95, 105, Bar{O: 107, H: 108, L: 94, C: 107}, OrdTarget, snapTick(107-half(107), 0.01, false), false},
		{"short: both inside, stop first", "short", 105, 95, Bar{O: 100, H: 106, L: 94, C: 100}, OrdStop, snapTick(105+half(100), 0.01, true), false},
		{"inside the range: nothing", "long", 95, 105, Bar{O: 100, H: 104, L: 96, C: 101}, "", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, b := testBook(t, ModeBacktest, 10000, DefaultLimits())
			openPos(t, b, xInst, c.side, 10, 100, 3, c.stop, c.target)
			b.mu.Lock()
			c.bar.Ts = t0
			b.processBar(xInst, c.bar)
			_, open := b.pos[posKey{1, xInst}]
			b.mu.Unlock()
			if c.noFills {
				if !open {
					t.Fatal("position closed")
				}
				return
			}
			var px float64
			var typ string
			b.s.db.QueryRow(`SELECT f.px, o.type FROM trade_fills f JOIN trade_orders o ON o.id=f.order_id WHERE o.reduce_only=1`).Scan(&px, &typ)
			if open || OrderType(typ) != c.kind || !approx(px, c.px, 1e-9) {
				t.Fatalf("open=%v fill %s @ %v, want %s @ %v", open, typ, px, c.kind, c.px)
			}
		})
	}
}

func TestFunding(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		s, md, b := testBook(t, ModeLive, 10000, DefaultLimits())
		openPos(t, b, btc, side, 1, 100000, 5, 0, 0) // notional 1000 USDT
		md.funding[btc] = map[int64]float64{t0 + hourMs: 0.0001, t0 + 3*hourMs: -0.0002}
		b.mu.Lock()
		b.settleFunding(t0+4*hourMs, t0+4*hourMs+5*minuteMs, 10*minuteMs) // hour 4 still within grace
		p := *b.pos[posKey{1, btc}]
		b.mu.Unlock()
		want := -0.0001*1000 + 0.0002*1000
		if side == "short" {
			want = -want
		}
		if got := ledgerSum(s, b.acc.ID, "funding"); !approx(got, want, 1e-9) {
			t.Fatalf("%s: funding %v, want %v", side, got, want)
		}
		if p.FundedTo != t0+3*hourMs {
			t.Fatalf("%s: fundedTo %v", side, p.FundedTo-t0)
		}
	}
}

func TestLiquidation(t *testing.T) {
	l := DefaultLimits()
	l.RequireStop, l.MaxLeverage = false, 10
	t.Run("live", func(t *testing.T) {
		s, md, b := testBook(t, ModeLive, 1000, l)
		openPos(t, b, xInst, "long", 1, 100, 10, 0, 0)
		b.mu.Lock()
		liq := b.pos[posKey{1, xInst}].LiqPx
		if !approx(liq, 90/0.995, 1e-9) {
			t.Fatalf("liq %v", liq)
		}
		b.onQuote(xInst, Quote{Last: 90.5, Mark: 90.5}, md.Now())
		if len(b.pos) != 1 {
			t.Fatal("liquidated above the liquidation price")
		}
		b.onQuote(xInst, Quote{Last: 90.4, Mark: 90.4}, md.Now())
		bal := b.balance
		b.mu.Unlock()
		if got := ledgerSum(s, b.acc.ID, "liquidation"); !approx(got, -10, 1e-9) {
			t.Fatalf("liquidation ledger %v", got)
		}
		openFee := 100 * 5 / 1e4
		if !approx(bal, 1000-10-openFee, 1e-9) {
			t.Fatalf("balance %v", bal)
		}
		var liqFlag int
		var px float64
		s.db.QueryRow(`SELECT liquidation, px FROM trade_fills WHERE reduce=1`).Scan(&liqFlag, &px)
		if liqFlag != 1 || !approx(px, liq, 1e-9) {
			t.Fatalf("fill liq=%d px=%v", liqFlag, px)
		}
	})
	t.Run("bar", func(t *testing.T) {
		s, _, b := testBook(t, ModeBacktest, 1000, l)
		openPos(t, b, xInst, "short", 1, 100, 10, 0, 0)
		b.mu.Lock()
		b.processBar(xInst, Bar{Ts: t0, O: 100, H: 111, L: 99, C: 105})
		n := len(b.pos)
		b.mu.Unlock()
		if n != 0 || !approx(ledgerSum(s, b.acc.ID, "liquidation"), -10, 1e-9) {
			t.Fatalf("short not liquidated: %d positions", n)
		}
	})
}

func TestDailyBreaker(t *testing.T) {
	s, md, b := testBook(t, ModeLive, 1000, DefaultLimits()) // 5% daily loss = 50 USDT
	inter := &fakeInteractor{}
	s.SetInteractor(inter)
	openPos(t, b, xInst, "long", 10, 100, 5, 90, 0)
	b.mu.Lock()
	b.marks[xInst] = 96 // −40 unrealized, −0.5 fee
	if b.checkBreaker(md.Now()) {
		t.Fatal("tripped at −40.5")
	}
	b.marks[xInst] = 94.9
	if !b.checkBreaker(md.Now()) {
		t.Fatal("not tripped at −51.5")
	}
	err := b.checkOpen(Signal{InstanceID: 2, InstID: btc, Action: OpenLong, StopLoss: 1}, 100000, md.Now(), false)
	if err == nil || !strings.Contains(err.Error(), "日内熔断") {
		t.Fatalf("open allowed while halted: %v", err)
	}
	view := b.view()
	next := t0 + dayMs
	tripped := b.checkBreaker(next)
	b.mu.Unlock()
	if !view.Halted || tripped {
		t.Fatalf("halted=%v, next day tripped=%v", view.Halted, tripped)
	}
	waitFor(t, "breaker event", func() bool {
		for _, ti := range inter.titles() {
			if strings.Contains(ti, "日内熔断") {
				return true
			}
		}
		return false
	})
}
