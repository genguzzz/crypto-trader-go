package trade

import (
	"math"
	"sort"
)

// SelectScreen filters and ranks screener candidates (shared by the live adapter and the
// replayer). Candidates carry NaN for an unscored side; the returned rows carry 0 there and
// only finite fact-panel values.
func SelectScreen(rows []ScreenRow, q ScreenerQuery) []ScreenRow {
	score := func(r ScreenRow) float64 {
		switch q.Side {
		case "short":
			return r.Short
		case "both": // the stronger side; an unscored side does not count
			if math.IsNaN(r.Long) || r.Short > r.Long {
				return r.Short
			}
		}
		return r.Long
	}
	var out []ScreenRow
	for _, r := range rows {
		if q.Rule != "" {
			v := score(r)
			if math.IsNaN(v) || v < q.MinScore {
				continue
			}
		}
		ok := true
		for k, rg := range q.Filter {
			v, have := r.Values[k]
			if !have || !finite(v) || v < rg[0] || v > rg[1] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	rank := func(r ScreenRow) int {
		if r.Rank <= 0 {
			return math.MaxInt32
		}
		return r.Rank
	}
	sort.SliceStable(out, func(i, j int) bool {
		if q.Rule != "" && score(out[i]) != score(out[j]) {
			return score(out[i]) > score(out[j])
		}
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i].InstID < out[j].InstID
	})
	if q.Top > 0 && len(out) > q.Top {
		out = out[:q.Top]
	}
	for i := range out { // an unscored side stays NaN (null on the wire): unscored, never 0
		if out[i].Values != nil {
			v := make(map[string]float64, len(out[i].Values))
			for k, x := range out[i].Values {
				if finite(x) {
					v[k] = x
				}
			}
			out[i].Values = v
		}
	}
	if out == nil {
		out = []ScreenRow{}
	}
	return out
}
