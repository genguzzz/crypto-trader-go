package market

import (
	"math"
	"testing"
)

func TestExhaustionHelpers(t *testing.T) {
	if got := ramp(2.5, 1.5, 3.5); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("ramp mid = %v", got)
	}
	if got := ramp(25, 35, 20); math.Abs(got-(10.0/15)) > 1e-9 { // descending ramp (RSI bottom)
		t.Errorf("descending ramp = %v", got)
	}
	if ramp(math.NaN(), 0, 1) != 0 || ramp(-1, 0, 1) != 0 || ramp(9, 0, 1) != 1 {
		t.Error("ramp clamps")
	}
	if m := median([]float64{3, 1, 2, 10}); m != 2.5 {
		t.Errorf("median = %v", m)
	}
	// RSI of a steadily rising series is 100, of an alternating one about 50
	up := make([]float64, 30)
	for i := range up {
		up[i] = float64(i)
	}
	if r := rsiSeries(up, 14); r[29] != 100 || !math.IsNaN(r[5]) {
		t.Errorf("rsi rising = %v, warmup %v", r[29], r[5])
	}
	alt := make([]float64, 60)
	for i := range alt {
		alt[i] = 100 + float64(i%2)
	}
	if r := rsiSeries(alt, 14); math.Abs(r[59]-50) > 5 {
		t.Errorf("rsi alternating = %v", r[59])
	}
}
