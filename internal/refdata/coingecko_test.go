package refdata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSymbolCollisionKeepsLargestCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, `[{"id":"bitcoin","symbol":"btc","market_cap":100,"circulating_supply":19,"total_supply":20,"max_supply":21},
			{"id":"fake-btc","symbol":"btc","market_cap":1,"circulating_supply":5,"total_supply":null,"max_supply":null},
			{"id":"ethereum","symbol":"eth","market_cap":50,"circulating_supply":120,"total_supply":120,"max_supply":null}]`)
	}))
	defer srv.Close()
	c := New(srv.Client())
	c.URL, c.Pause = srv.URL+"/?page=%d", 0
	if err := c.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if x, ok := c.Get("BTC"); !ok || x.ID != "bitcoin" || x.Total != 20 || x.Max != 21 {
		t.Fatalf("btc: %+v %v", x, ok)
	}
	if x, ok := c.Get("eth"); !ok || x.Max != 0 || x.Circulating != 120 {
		t.Fatalf("eth: %+v", x)
	}
	if _, ok := c.Get("DOGE"); ok {
		t.Fatal("unknown symbol found")
	}
}
