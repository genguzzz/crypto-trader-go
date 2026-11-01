package okxacct

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSign(t *testing.T) {
	// reference value from Python: base64(hmac.new(secret, ts+"GET"+path, sha256).digest())
	got := sign("22582BD0CFF14C41EDBF1AB98506286D", "2020-12-08T09:08:57.715Z", "GET", "/api/v5/account/balance?ccy=BTC", "")
	if want := "HiZhvSfMtWJA3uUIVXV3a/bSXNPCWvYFXoGCVS8V4zY="; got != want {
		t.Fatalf("sign = %s, want %s", got, want)
	}
}

func TestConfig(t *testing.T) {
	for _, c := range []Config{{}, {Key: "k", Secret: "s", Passphrase: "p"}, {Key: "k", Secret: "s", Proxy: &url.URL{Scheme: "http", Host: "x:1"}}} {
		if c.Enabled() {
			t.Fatalf("%+v enabled", c)
		}
		if New(c).Enabled() {
			t.Fatal("service enabled without a complete config")
		}
	}
	snap, err := New(Config{}).Snapshot(context.Background(), "")
	if err != nil || snap.Enabled || snap.Positions == nil {
		t.Fatalf("disabled snapshot: %+v %v", snap, err)
	}
	t.Setenv("OKX_PRIVATE_PROXY", "not a url")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("bad proxy accepted")
	}
}

func TestMerge(t *testing.T) {
	ps := []rawPosition{
		{InstID: "BTC-USDT-SWAP", PosSide: "long", MgnMode: "cross", Pos: "3", AvgPx: "100", MarkPx: "110", LiqPx: "60", Lever: "10",
			Upl: "30", UplRatio: "1", NotionalUsd: "330", CTime: "1", UTime: "2",
			CloseOrderAlgo: []rawAttached{{AlgoID: "a1", SlTriggerPx: "90", CloseFraction: "1"}}},
		{InstID: "ETH-USDT-SWAP", PosSide: "net", Pos: "-2", AvgPx: "50", NotionalUsd: "100"},
		{InstID: "SOL-USDT-SWAP", PosSide: "long", Pos: "0"},
	}
	algos := []rawAlgo{
		{AlgoID: "a1", InstID: "BTC-USDT-SWAP", OrdType: "conditional", Side: "sell", PosSide: "long", SlTriggerPx: "90", CloseFraction: "1"}, // same as attached
		{AlgoID: "a2", InstID: "BTC-USDT-SWAP", OrdType: "conditional", Side: "sell", PosSide: "long", Sz: "1", TpTriggerPx: "130"},
		{AlgoID: "a3", InstID: "BTC-USDT-SWAP", OrdType: "oco", Side: "sell", PosSide: "long", Sz: "3", TpTriggerPx: "120", SlTriggerPx: "95"},
		{AlgoID: "a4", InstID: "BTC-USDT-SWAP", OrdType: "conditional", Side: "buy", PosSide: "long", Sz: "1", TpTriggerPx: "80"}, // adds: not a TP
		{AlgoID: "e1", InstID: "ETH-USDT-SWAP", OrdType: "conditional", Side: "buy", PosSide: "net", Sz: "1", SlTriggerPx: "55"},
	}
	got := merge(ps, algos, func(id string) float64 { return map[string]float64{"BTC-USDT-SWAP": 0.01}[id] })
	if len(got) != 2 {
		t.Fatalf("positions: %+v", got)
	}
	btc, eth := got[0], got[1]
	if btc.Side != "long" || btc.Contracts != 3 || btc.Coins != 0.03 || btc.LiqPx != 60 {
		t.Fatalf("btc: %+v", btc)
	}
	if len(btc.TP) != 2 || btc.TP[0].Px != 120 || !btc.TP[0].Full || btc.TP[1].Px != 130 || btc.TP[1].Full || btc.TP[1].Contracts != 1 {
		t.Fatalf("btc tp (nearest first, sz ≥ position is full): %+v", btc.TP)
	}
	if len(btc.SL) != 2 || btc.SL[0].Px != 95 || btc.SL[1].Px != 90 || btc.SL[1].Kind != "conditional" {
		t.Fatalf("btc sl (deduplicated by algoId): %+v", btc.SL)
	}
	if eth.Side != "short" || eth.Contracts != 2 || eth.Coins != 0 || len(eth.SL) != 1 || eth.SL[0].Contracts != 1 || len(eth.TP) != 0 {
		t.Fatalf("eth (net mode short, ctVal unknown): %+v", eth)
	}
}

// fakeProxy is a plain-HTTP forward proxy that answers OKX itself, so the test sees every request
// the client sends and proves that none bypasses the proxy.
type fakeProxy struct {
	mu    sync.Mutex
	paths []string
	calls atomic.Int64
}

func (f *fakeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	rp := r.URL.RequestURI()
	ok := r.Method == http.MethodGet && r.URL.IsAbs() && r.Header.Get("User-Agent") == UserAgent &&
		r.Header.Get("Content-Type") == "application/json" && r.Header.Get("OK-ACCESS-KEY") == "k" && r.Header.Get("OK-ACCESS-PASSPHRASE") == "p" &&
		r.Header.Get("OK-ACCESS-SIGN") == sign("s", r.Header.Get("OK-ACCESS-TIMESTAMP"), "GET", rp, "")
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", r.Header.Get("OK-ACCESS-TIMESTAMP")); err != nil {
		ok = false
	}
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"code": "50113", "msg": "Invalid Sign", "data": []any{}})
		return
	}
	var data any = []any{}
	switch r.URL.Path {
	case "/api/v5/account/positions":
		data = []any{map[string]any{"instId": "BTC-USDT-SWAP", "posSide": "short", "pos": "2", "avgPx": "100", "markPx": "90", "upl": "20"}}
	case "/api/v5/trade/orders-algo-pending":
		if r.URL.Query().Get("ordType") == "oco" {
			data = []any{map[string]any{"algoId": "o1", "instId": "BTC-USDT-SWAP", "ordType": "oco", "side": "buy", "posSide": "short", "sz": "2",
				"tpTriggerPx": "80", "slTriggerPx": "110"}}
		}
	case "/api/v5/account/balance":
		data = []any{map[string]any{"totalEq": "1000.5"}}
	}
	json.NewEncoder(w).Encode(map[string]any{"code": "0", "msg": "", "data": data})
}

func TestSnapshotThroughProxy(t *testing.T) {
	fp := &fakeProxy{}
	px := httptest.NewServer(fp)
	defer px.Close()
	pu, _ := url.Parse(px.URL)
	s := New(Config{Key: "k", Secret: "s", Passphrase: "p", Proxy: pu})
	s.c.BaseURL = "http://okx.invalid" // only reachable through the proxy

	var wg sync.WaitGroup
	snaps := make([]Snapshot, 5)
	for i := range snaps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			snap, err := s.Snapshot(context.Background(), "")
			if err != nil {
				t.Error(err)
			}
			snaps[i] = snap
		}(i)
	}
	wg.Wait()
	if n := fp.calls.Load(); n != 4 { // positions, conditional, oco, balance: once for all callers
		t.Fatalf("%d requests, want 4 (single flight + cache): %v", n, fp.paths)
	}
	for _, p := range fp.paths {
		if !strings.HasPrefix(p, "GET ") {
			t.Fatalf("non-GET request: %s", p)
		}
	}
	snap := snaps[0]
	if !snap.Enabled || snap.Equity == nil || snap.Equity.TotalEq != 1000.5 || len(snap.Positions) != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	p := snap.Positions[0]
	if p.Side != "short" || len(p.TP) != 1 || p.TP[0].Px != 80 || !p.TP[0].Full || len(p.SL) != 1 || p.SL[0].Px != 110 || p.SL[0].Kind != "oco" {
		t.Fatalf("position: %+v", p)
	}
	if f, _ := s.Snapshot(context.Background(), "ETH-USDT-SWAP"); len(f.Positions) != 0 || !f.Enabled {
		t.Fatalf("filter: %+v", f)
	}
}

func TestReadOnlyPaths(t *testing.T) {
	c := NewClient(Config{Key: "k", Secret: "s", Passphrase: "p", Proxy: &url.URL{Scheme: "http", Host: "127.0.0.1:1"}})
	var out any
	if err := c.get(context.Background(), "/api/v5/trade/order", nil, &out); err == nil || !strings.Contains(err.Error(), "not an allowed") {
		t.Fatalf("order endpoint not refused: %v", err)
	}
	c = NewClient(Config{Key: "k", Secret: "s", Passphrase: "p"})
	if err := c.get(context.Background(), "/api/v5/account/positions", nil, &out); err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("request without a proxy: %v", err)
	}
}

func TestAPIErrorKeepsSecretsOut(t *testing.T) {
	fp := &fakeProxy{}
	px := httptest.NewServer(fp)
	defer px.Close()
	pu, _ := url.Parse(px.URL)
	pu.User = url.UserPassword("puser", "ppass")
	s := New(Config{Key: "k", Secret: "wrong-secret", Passphrase: "p", Proxy: pu})
	s.c.BaseURL = "http://okx.invalid"
	_, err := s.Snapshot(context.Background(), "")
	if err == nil {
		t.Fatal("bad signature accepted")
	}
	for _, secret := range []string{"wrong-secret", "ppass", "OK-ACCESS"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks %q: %v", secret, err)
		}
	}
}

func TestRoute(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{}).Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/account/positions?instId=btc-usdt-swap", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"enabled":false,"positions":[]}` {
		t.Fatalf("disabled route: %d %s", w.Code, w.Body)
	}
}

func TestReadAllowed(t *testing.T) {
	for p, want := range map[string]bool{
		"/api/v5/asset/deposit-history":    true,
		"/api/v5/account/bills-archive":    true,
		"/api/v5/trade/fills-history":      true,
		"/api/v5/asset/":                   false,
		"/api/v5/market/tickers":           false,
		"/api/v5/asset/../trade/order":     false,
		"/api/v5/asset/bills?x=1":          false,
		"/api/v5/asset/bills%2F..%2Fother": false,
		"/api/v5/account/positions":        true,
	} {
		if ReadAllowed(p) != want {
			t.Errorf("ReadAllowed(%q) = %v", p, !want)
		}
	}
}
