package market

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/rules"
	"crypto-trader-go/internal/store"
	"crypto-trader-go/internal/track"
)

// ---------- rules ----------

func (e *Engine) loadRules() error {
	saved, err := e.st.Rules()
	if err != nil {
		return err
	}
	e.rmu.Lock()
	defer e.rmu.Unlock()
	for id, body := range saved {
		var r rules.Rule
		if json.Unmarshal([]byte(body), &r) == nil {
			e.rules[id] = &r
		}
	}
	for _, p := range rules.Presets() {
		if _, ok := e.rules[p.ID]; !ok {
			p := p
			e.rules[p.ID] = &p
			e.st.PutRule(p.ID, p.JSON())
		}
	}
	return nil
}

func (e *Engine) Rules() []rules.Rule {
	e.rmu.RLock()
	defer e.rmu.RUnlock()
	out := make([]rules.Rule, 0, len(e.rules))
	for _, r := range e.rules {
		out = append(out, *r)
	}
	order := map[string]int{}
	for i, p := range rules.Presets() {
		order[p.ID] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Preset != out[j].Preset {
			return out[i].Preset
		}
		if out[i].Preset {
			return order[out[i].ID] < order[out[j].ID]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (e *Engine) Rule(id string) (rules.Rule, bool) {
	e.rmu.RLock()
	defer e.rmu.RUnlock()
	r, ok := e.rules[id]
	if !ok {
		return rules.Rule{}, false
	}
	return *r, true
}

// SaveRule creates a rule or publishes a new version of an existing one. Records of old
// versions are kept (immutable); the new version starts its own live record.
func (e *Engine) SaveRule(r rules.Rule) (rules.Rule, error) {
	if err := r.Validate(); err != nil {
		return r, err
	}
	e.rmu.Lock()
	if old, ok := e.rules[r.ID]; ok {
		r.Version = old.Version + 1
		r.Preset = old.Preset
	} else {
		r.Version, r.Preset = 1, false
	}
	e.rules[r.ID] = &r
	e.rmu.Unlock()
	if err := e.st.PutRule(r.ID, r.JSON()); err != nil {
		return r, err
	}
	e.rescore()
	go e.replayRule(r)
	return r, nil
}

func (e *Engine) DeleteRule(id string) error {
	e.rmu.Lock()
	r, ok := e.rules[id]
	if !ok {
		e.rmu.Unlock()
		return fmt.Errorf("not found")
	}
	if r.Preset {
		e.rmu.Unlock()
		return fmt.Errorf("预置对照规则不可删除（可编辑）")
	}
	delete(e.rules, id)
	e.rmu.Unlock()
	e.st.DeleteRule(id)
	e.rescore()
	return nil
}

func (e *Engine) SetWatch(id string, on bool) error {
	in := e.instFor(id)
	if in == nil && on {
		return fmt.Errorf("unknown instrument")
	}
	e.mu.Lock()
	if on {
		e.watch[id] = true
	} else {
		delete(e.watch, id)
	}
	e.mu.Unlock()
	if err := e.st.SetWatch(id, on); err != nil {
		return err
	}
	e.updateSubscriptions()
	if on {
		e.Touch(id) // download history now; the watch tier keeps it
	}
	e.rescore()
	return nil
}

// ---------- hourly replay (rule records before go-live) ----------

type replayCross struct {
	T    int64
	rows []replayRow
}

type replayRow struct {
	id    string
	vals  features.Values
	pct   map[string]float64
	close float64
}

type replayTable struct {
	from, to int64
	cross    []replayCross
}

// runReplay builds a point-in-time hourly table for the last ReplayHours: pool by rolling 12h
// hourly turnover (> threshold, cap N), features from the same features.Compute on hourly data.
func (e *Engine) runReplay(ctx context.Context) {
	e.replayMsg.Store("拉取 1H OI 历史")
	end := time.Now().UnixMilli() / hourMs * hourMs // decision times are hour closes ≤ now
	start := end - int64(e.conf().ReplayHours)*hourMs
	e.mu.RLock()
	cands := map[string]bool{}
	for id, in := range e.insts {
		if !in.Eligible {
			continue
		}
		for T := start; T <= end; T += hourMs {
			if q, ok := (hourlySeries{in}).Quote(T-12*hourMs, T); ok && q > e.conf().Threshold {
				cands[id] = true
				break
			}
		}
	}
	e.mu.RUnlock()
	ids := make([]string, 0, len(cands))
	for id := range cands {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	log.Printf("replay: %d candidate instruments over %dh", len(ids), e.conf().ReplayHours)
	go e.deepHistory(ctx, ids)
	parallel(ids, 3, func(id string) {
		e.mu.RLock()
		_, have := e.insts[id].oiHourly[start-8*hourMs]
		_, haveEnd := e.insts[id].oiHourly[end-2*hourMs]
		e.mu.RUnlock()
		if have && haveEnd {
			return // already fetched in a previous replay run
		}
		endMs := int64(0)
		for page := 0; page < e.conf().ReplayHours/100+2; page++ {
			xs, err := e.rest.OIHistory(ctx, id, "1H", endMs, 100)
			if err != nil || len(xs) == 0 {
				return
			}
			var persist []store.OISample
			e.mu.Lock()
			for _, x := range xs {
				e.insts[id].oiHourly[x.Ts] = x.OiCcy
				persist = append(persist, store.OISample{InstID: id, Ts: x.Ts, OiCcy: x.OiCcy, OiUsd: x.OiUsd})
			}
			need := len(e.insts[id].fund.settled) == 0
			e.mu.Unlock()
			e.st.PutOI(persist)
			endMs = xs[len(xs)-1].Ts
			if endMs < start-8*hourMs {
				break
			}
			if need && page == 0 {
				e.fetchFundingHistory(ctx, id)
			}
		}
	})
	e.replayMsg.Store("计算历史因子")
	tab := &replayTable{from: start, to: end}
	e.mu.RLock()
	btc := e.insts[BTC]
	for T := start; T <= end; T += hourMs {
		type cand struct {
			id   string
			turn float64
		}
		var pool []cand
		for _, id := range ids {
			if q, ok := (hourlySeries{e.insts[id]}).Quote(T-12*hourMs, T); ok && q > e.conf().Threshold {
				pool = append(pool, cand{id, q})
			}
		}
		sort.Slice(pool, func(a, b int) bool {
			if pool[a].turn != pool[b].turn {
				return pool[a].turn > pool[b].turn
			}
			return pool[a].id < pool[b].id
		})
		if len(pool) > e.conf().TopN {
			pool = pool[:e.conf().TopN]
		}
		cr := replayCross{T: T}
		for _, c := range pool {
			in := e.insts[c.id]
			hs := hourlySeries{in}
			cl, ok := hs.Close(T)
			if !ok {
				continue
			}
			var bs features.Series
			if btc != nil {
				bs = hourlySeries{btc}
			}
			cr.rows = append(cr.rows, replayRow{id: c.id, vals: features.Compute(hs, bs, T), close: cl})
		}
		fillReplayPct(cr.rows)
		if len(cr.rows) > 0 {
			tab.cross = append(tab.cross, cr)
		}
	}
	e.mu.RUnlock()
	e.replayMu.Lock()
	e.replay = tab
	e.replayMu.Unlock()
	for _, r := range e.Rules() {
		e.replayRule(r)
	}
	e.replayMsg.Store(fmt.Sprintf("完成：%d 个整点横截面，%s → %s（UTC）", len(tab.cross),
		time.UnixMilli(start).UTC().Format("01-02 15:04"), time.UnixMilli(end).UTC().Format("01-02 15:04")))
}

func fillReplayPct(rows []replayRow) {
	pools := map[string][]float64{}
	for _, r := range rows {
		for k, v := range r.vals {
			if finite(v) {
				pools[k] = append(pools[k], v)
			}
		}
	}
	fns := map[string]func(float64) float64{}
	for k, xs := range pools {
		fns[k] = features.Percentiles(xs)
	}
	for i := range rows {
		rows[i].pct = map[string]float64{}
		for k, v := range rows[i].vals {
			if fn, ok := fns[k]; ok && finite(v) {
				rows[i].pct[k] = fn(v)
			}
		}
	}
}

// ReplaySpreadBps is the assumed spread for replay rows (no historical book data).
const ReplaySpreadBps = 2.0

func (e *Engine) replayRule(r rules.Rule) {
	e.replayMu.Lock()
	tab := e.replay
	e.replayMu.Unlock()
	if tab == nil || !r.Replayable() {
		return
	}
	var out []store.ScoreRow
	for _, cr := range tab.cross {
		for _, row := range cr.rows {
			pct := func(k string) (float64, bool) { p, ok := row.pct[k]; return p, ok }
			sc := r.Apply(pct, row.vals)
			if sc.Long.Score == nil && sc.Short.Score == nil {
				continue
			}
			out = append(out, store.ScoreRow{Kind: "replay", RuleID: r.ID, RuleVersion: r.Version, Ts: cr.T, InstID: row.id,
				Long: sc.Long.Score, Short: sc.Short.Score, Close: row.close, Spread: ReplaySpreadBps})
		}
	}
	e.st.DeleteScores("replay", r.ID, r.Version)
	if err := e.st.PutScores(out); err != nil {
		log.Printf("replay %s: %v", r.ID, err)
	}
	e.trackMu.Lock()
	e.trackCache = map[string]trackEntry{}
	e.trackMu.Unlock()
}

// ---------- track record ----------

type trackEntry struct {
	at  time.Time
	val TrackReport
}

type TrackReport struct {
	RuleID      string        `json:"ruleId"`
	Version     int           `json:"version"`
	Replayable  bool          `json:"replayable"`
	ReplayState string        `json:"replayState"`
	Live        []track.Stats `json:"live"`
	Replay      []track.Stats `json:"replay"`
	LiveSince   int64         `json:"liveSince"`
}

func (e *Engine) Track(ruleID string) (TrackReport, bool) {
	r, ok := e.Rule(ruleID)
	if !ok {
		return TrackReport{}, false
	}
	key := fmt.Sprintf("%s@%d", r.ID, r.Version)
	e.trackMu.Lock()
	if c, ok := e.trackCache[key]; ok && time.Since(c.at) < time.Minute {
		e.trackMu.Unlock()
		return c.val, true
	}
	e.trackMu.Unlock()
	rep := TrackReport{RuleID: r.ID, Version: r.Version, Replayable: r.Replayable(), ReplayState: e.replayMsg.Load().(string)}
	price := func(id string, t int64) (float64, bool) {
		e.mu.RLock()
		defer e.mu.RUnlock()
		in := e.insts[id]
		if in == nil {
			return 0, false
		}
		return liveSeries{in}.Close(t)
	}
	for _, kind := range []string{"live", "replay"} {
		rows, err := e.st.Scores(kind, r.ID, r.Version)
		if err != nil {
			continue
		}
		var stats []track.Stats
		for _, h := range []int{1, 4, 24} {
			stats = append(stats, track.Evaluate(rows, h, price))
		}
		if kind == "live" {
			rep.Live = stats
			if len(rows) > 0 {
				rep.LiveSince = rows[0].Ts
			}
		} else {
			rep.Replay = stats
		}
	}
	e.trackMu.Lock()
	e.trackCache[key] = trackEntry{time.Now(), rep}
	e.trackMu.Unlock()
	return rep, true
}
