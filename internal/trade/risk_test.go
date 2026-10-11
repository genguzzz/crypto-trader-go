package trade

import (
	"math"
	"strings"
	"testing"
)

func TestSize(t *testing.T) {
	btc := Spec{InstID: "BTC-USDT-SWAP", CtVal: 0.01, TickSz: 0.1, LotSz: 0.01, MinSz: 0.01, MaxLever: 100}
	x := Spec{InstID: "X-USDT-SWAP", CtVal: 1, TickSz: 0.01, LotSz: 1, MinSz: 1, MaxLever: 50}
	l := DefaultLimits() // risk 1%, max inst 30%, max lever 5
	cases := []struct {
		name    string
		in      sizeIn
		qty     float64
		lever   float64
		errLike string
	}{
		{"risk based: 10000×1% / (1000×0.01) = 10 contracts",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100000, Stop: 99000, Long: true, Spec: btc, Limits: l, MMR: 0.005}, 10, 5, ""},
		{"strength and scale shrink the risk",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100000, Stop: 99000, Long: true, Strength: 0.5, Scale: 0.5, Spec: btc, Limits: l, MMR: 0.005}, 2.5, 5, ""},
		{"capped by MaxInstPct margin: 3000×5/(100000×0.01) = 15",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100000, Stop: 99900, Long: true, Spec: btc, Limits: l, MMR: 0.005}, 15, 5, ""},
		{"existing instrument margin reduces the cap",
			sizeIn{Equity: 10000, Cash: 10000, InstMargin: 2000, Entry: 100000, Stop: 99900, Long: true, Spec: btc, Limits: l, MMR: 0.005}, 5, 5, ""},
		{"short with stop above",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100000, Stop: 101000, Long: false, Spec: btc, Limits: l, MMR: 0.005}, 10, 5, ""},
		{"rounded down to the lot",
			sizeIn{Equity: 1000, Cash: 1000, Entry: 3.33, Stop: 3.0, Long: true, Spec: x, Limits: l, MMR: 0.005}, 30, 5, ""},
		{"below min size",
			sizeIn{Equity: 10, Cash: 10, Entry: 3.33, Stop: 3.0, Long: true, Spec: x, Limits: l, MMR: 0.005}, 0, 0, "低于最小下单量"},
		{"wrong stop side",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100, Stop: 101, Long: true, Spec: x, Limits: l, MMR: 0.005}, 0, 0, "错误一侧"},
		{"far stop lowers leverage so liquidation stays beyond it",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100, Stop: 70, Long: true, Spec: x, Limits: l, MMR: 0.005}, 3, 2.64, ""},
		{"requested leverage above the cap is capped",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100, Stop: 99, Long: true, Lever: 50, Spec: x, Limits: l, MMR: 0.005}, 100, 5, ""},
		{"no stop: margin = equity × MaxInstPct × strength",
			sizeIn{Equity: 10000, Cash: 10000, Entry: 100, Long: true, Strength: 0.5, Spec: x, Limits: l, MMR: 0.005}, 75, 5, ""},
		{"no cash",
			sizeIn{Equity: 10000, Cash: 0, Entry: 100, Stop: 99, Long: true, Spec: x, Limits: l, MMR: 0.005}, 0, 0, "可用保证金不足"},
	}
	for _, c := range cases {
		out, err := size(c.in)
		if c.errLike != "" {
			if err == nil || !strings.Contains(err.Error(), c.errLike) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.errLike)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !approx(out.Qty, c.qty, 1e-9) || !approx(out.Lever, c.lever, 1e-9) {
			t.Errorf("%s: qty %v lever %v, want %v %v", c.name, out.Qty, out.Lever, c.qty, c.lever)
		}
		if c.in.Stop > 0 {
			liq := liqPrice(map[bool]string{true: "long", false: "short"}[c.in.Long], c.in.Entry, out.Margin, out.Qty, c.in.Spec.CtVal, c.in.MMR)
			if (c.in.Long && liq >= c.in.Stop) || (!c.in.Long && liq <= c.in.Stop) {
				t.Errorf("%s: liquidation %v not beyond stop %v", c.name, liq, c.in.Stop)
			}
		}
	}
}

func TestLiqPrice(t *testing.T) {
	// 10x long at 100: margin 10 per contract (ctVal 1) → (100−10)/(1−0.005)
	if got := liqPrice("long", 100, 10, 1, 1, 0.005); !approx(got, 90/0.995, 1e-9) {
		t.Fatalf("long liq %v", got)
	}
	if got := liqPrice("short", 100, 10, 1, 1, 0.005); !approx(got, 110/1.005, 1e-9) {
		t.Fatalf("short liq %v", got)
	}
}

func TestSelectScreen(t *testing.T) {
	nan := func() float64 { var z float64; return z / z }()
	rows := []ScreenRow{
		{InstID: "A", Rank: 3, Long: 80, Short: nan, Values: map[string]float64{"ret_1h": 0.01}},
		{InstID: "B", Rank: 1, Long: 90, Short: 10, Values: map[string]float64{"ret_1h": 0.05}},
		{InstID: "C", Rank: 2, Long: nan, Short: 70},
		{InstID: "D", Rank: 4, Long: 60, Short: 40, Values: map[string]float64{"ret_1h": -0.02}},
	}
	got := SelectScreen(rows, ScreenerQuery{Rule: "r", Side: "long", Top: 2})
	if len(got) != 2 || got[0].InstID != "B" || got[1].InstID != "A" || !math.IsNaN(got[1].Short) { // unscored stays unscored, never 0
		t.Fatalf("long top2 = %+v", got)
	}
	got = SelectScreen(rows, ScreenerQuery{Rule: "r", Side: "long", MinScore: 61, Filter: map[string][2]float64{"ret_1h": {0, 0.02}}})
	if len(got) != 1 || got[0].InstID != "A" {
		t.Fatalf("filtered = %+v", got)
	}
	got = SelectScreen(rows, ScreenerQuery{Top: 3})
	if len(got) != 3 || got[0].InstID != "B" || got[2].InstID != "A" {
		t.Fatalf("by rank = %+v", got)
	}
}
