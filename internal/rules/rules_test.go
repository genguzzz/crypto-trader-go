package rules

import (
	"math"
	"testing"

	"crypto-trader-go/internal/features"
)

func TestApplyWeightsAndMissing(t *testing.T) {
	r := Rule{ID: "t", Name: "t", Long: map[string]float64{"ret_4h": 1, "oi_chg_4h": 1}, Short: map[string]float64{"ret_4h": -1}, Gates: Gates{MaxSpreadBps: 10}}
	pct := map[string]float64{"ret_4h": 100, "oi_chg_4h": 50}
	s := r.Apply(func(k string) (float64, bool) { v, ok := pct[k]; return v, ok }, features.Values{"spread_bps": 3})
	if s.Long.Score == nil || math.Abs(*s.Long.Score-75) > 1e-9 {
		t.Fatalf("long = %v, want 75", s.Long.Score)
	}
	if s.Short.Score == nil || *s.Short.Score != 0 {
		t.Fatalf("short = %v, want 0", s.Short.Score)
	}
	if s.Gate != "PASS" {
		t.Fatalf("gate %s", s.Gate)
	}
	delete(pct, "oi_chg_4h")
	s = r.Apply(func(k string) (float64, bool) { v, ok := pct[k]; return v, ok }, features.Values{"spread_bps": 30})
	if s.Long.Score != nil || len(s.Long.Missing) != 1 {
		t.Fatalf("missing input must leave long unrated: %+v", s.Long)
	}
	if s.Gate == "PASS" {
		t.Fatalf("spread gate should block")
	}
}

func TestValidate(t *testing.T) {
	bad := []Rule{
		{ID: "X", Name: "n", Long: map[string]float64{"ret_4h": 1}},
		{ID: "ok", Name: "n", Long: map[string]float64{"nope": 1}},
		{ID: "ok", Name: "n", Long: map[string]float64{"turnover12h": 1}},
		{ID: "ok", Name: "n"},
	}
	for i, r := range bad {
		if r.Validate() == nil {
			t.Fatalf("case %d should fail", i)
		}
	}
	for _, p := range Presets() {
		if err := p.Validate(); err != nil {
			t.Fatalf("preset %s: %v", p.ID, err)
		}
	}
}
