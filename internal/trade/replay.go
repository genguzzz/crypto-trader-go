package trade

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/store"
)

// replayer is the backtest MarketData over SQLite history. Every read is as of its clock (the
// close of the last replayed base bar): bars that close later, scores stamped later and funding
// settled later are invisible.
//
// Base resolution: 1m bars (candles_1m) when any instance runs below 1H, else 1H bars
// (candles_1h). Factors are computed with features.Compute on 1H bars, OI and settled funding at
// the last hour close (the replayable fields only); Score and Screener read the hourly
// score_snapshots (live record preferred over replay) and the per-minute pool membership
// (universe_snapshots); a market-scope Screener ranks the crypto swaps by the 1H quote volume of
// the 12 complete hours before the clock.
type replayer struct {
	st       *store.Store
	db       *sql.DB
	specs    func(string) (Spec, bool)
	from, to int64
	base     int64
	table    string

	mu        sync.RWMutex
	now       int64
	bars      map[string]*barSeries // base bars
	fine      map[string]*barSeries // 1m bars for timeframes below an hourly base (read on demand)
	hourly    map[string]*barSeries // 1H bars for factors
	oi        map[string][]store.OISample
	funding   map[string][]store.FundingSample // settled, ascending
	scores    map[string]*scoreSet
	crypto    []string    // crypto swaps with 1H history (market screener), nil until loaded
	market    []ScreenRow // last full market ranking (kept across a gap in candles_1h)
	marketAt  int64       // replay clock of that ranking
	marketEnd int64       // window end of the last computed result (the ranking changes once per hour)
	marketRes []ScreenRow // that result
}

type barSeries struct {
	from int64 // loaded range start
	bars []Bar // ascending
}

type scoreRow struct {
	long, short *float64
	close       float64
}

type scoreSet struct {
	ts   []int64
	rows map[int64]map[string]scoreRow
}

func newReplayer(st *store.Store, specs func(string) (Spec, bool), from, to, base int64) *replayer {
	table := "candles_1m"
	if base == hourMs {
		table = "candles_1h"
	}
	return &replayer{st: st, db: st.DB, specs: specs, from: from, to: to, base: base, table: table, now: from,
		bars: map[string]*barSeries{}, fine: map[string]*barSeries{}, hourly: map[string]*barSeries{}, oi: map[string][]store.OISample{},
		funding: map[string][]store.FundingSample{}, scores: map[string]*scoreSet{}}
}

func (r *replayer) setNow(t int64) {
	r.mu.Lock()
	r.now = t
	r.mu.Unlock()
}

func (r *replayer) Now() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.now
}

func (r *replayer) Spec(id string) (Spec, bool) {
	if r.specs == nil {
		return Spec{}, false
	}
	return r.specs(id)
}

func (r *replayer) Keep(string, bool) {}

func (r *replayer) warm() int64 {
	if r.base == minuteMs {
		return 3 * dayMs
	}
	return 60 * dayMs
}

// series returns an instrument's bars of a table loaded from `need` (extending the cache).
// Caller holds r.mu (write).
func (r *replayer) series(cache map[string]*barSeries, table, id string, need int64) *barSeries {
	s := cache[id]
	if s == nil {
		lo := min(need, r.from-r.warm())
		s = &barSeries{from: lo, bars: r.load(table, id, lo, r.to)}
		cache[id] = s
		return s
	}
	if need < s.from {
		older := r.load(table, id, need, s.from-1)
		s.bars = append(older, s.bars...)
		s.from = need
	}
	return s
}

func (r *replayer) load(table, id string, from, to int64) []Bar {
	xs, err := r.st.Bars(table, id, from, to)
	if err != nil {
		return nil
	}
	out := make([]Bar, len(xs))
	for i, x := range xs {
		out[i] = Bar{Ts: x.Ts, O: x.O, H: x.H, L: x.L, C: x.C, Vol: x.Vol, VolQ: x.Quote}
	}
	return out
}

// coverage counts base bars of an instrument inside the replay range.
func (r *replayer) coverage(id string) int {
	r.mu.Lock()
	s := r.series(r.bars, r.table, id, r.from)
	r.mu.Unlock()
	n := 0
	for _, b := range s.bars {
		if b.Ts >= r.from && b.Ts < r.to {
			n++
		}
	}
	return n
}

// barAt returns the base bar opening at t.
func (r *replayer) barAt(id string, t int64) (Bar, bool) {
	r.mu.Lock()
	s := r.series(r.bars, r.table, id, r.from)
	r.mu.Unlock()
	i := sort.Search(len(s.bars), func(i int) bool { return s.bars[i].Ts >= t })
	if i < len(s.bars) && s.bars[i].Ts == t {
		return s.bars[i], true
	}
	return Bar{}, false
}

// lastClose is the close of the newest base bar closed by the clock.
func (r *replayer) lastClose(id string) (float64, bool) {
	r.mu.Lock()
	s := r.series(r.bars, r.table, id, r.from)
	now := r.now
	r.mu.Unlock()
	i := sort.Search(len(s.bars), func(i int) bool { return s.bars[i].Ts+r.base > now }) - 1
	if i < 0 || now-(s.bars[i].Ts+r.base) > 3*dayMs {
		return 0, false
	}
	return s.bars[i].C, true
}

func (r *replayer) Quote(_ context.Context, id string) (Quote, error) {
	c, ok := r.lastClose(id)
	if !ok {
		return Quote{}, fmt.Errorf("%s 在 %s 前没有 K 线", id, fmtTime(r.Now()))
	}
	half := c * barSpreadBp / 2 / 1e4
	return Quote{Last: c, Bid: c - half, Ask: c + half, Mark: c, Ts: r.Now()}, nil
}

func (r *replayer) Bars(id, tf string, n int) ([]Bar, error) {
	step, ok := TFMillis(tf)
	if !ok {
		return nil, fmt.Errorf("timeframe must be one of 1m,5m,15m,1H,4H,1D")
	}
	n = max(1, min(n, 1000))
	r.mu.Lock()
	now := r.now
	end := now / step * step
	from := end - int64(n)*step
	var s *barSeries
	if step < r.base { // e.g. 15m bars in a 1H backtest: from 1m history, as far as it reaches
		s = r.series(r.fine, "candles_1m", id, from)
	} else {
		s = r.series(r.bars, r.table, id, from)
	}
	r.mu.Unlock()
	lo := sort.Search(len(s.bars), func(i int) bool { return s.bars[i].Ts >= from })
	var out []Bar
	for _, b := range s.bars[lo:] {
		if b.Ts >= end {
			break
		}
		bucket := b.Ts / step * step
		k := len(out)
		if k == 0 || out[k-1].Ts != bucket {
			out = append(out, Bar{Ts: bucket, O: b.O, H: b.H, L: b.L, C: b.C, Vol: b.Vol, VolQ: b.VolQ})
			continue
		}
		c := &out[k-1]
		c.H, c.L, c.C = math.Max(c.H, b.H), math.Min(c.L, b.L), b.C
		c.Vol += b.Vol
		c.VolQ += b.VolQ
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// ---------- factors (replayable fields at the last hour close) ----------

type replaySeries struct {
	r  *replayer
	id string
	h  *barSeries
}

func (x replaySeries) bar(open int64) (Bar, bool) {
	bs := x.h.bars
	i := sort.Search(len(bs), func(i int) bool { return bs[i].Ts >= open })
	if i < len(bs) && bs[i].Ts == open {
		return bs[i], true
	}
	return Bar{}, false
}

func (x replaySeries) Close(t int64) (float64, bool) {
	if t%hourMs != 0 {
		return 0, false
	}
	b, ok := x.bar(t - hourMs)
	return b.C, ok
}

func (x replaySeries) Quote(from, to int64) (float64, bool) {
	if from%hourMs != 0 || to%hourMs != 0 {
		return 0, false
	}
	sum := 0.0
	for ts := from; ts < to; ts += hourMs {
		b, ok := x.bar(ts)
		if !ok {
			return 0, false
		}
		sum += b.VolQ
	}
	return sum, true
}

func (x replaySeries) OI(t int64) (float64, bool) {
	xs := x.r.oi[x.id]
	i := sort.Search(len(xs), func(i int) bool { return xs[i].Ts > t }) - 1
	if i >= 0 && t-xs[i].Ts <= 2*hourMs {
		return xs[i].OiCcy, true
	}
	return 0, false
}

func (x replaySeries) FundingSettled(t int64) (float64, float64, bool) {
	xs := x.r.funding[x.id]
	i := sort.Search(len(xs), func(i int) bool { return xs[i].FundingTime > t }) - 1
	if i < 1 {
		return 0, 0, false
	}
	iv := float64(xs[i].FundingTime-xs[i-1].FundingTime) / float64(hourMs)
	if iv <= 0 {
		return 0, 0, false
	}
	return xs[i].Rate, iv, true
}

// prepare loads an instrument's 1H bars, OI and settled funding. Caller holds r.mu (write).
func (r *replayer) prepare(id string) replaySeries {
	h := r.series(r.hourly, "candles_1h", id, r.from-30*dayMs)
	if _, ok := r.oi[id]; !ok {
		xs, _ := r.st.OI(id, r.from-30*dayMs, r.to)
		if xs == nil {
			xs = []store.OISample{}
		}
		r.oi[id] = xs
	}
	r.loadFunding(id)
	return replaySeries{r: r, id: id, h: h}
}

func (r *replayer) loadFunding(id string) {
	if _, ok := r.funding[id]; ok {
		return
	}
	xs, _ := r.st.Funding(id, r.from-60*dayMs, r.to)
	out := []store.FundingSample{}
	for _, x := range xs {
		if x.Settled {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FundingTime < out[j].FundingTime })
	r.funding[id] = out
}

func (r *replayer) Factors(id string) (map[string]float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.now / hourMs * hourMs
	s := r.prepare(id)
	var btc features.Series
	if id != market.BTC {
		btc = r.prepare(market.BTC)
	}
	v := features.Compute(s, btc, t)
	out := map[string]float64{}
	for k, x := range v {
		if finite(x) {
			out[k] = x
		}
	}
	return out, len(out) > 0
}

// ---------- scores, screener, funding ----------

// scoreAt returns a rule's score cross-section stamped at the newest hour ≤ now (within 2 h).
func (r *replayer) scoreAt(rule string) (map[string]scoreRow, int64, error) {
	r.mu.Lock()
	set := r.scores[rule]
	if set == nil {
		var err error
		if set, err = r.loadScores(rule); err != nil {
			r.mu.Unlock()
			return nil, 0, err
		}
		r.scores[rule] = set
	}
	now := r.now
	r.mu.Unlock()
	i := sort.Search(len(set.ts), func(i int) bool { return set.ts[i] > now }) - 1
	if i < 0 || now-set.ts[i] > 2*hourMs {
		return nil, 0, fmt.Errorf("缺少规则 %s 在 %s 前 2 小时内的评分快照，无法回放", rule, fmtTime(now))
	}
	return set.rows[set.ts[i]], set.ts[i], nil
}

func (r *replayer) loadScores(rule string) (*scoreSet, error) {
	var ver sql.NullInt64
	r.db.QueryRow(`SELECT MAX(rule_version) FROM score_snapshots WHERE rule_id=? AND ts>=? AND ts<=?`, rule, r.from-2*hourMs, r.to).Scan(&ver)
	set := &scoreSet{rows: map[int64]map[string]scoreRow{}}
	if !ver.Valid {
		return set, nil
	}
	rows, err := r.db.Query(`SELECT kind, ts, inst_id, long, short, close FROM score_snapshots WHERE rule_id=? AND rule_version=? AND ts>=? AND ts<=? ORDER BY ts`,
		rule, ver.Int64, r.from-2*hourMs, r.to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[int64]map[string]bool{}
	for rows.Next() {
		var kind, id string
		var ts int64
		var l, s sql.NullFloat64
		var c float64
		if err := rows.Scan(&kind, &ts, &id, &l, &s, &c); err != nil {
			return nil, err
		}
		m := set.rows[ts]
		if m == nil {
			m = map[string]scoreRow{}
			set.rows[ts] = m
			set.ts = append(set.ts, ts)
			live[ts] = map[string]bool{}
		}
		if kind != "live" && live[ts][id] {
			continue // the live record wins over the replay estimate
		}
		row := scoreRow{close: c}
		if l.Valid {
			v := l.Float64
			row.long = &v
		}
		if s.Valid {
			v := s.Float64
			row.short = &v
		}
		m[id] = row
		if kind == "live" {
			live[ts][id] = true
		}
	}
	return set, rows.Err()
}

func (r *replayer) Score(rule, id string) (float64, float64, bool) {
	rows, _, err := r.scoreAt(rule)
	if err != nil {
		return 0, 0, false
	}
	x, ok := rows[id]
	if !ok || (x.long == nil && x.short == nil) {
		return 0, 0, false
	}
	l, s := math.NaN(), math.NaN() // an unscored side
	if x.long != nil {
		l = *x.long
	}
	if x.short != nil {
		s = *x.short
	}
	return l, s, true
}

// poolAt returns the active pool (by rank) recorded within 5 minutes before now.
func (r *replayer) poolAt(now int64) ([]ScreenRow, error) {
	var ts sql.NullInt64
	r.db.QueryRow(`SELECT MAX(ts) FROM universe_snapshots WHERE ts<=? AND ts>?`, now, now-5*minuteMs).Scan(&ts)
	if !ts.Valid {
		return nil, nil
	}
	rows, err := r.db.Query(`SELECT inst_id, rank FROM universe_snapshots WHERE ts=? ORDER BY rank`, ts.Int64)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScreenRow
	for rows.Next() {
		x := ScreenRow{Long: math.NaN(), Short: math.NaN()}
		if err := rows.Scan(&x.InstID, &x.Rank); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// cryptoIDs lists the crypto swaps with stored 1H candles (once per run; instruments without a
// known spec cannot be traded and are left out).
func (r *replayer) cryptoIDs() ([]string, error) {
	r.mu.RLock()
	cached := r.crypto
	r.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	if r.specs == nil {
		return nil, fmt.Errorf("缺少合约规格，无法按品类筛选全市场")
	}
	ids, err := r.st.InstIDs("candles_1h") // a full scan: done without holding r.mu
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, id := range ids {
		if sp, ok := r.specs(id); ok && sp.Category == CategoryCrypto {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if len(out) > 0 { // an empty list may mean the catalog was not loaded yet: ask again next time
		r.mu.Lock()
		r.crypto = out
		r.mu.Unlock()
	}
	return out, nil
}

// marketGapMs is how long a market ranking is kept across a gap in the 1H history.
const marketGapMs = 24 * hourMs

// marketRank ranks the crypto swaps by USDT turnover over the 12 complete hours before now
// (candles_1h quote volume; an instrument missing any of the 12 hours is left out — a gap, not
// zero), descending.
func (r *replayer) marketRank(now int64) ([]ScreenRow, error) {
	ids, err := r.cryptoIDs()
	if err != nil {
		return nil, err
	}
	end := now / hourMs * hourMs
	r.mu.Lock()
	if r.marketRes != nil && r.marketEnd == end {
		res := append([]ScreenRow(nil), r.marketRes...)
		r.mu.Unlock()
		return res, nil
	}
	r.mu.Unlock()
	var out []ScreenRow
	var turn []float64
	// one primary-key range per instrument (candles_1h has no ts index)
	stmt, err := r.db.Prepare(`SELECT COUNT(quote), COALESCE(SUM(quote), 0) FROM candles_1h WHERE inst_id=? AND ts>=? AND ts<?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, id := range ids {
		var n int
		var v float64
		if err := stmt.QueryRow(id, end-12*hourMs, end).Scan(&n, &v); err != nil {
			return nil, err
		}
		if n == 12 && v > 0 {
			out = append(out, ScreenRow{InstID: id, Long: math.NaN(), Short: math.NaN()})
			turn = append(turn, v)
		}
	}
	r.mu.Lock()
	last, lastAt := r.market, r.marketAt
	r.mu.Unlock()
	// A gap in the stored 1H history (recorder downtime) leaves out every instrument missing an
	// hour: when fewer than half of the last ranking remain, keep that ranking for up to a day,
	// then run with an empty universe until data resumes, rather than failing the run. Only a run
	// that has never had a ranking fails.
	if len(out) < len(last)/2 {
		if now-lastAt <= marketGapMs {
			return r.keepMarket(end, last), nil
		}
		if len(out) == 0 {
			return r.keepMarket(end, []ScreenRow{}), nil
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("缺少 %s 前 12 小时的 1H K 线，无法按成交额回放全市场", fmtTime(end))
	}
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if turn[idx[a]] != turn[idx[b]] {
			return turn[idx[a]] > turn[idx[b]]
		}
		return out[idx[a]].InstID < out[idx[b]].InstID
	})
	ranked := make([]ScreenRow, len(out))
	for i, k := range idx {
		ranked[i] = out[k]
		ranked[i].Rank = i + 1
	}
	r.mu.Lock()
	r.market, r.marketAt = ranked, now
	r.mu.Unlock()
	return r.keepMarket(end, ranked), nil
}

// keepMarket caches the result for the window ending at end and returns a copy.
func (r *replayer) keepMarket(end int64, rows []ScreenRow) []ScreenRow {
	r.mu.Lock()
	r.marketEnd, r.marketRes = end, rows
	r.mu.Unlock()
	return append([]ScreenRow(nil), rows...)
}

func (r *replayer) Screener(q ScreenerQuery) ([]ScreenRow, error) {
	now := r.Now()
	if q.Market() {
		if q.Rule != "" {
			return nil, fmt.Errorf("全市场筛选不能指定评分规则")
		}
		rows, err := r.marketRank(now)
		if err != nil {
			return nil, err
		}
		q = q.CappedTop()
		if len(q.Filter) > 0 {
			// rows are in rank order: compute factors only until Top rows pass the filter
			var kept []ScreenRow
			for i := range rows {
				rows[i].Values, _ = r.Factors(rows[i].InstID)
				if len(SelectScreen(rows[i:i+1], q)) == 1 {
					kept = append(kept, rows[i])
					if q.Top > 0 && len(kept) >= q.Top {
						break
					}
				}
			}
			rows = kept
		}
		out := SelectScreen(rows, q)
		for i := range out { // only the selected rows: loading every candidate's bars is costly
			if c, ok := r.lastClose(out[i].InstID); ok {
				out[i].Last = c
			}
		}
		return out, nil
	}
	pool, err := r.poolAt(now)
	if err != nil {
		return nil, err
	}
	var scores map[string]scoreRow
	if q.Rule != "" {
		if scores, _, err = r.scoreAt(q.Rule); err != nil {
			return nil, err
		}
	}
	if len(pool) == 0 {
		if scores == nil {
			return nil, fmt.Errorf("缺少 %s 的池快照（universe_snapshots），无法回放动态池；可指定评分规则改用整点评分横截面", fmtTime(now))
		}
		for id := range scores {
			pool = append(pool, ScreenRow{InstID: id, Long: math.NaN(), Short: math.NaN()})
		}
		sort.Slice(pool, func(i, j int) bool { return pool[i].InstID < pool[j].InstID })
	}
	for i := range pool {
		x := &pool[i]
		if c, ok := r.lastClose(x.InstID); ok {
			x.Last = c
		}
		if sc, ok := scores[x.InstID]; ok {
			if sc.long != nil {
				x.Long = *sc.long
			}
			if sc.short != nil {
				x.Short = *sc.short
			}
			if x.Last == 0 {
				x.Last = sc.close
			}
		}
		if len(q.Filter) > 0 {
			x.Values, _ = r.Factors(x.InstID)
		}
	}
	return SelectScreen(pool, q), nil
}

func (r *replayer) FundingRate(id string, at int64) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if at > r.now {
		return 0, false
	}
	r.loadFunding(id)
	xs := r.funding[id]
	i := sort.Search(len(xs), func(i int) bool { return xs[i].FundingTime >= at })
	if i < len(xs) && xs[i].FundingTime == at {
		return xs[i].Rate, true
	}
	return 0, false
}

func fmtTime(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC") }
