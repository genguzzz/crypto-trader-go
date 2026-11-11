package signals

import (
	"compress/gzip"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/store"
)

// Real OKX history for the replay tests: 1m candles of 10 perpetuals, 2026-10-30 12:00 – 10-31 18:00
// UTC+8 (testdata/okx-1m-20261030.csv.gz, shared with strategies/tests). ENA and PEPE move ≥ 2% in
// 15 minutes many times; the majors never do, so thresholds both fire and stay quiet.

const fixtureFile = "../../testdata/okx-1m-20261030.csv.gz"

// fixtureCandle is a full 1m candle (scripts need open and volumes too).
type fixtureCandle struct {
	T                   int64
	O, H, L, C, Vol, VQ float64
}

var fixtureOnce struct {
	sync.Once
	bars map[string][]fixtureCandle
	ids  []string
	err  error
}

func loadFixture(t *testing.T) (map[string][]fixtureCandle, []string) {
	t.Helper()
	fixtureOnce.Do(func() {
		f, err := os.Open(fixtureFile)
		if err != nil {
			fixtureOnce.err = err
			return
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			fixtureOnce.err = err
			return
		}
		r := csv.NewReader(gz)
		if _, err := r.Read(); err != nil {
			fixtureOnce.err = err
			return
		}
		out := map[string][]fixtureCandle{}
		for {
			rec, err := r.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fixtureOnce.err = err
				return
			}
			n := func(i int) float64 { v, _ := strconv.ParseFloat(rec[i], 64); return v }
			ts, _ := strconv.ParseInt(rec[1], 10, 64)
			out[rec[0]] = append(out[rec[0]], fixtureCandle{T: ts, O: n(2), H: n(3), L: n(4), C: n(5), Vol: n(6), VQ: n(7)})
		}
		for id := range out {
			sort.Slice(out[id], func(i, j int) bool { return out[id][i].T < out[id][j].T })
			fixtureOnce.ids = append(fixtureOnce.ids, id)
		}
		sort.Strings(fixtureOnce.ids)
		fixtureOnce.bars = out
	})
	if fixtureOnce.err != nil {
		t.Fatalf("fixture %s: %v", filepath.Base(fixtureFile), fixtureOnce.err)
	}
	return fixtureOnce.bars, fixtureOnce.ids
}

// fixtureHistory is the fixture as a replay History (members in id order).
func fixtureHistory(t *testing.T) (*History, int64, int64) {
	bars, ids := loadFixture(t)
	h := &History{Bars: map[string][]Bar{}, Members: ids}
	var from, to int64
	for _, id := range ids {
		for _, c := range bars[id] {
			h.Bars[id] = append(h.Bars[id], Bar{T: c.T, H: c.H, L: c.L, C: c.C})
		}
		from, to = bars[id][0].T, bars[id][len(bars[id])-1].T+60_000
	}
	return h, from, to
}

// liveFake drives the real Service on the fixture: data from a histSource as of its clock, the agent
// and the pushes recorded.
type liveFake struct {
	*histSource
	mu      sync.Mutex
	members []string // nil = the history's members
	woken   []string
	notes   []string
}

func (f *liveFake) Members(scope string, top int) []string {
	f.mu.Lock()
	m := f.members
	f.mu.Unlock()
	if m != nil {
		return m
	}
	return f.histSource.Members(scope, top)
}
func (f *liveFake) Wake(chat int64, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.woken = append(f.woken, msg)
	return nil
}
func (f *liveFake) Start(string, string, string) (int64, error) { return 99, nil }

// Bars1m: replays see the bars closed by the clock.
func (f *liveFake) Bars1m(id string, from, to int64) []Bar {
	var out []Bar
	for _, b := range f.h.Bars[id] {
		if b.T >= from && b.T < to && b.T+60_000 <= f.now {
			out = append(out, b)
		}
	}
	return out
}
func (f *liveFake) ChatTitle(chat int64) (string, bool) { return "扫描对话", chat == 12 }
func (f *liveFake) clock() time.Time                    { return time.UnixMilli(f.now) }

// newLiveEnv is a Service on the fixture with its clock at the close of the minute `at` (bar open).
func newLiveEnv(t *testing.T, at int64, opts Options) (*Service, *liveFake, *store.Store) {
	t.Helper()
	h, _, _ := fixtureHistory(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	f := &liveFake{histSource: newHistSource(h)}
	f.now = at + 60_000
	opts.Now, opts.Logf = f.clock, func(string, ...any) {}
	s, err := New(st, f, opts)
	if err != nil {
		t.Fatal(err)
	}
	s.SetWaker(f)
	s.SetNotifier(func(text, link string) { f.mu.Lock(); f.notes = append(f.notes, text); f.mu.Unlock() })
	return s, f, st
}

// minuteAt moves the clock to the close of minute m and runs that confirmed-minute event.
func (f *liveFake) minuteAt(s *Service, m int64) {
	f.mu.Lock()
	f.now = m + 60_000
	f.mu.Unlock()
	s.expireDue()
	s.evaluate(event{kind: "minute", minute: m})
}

// ---------- brute force: what a pool scan should fire, computed straight from the bars ----------

type expFire struct {
	T     int64
	Insts []string
}

// bruteMove/bruteRange compute each member's value at minute m (bar open) independently of pool.go.
func bruteMove(bars []Bar, idx map[int64]int, m int64, w int) (float64, bool) {
	i, ok := idx[m]
	j, ok2 := idx[m-int64(w)*60_000]
	if !ok || !ok2 {
		return 0, false
	}
	return (bars[i].C/bars[j].C - 1) * 100, true
}

func bruteRange(bars []Bar, idx map[int64]int, m int64, w int) (float64, bool) {
	if _, ok := idx[m]; !ok {
		return 0, false
	}
	hi, lo, n := 0.0, 1e300, 0
	for k := 0; k < w; k++ {
		if i, ok := idx[m-int64(k)*60_000]; ok {
			hi, lo, n = max(hi, bars[i].H), min(lo, bars[i].L), n+1
		}
	}
	if n < max(1, w*4/5) {
		return 0, false
	}
	return (hi/lo - 1) * 100, true
}

// brutePool replays the edge rule by hand: a member's first known value primes it, then it fires on
// false→true when its last fire is at least cooldown ago; fires of one minute are grouped.
func brutePool(h *History, from, to int64, cooldownMin int, value func(bars []Bar, idx map[int64]int, m int64) (float64, bool), hit func(float64) bool) []expFire {
	state := map[string]int{}
	last := map[string]int64{}
	idxs := map[string]map[int64]int{}
	for _, id := range h.Members {
		idxs[id] = map[int64]int{}
		for i, b := range h.Bars[id] {
			idxs[id][b.T] = i
		}
	}
	var out []expFire
	for m := from; m < to; m += 60_000 {
		now := m + 60_000
		var fired []string
		for _, id := range h.Members {
			v, ok := value(h.Bars[id], idxs[id], m)
			if !ok {
				continue
			}
			x := 0
			if hit(v) {
				x = 1
			}
			prev, had := state[id]
			state[id] = x
			if !had || prev == 1 || x == 0 {
				continue
			}
			if t, ok := last[id]; ok && now-t < int64(cooldownMin)*60_000 {
				continue
			}
			last[id] = now
			fired = append(fired, id)
		}
		if len(fired) > 0 {
			sort.Strings(fired)
			out = append(out, expFire{T: now, Insts: fired})
		}
	}
	return out
}
