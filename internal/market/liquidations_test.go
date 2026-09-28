package market

import (
	"math"
	"testing"
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

func TestEstimateLiqLevels(t *testing.T) {
	h := int64(hourMs)
	tiers := []liqTier{{10, 1}}
	flat := func(t0 int64, px float64) Candle { return Candle{T: t0, O: px, H: px, L: px, C: px} }
	// hour 0: baseline OI; hour 1: +10 coins opened at 100 → long level ~90.5, short ~109.5 (10x, 0.5% MMR)
	cs := []Candle{flat(0, 100), flat(h, 100), flat(2*h, 100)}
	oi := map[int64]float64{0: 100, h: 110, 2 * h: 110}
	longs, shorts := estimateLiqLevels(cs, oi, tiers, 1)
	if longs[90] != 1000 || shorts[109] != 1000 || len(longs) != 1 || len(shorts) != 1 {
		t.Fatalf("levels long %v short %v", longs, shorts)
	}
	// OI falls by half → both sides shrink by the same share of OI
	cs = append(cs, flat(3*h, 100))
	oi[3*h] = 55
	longs, shorts = estimateLiqLevels(cs, oi, tiers, 1)
	if longs[90] != 500 || shorts[109] != 500 {
		t.Fatalf("after OI drop long %v short %v", longs, shorts)
	}
	// a later candle trading down through 90 removes the long level only
	cs = append(cs, Candle{T: 4 * h, O: 100, H: 101, L: 89.9, C: 95})
	oi[4*h] = 55
	longs, shorts = estimateLiqLevels(cs, oi, tiers, 1)
	if len(longs) != 0 || shorts[109] != 500 {
		t.Fatalf("after sweep long %v short %v", longs, shorts)
	}
	// missing OI in the middle: the change across the gap is not attributed
	cs2 := []Candle{flat(0, 100), flat(h, 100), flat(2*h, 100)}
	longs, _ = estimateLiqLevels(cs2, map[int64]float64{0: 100, 2 * h: 200}, tiers, 1)
	if len(longs) != 0 {
		t.Fatalf("gap bridged: %v", longs)
	}
}
