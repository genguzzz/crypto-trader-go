package refdata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshBySymbolKeepsPreviousOnFailedBatch(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("include_tokens") != "top" || q.Get("vs_currency") != "usd" {
			t.Errorf("query %v", q)
		}
		if fail {
			w.WriteHeader(429)
			return
		}
		var out []string
		for _, s := range strings.Split(q.Get("symbols"), ",") {
			switch s {
			case "btc":
				out = append(out, `{"id":"bitcoin","symbol":"btc","market_cap":100,"market_cap_rank":1,"circulating_supply":19,"total_supply":20,"max_supply":21}`)
			case "xdp":
				out = append(out, `{"id":"doppler","symbol":"xdp","market_cap":null,"circulating_supply":null,"total_supply":10,"max_supply":null}`)
			}
		}
		fmt.Fprint(w, "["+strings.Join(out, ",")+"]")
	}))
	defer srv.Close()
	saved := ""
	c := New(srv.Client())
	c.URL, c.Pause, c.Backoff = srv.URL, 0, 0
	c.Save = func(s string) error { saved = s; return nil }
	if err := c.refresh(context.Background(), []string{"BTC", "xdp", "DOGE"}); err != nil {
		t.Fatal(err)
	}
	if x, ok := c.Get("btc"); !ok || x.ID != "bitcoin" || x.Total != 20 || x.Max != 21 || x.Rank != 1 {
		t.Fatalf("btc: %+v %v", x, ok)
	}
	if x, ok := c.Get("XDP"); !ok || x.MarketCap != 0 || x.Total != 10 {
		t.Fatalf("xdp without market cap should still carry supply: %+v", x)
	}
	if _, ok := c.Get("DOGE"); ok {
		t.Fatal("unknown symbol found")
	}
	if !strings.Contains(saved, "bitcoin") {
		t.Fatalf("snapshot not saved: %q", saved)
	}
	fail = true // rate limited: previous figures must survive
	if err := c.refresh(context.Background(), []string{"BTC"}); err == nil {
		t.Fatal("expected error when every batch fails")
	}
	if x, ok := c.Get("BTC"); !ok || x.MarketCap != 100 {
		t.Fatalf("previous entry lost: %+v", x)
	}
}
