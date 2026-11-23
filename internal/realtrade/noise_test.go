package realtrade

import (
	"context"
	"strings"
	"testing"

	"crypto-trader-go/internal/okxacct"
	"crypto-trader-go/internal/okxorder"
	"crypto-trader-go/internal/store"
)

func TestNoiseFloor(t *testing.T) {
	e := newEnv(t, okxorder.NewDryRun())
	e.s.opts.Limits.MinStopATR = 1.5
	e.s.opts.Noise = func(string) (float64, bool) { return 1, true } // 1H ATR = 1% of price
	in := input()
	in.SL, in.TP = 99000, 104000 // 1% < 1.5 × 1%
	if _, err := e.s.Propose(context.Background(), in); err == nil || !strings.Contains(err.Error(), "inside the normal noise") {
		t.Fatalf("tight stop: %v", err)
	}
	in.SL = 98400 // 1.6%
	if _, err := e.s.Propose(context.Background(), in); err != nil {
		t.Fatalf("stop beyond the noise: %v", err)
	}
	e.s.opts.Noise = func(string) (float64, bool) { return 0, false } // unknown volatility: unchecked
	in.InstID, in.SL, in.TP = "eth", 99000, 104000
	if _, err := e.s.Propose(context.Background(), in); err != nil {
		t.Fatalf("no ATR: %v", err)
	}
}

func TestTightens(t *testing.T) {
	pos := okxacct.Position{SL: []okxacct.Trigger{{Px: 95, Full: true}, {Px: 98, Full: false}}}
	for _, c := range []struct {
		side string
		sl   float64
		want bool
	}{{"long", 96, true}, {"long", 95, false}, {"long", 94, false}, {"short", 94, true}, {"short", 96, false}} {
		p := pos
		if c.side == "short" {
			p.SL = []okxacct.Trigger{{Px: 95, Full: true}}
		}
		if got := tightens(c.side, c.sl, p); got != c.want {
			t.Fatalf("%s %v: %v", c.side, c.sl, got)
		}
	}
	if !tightens("long", 90, okxacct.Position{}) {
		t.Fatal("no stop yet")
	}
}

func TestATRPct(t *testing.T) {
	bars := []store.Bar{{C: 100}, {H: 102, L: 99, C: 101}, {H: 101, L: 97, C: 98}} // TR 3, 4
	if v, ok := ATRPct(bars, 2); !ok || v < 3.57 || v > 3.58 {                     // 3.5 / 98
		t.Fatalf("atr %v %v", v, ok)
	}
	if _, ok := ATRPct(bars, 3); ok {
		t.Fatal("too few bars")
	}
}
