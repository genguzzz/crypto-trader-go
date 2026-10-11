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
)

// fakeHistory serves /market/history-candles 1H pages (newest first, `after` exclusive, nothing
// before `listed`) and /public/funding-rate-history with an 8h settlement since `listed`.
func fakeHistory(t *testing.T, last, listed int64, requests *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		after := int64(1 << 62)
		if a := q.Get("after"); a != "" {
			after, _ = strconv.ParseInt(a, 10, 64)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/market/history-candles"):
			var data [][]string
			for ts := min(last, (after-1)/hourMs*hourMs); ts >= listed && len(data) < limit; ts -= hourMs {
				data = append(data, []string{strconv.FormatInt(ts, 10), "1", "2", "0.5", "1.5", "10", "10", "15", "1"})
			}
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": data})
		case strings.HasSuffix(r.URL.Path, "/public/funding-rate-history"):
			var data []map[string]string
			for ts := min(last, after-1) / (8 * hourMs) * (8 * hourMs); ts >= listed && len(data) < limit; ts -= 8 * hourMs {
				data = append(data, map[string]string{"instId": "X-USDT-SWAP", "fundingTime": strconv.FormatInt(ts, 10), "realizedRate": "0.0001"})
			}
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": data})
		default:
			json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": []any{}})
		}
	}))
}

func TestHourFillBackfillsToListingAndRepairsHoles(t *testing.T) {
	st := openStore(t)
	now := time.Now().UnixMilli()
	last := now/hourMs*hourMs - hourMs
	listed := last - 20*24*hourMs // 20 days of history: 481 bars
	reqs := 0
	srv := fakeHistory(t, last, listed, &reqs)
	defer srv.Close()
	rest := okx.NewREST()
	rest.BaseURL = srv.URL
	e := New(Config{Threshold: 1, TopN: 10}, rest, st)
	in := newInst("X-USDT-SWAP")
	in.ListTime = listed
	e.insts[in.ID] = in
	ctx := context.Background()

	n, err := e.hourFill(ctx, in.ID)
	if err != nil || n != 481 {
		t.Fatalf("first fill wrote %d bars (%v), want 481", n, err)
	}
	reqs = 0
	if n, _ := e.hourFill(ctx, in.ID); n != 0 || reqs != 0 {
		t.Fatalf("complete history should need no request: %d bars, %d requests", n, reqs)
	}
	// a hole in the middle (outage): coverage drops below 99 % → re-paged from now
	if _, err := st.DB.Exec(`DELETE FROM candles_1h WHERE inst_id=? AND ts BETWEEN ? AND ?`, in.ID, last-200*hourMs, last-190*hourMs); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.hourFill(ctx, in.ID); n < 11 {
		t.Fatalf("hole not repaired: wrote %d", n)
	}
	if _, cnt, _ := st.Span("candles_1h", in.ID, listed); cnt != 481 {
		t.Fatalf("after repair: %d bars, want 481", cnt)
	}

	want := 0
	for ts := last / (8 * hourMs) * (8 * hourMs); ts >= listed; ts -= 8 * hourMs {
		want++
	}
	f, err := e.fundingFill(ctx, in.ID)
	if err != nil || f != want {
		t.Fatalf("funding: %d samples (%v), want %d", f, err, want)
	}
}
