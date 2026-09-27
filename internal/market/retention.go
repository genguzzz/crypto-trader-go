package market

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// ---------- retention: tiers × per-table ages × storage cap ----------
//
// Every instrument falls into one tier (highest wins). Each stored series (1m candles, 1H candles,
// OI samples, funding) has its own age per tier and its own share of the storage cap, because their
// row rates differ by orders of magnitude. The job first applies the ages, then — if a table still
// exceeds its share — tightens ages tier by tier, lowest tier first, never below the tier's floor.

type Tier int

const (
	TierNone   Tier = iota // not recorded: delisted / category excluded, not viewed recently
	TierBase               // recorded by the full-pool feed but not in the active pool
	TierViewed             // detail opened within ViewedFor
	TierPool               // in the active pool now or within PoolGrace
	TierWatch              // watchlist
	nTiers
)

var tierNames = [nTiers]string{"none", "base", "viewed", "pool", "watch"}

const (
	day            = 24 * time.Hour
	PoolGrace      = 7 * day          // left the pool less than this ago → still pool tier
	ViewedFor      = 3 * day          // detail opened less than this ago → viewed tier
	RecordViewed   = 30 * time.Minute // adhoc (non-pool-universe) instruments stream live this long after a view
	DefaultCapMB   = 2048
	retentionEvery = 30 * time.Minute
	universeKeep   = 30 * day
)

// tableRule is one retention unit. Share is its part of the storage cap; tables sharing a Group
// (the two funding rules) share one budget.
type tableRule struct {
	Name  string // report key
	Table string
	Where string // extra delete condition
	Group string // budget group (= table)
	Share float64
	Age   [nTiers]time.Duration
	Floor [nTiers]time.Duration // capacity pressure never cuts below this
}

var retentionRules = []tableRule{
	{Name: "candles_1m", Table: "candles_1m", Group: "candles_1m", Share: 0.55,
		Age:   [nTiers]time.Duration{0, 2 * day, 7 * day, 7 * day, 30 * day},
		Floor: [nTiers]time.Duration{0, 26 * time.Hour, 26 * time.Hour, 26 * time.Hour, 3 * day}},
	{Name: "oi_samples", Table: "oi_samples", Group: "oi_samples", Share: 0.15,
		Age:   [nTiers]time.Duration{0, 2 * day, 7 * day, 7 * day, 30 * day},
		Floor: [nTiers]time.Duration{0, 26 * time.Hour, 26 * time.Hour, 26 * time.Hour, 3 * day}},
	{Name: "candles_1h", Table: "candles_1h", Group: "candles_1h", Share: 0.15,
		Age:   [nTiers]time.Duration{30 * day, 30 * day, 60 * day, 60 * day, 180 * day},
		Floor: [nTiers]time.Duration{7 * day, 21 * day, 21 * day, 21 * day, 30 * day}},
	{Name: "funding_settled", Table: "funding_samples", Where: "settled=1", Group: "funding_samples", Share: 0.05,
		Age:   [nTiers]time.Duration{30 * day, 90 * day, 90 * day, 90 * day, 365 * day},
		Floor: [nTiers]time.Duration{7 * day, 14 * day, 14 * day, 14 * day, 30 * day}},
	{Name: "funding_obs", Table: "funding_samples", Where: "settled=0", Group: "funding_samples",
		Age:   [nTiers]time.Duration{day, 3 * day, 3 * day, 3 * day, 7 * day},
		Floor: [nTiers]time.Duration{day, day, day, day, day}},
}

// OtherShare is left for rules, score snapshots (never evicted), universe snapshots and settings.
const OtherShare = 0.10

// effAges holds the ages actually in force after capacity pressure (per rule, per tier).
type effAges map[string][nTiers]time.Duration

func defaultAges() effAges {
	m := effAges{}
	for _, r := range retentionRules {
		m[r.Name] = r.Age
	}
	return m
}

// tierOf classifies an instrument. Caller holds e.mu (read).
func (e *Engine) tierOf(id string, now int64) Tier {
	if e.watch[id] {
		return TierWatch
	}
	if t, ok := e.lastPool[id]; ok && now-t < PoolGrace.Milliseconds() {
		return TierPool
	}
	if t, ok := e.views[id]; ok && now-t < ViewedFor.Milliseconds() {
		return TierViewed
	}
	if in := e.insts[id]; in != nil && in.Eligible {
		return TierBase
	}
	return TierNone
}

// keep1mFor is how far back 1m history should be downloaded for a tier (7 days, or less if
// capacity pressure has cut that tier's 1m age) so backfills don't fight the evictor.
func (e *Engine) keep1mFor(t Tier) time.Duration {
	a := e.eff.Load()
	age := retentionRules[0].Age[t]
	if a != nil {
		age = (*a)["candles_1m"][t]
	}
	return min(age, HistoryDays*day)
}

// ---------- report ----------

type TableUsage struct {
	Name   string   `json:"name"`
	Table  string   `json:"table"`
	Bytes  int64    `json:"bytes"`
	Budget int64    `json:"budget"`
	Ages   []string `json:"ages"` // per tier, in force (e.g. "7d"; "—" = not kept)
	Tight  bool     `json:"tight"`
}

type RetentionReport struct {
	At        int64          `json:"at"`
	TookMs    int64          `json:"tookMs"`
	CapBytes  int64          `json:"capBytes"`
	FileBytes int64          `json:"fileBytes"`
	FreeBytes int64          `json:"freeBytes"` // reusable pages inside the file
	UsedBytes int64          `json:"usedBytes"`
	Deleted   map[string]int `json:"deleted"`
	Tables    []TableUsage   `json:"tables"`
	Tiers     map[string]int `json:"tiers"` // instruments per tier
	Tier      []string       `json:"tierNames"`
	Error     string         `json:"error,omitempty"`
}

func (e *Engine) Retention() *RetentionReport { return e.retReport.Load() }

// ---------- job ----------

func (e *Engine) retentionLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	e.runRetention()
	t := time.NewTicker(retentionEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.runRetention()
		case <-e.retKick:
			e.runRetention()
		}
	}
}

// requestRetention runs the job soon (e.g. after the cap was lowered).
func (e *Engine) requestRetention() {
	select {
	case e.retKick <- struct{}{}:
	default:
	}
}

var retRunning atomic.Bool

func (e *Engine) runRetention() {
	// Tiers are only meaningful once the instrument list is loaded; before that every instrument
	// would look untracked and lose its data.
	e.mu.RLock()
	ready := len(e.catalog) > 0 && e.Phase() != "starting"
	e.mu.RUnlock()
	if !ready {
		log.Printf("retention: skipped (instrument list not loaded yet)")
		return
	}
	if !retRunning.CompareAndSwap(false, true) {
		return
	}
	defer retRunning.Store(false)
	start := time.Now()
	now := start.UnixMilli()
	rep := &RetentionReport{At: now, CapBytes: e.conf().StorageCap, Deleted: map[string]int{}, Tiers: map[string]int{}, Tier: tierNames[:]}

	// instruments: everything in memory, on the watch/view/pool lists, and anything left in the DB
	ids := map[string]bool{}
	if time.Since(e.dbIDsAt) > 6*time.Hour {
		e.dbIDs = map[string]bool{}
		for _, tb := range []string{"candles_1m", "candles_1h", "oi_samples", "funding_samples"} {
			xs, err := e.st.InstIDs(tb)
			if err != nil {
				log.Printf("retention: ids %s: %v", tb, err)
				continue
			}
			for _, id := range xs {
				e.dbIDs[id] = true
			}
		}
		e.dbIDsAt = time.Now()
	}
	for id := range e.dbIDs {
		ids[id] = true
	}
	tiers := map[string]Tier{}
	e.mu.Lock()
	for id := range e.insts {
		ids[id] = true
	}
	for id := range e.watch {
		ids[id] = true
	}
	for id, t := range e.lastPool {
		if now-t >= PoolGrace.Milliseconds() {
			delete(e.lastPool, id)
		}
	}
	for id := range ids {
		tiers[id] = e.tierOf(id, now)
		rep.Tiers[tierNames[tiers[id]]]++
	}
	// Instruments are never removed from memory: in-flight backfills/replays hold their IDs, and the
	// in-memory set is bounded by the catalog (same as recording every category).
	e.mu.Unlock()

	eff := defaultAges()
	del := func(r tableRule, t Tier, age time.Duration) int {
		n := 0
		for id, tt := range tiers {
			if tt != t {
				continue
			}
			k, err := e.st.DeleteBefore(r.Table, id, now-age.Milliseconds(), r.Where)
			if err != nil {
				log.Printf("retention: %s %s: %v", r.Name, id, err)
				continue
			}
			n += int(k)
		}
		rep.Deleted[r.Name] += n
		return n
	}
	// 1) ages
	for _, r := range retentionRules {
		for t := Tier(0); t < nTiers; t++ {
			del(r, t, r.Age[t])
		}
	}
	e.st.PruneUniverse(now - universeKeep.Milliseconds())

	// 2) storage cap, per table group; tighten lowest tier first, by thirds, down to the floor
	sizes, err := e.st.TableBytes()
	if err != nil {
		rep.Error = "表空间统计失败：" + err.Error()
		log.Printf("retention: dbstat: %v", err)
	}
	budget := func(group string) int64 {
		sh := 0.0
		for _, r := range retentionRules {
			if r.Group == group {
				sh += r.Share
			}
		}
		return int64(float64(rep.CapBytes) * sh)
	}
	tight := map[string]bool{}
	if err == nil && rep.CapBytes > 0 {
		for _, group := range []string{"candles_1m", "oi_samples", "candles_1h", "funding_samples"} {
			b := budget(group)
			if sizes[group] <= b {
				continue
			}
			target := b * 9 / 10
			tight[group] = true
			for t := Tier(0); t < nTiers && sizes[group] > target; t++ {
				for sizes[group] > target {
					moved := false
					for _, r := range retentionRules {
						if r.Group != group {
							continue
						}
						a := eff[r.Name]
						next := max(r.Floor[t], a[t]*2/3)
						if next >= a[t] {
							continue
						}
						a[t] = next
						eff[r.Name] = a
						del(r, t, next)
						moved = true
					}
					if !moved {
						break
					}
					if s, err := e.st.TableBytes(); err == nil {
						sizes = s
					} else {
						break
					}
				}
			}
			if sizes[group] > b {
				log.Printf("retention: %s still %d MB over its %d MB budget at every floor", group, sizes[group]>>20, b>>20)
			}
		}
	}
	e.eff.Store(&eff)

	// report
	rep.FileBytes, rep.FreeBytes = e.st.FileBytes()
	rep.UsedBytes = rep.FileBytes - rep.FreeBytes
	for _, r := range retentionRules {
		u := TableUsage{Name: r.Name, Table: r.Table, Budget: budget(r.Group), Tight: tight[r.Group]}
		if r.Name == "funding_obs" {
			u.Budget = 0 // shares funding_settled's
		} else {
			u.Bytes = sizes[r.Group]
		}
		for t := Tier(0); t < nTiers; t++ {
			u.Ages = append(u.Ages, fmtAge(eff[r.Name][t]))
		}
		rep.Tables = append(rep.Tables, u)
	}
	var other int64
	for tb, n := range sizes {
		switch tb {
		case "candles_1m", "oi_samples", "candles_1h", "funding_samples":
		default:
			other += n
		}
	}
	rep.Tables = append(rep.Tables, TableUsage{Name: "other", Table: "rules/score_snapshots/universe_snapshots/…", Bytes: other,
		Budget: int64(float64(rep.CapBytes) * OtherShare)})
	rep.TookMs = time.Since(start).Milliseconds()
	e.retReport.Store(rep)
	total := 0
	for _, n := range rep.Deleted {
		total += n
	}
	log.Printf("retention: deleted %d rows, db %d MB (used %d MB, cap %d MB) in %s", total, rep.FileBytes>>20, rep.UsedBytes>>20, rep.CapBytes>>20, time.Since(start).Round(time.Millisecond))
}

func fmtAge(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d%day == 0:
		return fmt.Sprintf("%dd", d/day)
	case d >= 3*day:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
