package trade

import (
	"math"
	"testing"
)

func closePos(t *testing.T, b *book, inst string, px float64) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pos[posKey{1, inst}]
	o := &Order{InstanceID: 1, InstID: inst, Side: sideOf(p.PosSide, false), PosSide: p.PosSide, Type: OrdMarket, Qty: p.Qty, ReduceOnly: true,
		Leverage: p.Leverage, State: OsLive, CreatedAt: b.market().Now()}
	b.addOrder(b.s.db, o)
	f, closes, err := b.fill(o, fillReq{qty: p.Qty, px: px, ts: b.market().Now()})
	if err != nil {
		t.Fatal(err)
	}
	b.afterFill(o, f, closes, "fill")
}

func TestReportMath(t *testing.T) {
	l := DefaultLimits()
	l.MaxInstPct = 1
	s, _, b := testBook(t, ModeLive, 10000, l)
	openPos(t, b, xInst, "long", 10, 100, 5, 0, 0)
	closePos(t, b, xInst, 110) // +100, fees 0.5 + 0.55
	openPos(t, b, xInst, "short", 10, 110, 5, 0, 0)
	closePos(t, b, xInst, 115) // −50, fees 0.55 + 0.575
	b.mu.Lock()
	insertLedger(s.db, LedgerEntry{AccountID: b.acc.ID, Kind: "funding", Amount: -2, InstID: xInst, Ts: t0})
	b.balance -= 2
	b.mu.Unlock()
	for i, eq := range []float64{10000, 10100, 9000, 9500} {
		s.db.Exec(`INSERT INTO trade_equity VALUES(?,?,?)`, b.acc.ID, t0+int64(i)*dayMs, eq)
	}
	r, err := s.Report(b.acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	fees := 0.5 + 0.55 + 0.55 + 0.575
	checks := []struct {
		name      string
		got, want float64
	}{
		{"trades", float64(r.Trades), 2},
		{"winRate", *r.WinRate, 0.5},
		{"profitFactor", *r.ProfitFactor, round((100-1.05)/(50+1.125), 4)},
		{"fees", r.Fees, fees},
		{"funding", r.Funding, -2},
		{"net", r.Net, 100 - 50 - fees - 2},
		{"gross", r.Gross, 50},
		{"realized", r.Realized, 50},
		{"turnover", r.Turnover, 1000 + 1100 + 1100 + 1150},
		{"maxDrawdown", r.MaxDrawdown, round(1100.0/10100, 6)},
		{"byInst pnl", r.ByInst[0].Pnl, 100 - 50 - fees - 2},
	}
	for _, c := range checks {
		if !approx(c.got, c.want, 1e-6) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if r.Sharpe != nil {
		t.Errorf("sharpe with 3 daily returns should be null")
	}
}

func TestDailySharpe(t *testing.T) {
	var eq [][2]float64
	v := 100.0
	rets := []float64{0.01, -0.005, 0.02, 0.0, 0.01, -0.01}
	eq = append(eq, [2]float64{float64(t0), v})
	for i, r := range rets {
		v *= 1 + r
		eq = append(eq, [2]float64{float64(t0 + int64(i+1)*dayMs), v})
		eq = append(eq, [2]float64{float64(t0 + int64(i+1)*dayMs + hourMs), v}) // same day: last sample counts
	}
	mean, n := 0.0, float64(len(rets))
	for _, r := range rets {
		mean += r
	}
	mean /= n
	ss := 0.0
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	want := mean / math.Sqrt(ss/(n-1)) * math.Sqrt(365)
	got := dailySharpe(eq)
	if got == nil || !approx(*got, want, 1e-9) {
		t.Fatalf("sharpe %v, want %v", got, want)
	}
}
