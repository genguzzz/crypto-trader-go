package market

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

func TestMissingRanges(t *testing.T) {
	have := map[int64]bool{2 * minuteMs: true, 3 * minuteMs: true, 6 * minuteMs: true}
	got := missingRanges(0, 8*minuteMs, have)
	want := [][2]int64{{0, 2 * minuteMs}, {4 * minuteMs, 6 * minuteMs}, {7 * minuteMs, 8 * minuteMs}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v want %v", got, want)
	}
}

// Backfill rebuilds missing minutes from a daily trade file plus history-trades, writes quiet
// minutes as zero rows, and never overwrites a minute that live recording already stored.
func TestBackfillFlow(t *testing.T) {
	now := time.Now().UnixMilli()
	nowMin := now / minuteMs * minuteMs
	// trades: one per minute (buy on even minutes, sell on odd), 2 contracts at 100, in two windows
	fileStart := (nowMin+8*hourMs)/utc8DayMs*utc8DayMs - 8*hourMs - utc8DayMs // yesterday, UTC+8
	restFrom := nowMin - 3*hourMs
	trade := func(m int64) okx.Trade { return okx.Trade{ID: m / minuteMs, Px: 100, Sz: 2, Buy: (m/minuteMs)%2 == 0, Ts: m + 30_000} }

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	fw, _ := zw.Create("X-USDT-SWAP-trades.csv")
	fmt.Fprintln(fw, "instrument_name,trade_id,side,price,size,created_time,source")
	for m := fileStart; m < fileStart+utc8DayMs; m += minuteMs {
		if (m/minuteMs)%10 == 0 {
			continue // quiet minutes
		}
		x := trade(m)
		side := map[bool]string{true: "buy", false: "sell"}[x.Buy]
		fmt.Fprintf(fw, "X-USDT-SWAP,%d,%s,100,2,%d,0\n", x.ID, side, x.Ts)
	}
	zw.Close()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/file.zip":
			w.Write(zbuf.Bytes())
			return
		case "/public/market-data-history":
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "data": []any{map[string]any{"details": []any{map[string]any{
				"groupDetails": []any{map[string]string{"dateTs": fmt.Sprint(fileStart), "filename": "X-USDT-SWAP-trades-d.zip", "sizeMB": "1", "url": srv.URL + "/file.zip"}}}}}}})
			return
		case "/market/history-trades":
			after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
			before := after // type=2: ts; type=1: trade id (= minute index)
			if q.Get("type") == "1" {
				before = after * minuteMs
			}
			var data []map[string]string
			for m := (before - 1) / minuteMs * minuteMs; m >= restFrom && len(data) < 100; m -= minuteMs {
				x := trade(m)
				if x.Ts >= before {
					continue
				}
				data = append(data, map[string]string{"tradeId": fmt.Sprint(x.ID), "px": "100", "sz": "2", "side": map[bool]string{true: "buy", false: "sell"}[x.Buy], "ts": fmt.Sprint(x.Ts)})
			}
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "data": data})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	st := openStore(t)
	rest := okx.NewREST()
	rest.BaseURL = srv.URL
	e := New(Config{Threshold: 1, TopN: 10}, rest, st)
	id := "X-USDT-SWAP"
	e.catalog[id] = okx.Instrument{InstID: id, CtVal: 0.5, TickSz: 0.1}
	e.views[id] = now // viewed tier: order flow kept 3 days
	live := nowMin - hourMs
	st.PutFlow([]store.FlowRow{{InstID: id, Ts: live, Buy: 1, Sell: 1, N: 7, Step: 0.01}})

	n, err := e.backfillFlow(context.Background(), id, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	hi := (now - flowGrace.Milliseconds() - minuteMs) / minuteMs * minuteMs
	rows, _ := st.Flow(id, 0, now, true)
	by := map[int64]store.FlowRow{}
	for _, r := range rows {
		by[r.Ts] = r
	}
	if n == 0 || len(rows) != n+1 {
		t.Fatalf("wrote %d, stored %d", n, len(rows))
	}
	if r := by[live]; r.N != 7 {
		t.Fatalf("live minute overwritten: %+v", r)
	}
	// file day: a buy minute, a sell minute, a quiet minute
	for m := fileStart; m < fileStart+3*minuteMs*10; m += minuteMs {
		r, ok := by[m]
		if !ok {
			t.Fatalf("file minute %d missing", m)
		}
		switch {
		case (m/minuteMs)%10 == 0:
			if r.N != 0 || r.Buy != 0 {
				t.Fatalf("quiet minute: %+v", r)
			}
		case (m/minuteMs)%2 == 0:
			if r.Buy != 100 || r.Sell != 0 || r.N != 1 || len(r.Levels) != 12 { // 100 × 2 × 0.5
				t.Fatalf("buy minute: %+v", r)
			}
		default:
			if r.Sell != 100 || r.Buy != 0 {
				t.Fatalf("sell minute: %+v", r)
			}
		}
	}
	// recent hours from history-trades, up to the last closed minute
	for m := max(restFrom, fileStart+utc8DayMs); m < hi; m += minuteMs {
		r, ok := by[m]
		if !ok || (m != live && r.N != 1) {
			t.Fatalf("recent minute %d: %+v %v", m, r, ok)
		}
	}
	// a second run has nothing left to do
	if n2, _ := e.backfillFlow(context.Background(), id, func(string) {}); n2 != 0 {
		t.Fatalf("second run wrote %d", n2)
	}
}
