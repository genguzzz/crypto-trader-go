package okxorder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"crypto-trader-go/internal/okxacct"
)

var btc = Spec{CtVal: 0.01, LotSz: 0.01, MinSz: 0.01, TickSz: 0.1, MaxLever: 100}

func intent() Intent {
	return Intent{InstID: "BTC-USDT-SWAP", Side: "long", MarginUSDT: 20, Leverage: 5, SL: 98000, TP: 104000.04, ClientID: "ctgp1"}
}

func TestBuildLongShortMode(t *testing.T) {
	o, err := Build(intent(), btc, 100000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	z := o.Sizing()
	// 20 U × 5 / (100000 × 0.01) = 0.1 contracts
	if z.Contracts != 0.1 || z.NotionalUSDT != 100 || z.MarginUSDT != 20 || z.RR < 1.99 || z.RR > 2.01 {
		t.Fatalf("sizing %+v", z)
	}
	rq := o.Requests()
	if len(rq) != 2 || rq[0].Path != "/api/v5/account/set-leverage" || rq[1].Path != "/api/v5/trade/order" || rq[0].Method != "POST" {
		t.Fatalf("requests %+v", rq)
	}
	if got := string(rq[0].Body); got != `{"instId":"BTC-USDT-SWAP","lever":"5","mgnMode":"isolated","posSide":"long"}` {
		t.Fatalf("leverage body %s", got)
	}
	want := `{"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"buy","posSide":"long","ordType":"market","sz":"0.10","clOrdId":"ctgp1",` +
		`"attachAlgoOrds":[{"tpTriggerPx":"104000.0","tpOrdPx":"-1","tpTriggerPxType":"last","slTriggerPx":"98000.0","slOrdPx":"-1","slTriggerPxType":"last"}]}`
	if got := string(rq[1].Body); got != want {
		t.Fatalf("order body\n got %s\nwant %s", got, want)
	}
}

func TestBuildShortNetMode(t *testing.T) {
	in := intent()
	in.Side, in.SL, in.TP = "short", 102000, 96000
	o, err := Build(in, btc, 100000, NetMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	b := string(o.Requests()[1].Body)
	if !strings.Contains(b, `"side":"sell"`) || strings.Contains(b, "posSide") {
		t.Fatalf("net-mode short: %s", b)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]func(*Intent){
		"stop on the wrong side":      func(i *Intent) { i.SL = 101000 },
		"target on the wrong side":    func(i *Intent) { i.TP = 99000 },
		"no stop":                     func(i *Intent) { i.SL = 0 },
		"no target":                   func(i *Intent) { i.TP = 0 },
		"stop too tight":              func(i *Intent) { i.SL = 99900 },
		"stop too wide":               func(i *Intent) { i.SL, i.TP = 80000, 150000 },
		"reward below risk":           func(i *Intent) { i.TP = 101000 },
		"leverage above cap":          func(i *Intent) { i.Leverage = 6 },
		"leverage zero":               func(i *Intent) { i.Leverage = 0 },
		"margin above cap":            func(i *Intent) { i.MarginUSDT = 51 },
		"margin zero":                 func(i *Intent) { i.MarginUSDT = 0 },
		"not a USDT swap":             func(i *Intent) { i.InstID = "BTC-USD-SWAP" },
		"bad side":                    func(i *Intent) { i.Side = "buy" },
		"below minimum size":          func(i *Intent) { i.MarginUSDT = 1 },
		"stop beyond liquidation":     func(i *Intent) { i.Leverage, i.SL, i.TP = 5, 83000, 120000 },
		"bad client id":               func(i *Intent) { i.ClientID = "a-b" },
		"leverage above the contract": func(i *Intent) { i.Leverage = 5 },
	}
	for name, mod := range cases {
		in := intent()
		mod(&in)
		spec := btc
		if name == "leverage above the contract" {
			spec.MaxLever = 3
		}
		if name == "stop beyond liquidation" {
			lim := Limits{MaxStopPct: 50}
			if _, err := Build(in, spec, 100000, LongShortMode, lim); err == nil || !strings.Contains(err.Error(), "liquidation") {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if _, err := Build(in, spec, 100000, LongShortMode, Limits{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Build(intent(), btc, 0, LongShortMode, Limits{}); err == nil {
		t.Error("no price accepted")
	}
	if _, err := Build(intent(), btc, 100000, "", Limits{}); err == nil {
		t.Error("unknown position mode accepted")
	}
}

func TestModeFromEnv(t *testing.T) {
	for v, want := range map[string]Mode{"": DryRun, "live": Live, "LIVE": DryRun, " live": DryRun, "dryrun": DryRun, "true": DryRun} {
		t.Setenv("CTG_TRADE_MODE", v)
		if got := ModeFromEnv(); got != want {
			t.Errorf("%q → %s", v, got)
		}
	}
}

// fakeOKX is a forward proxy answering OKX itself: whatever the client sends ends up here.
type fakeOKX struct {
	hits  atomic.Int32
	paths []string
	fail  string // path answered with an error
}

func (f *fakeOKX) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	io.ReadAll(r.Body)
	if r.Header.Get("OK-ACCESS-SIGN") == "" || r.Header.Get("OK-ACCESS-KEY") != "k" {
		w.Write([]byte(`{"code":"50113","msg":"unsigned"}`))
		return
	}
	switch {
	case r.URL.Path == f.fail:
		w.Write([]byte(`{"code":"1","msg":"All operations failed","data":[{"sCode":"51008","sMsg":"Insufficient margin"}]}`))
	case r.URL.Path == "/api/v5/trade/order":
		w.Write([]byte(`{"code":"0","msg":"","data":[{"ordId":"777","clOrdId":"ctgp1","sCode":"0","sMsg":""}]}`))
	default:
		w.Write([]byte(`{"code":"0","msg":"","data":[{"lever":"5"}]}`))
	}
}

func cfgVia(srv *httptest.Server) okxacct.Config {
	u, _ := url.Parse(srv.URL)
	return okxacct.Config{Key: "k", Secret: "s", Passphrase: "p", Proxy: u}
}

// A dry run never sends: the client has no HTTP path at all, even with a complete key and proxy.
func TestDryRunNeverSends(t *testing.T) {
	f := &fakeOKX{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, m := range []Mode{DryRun, "", "LIVE", "paper"} {
		c, err := New(m, cfgVia(srv))
		if err != nil || c.Mode() != DryRun {
			t.Fatalf("mode %q: %v %v", m, c, err)
		}
		o, _ := Build(intent(), btc, 100000, LongShortMode, Limits{})
		res, err := c.Place(context.Background(), o)
		if err != nil || res.Mode != DryRun || len(res.Requests) != 2 || res.OrdID != "" || len(res.Responses) != 0 {
			t.Fatalf("dry run result %+v %v", res, err)
		}
	}
	if f.hits.Load() != 0 {
		t.Fatalf("dry run sent %d requests", f.hits.Load())
	}
	if n := reflect.TypeOf(dryRun{}).NumField(); n != 0 {
		t.Fatalf("dryRun has %d fields; it must hold no HTTP client", n)
	}
	if _, err := (dryRun{}).Place(context.Background(), Order{}); err == nil {
		t.Fatal("unvalidated order placed")
	}
}

func TestLiveSendsThroughProxy(t *testing.T) {
	f := &fakeOKX{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c, err := New(Live, cfgVia(srv))
	if err != nil {
		t.Fatal(err)
	}
	c.(*live).base = "http://okx.invalid" // only reachable through the proxy
	o, _ := Build(intent(), btc, 100000, LongShortMode, Limits{})
	res, err := c.Place(context.Background(), o)
	if err != nil || res.OrdID != "777" || len(res.Responses) != 2 {
		t.Fatalf("live result %+v %v", res, err)
	}
	if strings.Join(f.paths, ",") != "POST /api/v5/account/set-leverage,POST /api/v5/trade/order" {
		t.Fatalf("paths %v", f.paths)
	}
	// a failed leverage call stops before the order
	f2 := &fakeOKX{fail: "/api/v5/account/set-leverage"}
	srv2 := httptest.NewServer(f2)
	defer srv2.Close()
	c2, _ := New(Live, cfgVia(srv2))
	c2.(*live).base = "http://okx.invalid"
	res, err = c2.Place(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "Insufficient margin") || len(f2.paths) != 1 || res.OrdID != "" {
		t.Fatalf("leverage failure: %v %v %+v", err, f2.paths, res)
	}
	// only the two allowed POSTs exist
	if _, _, err := c.(*live).post(context.Background(), Request{Method: "POST", Path: "/api/v5/asset/withdrawal", Body: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("withdrawal path allowed")
	}
	if _, err := New(Live, okxacct.Config{}); err == nil {
		t.Fatal("live without a key")
	}
}

func TestLeverageRange(t *testing.T) {
	lim := Limits{MinLeverage: 5, MaxLeverage: 8}
	for lev, ok := range map[int]bool{4: false, 5: true, 8: true, 9: false} {
		in := intent()
		in.Leverage = lev
		_, err := Build(in, btc, 100000, LongShortMode, lim)
		if (err == nil) != ok {
			t.Errorf("%d×: %v", lev, err)
		}
		if err != nil && !strings.Contains(err.Error(), "5–8×") {
			t.Errorf("%d×: message %v", lev, err)
		}
	}
	if l := (Limits{MinLeverage: 10, MaxLeverage: 8}).WithDefaults(); l.MinLeverage != 1 {
		t.Fatalf("a floor above the cap is ignored: %+v", l)
	}
}

func TestMarginRange(t *testing.T) {
	lim := Limits{MinMarginUSDT: 50, MaxMarginUSDT: 200, MaxLeverage: 8}
	for m, ok := range map[float64]bool{40: false, 50: true, 120: true, 200: true, 201: false} {
		in := intent()
		in.MarginUSDT = m
		_, err := Build(in, btc, 100000, LongShortMode, lim)
		if (err == nil) != ok {
			t.Errorf("%g U: %v", m, err)
		}
		if err != nil && !strings.Contains(err.Error(), "50–200 USDT") {
			t.Errorf("%g U: message %v", m, err)
		}
	}
}
