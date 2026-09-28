package store

import "database/sql"

// Liq is one filled liquidation order (OKX public liquidation-orders).
type Liq struct {
	Ts   int64
	Long bool    // a long position was liquidated (forced sell)
	Px   float64 // bankruptcy price
	Sz   float64 // contracts
	USD  float64 // px × sz × ctVal
}

// PutLiqs stores liquidations; re-fetched orders (same instrument, time, side, price, size) are ignored.
func (s *Store) PutLiqs(instID string, xs []Liq) error {
	if len(xs) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := tx.Prepare(`INSERT OR IGNORE INTO liquidations VALUES(?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range xs {
			if _, err := st.Exec(instID, x.Ts, x.Long, x.Px, x.Sz, x.USD); err != nil {
				return err
			}
		}
		return nil
	})
}

// Liqs returns stored liquidations in [from, to], ascending.
func (s *Store) Liqs(instID string, from, to int64) ([]Liq, error) {
	rows, err := s.DB.Query(`SELECT ts, long, px, sz, usd FROM liquidations WHERE inst_id=? AND ts>=? AND ts<=? ORDER BY ts`, instID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Liq
	for rows.Next() {
		var x Liq
		if err := rows.Scan(&x.Ts, &x.Long, &x.Px, &x.Sz, &x.USD); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// LiqSpan returns the newest and oldest stored liquidation time (0 if none).
func (s *Store) LiqSpan(instID string) (newest, oldest int64) {
	var n, o sql.NullInt64
	s.DB.QueryRow(`SELECT MAX(ts), MIN(ts) FROM liquidations WHERE inst_id=?`, instID).Scan(&n, &o)
	return n.Int64, o.Int64
}

// PruneLiqs deletes liquidations older than ts.
func (s *Store) PruneLiqs(ts int64) (int64, error) {
	r, err := s.DB.Exec(`DELETE FROM liquidations WHERE ts<?`, ts)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
