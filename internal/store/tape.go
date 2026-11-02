package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Tape is a second SQLite file for the bulky microstructure data: raw public trades per minute
// (spot and swap), order-book wall events and per-minute book summaries. Kept apart from the main
// DB so its churn (48h of trades) neither counts against nor fragments the main storage cap.
type Tape struct{ DB *sql.DB }

const tapeSchema = `
CREATE TABLE IF NOT EXISTS tape(inst_id TEXT NOT NULL, ts INTEGER NOT NULL, n INTEGER NOT NULL, data BLOB);
CREATE UNIQUE INDEX IF NOT EXISTS tape_key ON tape(inst_id, ts);
CREATE INDEX IF NOT EXISTS tape_ts ON tape(ts);
CREATE TABLE IF NOT EXISTS walls(id INTEGER PRIMARY KEY, inst_id TEXT NOT NULL, bid INTEGER NOT NULL, px REAL NOT NULL,
  t0 INTEGER NOT NULL, t1 INTEGER NOT NULL, first_usd REAL NOT NULL, peak_usd REAL NOT NULL, end_usd REAL NOT NULL,
  peak_orders INTEGER NOT NULL, filled_usd REAL NOT NULL, dist0_bps REAL NOT NULL, min_dist_bps REAL NOT NULL,
  end_dist_bps REAL NOT NULL, outcome TEXT NOT NULL, chain INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS walls_inst ON walls(inst_id, t1);
CREATE TABLE IF NOT EXISTS book_1m(inst_id TEXT NOT NULL, ts INTEGER NOT NULL, mid REAL, spread_bps REAL,
  bid05 REAL, ask05 REAL, bid1 REAL, ask1 REAL, bid2 REAL, ask2 REAL, wall_bid REAL, wall_ask REAL,
  max_bid REAL, max_ask REAL, cover REAL, PRIMARY KEY(inst_id, ts)) WITHOUT ROWID;
`

func OpenTape(path string) (*Tape, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(tapeSchema); err != nil {
		return nil, fmt.Errorf("tape schema: %w", err)
	}
	return &Tape{DB: db}, nil
}

// TapeRow is one minute of one instrument's trades (Data = encoded, nil when the minute was
// covered and had no trades).
type TapeRow struct {
	InstID string
	Ts     int64
	N      int
	Data   []byte
}

// PutTape inserts minutes; existing minutes are kept (live recording and backfill never fight).
func (t *Tape) PutTape(rows []TapeRow) error {
	if len(rows) == 0 {
		return nil
	}
	return inTx(t.DB, func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR IGNORE INTO tape VALUES(?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, r := range rows {
			if _, err := st.Exec(r.InstID, r.Ts, r.N, r.Data); err != nil {
				return err
			}
		}
		return nil
	})
}

// TapeRows returns the stored minutes in [from, to), ascending.
func (t *Tape) TapeRows(instID string, from, to int64) ([]TapeRow, error) {
	rows, err := t.DB.Query(`SELECT ts, n, data FROM tape WHERE inst_id=? AND ts>=? AND ts<? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TapeRow
	for rows.Next() {
		r := TapeRow{InstID: instID}
		if err := rows.Scan(&r.Ts, &r.N, &r.Data); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TapeMinutes returns the set of stored minutes in [from, to).
func (t *Tape) TapeMinutes(instID string, from, to int64) (map[int64]bool, error) {
	rows, err := t.DB.Query(`SELECT ts FROM tape WHERE inst_id=? AND ts>=? AND ts<?`, instID, from, to)
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

// Wall is one order-book wall's life: a price level far larger than its neighbours, from when it
// appeared to when it shrank away, and how it went (filled by trades or pulled).
type Wall struct {
	ID         int64   `json:"id"`
	InstID     string  `json:"instId"`
	Bid        bool    `json:"bid"`
	Px         float64 `json:"px"`
	T0         int64   `json:"t0"`
	T1         int64   `json:"t1"`
	FirstUsd   float64 `json:"firstUsd"`
	PeakUsd    float64 `json:"peakUsd"`
	EndUsd     float64 `json:"endUsd"`
	PeakOrders int     `json:"peakOrders"`
	FilledUsd  float64 `json:"filledUsd"`
	Dist0Bps   float64 `json:"dist0Bps"`
	MinDistBps float64 `json:"minDistBps"`
	EndDistBps float64 `json:"endDistBps"`
	Outcome    string  `json:"outcome"`
	Chain      int64   `json:"chain"`
}

func (t *Tape) PutWalls(ws []Wall) error {
	if len(ws) == 0 {
		return nil
	}
	return inTx(t.DB, func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO walls VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, w := range ws {
			if _, err := st.Exec(w.ID, w.InstID, w.Bid, w.Px, w.T0, w.T1, w.FirstUsd, w.PeakUsd, w.EndUsd, w.PeakOrders,
				w.FilledUsd, w.Dist0Bps, w.MinDistBps, w.EndDistBps, w.Outcome, w.Chain); err != nil {
				return err
			}
		}
		return nil
	})
}

// Walls returns walls of an instrument that ended in [from, to), oldest first.
func (t *Tape) Walls(instID string, from, to int64) ([]Wall, error) {
	rows, err := t.DB.Query(`SELECT id, bid, px, t0, t1, first_usd, peak_usd, end_usd, peak_orders, filled_usd, dist0_bps,
		min_dist_bps, end_dist_bps, outcome, chain FROM walls WHERE inst_id=? AND t1>=? AND t1<? ORDER BY t1`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wall
	for rows.Next() {
		w := Wall{InstID: instID}
		if err := rows.Scan(&w.ID, &w.Bid, &w.Px, &w.T0, &w.T1, &w.FirstUsd, &w.PeakUsd, &w.EndUsd, &w.PeakOrders, &w.FilledUsd,
			&w.Dist0Bps, &w.MinDistBps, &w.EndDistBps, &w.Outcome, &w.Chain); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// BookMin is one minute's average book shape (depth in USDT within ±0.5/1/2% of mid).
type BookMin struct {
	InstID                 string
	Ts                     int64
	Mid, SpreadBps         float64
	Bid05, Ask05           float64
	Bid1, Ask1, Bid2, Ask2 float64
	WallBid, WallAsk       float64 // USDT in walls per side
	MaxBid, MaxAsk         float64 // largest wall per side
	Cover                  float64 // % from mid the recorded book reaches (min of both sides)
}

func (t *Tape) PutBookMins(xs []BookMin) error {
	if len(xs) == 0 {
		return nil
	}
	return inTx(t.DB, func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR REPLACE INTO book_1m VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, b := range xs {
			if _, err := st.Exec(b.InstID, b.Ts, b.Mid, b.SpreadBps, b.Bid05, b.Ask05, b.Bid1, b.Ask1, b.Bid2, b.Ask2,
				b.WallBid, b.WallAsk, b.MaxBid, b.MaxAsk, b.Cover); err != nil {
				return err
			}
		}
		return nil
	})
}

func (t *Tape) BookMins(instID string, from, to int64) ([]BookMin, error) {
	rows, err := t.DB.Query(`SELECT ts, mid, spread_bps, bid05, ask05, bid1, ask1, bid2, ask2, wall_bid, wall_ask, max_bid, max_ask, cover
		FROM book_1m WHERE inst_id=? AND ts>=? AND ts<? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BookMin
	for rows.Next() {
		b := BookMin{InstID: instID}
		if err := rows.Scan(&b.Ts, &b.Mid, &b.SpreadBps, &b.Bid05, &b.Ask05, &b.Bid1, &b.Ask1, &b.Bid2, &b.Ask2,
			&b.WallBid, &b.WallAsk, &b.MaxBid, &b.MaxAsk, &b.Cover); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Prune deletes trades older than tapeBefore and walls / book minutes older than restBefore.
func (t *Tape) Prune(tapeBefore, restBefore int64) (int64, error) {
	var total int64
	for _, q := range []struct {
		sql string
		arg int64
	}{{`DELETE FROM tape WHERE ts<?`, tapeBefore}, {`DELETE FROM walls WHERE t1<?`, restBefore}, {`DELETE FROM book_1m WHERE ts<?`, restBefore}} {
		r, err := t.DB.Exec(q.sql, q.arg)
		if err != nil {
			return total, err
		}
		n, _ := r.RowsAffected()
		total += n
	}
	return total, nil
}

// PruneExcept deletes trade minutes older than before of every instrument not in keep.
func (t *Tape) PruneExcept(keep []string, before int64) (int64, error) {
	q := `DELETE FROM tape WHERE ts<?`
	args := []any{before}
	if len(keep) > 0 {
		q += ` AND inst_id NOT IN (?` + strings.Repeat(`,?`, len(keep)-1) + `)`
		for _, id := range keep {
			args = append(args, id)
		}
	}
	r, err := t.DB.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// RecentTape returns the newest stored minute per instrument among minutes at/after since.
func (t *Tape) RecentTape(since int64) (map[string]int64, error) {
	rows, err := t.DB.Query(`SELECT inst_id, MAX(ts) FROM tape WHERE ts>=? GROUP BY inst_id`, since)
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

// UsedBytes is the file size minus reusable free pages.
func (t *Tape) UsedBytes() (used, file int64) {
	var pages, free, size int64
	t.DB.QueryRow(`PRAGMA page_count`).Scan(&pages)
	t.DB.QueryRow(`PRAGMA freelist_count`).Scan(&free)
	t.DB.QueryRow(`PRAGMA page_size`).Scan(&size)
	return (pages - free) * size, pages * size
}

// OldestTape is the oldest stored trade minute (0 = none).
func (t *Tape) OldestTape() int64 {
	var ts sql.NullInt64
	t.DB.QueryRow(`SELECT MIN(ts) FROM tape`).Scan(&ts)
	return ts.Int64
}

func inTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
