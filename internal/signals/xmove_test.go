package signals

import (
	"math"
	"sort"
	"strings"
	"testing"
)

// bruteTypical is the median absolute change over the non-overlapping windows of the 24 h before m,
// written independently of typicalMove (index lookups instead of a map of closes).
func bruteTypical(bars []Bar, idx map[int64]int, m int64, w int) (float64, bool) {
	step := int64(w) * 60_000
	var moves []float64
	for k := int64(1); (k+1)*step <= 24*3600_000; k++ {
		i, ok := idx[m-k*step]
		j, ok2 := idx[m-(k+1)*step]
		if ok && ok2 {
			moves = append(moves, math.Abs(bars[i].C/bars[j].C-1)*100)
		}
	}
	if len(moves) < 12 {
		return 0, false
	}
	sort.Float64s(moves)
	return moves[len(moves)/2], true
}

// xMove: each coin fires at min(pct, max(0.5 %, xMove × its own typical move)). On real history the
// replay must equal a brute force of that rule, the calm majors must fire (they never reach pct) and
// every pct-only fire must still be there.
func TestPoolMoveRelativeToTheCoin(t *testing.T) {
	h, from, to := fixtureHistory(t)
	const pct, x, w, cool = 3.0, 6.0, 15, 30
	rep, err := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":3,"xMove":6}`), h, from, to, cool)
	if err != nil {
		t.Fatal(err)
	}
	ratio := func(bars []Bar, idx map[int64]int, m int64) (float64, bool) { // |move| / the coin's threshold
		v, ok := bruteMove(bars, idx, m, w)
		if !ok {
			return 0, false
		}
		need := pct
		if typ, ok := bruteTypical(bars, idx, m, w); ok {
			need = math.Min(pct, math.Max(0.5, x*typ))
		}
		return math.Abs(v) / need, true
	}
	want := brutePool(h, from, to, cool, ratio, func(v float64) bool { return v >= 1 })
	byInst := map[string]int{}
	for _, f := range want {
		for _, id := range f.Insts {
			byInst[id]++
		}
	}
	if rep.FireCount != len(want) || len(rep.ByInst) != len(byInst) {
		t.Fatalf("replay %d fires %v, brute force %d %v", rep.FireCount, rep.ByInst, len(want), byInst)
	}
	for id, n := range byInst {
		if rep.ByInst[id] != n {
			t.Fatalf("%s: replay %d, brute force %d", id, rep.ByInst[id], n)
		}
	}
	plain, _ := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":3}`), h, from, to, cool)
	if plain.ByInst[btc] != 0 || rep.ByInst[btc] == 0 {
		t.Fatalf("BTC never moves 3%% in 15 min here but must fire on a move large for it: plain %v, xMove %v", plain.ByInst, rep.ByInst)
	}
	for id, n := range plain.ByInst {
		if rep.ByInst[id] < n {
			t.Fatalf("%s lost fires with xMove: %d < %d", id, rep.ByInst[id], n)
		}
	}
	said := false
	for _, f := range rep.Fires {
		for _, hit := range f.Hits {
			if math.Abs(hit.Value) < 0.5 {
				t.Fatalf("a move under the 0.5%% floor fired: %+v", hit)
			}
			said = said || strings.Contains(hit.Text, "是它常态")
		}
	}
	if !said {
		t.Fatal("no hit says how large the move is for the coin")
	}
	if d := poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":3,"xMove":6}`).describePool(); !strings.Contains(d, "自身常态波动的 6 倍") {
		t.Fatalf("description %q", d)
	}
	for _, bad := range []string{
		`{"type":"pool","metric":"move","windowMin":15,"pct":3,"xMove":1}`,  // not a multiple worth the name
		`{"type":"pool","metric":"move","windowMin":240,"pct":3,"xMove":6}`, // too few windows in a day
		`{"type":"pool","metric":"range","windowMin":15,"pct":3,"xMove":6}`, // move only
	} {
		if _, err := ReplaySpec(poolSpec(bad), h, from, to, cool); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
