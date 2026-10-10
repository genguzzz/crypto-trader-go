package trade

import (
	"math"
	"sort"
)

// RankByTurnover orders market-scope rows by turnover, descending (ties by instId), and sets
// Rank = position; live (mdlive.MarketRows) and replay share it so both rank the same way.
func RankByTurnover(rows []ScreenRow, turnover []float64) []ScreenRow {
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if turnover[idx[a]] != turnover[idx[b]] {
			return turnover[idx[a]] > turnover[idx[b]]
		}
		return rows[idx[a]].InstID < rows[idx[b]].InstID
	})
	out := make([]ScreenRow, len(rows))
	for i, k := range idx {
		out[i] = rows[k]
		out[i].Rank = i + 1
	}
	return out
}

// SelectScreen filters and ranks screener candidates (shared by the live adapter and the
// replayer). An unscored side stays NaN (null on the wire: unscored, never 0); only finite
// fact-panel values are kept.
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
