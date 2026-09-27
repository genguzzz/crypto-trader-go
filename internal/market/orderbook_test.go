package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

func TestOrderBookUSDTNotional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": "0", "data": []map[string]any{{
			"asks": [][]string{{"101", "10", "0", "1"}, {"102", "5", "0", "1"}},
			"bids": [][]string{{"99", "20", "0", "2"}},
			"ts":   "1700000000000",
		}}})
	}))
	defer srv.Close()
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	rest := okx.NewREST()
	rest.BaseURL = srv.URL
	e := New(Config{Threshold: 1, TopN: 1}, rest, st)
	in := newInst("X-USDT-SWAP")
	in.CtVal = 0.1
	e.insts[in.ID] = in
	ob, err := e.OrderBook(context.Background(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ob.Asks[0][1] != 101*10*0.1 || ob.Bids[0][1] != 99*20*0.1 {
		t.Fatalf("usdt notional wrong: %+v %+v", ob.Asks[0], ob.Bids[0])
	}
	if ob.Mid != 100 || ob.SpreadBps != 200 {
		t.Fatalf("mid %v spread %v", ob.Mid, ob.SpreadBps)
	}
}
