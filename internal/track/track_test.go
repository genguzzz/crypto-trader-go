package track

import (
	"testing"

	"crypto-trader-go/internal/store"
)

func f(v float64) *float64 { return &v }

// A perfectly informative long score should give IC≈1 and a negative short IC when short = long.
func TestEvaluatePerfectSignal(t *testing.T) {
	var rows []store.ScoreRow
	fwd := map[string]float64{}
	ids := []string{"A", "B", "C", "D", "E", "F"}
	for k := int64(0); k < 40; k++ {
		ts := (1_700_000_000_000/hourMs + k) * hourMs
		for i, id := range ids {
			rows = append(rows, store.ScoreRow{Ts: ts, InstID: id, Long: f(float64(i)), Short: f(float64(i)), Close: 100})
			fwd[id] = 100 * (1 + 0.001*float64(i))
		}
	}
	st := Evaluate(rows, 1, func(id string, t int64) (float64, bool) { return fwd[id], true })
	if st.CrossSects != 40 || st.LongIC < 0.99 || st.ShortIC > -0.99 {
		t.Fatalf("unexpected %+v", st)
	}
	if st.Verdict != "有初步证据（仍需更长样本）" && st.LongTopNet <= 0 {
		t.Logf("net after costs non-positive is acceptable: %v", st.LongTopNet)
	}
	// 4h horizon samples every 4th hour only (non-overlapping)
	if s4 := Evaluate(rows, 4, func(id string, t int64) (float64, bool) { return fwd[id], true }); s4.CrossSects != 10 {
		t.Fatalf("4h cross-sections = %d, want 10", s4.CrossSects)
	}
}
