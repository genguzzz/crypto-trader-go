package trade

import (
	"fmt"
	"math"
	"time"
)

const (
	minuteMs = int64(60_000)
	hourMs   = 60 * minuteMs
	dayMs    = 24 * hourMs
)

// Timeframes are aligned to UTC epoch multiples (1D = 00:00–24:00 UTC). OKX aligns 4H the same
// way; its "1D" starts at 00:00 UTC+8, so a strategy 1D bar here equals OKX "1Dutc".
var tfMillis = map[string]int64{"1m": minuteMs, "5m": 5 * minuteMs, "15m": 15 * minuteMs, "1H": hourMs, "4H": 4 * hourMs, "1D": dayMs}

// TFMillis returns the length of a strategy timeframe.
func TFMillis(tf string) (int64, bool) { v, ok := tfMillis[tf]; return v, ok }

func refreshMillis(s string) (int64, error) {
	if s == "" {
		return hourMs, nil
	}
	v, ok := tfMillis[s]
	if !ok || v < 5*minuteMs {
		return 0, fmt.Errorf("refresh 需为 5m/15m/1H/4H/1D")
	}
	return v, nil
}

var cst = time.FixedZone("UTC+8", 8*3600)

// dayKey numbers UTC+8 calendar days (the breaker and daily summary boundary).
func dayKey(ts int64) int64 { return (ts + 8*hourMs) / dayMs }

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// floorStep rounds x down to a multiple of step (tolerating float noise).
func floorStep(x, step float64) float64 {
	if step <= 0 {
		return x
	}
	return math.Floor(x/step+1e-9) * step
}

func round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}
