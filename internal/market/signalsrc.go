package market

import (
	"sort"
	"time"

	"crypto-trader-go/internal/store"
)

// ---------- reads for pool-wide signals (internal/signals pool type) ----------

// PoolMembers lists the instruments of a signal scope: "pool" = the active pool (top > 0: ranks
// ≤ top), "watch" = the watchlist. Unknown scopes are empty.
func (e *Engine) PoolMembers(scope string, top int) []string {
	var out []string
	switch scope {
	case "pool":
		for _, r := range e.snap.Load().Rows {
			if r.Active && (top <= 0 || r.Rank <= top) {
				out = append(out, r.InstID)
			}
		}
	case "watch":
		e.mu.RLock()
		for id := range e.watch {
			out = append(out, id)
		}
		e.mu.RUnlock()
		sort.Strings(out)
	}
	return out
}

// Recent1m returns up to the last n confirmed 1m bars held in memory, oldest first (no DB read: a
// pool scan touches every member each minute). Gaps are simply absent.
func (e *Engine) Recent1m(id string, n int) []store.Bar {
	e.mu.RLock()
	defer e.mu.RUnlock()
	in := e.insts[id]
	if in == nil || in.lastBar == 0 || n <= 0 {
		return nil
	}
	n = min(n, keep1m)
	out := make([]store.Bar, 0, n)
	for ts := in.lastBar - int64(n-1)*minuteMs; ts <= in.lastBar; ts += minuteMs {
		if b, ok := in.bars.get(ts); ok {
			out = append(out, b)
		}
	}
	return out
}

// Bars1m returns confirmed 1m bars with open time in [from, to), oldest first: the database plus the
// in-memory ring (replays of signal conditions).
func (e *Engine) Bars1m(id string, from, to int64) []store.Bar {
	base := map[int64]store.Bar{}
	if db, err := e.st.Bars("candles_1m", id, from, to-1); err == nil {
		for _, b := range db {
			base[b.Ts] = b
		}
	}
	e.mu.RLock()
	if in := e.insts[id]; in != nil {
		in.bars.each(from, func(b store.Bar) {
			if b.Ts < to {
				base[b.Ts] = b
			}
		})
	}
	e.mu.RUnlock()
	out := make([]store.Bar, 0, len(base))
	for _, b := range base {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ts < out[j].Ts })
	return out
}

// LastConfirmedMinute is the open time of the newest confirmed pool minute (0 before the first).
func (e *Engine) LastConfirmedMinute() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.lastMin == 0 {
		return time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs
	}
	return e.lastMin
}
