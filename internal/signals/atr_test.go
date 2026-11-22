package signals

import (
	"math"
	"strings"
	"testing"
)

// bruteATR is the mean true range (percent of the previous close) of the 28 bars of w minutes ending
// at m, m − w, …, written independently of atrPct (index lookups; a bar with a missing minute is skipped).
func bruteATR(bars []Bar, idx map[int64]int, m int64, w int) (float64, bool) {
	step := int64(w) * 60_000
	sum, n := 0.0, 0
	for k := int64(0); k < 28; k++ {
		end := m - k*step
		p, ok := idx[end-step]
		if !ok {
			continue
		}
		hi, lo, whole := bars[p].C, bars[p].C, true
		for t := end - step + 60_000; t <= end; t += 60_000 {
			i, ok := idx[t]
			if !ok {
				whole = false
				break
			}
			hi, lo = math.Max(hi, bars[i].H), math.Min(lo, bars[i].L)
		}
		if whole {
			sum += (hi - lo) / bars[p].C * 100
			n++
		}
	}
	if n < 21 {
		return 0, false
	}
	return sum / float64(n), true
}

// atrX: each coin fires at max(0.5 %, atrX × its own ATR). On real history the replay must equal a
// brute force of that rule; the calm majors fire (a fixed 3 % never reaches them), the wild coin does
// not fire more than they do by an order of magnitude, and no move under the floor fires.
func TestPoolMoveInATR(t *testing.T) {
	h, from, to := fixtureHistory(t)
	const pct, x, w, cool = 3.0, 2.5, 15, 30
	raw := `{"type":"pool","metric":"move","windowMin":15,"pct":3,"atrX":2.5}`
	rep, err := ReplaySpec(poolSpec(raw), h, from, to, cool)
	if err != nil {
		t.Fatal(err)
	}
	ratio := func(bars []Bar, idx map[int64]int, m int64) (float64, bool) { // |move| / the coin's threshold
		v, ok := bruteMove(bars, idx, m, w)
		if !ok {
			return 0, false
		}
		need := pct
		if atr, ok := bruteATR(bars, idx, m-int64(w)*60_000, w); ok {
			need = math.Max(0.5, x*atr)
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
	lo, hi := math.MaxInt, 0
	for id, n := range byInst {
		if rep.ByInst[id] != n {
			t.Fatalf("%s: replay %d, brute force %d", id, rep.ByInst[id], n)
		}
		lo, hi = min(lo, n), max(hi, n)
	}
	plain, _ := ReplaySpec(poolSpec(`{"type":"pool","metric":"move","windowMin":15,"pct":3}`), h, from, to, cool)
	if plain.ByInst[btc] != 0 || rep.ByInst[btc] == 0 || len(rep.ByInst) < 4 {
		t.Fatalf("a fixed 3%% is silent on BTC; in ATR the majors fire too: plain %v, atrX %v", plain.ByInst, rep.ByInst)
	}
	if hi > 6*lo {
		t.Fatalf("fires per coin are not comparable: %v", rep.ByInst)
	}
	said := false
	for _, f := range rep.Fires {
		for _, hit := range f.Hits {
			if math.Abs(hit.Value) < 0.5 {
				t.Fatalf("a move under the 0.5%% floor fired: %+v", hit)
			}
			said = said || strings.Contains(hit.Text, "是它 ATR")
		}
	}
	if !said {
		t.Fatal("no hit says how large the move is in the coin's ATR")
	}
	if d := poolSpec(raw).describePool(); !strings.Contains(d, "自身 ATR 的 2.5 倍") {
		t.Fatalf("description %q", d)
	}
	for _, bad := range []string{
		`{"type":"pool","metric":"move","windowMin":15,"pct":3,"atrX":0.5}`,  // below one ATR is every bar
		`{"type":"pool","metric":"move","windowMin":60,"pct":3,"atrX":2.5}`,  // 28 such bars are not kept in memory
		`{"type":"pool","metric":"range","windowMin":15,"pct":3,"atrX":2.5}`, // move only
	} {
		if _, err := ReplaySpec(poolSpec(bad), h, from, to, cool); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
