package market

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// The research's reference implementation (rev-study score.py) on three real 150-bar windows.
func TestMoveRiskMatchesReference(t *testing.T) {
	b, err := os.ReadFile("testdata/moverisk_candles.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct {
		Inst     string      `json:"inst"`
		Side     string      `json:"side"`
		Candles  [][]float64 `json:"candles"`
		Expected struct {
			InState  bool               `json:"in_state"`
			Ext      float64            `json:"ext"`
			Pos30    float64            `json:"pos30"`
			PTail    float64            `json:"p_tail"`
			PBig3    float64            `json:"p_big3"`
			Features map[string]float64 `json:"features"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		cs := make([]Candle, len(v.Candles))
		for i, r := range v.Candles {
			cs[i] = Candle{T: int64(r[0]), O: r[1], H: r[2], L: r[3], C: r[4], Quote: r[5], Confirmed: true}
		}
		r := scoreMoveRisk(cs)
		got := r.Top
		if v.Side == "bottom" {
			got = r.Bottom
		}
		near := func(name string, a, b float64) {
			if math.Abs(a-b) > 1e-9*math.Max(1, math.Abs(b)) {
				t.Errorf("%s %s %s: got %.12g want %.12g", v.Inst, v.Side, name, a, b)
			}
		}
		for k, want := range v.Expected.Features {
			near(k, got.Features[k], want)
		}
		near("ext", got.Ext, v.Expected.Ext)
		near("pos30", got.Pos30, v.Expected.Pos30)
		near("pTail", got.PTail, v.Expected.PTail)
		near("pBig3", got.PBig3, v.Expected.PBig3)
		if got.InState != v.Expected.InState {
			t.Errorf("%s %s inState %v", v.Inst, v.Side, got.InState)
		}
	}
}

func TestInterpU(t *testing.T) {
	cuts := make([]float64, 21)
	for i := range cuts {
		cuts[i] = float64(i) // u = x/20
	}
	for x, want := range map[float64]float64{-3: -0.5, 0: -0.5, 10: 0, 2.5: 0.125 - 0.5, 20: 0.5, 99: 0.5} {
		if got := interpU(x, cuts); math.Abs(got-want) > 1e-12 {
			t.Errorf("interpU(%v) = %v, want %v", x, got, want)
		}
	}
	if interpU(math.NaN(), cuts) != 0 {
		t.Error("NaN → 0")
	}
}
