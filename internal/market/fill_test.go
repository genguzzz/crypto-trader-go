package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"crypto-trader-go/internal/okx"
	"crypto-trader-go/internal/store"
)

// fakeOKX serves /market/candles 1m pages (newest first, `after` exclusive) for any instrument.
func fakeOKX(t *testing.T, last int64, requests *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		if strings.Contains(r.URL.Path, "/rubik/") { // no OI / taker history in this fake
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": [][]string{}})
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		top := last
		if a := q.Get("after"); a != "" {
			v, _ := strconv.ParseInt(a, 10, 64)
			top = v - minuteMs
		}
		var data [][]string
		for k := 0; k < limit; k++ {
			ts := top - int64(k)*minuteMs
			data = append(data, []string{strconv.FormatInt(ts, 10), "1", "1", "1", "1", "10", "10", "10", "1"})
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": data})
	}))
}

func TestFillMinutesRepairsDeepGap(t *testing.T) {
	st := openStore(t)
	last := time.Now().UnixMilli()/minuteMs*minuteMs - minuteMs
	reqs := 0
	srv := fakeOKX(t, last, &reqs)
	defer srv.Close()
	rest := okx.NewREST()
	rest.BaseURL = srv.URL
	e := New(Config{Threshold: 1, TopN: 10}, rest, st)
	ctx := context.Background()
	runWriter(t, e)
	in := newInst("X-USDT-SWAP")
	e.insts[in.ID] = in
	for k := int64(0); k < 720; k++ {
		in.putBar(store.Bar{Ts: last - k*minuteMs, C: 1, Quote: 10})
	}
	hole := last - 650*minuteMs // older than the first 300-bar page
	in.bars.del(hole)

	e.fillMinutes(ctx, in.ID, 720)
	if !in.bars.has(hole) {
		t.Fatalf("deep gap not repaired after %d requests", reqs)
	}
	if reqs != 3 {
		t.Fatalf("expected 3 pages to reach the hole, got %d", reqs)
	}
	reqs = 0
	e.fillMinutes(ctx, in.ID, 720)
	if reqs != 1 {
		t.Fatalf("complete window should need 1 request, got %d", reqs)
	}
	if _, have := in.window12h(last); have != 720 {
		t.Fatalf("window incomplete: %d", have)
	}
}
