package features

import (
	"math"
	"testing"
)

// fakeSeries: hourly-aligned closes growing 1%/h, constant quote, OI growing 2%/h, funding 0.0001 per 8h.
type fakeSeries struct {
	t0     int64
	growth float64
	noOI   bool
}

func (f fakeSeries) Close(t int64) (float64, bool) {
	if t%Minute != 0 {
		return 0, false
	}
	h := float64(t-f.t0) / float64(Hour)
	return 100 * math.Exp(f.growth*h), true
}
func (f fakeSeries) Quote(from, to int64) (float64, bool) {
	return float64(to-from) / float64(Hour) * 1000, true
}
func (f fakeSeries) OI(t int64) (float64, bool) {
	if f.noOI {
		return 0, false
	}
	return 50 * math.Exp(0.02*float64(t-f.t0)/float64(Hour)), true
}
func (f fakeSeries) FundingSettled(t int64) (float64, float64, bool) { return 0.0001, 8, true }

func TestComputeUnitsAndSigns(t *testing.T) {
	t0 := int64(1_700_000_000_000) / Hour * Hour
	T := t0 + 300*Hour
	coin := fakeSeries{t0: t0, growth: 0.01}
	btc := fakeSeries{t0: t0, growth: 0.002}
	v := Compute(coin, btc, T)
	near := func(k string, want float64) {
		t.Helper()
		if got, ok := v[k]; !ok || math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s = %v (present %v), want %v", k, got, ok, want)
		}
	}
	near("ret_1h", 0.01)
	near("ret_4h", 0.04)
	near("ret_24h", 0.24)
	near("excess_btc_4h", 0.04-0.008)
	near("oi_chg_4h", 0.08)
	near("vol_surprise_4h", 0) // constant turnover → no surprise
	near("funding_hourly", 0.0001/8)
	if _, ok := v["trend_z_4h"]; ok {
		t.Fatalf("trend_z_4h must be absent when hourly volatility is zero")
	}
}

func TestMissingIsAbsentNotZero(t *testing.T) {
	t0 := int64(1_700_000_000_000) / Hour * Hour
	v := Compute(fakeSeries{t0: t0, growth: 0.01, noOI: true}, nil, t0+300*Hour)
	for _, k := range []string{"oi_chg_1h", "oi_chg_4h", "excess_btc_4h"} {
		if _, ok := v[k]; ok {
			t.Fatalf("%s should be missing", k)
		}
	}
}

func TestPercentilesMidRank(t *testing.T) {
	p := Percentiles([]float64{1, 2, 2, 3})
	if got := p(2); got != 50 {
		t.Fatalf("tie mid-rank = %v, want 50", got)
	}
	if p(0) != 0 || p(10) != 100 {
		t.Fatalf("bounds: %v %v", p(0), p(10))
	}
}

// funcSeries: closes from a function of hours since t0 (any minute), no OI/funding.
type funcSeries struct {
	t0 int64
	px func(h float64) float64
}

func (f funcSeries) Close(t int64) (float64, bool) {
	if t%Minute != 0 {
		return 0, false
	}
	return f.px(float64(t-f.t0) / float64(Hour)), true
}
func (f funcSeries) Quote(from, to int64) (float64, bool)            { return 0, false }
func (f funcSeries) OI(t int64) (float64, bool)                      { return 0, false }
func (f funcSeries) FundingSettled(t int64) (float64, float64, bool) { return 0, 0, false }

func TestResidualBetaTrendEfficiency(t *testing.T) {
	t0 := int64(1_700_000_000_000) / Hour * Hour
	T := t0 + 300*Hour + 30*Minute // inside an hour, like live
	wig := func(h float64) float64 { return 0.01 * math.Sin(h*1.7) }
	btc := funcSeries{t0, func(h float64) float64 { return 100 * math.Exp(wig(h)) }}
	// coin = 2×BTC moves + its own 0.5%/h drift → β = 2, residual = drift
	coin := funcSeries{t0, func(h float64) float64 { return 10 * math.Exp(2*wig(h)+0.005*h) }}
	v := Compute(coin, btc, T)
	near := func(k string, want, tol float64) {
		t.Helper()
		if got, ok := v[k]; !ok || math.Abs(got-want) > tol {
			t.Fatalf("%s = %v (present %v), want %v", k, got, ok, want)
		}
	}
	near("beta_btc_48h", 2, 0.05)
	near("resid_1h", 0.005, 0.001)
	near("resid_4h", 0.02, 0.002)
	near("resid_15m", 0.00125, 0.0005)
	if v["trend_ema"] <= 0 {
		t.Fatalf("trend_ema should be positive in a drifting-up series: %v", v["trend_ema"])
	}
	if er := v["eff_ratio_24h"]; er <= 0 || er >= 1 {
		t.Fatalf("eff_ratio_24h out of (0,1) on a noisy trend: %v", er)
	}
	// a straight line is perfectly efficient
	line := Compute(funcSeries{t0, func(h float64) float64 { return 100 + h }}, nil, T)
	if math.Abs(line["eff_ratio_24h"]-1) > 1e-9 {
		t.Fatalf("straight line efficiency = %v", line["eff_ratio_24h"])
	}
	if _, ok := line["resid_4h"]; ok {
		t.Fatalf("residual needs BTC")
	}
}
