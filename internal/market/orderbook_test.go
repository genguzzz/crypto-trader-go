package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crypto-trader-go/internal/okx"
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
	st := openStore(t)
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

// Deep levels are merged into price buckets without changing the side's total or its reach.
func TestCompressSidePreservesDepth(t *testing.T) {
	var asks [][3]float64
	total := 0.0
	for i := 0; i < 5000; i++ {
		asks = append(asks, [3]float64{100 + float64(i)*0.01, 10, 1})
		total += 10
	}
	out := compressSide(asks, 100)
	sum := 0.0
	for _, l := range out {
		sum += l[1]
	}
	if len(out) > bookExact+301 || len(out) <= bookExact {
		t.Fatalf("%d levels", len(out))
	}
	if sum != total || out[len(out)-1][0] != asks[len(asks)-1][0] || out[bookExact-1] != asks[bookExact-1] {
		t.Fatalf("sum %v/%v last %v", sum, total, out[len(out)-1])
	}
}
