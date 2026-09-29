package market

import (
	"math"
	"testing"

	"crypto-trader-go/internal/store"
)

func TestLiqTiersFor(t *testing.T) {
	ts := liqTiersFor(20) // 5x and 10x only
	if len(ts) != 2 || ts[0].Lever != 5 || ts[1].Lever != 10 {
		t.Fatalf("tiers %+v", ts)
	}
	if w := ts[0].W + ts[1].W; math.Abs(w-1) > 1e-9 {
		t.Fatalf("weights sum %v", w)
	}
	if ts := liqTiersFor(3); len(ts) != 1 || ts[0].Lever != 3 {
		t.Fatalf("below the lowest tier: %+v", ts)
	}
}

// steps builds model input from hourly candles and OI keyed by hour open (missing = no sample).
func steps(cs []Candle, oi map[int64]float64) []liqStep {
	out := make([]liqStep, len(cs))
	for i, c := range cs {
		v, ok := oi[c.T]
		out[i] = liqStep{T: c.T, H: c.H, L: c.L, C: c.C, OI: v, HasOI: ok}
	}
	return out
}

func TestEstimateLiqLevels(t *testing.T) {
	h := int64(hourMs)
	tiers := []liqTier{{10, 1}}
	flat := func(t0 int64, px float64) Candle { return Candle{T: t0, O: px, H: px, L: px, C: px} }
	// hour 0: baseline OI; hour 1: +10 coins opened at 100 → long level ~90.5, short ~109.5 (10x, 0.5% MMR)
	cs := []Candle{flat(0, 100), flat(h, 100), flat(2*h, 100)}
	oi := map[int64]float64{0: 100, h: 110, 2 * h: 110}
	longs, shorts := estimateLiqLevels(steps(cs, oi), tiers, 1)
	if longs[90][0] != 1000 || shorts[109][0] != 1000 || len(longs) != 1 || len(shorts) != 1 {
		t.Fatalf("levels long %v short %v", longs, shorts)
	}
	// OI falls by half → both sides shrink by the same share of OI
	cs = append(cs, flat(3*h, 100))
	oi[3*h] = 55
	longs, shorts = estimateLiqLevels(steps(cs, oi), tiers, 1)
	if longs[90][0] != 500 || shorts[109][0] != 500 {
		t.Fatalf("after OI drop long %v short %v", longs, shorts)
	}
	// a later candle trading down through 90 removes the long level only
	cs = append(cs, Candle{T: 4 * h, O: 100, H: 101, L: 89.9, C: 95})
	oi[4*h] = 55
	longs, shorts = estimateLiqLevels(steps(cs, oi), tiers, 1)
	if len(longs) != 0 || shorts[109][0] != 500 {
		t.Fatalf("after sweep long %v short %v", longs, shorts)
	}
	// missing OI in the middle: the change across the gap is not attributed
	cs2 := []Candle{flat(0, 100), flat(h, 100), flat(2*h, 100)}
	longs, _ = estimateLiqLevels(steps(cs2, map[int64]float64{0: 100, 2 * h: 200}), tiers, 1)
	if len(longs) != 0 {
		t.Fatalf("gap bridged: %v", longs)
	}
}

func TestEstimateLiqLevelsByTier(t *testing.T) {
	h := int64(hourMs)
	tiers := []liqTier{{10, 0.5}, {50, 0.5}}
	flat := func(t0 int64, px float64) Candle { return Candle{T: t0, O: px, H: px, L: px, C: px} }
	cs := []Candle{flat(0, 100), flat(h, 100)}
	longs, shorts := estimateLiqLevels(steps(cs, map[int64]float64{0: 100, h: 110}), tiers, 1)
	// 10x → long ~90.5 / short ~109.5; 50x → long ~98.5 / short ~101.5
	if longs[90][0] != 500 || longs[90][1] != 0 || longs[98][1] != 500 || shorts[101][1] != 500 || shorts[109][0] != 500 {
		t.Fatalf("tiers long %v short %v", longs, shorts)
	}
}

func TestLiqSteps(t *testing.T) {
	h, m5 := int64(hourMs), int64(5*minuteMs)
	c := func(t0 int64) Candle { return Candle{T: t0, O: 100, H: 100, L: 100, C: 100} }
	hourly := []Candle{c(0), c(h), c(2 * h), c(3 * h)}
	var fine []Candle
	var ois []store.OISample
	for ts := h + 30*minuteMs; ts < 4*h; ts += m5 { // 5m data from 1:30 on
		fine = append(fine, c(ts))
		ois = append(ois, store.OISample{Ts: ts + 4*minuteMs, OiCcy: 100})
	}
	ois = append(ois, store.OISample{Ts: 0, OiCcy: 100})
	st, from := liqSteps(hourly, fine, ois)
	if from != 2*h {
		t.Fatalf("fine from %d", from)
	}
	if len(st) != 2+12*2 || st[1].T != h || st[2].T != 2*h || st[3].T != 2*h+m5 {
		t.Fatalf("steps %d: %+v", len(st), st[:4])
	}
	// a hole in the 5m candles: fine data starts after it
	fine = append(fine[:5:5], fine[6:]...)
	if _, from = liqSteps(hourly, fine, ois); from != 2*h {
		t.Fatalf("fine from after hole %d", from)
	}
	// no per-minute OI: hourly only
	if _, from = liqSteps(hourly, fine, []store.OISample{{Ts: 0, OiCcy: 1}}); from != 0 {
		t.Fatalf("fine from without OI %d", from)
	}
}
