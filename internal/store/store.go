// Package store persists recorded public market data and user rules in a single SQLite (WAL) file.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS candles_1m(inst_id TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, vol REAL, vol_ccy REAL, quote REAL, PRIMARY KEY(inst_id, ts)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS candles_1h(inst_id TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, vol REAL, vol_ccy REAL, quote REAL, PRIMARY KEY(inst_id, ts)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS oi_samples(inst_id TEXT NOT NULL, ts INTEGER NOT NULL, oi_ccy REAL, oi_usd REAL,
  PRIMARY KEY(inst_id, ts)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS funding_samples(inst_id TEXT NOT NULL, ts INTEGER NOT NULL, rate REAL,
  funding_time INTEGER, next_funding_time INTEGER, settled INTEGER, PRIMARY KEY(inst_id, ts)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS universe_snapshots(ts INTEGER NOT NULL, inst_id TEXT NOT NULL, turnover REAL, rank INTEGER,
  PRIMARY KEY(ts, inst_id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS rules(id TEXT PRIMARY KEY, body TEXT NOT NULL, updated_at INTEGER);
CREATE TABLE IF NOT EXISTS score_snapshots(kind TEXT NOT NULL, rule_id TEXT NOT NULL, rule_version INTEGER NOT NULL,
  ts INTEGER NOT NULL, inst_id TEXT NOT NULL, long REAL, short REAL, close REAL, spread_bps REAL,
  PRIMARY KEY(kind, rule_id, rule_version, ts, inst_id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS watchlist(inst_id TEXT PRIMARY KEY, added_at INTEGER);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER);
CREATE TABLE IF NOT EXISTS inst_views(inst_id TEXT PRIMARY KEY, viewed_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS orderflow_1m(inst_id TEXT NOT NULL, ts INTEGER NOT NULL, buy REAL, sell REAL, n INTEGER,
  step REAL, levels BLOB, PRIMARY KEY(inst_id, ts)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS taker_volume(inst_id TEXT NOT NULL, period TEXT NOT NULL, ts INTEGER NOT NULL, buy REAL, sell REAL,
  PRIMARY KEY(inst_id, period, ts)) WITHOUT ROWID;
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{DB: db}, nil
}

type Bar struct {
	Ts                 int64
	O, H, L, C         float64
	Vol, VolCcy, Quote float64
}

func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

type InstBar struct {
	InstID string
	Bar
}

func (s *Store) PutBars(table string, bars []InstBar) error {
	if len(bars) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO ` + table + ` VALUES(?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, b := range bars {
			if _, err := st.Exec(b.InstID, b.Ts, b.O, b.H, b.L, b.C, b.Vol, b.VolCcy, b.Quote); err != nil {
				return err
			}
		}
		return nil
	})
}

// Bars returns bars with ts in [from, to], ascending.
func (s *Store) Bars(table, instID string, from, to int64) ([]Bar, error) {
	rows, err := s.DB.Query(`SELECT ts,o,h,l,c,vol,vol_ccy,quote FROM `+table+` WHERE inst_id=? AND ts>=? AND ts<=? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bar
	for rows.Next() {
		var b Bar
		if err := rows.Scan(&b.Ts, &b.O, &b.H, &b.L, &b.C, &b.Vol, &b.VolCcy, &b.Quote); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// EachBarSince streams all instruments' bars since `from` (used on restart; no big allocation).
func (s *Store) EachBarSince(table string, from int64, fn func(string, Bar)) (int, error) {
	rows, err := s.DB.Query(`SELECT inst_id,ts,o,h,l,c,vol,vol_ccy,quote FROM `+table+` WHERE ts>=?`, from)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		var b Bar
		if err := rows.Scan(&id, &b.Ts, &b.O, &b.H, &b.L, &b.C, &b.Vol, &b.VolCcy, &b.Quote); err != nil {
			return n, err
		}
		fn(id, b)
		n++
	}
	return n, rows.Err()
}

// AllBarsSince loads all instruments' bars since `from`.
func (s *Store) AllBarsSince(table string, from int64) (map[string][]Bar, error) {
	rows, err := s.DB.Query(`SELECT inst_id,ts,o,h,l,c,vol,vol_ccy,quote FROM `+table+` WHERE ts>=? ORDER BY inst_id, ts`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Bar{}
	for rows.Next() {
		var id string
		var b Bar
		if err := rows.Scan(&id, &b.Ts, &b.O, &b.H, &b.L, &b.C, &b.Vol, &b.VolCcy, &b.Quote); err != nil {
			return nil, err
		}
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}

// Span returns the earliest bar time and bar count for an instrument since `from`.
func (s *Store) Span(table, instID string, from int64) (earliest int64, n int, err error) {
	var e sql.NullInt64
	err = s.DB.QueryRow(`SELECT MIN(ts), COUNT(*) FROM `+table+` WHERE inst_id=? AND ts>=?`, instID, from).Scan(&e, &n)
	return e.Int64, n, err
}

type OISample struct {
	InstID       string
	Ts           int64
	OiCcy, OiUsd float64
}

func (s *Store) PutOI(xs []OISample) error {
	if len(xs) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO oi_samples VALUES(?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range xs {
			if _, err := st.Exec(x.InstID, x.Ts, x.OiCcy, x.OiUsd); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) OI(instID string, from, to int64) ([]OISample, error) {
	rows, err := s.DB.Query(`SELECT ts, oi_ccy, oi_usd FROM oi_samples WHERE inst_id=? AND ts>=? AND ts<=? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OISample
	for rows.Next() {
		x := OISample{InstID: instID}
		if err := rows.Scan(&x.Ts, &x.OiCcy, &x.OiUsd); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) AllOISince(from int64) (map[string][]OISample, error) {
	rows, err := s.DB.Query(`SELECT inst_id, ts, oi_ccy, oi_usd FROM oi_samples WHERE ts>=? ORDER BY inst_id, ts`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]OISample{}
	for rows.Next() {
		var x OISample
		if err := rows.Scan(&x.InstID, &x.Ts, &x.OiCcy, &x.OiUsd); err != nil {
			return nil, err
		}
		out[x.InstID] = append(out[x.InstID], x)
	}
	return out, rows.Err()
}

type FundingSample struct {
	InstID                   string
	Ts                       int64
	Rate                     float64
	FundingTime, NextFunding int64
	Settled                  bool
}

func (s *Store) PutFunding(xs []FundingSample) error {
	if len(xs) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO funding_samples VALUES(?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range xs {
			settled := 0
			if x.Settled {
				settled = 1
			}
			if _, err := st.Exec(x.InstID, x.Ts, x.Rate, x.FundingTime, x.NextFunding, settled); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Funding(instID string, from, to int64) ([]FundingSample, error) {
	rows, err := s.DB.Query(`SELECT ts, rate, funding_time, next_funding_time, settled FROM funding_samples WHERE inst_id=? AND ts>=? AND ts<=? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FundingSample
	for rows.Next() {
		x := FundingSample{InstID: instID}
		var settled int
		if err := rows.Scan(&x.Ts, &x.Rate, &x.FundingTime, &x.NextFunding, &settled); err != nil {
			return nil, err
		}
		x.Settled = settled == 1
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) PutUniverse(ts int64, ids []string, turnover []float64) error {
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO universe_snapshots VALUES(?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for i, id := range ids {
			if _, err := st.Exec(ts, id, turnover[i], i+1); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- rules / snapshots / watchlist ----

func (s *Store) Rules() (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT id, body FROM rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		out[id] = body
	}
	return out, rows.Err()
}

func (s *Store) PutRule(id, body string) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO rules VALUES(?,?,?)`, id, body, time.Now().UnixMilli())
	return err
}

func (s *Store) DeleteRule(id string) error {
	_, err := s.DB.Exec(`DELETE FROM rules WHERE id=?`, id)
	return err
}

type ScoreRow struct {
	Kind, RuleID  string
	RuleVersion   int
	Ts            int64
	InstID        string
	Long, Short   *float64
	Close, Spread float64
}

func (s *Store) PutScores(rows []ScoreRow) error {
	if len(rows) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO score_snapshots VALUES(?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, r := range rows {
			if _, err := st.Exec(r.Kind, r.RuleID, r.RuleVersion, r.Ts, r.InstID, r.Long, r.Short, r.Close, r.Spread); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) DeleteScores(kind, ruleID string, version int) error {
	_, err := s.DB.Exec(`DELETE FROM score_snapshots WHERE kind=? AND rule_id=? AND rule_version=?`, kind, ruleID, version)
	return err
}

func (s *Store) Scores(kind, ruleID string, version int) ([]ScoreRow, error) {
	rows, err := s.DB.Query(`SELECT ts, inst_id, long, short, close, spread_bps FROM score_snapshots
		WHERE kind=? AND rule_id=? AND rule_version=? ORDER BY ts`, kind, ruleID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScoreRow
	for rows.Next() {
		r := ScoreRow{Kind: kind, RuleID: ruleID, RuleVersion: version}
		var l, sh sql.NullFloat64
		if err := rows.Scan(&r.Ts, &r.InstID, &l, &sh, &r.Close, &r.Spread); err != nil {
			return nil, err
		}
		if l.Valid {
			r.Long = &l.Float64
		}
		if sh.Valid {
			r.Short = &sh.Float64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Setting(key string) (string, bool) {
	var v string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) PutSetting(key, value string) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO settings VALUES(?,?,?)`, key, value, time.Now().UnixMilli())
	return err
}

func (s *Store) Watchlist() ([]string, error) {
	rows, err := s.DB.Query(`SELECT inst_id FROM watchlist ORDER BY added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) SetWatch(id string, on bool) error {
	var err error
	if on {
		_, err = s.DB.Exec(`INSERT OR IGNORE INTO watchlist VALUES(?,?)`, id, time.Now().UnixMilli())
	} else {
		_, err = s.DB.Exec(`DELETE FROM watchlist WHERE inst_id=?`, id)
	}
	return err
}

// ---- retention support ----

// PutView records when an instrument's detail was last opened (retention tier "viewed").
func (s *Store) PutView(id string, at int64) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO inst_views VALUES(?,?)`, id, at)
	return err
}

func (s *Store) Views() (map[string]int64, error) {
	return s.idTimes(`SELECT inst_id, viewed_at FROM inst_views`)
}

// LastInPool returns each instrument's latest active-pool minute since `since` (universe snapshots).
func (s *Store) LastInPool(since int64) (map[string]int64, error) {
	return s.idTimes(`SELECT inst_id, MAX(ts) FROM universe_snapshots WHERE ts>=? GROUP BY inst_id`, since)
}

func (s *Store) idTimes(q string, args ...any) (map[string]int64, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, err
		}
		out[id] = ts
	}
	return out, rows.Err()
}

// InstIDs lists the distinct instruments present in a per-instrument table (catches delisted ones).
func (s *Store) InstIDs(table string) ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT inst_id FROM ` + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteBefore removes one instrument's rows older than ts (primary-key range, cheap). `where` is an
// optional extra condition (e.g. "settled=1").
func (s *Store) DeleteBefore(table, instID string, ts int64, where string) (int64, error) {
	q := `DELETE FROM ` + table + ` WHERE inst_id=? AND ts<?`
	if where != "" {
		q += ` AND ` + where
	}
	res, err := s.DB.Exec(q, instID, ts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneUniverse drops pool-membership snapshots older than ts.
func (s *Store) PruneUniverse(ts int64) error {
	_, err := s.DB.Exec(`DELETE FROM universe_snapshots WHERE ts<?`, ts)
	return err
}

// TableBytes returns the bytes of pages used by each table including its indexes (SQLite dbstat).
func (s *Store) TableBytes() (map[string]int64, error) {
	rows, err := s.DB.Query(`SELECT d.name, d.pgsize, COALESCE(m.tbl_name, d.name) FROM dbstat AS d
		LEFT JOIN sqlite_schema AS m ON m.name=d.name WHERE d.aggregate=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name, tbl string
		var n int64
		if err := rows.Scan(&name, &n, &tbl); err != nil {
			return nil, err
		}
		out[tbl] += n
	}
	return out, rows.Err()
}

// FileBytes returns the database file size and the part of it on the free list (reusable).
func (s *Store) FileBytes() (total, free int64) {
	var ps, pc, fc int64
	s.DB.QueryRow(`PRAGMA page_size`).Scan(&ps)
	s.DB.QueryRow(`PRAGMA page_count`).Scan(&pc)
	s.DB.QueryRow(`PRAGMA freelist_count`).Scan(&fc)
	return ps * pc, ps * fc
}

func (s *Store) Counts() map[string]int64 {
	out := map[string]int64{}
	for _, t := range []string{"candles_1m", "candles_1h", "oi_samples", "funding_samples", "orderflow_1m", "taker_volume", "score_snapshots"} {
		var n int64
		s.DB.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n)
		out[t] = n
	}
	return out
}

// ---- order flow ----

// FlowRow is one recorded minute of trades: taker buy/sell USDT notional, trade count and the
// footprint (per price level buy/sell), levels packed as [int32 level index, float32 buy, float32 sell].
type FlowRow struct {
	InstID    string
	Ts        int64
	Buy, Sell float64
	N         int
	Step      float64
	Levels    []byte
}

func (s *Store) PutFlow(rows []FlowRow) error {
	if len(rows) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO orderflow_1m VALUES(?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, r := range rows {
			if _, err := st.Exec(r.InstID, r.Ts, r.Buy, r.Sell, r.N, r.Step, r.Levels); err != nil {
				return err
			}
		}
		return nil
	})
}

// Flow returns recorded minutes in [from, to]; levels are only read when withLevels.
func (s *Store) Flow(instID string, from, to int64, withLevels bool) ([]FlowRow, error) {
	cols := `ts, buy, sell, n, step, NULL`
	if withLevels {
		cols = `ts, buy, sell, n, step, levels`
	}
	rows, err := s.DB.Query(`SELECT `+cols+` FROM orderflow_1m WHERE inst_id=? AND ts>=? AND ts<=? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlowRow
	for rows.Next() {
		r := FlowRow{InstID: instID}
		if err := rows.Scan(&r.Ts, &r.Buy, &r.Sell, &r.N, &r.Step, &r.Levels); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FlowMinutes returns the set of stored minute timestamps in [from, to].
func (s *Store) FlowMinutes(instID string, from, to int64) (map[int64]bool, error) {
	rows, err := s.DB.Query(`SELECT ts FROM orderflow_1m WHERE inst_id=? AND ts>=? AND ts<=?`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out[ts] = true
	}
	return out, rows.Err()
}

// LastFlowStep is the footprint level size of the newest stored minute (0 = none).
func (s *Store) LastFlowStep(instID string) float64 {
	var step float64
	_ = s.DB.QueryRow(`SELECT step FROM orderflow_1m WHERE inst_id=? AND step>0 ORDER BY ts DESC LIMIT 1`, instID).Scan(&step)
	return step
}

type TakerRow struct {
	Ts        int64
	Buy, Sell float64
}

func (s *Store) PutTaker(instID, period string, xs []TakerRow) error {
	if len(xs) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO taker_volume VALUES(?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range xs {
			if _, err := st.Exec(instID, period, x.Ts, x.Buy, x.Sell); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Taker(instID, period string, from, to int64) ([]TakerRow, error) {
	rows, err := s.DB.Query(`SELECT ts, buy, sell FROM taker_volume WHERE inst_id=? AND period=? AND ts>=? AND ts<=? ORDER BY ts`, instID, period, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TakerRow
	for rows.Next() {
		var x TakerRow
		if err := rows.Scan(&x.Ts, &x.Buy, &x.Sell); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// TakerSpan returns the newest and oldest stored period start (0 if none).
func (s *Store) TakerSpan(instID, period string) (newest, oldest int64) {
	var n, o sql.NullInt64
	s.DB.QueryRow(`SELECT MAX(ts), MIN(ts) FROM taker_volume WHERE inst_id=? AND period=?`, instID, period).Scan(&n, &o)
	return n.Int64, o.Int64
}
