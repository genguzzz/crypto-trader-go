// sqlitebench checks whether single-file SQLite (WAL, pure-Go driver) comfortably handles the
// blueprint's persistence load: one transaction per minute with ~473 confirmed 1m bars, N days
// of history, and the read patterns the API needs (12h window sum, one instrument's chart).
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	days := flag.Int("days", 30, "days of 1m history to write")
	insts := flag.Int("insts", 473, "instruments")
	path := flag.String("db", "results/bench.db", "db path (deleted first)")
	flag.Parse()
	os.Remove(*path)
	os.Remove(*path + "-wal")
	os.Remove(*path + "-shm")
	db, err := sql.Open("sqlite", *path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	must(err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE candles_1m(inst_id INTEGER NOT NULL, open_ms INTEGER NOT NULL,
		o TEXT, h TEXT, l TEXT, c TEXT, vol TEXT, vol_ccy TEXT, vol_quote TEXT, PRIMARY KEY(inst_id, open_ms)) WITHOUT ROWID`)
	must(err)
	start := time.Now().Add(-time.Duration(*days) * 24 * time.Hour).Truncate(time.Minute)
	minutes := *days * 1440
	var worst time.Duration
	t0 := time.Now()
	for m := 0; m < minutes; m++ {
		ts := start.Add(time.Duration(m) * time.Minute).UnixMilli()
		ts0 := time.Now()
		tx, _ := db.Begin()
		st, _ := tx.Prepare(`INSERT INTO candles_1m VALUES(?,?,?,?,?,?,?,?,?)`)
		for i := 0; i < *insts; i++ {
			p := fmt.Sprintf("%.6f", 100+rand.Float64())
			_, err := st.Exec(i, ts, p, p, p, p, "1234.5", "12.345", fmt.Sprintf("%.2f", rand.Float64()*1e5))
			must(err)
		}
		st.Close()
		must(tx.Commit())
		if d := time.Since(ts0); d > worst {
			worst = d
		}
	}
	total := time.Since(t0)
	fi, _ := os.Stat(*path)
	fmt.Printf("wrote %d rows (%d days × %d insts) in %s; avg per-minute tx %.1fms, worst %.1fms; db size %.0f MB\n",
		minutes**insts, *days, *insts, total.Round(time.Second), float64(total.Milliseconds())/float64(minutes), float64(worst.Microseconds())/1000, float64(fi.Size())/1e6)
	end := start.Add(time.Duration(minutes-1) * time.Minute).UnixMilli()
	q := func(name, sqlq string, args ...any) {
		t := time.Now()
		rows, err := db.Query(sqlq, args...)
		must(err)
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		fmt.Printf("%-44s %4d rows %8.1fms\n", name, n, float64(time.Since(t).Microseconds())/1000)
	}
	q("12h window sum, all instruments (cold start)", `SELECT inst_id, SUM(CAST(vol_quote AS REAL)) FROM candles_1m WHERE open_ms > ? GROUP BY inst_id`, end-720*60000)
	q("one instrument, 3 days of 1m for chart", `SELECT * FROM candles_1m WHERE inst_id=7 AND open_ms > ? ORDER BY open_ms`, end-3*1440*60000)
	q("one instrument, 30d aggregated to 1h", `SELECT open_ms/3600000, MAX(CAST(h AS REAL)), SUM(CAST(vol_quote AS REAL)) FROM candles_1m WHERE inst_id=7 GROUP BY 1`)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
