package okxorder

import (
	"context"
	"encoding/json"
	"io"
	"math"
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

// ---- kinds × order types × margin modes (2026-11-01) ----

func bodies(o Order) []string {
	var out []string
	for _, r := range o.Requests() {
		s := r.Path + " " + string(r.Body)
		if r.Optional {
			s += " (optional)"
		}
		out = append(out, s)
	}
	return out
}

func wantBodies(t *testing.T, o Order, want ...string) {
	t.Helper()
	got := bodies(o)
	if len(got) != len(want) {
		t.Fatalf("requests\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

func TestOpenLimitCross(t *testing.T) {
	in := intent()
	in.MarginMode, in.OrdType, in.Px, in.AlgoID = Cross, Limit, 99500, "ctga1"
	if _, err := Build(in, btc, 100000, LongShortMode, Limits{}); err != nil {
		t.Fatal(err)
	}
	o, err := Build(in, btc, 100000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if z := o.Sizing(); z.EntryPx != 99500 || z.Contracts != 0.1 || z.Note == "" { // 20 × 5 / (99500 × 0.01) = 0.1005 → 0.1; equity unknown
		t.Fatalf("sizing %+v", z)
	}
	wantBodies(t, o,
		`/api/v5/account/set-leverage {"instId":"BTC-USDT-SWAP","lever":"5","mgnMode":"cross"}`,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"cross","side":"buy","posSide":"long","ordType":"limit","sz":"0.10","px":"99500.0","clOrdId":"ctgp1",`+
			`"attachAlgoOrds":[{"attachAlgoClOrdId":"ctga1","tpTriggerPx":"104000.0","tpOrdPx":"-1","tpTriggerPxType":"last","slTriggerPx":"98000.0","slOrdPx":"-1","slTriggerPxType":"last"}]}`)
	// short limit, isolated, net mode
	in = intent()
	in.Side, in.SL, in.TP, in.OrdType, in.Px = "short", 102500, 96000, Limit, 100400
	o, err = Build(in, btc, 100000, NetMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wantBodies(t, o,
		`/api/v5/account/set-leverage {"instId":"BTC-USDT-SWAP","lever":"5","mgnMode":"isolated"}`,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","ordType":"limit","sz":"0.09","px":"100400.0","clOrdId":"ctgp1",`+
			`"attachAlgoOrds":[{"tpTriggerPx":"96000.0","tpOrdPx":"-1","tpTriggerPxType":"last","slTriggerPx":"102500.0","slOrdPx":"-1","slTriggerPxType":"last"}]}`)
	// cross: the stop is checked against the liquidation the available equity allows
	in = intent()
	in.MarginMode, in.Equity = Cross, 110 // 110 U behind 100 U notional ≈ 109.5% away: fine
	if o, err := Build(in, btc, 100000, LongShortMode, Limits{}); err != nil || o.Sizing().Note != "" || o.Sizing().LiqPx > 0 {
		t.Fatalf("cross with equity: %+v %v", o.Sizing(), err)
	}
	in.Leverage, in.MarginUSDT, in.Equity = 5, 50, 6 // 250 U notional on 6 U: liquidation ≈ 1.9% away, stop 2%: too close
	if _, err := Build(in, btc, 100000, LongShortMode, Limits{}); err == nil || !strings.Contains(err.Error(), "cross margin") {
		t.Fatalf("cross liquidation: %v", err)
	}
	// a limit far from the price, or without a price
	in = intent()
	in.OrdType, in.Px = Limit, 90000
	if _, err := Build(in, btc, 100000, LongShortMode, Limits{}); err == nil || !strings.Contains(err.Error(), "limit price") {
		t.Fatalf("far limit: %v", err)
	}
	in.Px = 0
	if _, err := Build(in, btc, 100000, LongShortMode, Limits{}); err == nil {
		t.Fatal("limit without a price")
	}
}

func TestResting(t *testing.T) {
	cases := []struct {
		side    string
		closing bool
		px      float64
		ok      bool
	}{{"long", false, 99000, true}, {"long", false, 100200, false}, {"short", false, 100500, true}, {"short", false, 99000, false},
		{"long", true, 101000, true}, {"long", true, 99000, false}, {"short", true, 99000, true}, {"short", true, 101000, false}}
	for _, c := range cases {
		if err := Resting(c.side, c.closing, c.px, 100000); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

// a long of 0.1 contracts at 100000 with ctg's attached TP/SL (ctga1) and one of the owner's own
func longPos() okxacct.Position {
	return okxacct.Position{InstID: "BTC-USDT-SWAP", PosSide: "long", Side: "long", MgnMode: Isolated, Contracts: 0.1, AvgPx: 100000, Lever: 5,
		TP: []okxacct.Trigger{{AlgoID: "A1", ClientID: "ctga1", Px: 104000, Contracts: 0.1}, {AlgoID: "O1", Px: 106000, Contracts: 0.05}},
		SL: []okxacct.Trigger{{AlgoID: "A1", ClientID: "ctga1", Px: 98000, Contracts: 0.1}}}
}

func addIntent() Intent {
	return Intent{InstID: "BTC-USDT-SWAP", Side: "long", MarginUSDT: 20, Leverage: 5, SL: 98500, TP: 104000, ClientID: "ctgp2", AlgoID: "ctgt2"}
}

func TestAdd(t *testing.T) {
	o, err := BuildAdd(addIntent(), longPos(), btc, 101000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	z := o.Sizing()
	// 20 × 5 / (101000 × 0.01) = 0.099 → 0.09; average (0.1 × 100000 + 0.09 × 101000) / 0.19
	if z.Contracts != 0.09 || z.PosContracts != 0.1 || math.Abs(z.NewAvgPx-100473.68) > 0.01 || z.LiqPx < 80000 || z.LiqPx > 82000 {
		t.Fatalf("sizing %+v", z)
	}
	if !strings.Contains(z.TPSL, "整仓") || !strings.Contains(z.TPSL, "撤掉 ctg 原先") || !strings.Contains(z.TPSL, "保留不动") {
		t.Fatalf("tpsl text %q", z.TPSL)
	}
	wantBodies(t, o,
		`/api/v5/trade/order-algo {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","posSide":"long","ordType":"oco","closeFraction":"1",`+
			`"tpTriggerPx":"104000.0","tpOrdPx":"-1","tpTriggerPxType":"last","slTriggerPx":"98500.0","slOrdPx":"-1","slTriggerPxType":"last","algoClOrdId":"ctgt2"}`,
		`/api/v5/trade/cancel-algos [{"instId":"BTC-USDT-SWAP","algoId":"A1"}] (optional)`,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"buy","posSide":"long","ordType":"market","sz":"0.09","clOrdId":"ctgp2"}`)

	// a whole-position TP/SL already there (the owner's): amended, and the proposal says whose
	pos := longPos()
	pos.TP = append(pos.TP, okxacct.Trigger{AlgoID: "F9", Px: 107000, Full: true})
	pos.SL = append(pos.SL, okxacct.Trigger{AlgoID: "F9", Px: 97000, Full: true})
	in := addIntent()
	in.OrdType, in.Px = Limit, 100500
	o, err = BuildAdd(in, pos, btc, 101000, NetMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.Sizing().TPSL, "你在 OKX 设置的整仓止盈止损（algoId F9）") {
		t.Fatalf("tpsl text %q", o.Sizing().TPSL)
	}
	wantBodies(t, o,
		`/api/v5/trade/amend-algos {"instId":"BTC-USDT-SWAP","algoId":"F9","newTpTriggerPx":"104000.0","newTpOrdPx":"-1","newTpTriggerPxType":"last",`+
			`"newSlTriggerPx":"98500.0","newSlOrdPx":"-1","newSlTriggerPxType":"last"}`,
		`/api/v5/trade/cancel-algos [{"instId":"BTC-USDT-SWAP","algoId":"A1"}] (optional)`,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"buy","ordType":"limit","sz":"0.09","px":"100500.0","clOrdId":"ctgp2"}`)

	bad := map[string]func(*Intent, *okxacct.Position){
		"wider stop":         func(i *Intent, _ *okxacct.Position) { i.SL = 97500 },
		"other leverage":     func(i *Intent, _ *okxacct.Position) { i.Leverage = 4 },
		"other margin mode":  func(i *Intent, _ *okxacct.Position) { i.MarginMode = Cross },
		"other side":         func(i *Intent, p *okxacct.Position) { p.Side = "short" },
		"no position":        func(i *Intent, p *okxacct.Position) { p.Contracts = 0 },
		"reward below risk":  func(i *Intent, _ *okxacct.Position) { i.TP = 102000 },
		"margin above cap":   func(i *Intent, _ *okxacct.Position) { i.MarginUSDT = 60 },
		"leverage above cap": func(i *Intent, p *okxacct.Position) { i.Leverage, p.Lever = 10, 10 },
		"no TP/SL client id": func(i *Intent, _ *okxacct.Position) { i.AlgoID = "" },
	}
	for name, mod := range bad {
		in, p := addIntent(), longPos()
		mod(&in, &p)
		if _, err := BuildAdd(in, p, btc, 101000, LongShortMode, Limits{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReduceAndClose(t *testing.T) {
	in := Intent{InstID: "BTC-USDT-SWAP", Side: "long", Fraction: 0.5, ClientID: "ctgp3", AlgoID: "ctgt3"}
	o, err := BuildReduce(in, false, longPos(), btc, 101000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if z := o.Sizing(); z.Contracts != 0.05 || z.PosContracts != 0.1 || !strings.Contains(z.TPSL, "整仓") {
		t.Fatalf("sizing %+v", z)
	}
	// ctg's size-bound TP/SL becomes one for the whole position (same levels), then the reduce-only sell
	wantBodies(t, o,
		`/api/v5/trade/order-algo {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","posSide":"long","ordType":"oco","closeFraction":"1",`+
			`"tpTriggerPx":"104000.0","tpOrdPx":"-1","tpTriggerPxType":"last","slTriggerPx":"98000.0","slOrdPx":"-1","slTriggerPxType":"last","algoClOrdId":"ctgt3"}`,
		`/api/v5/trade/cancel-algos [{"instId":"BTC-USDT-SWAP","algoId":"A1"}] (optional)`,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","posSide":"long","ordType":"market","sz":"0.05","clOrdId":"ctgp3"}`)

	// net mode, limit, a whole-position TP/SL already covering it: only the reduce-only order
	pos := longPos()
	pos.PosSide, pos.MgnMode = "net", Cross
	pos.TP, pos.SL = []okxacct.Trigger{{AlgoID: "T7", ClientID: "ctgt7", Px: 104000, Full: true}}, []okxacct.Trigger{{AlgoID: "T7", ClientID: "ctgt7", Px: 98000, Full: true}}
	in.OrdType, in.Px, in.Fraction = Limit, 102000, 0.3
	o, err = BuildReduce(in, false, pos, btc, 101000, NetMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wantBodies(t, o,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"cross","side":"sell","ordType":"limit","sz":"0.03","px":"102000.0","reduceOnly":true,"clOrdId":"ctgp3"}`)

	// full market close: close-position, then ctg's TP/SL cancelled (best effort)
	o, err = BuildReduce(Intent{InstID: "BTC-USDT-SWAP", Side: "long", ClientID: "ctgp4"}, true, longPos(), btc, 101000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wantBodies(t, o,
		`/api/v5/trade/close-position {"instId":"BTC-USDT-SWAP","mgnMode":"isolated","posSide":"long","autoCxl":false,"clOrdId":"ctgp4"}`,
		`/api/v5/trade/cancel-algos [{"instId":"BTC-USDT-SWAP","algoId":"A1"}] (optional)`)
	// full limit close of a short: a reduce-only buy for the whole size; ctg's algos are listed to cancel after the fill
	pos = longPos()
	pos.Side, pos.PosSide = "short", "short"
	pos.TP, pos.SL = []okxacct.Trigger{{AlgoID: "A5", ClientID: "ctga5", Px: 96000}}, []okxacct.Trigger{{AlgoID: "A5", ClientID: "ctga5", Px: 102000}}
	o, err = BuildReduce(Intent{InstID: "BTC-USDT-SWAP", Side: "short", OrdType: Limit, Px: 99000, ClientID: "ctgp5"}, true, pos, btc, 100000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := o.Sizing().AlgoIDs; len(ids) != 1 || ids[0] != "A5" {
		t.Fatalf("algo ids %v", ids)
	}
	wantBodies(t, o,
		`/api/v5/trade/order {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"buy","posSide":"short","ordType":"limit","sz":"0.10","px":"99000.0","clOrdId":"ctgp5"}`)

	for name, f := range map[string]float64{"all of it": 1, "none": 0, "below min": 0.05} {
		in := Intent{InstID: "BTC-USDT-SWAP", Side: "long", Fraction: f, ClientID: "ctgp3", AlgoID: "ctgt3"}
		if _, err := BuildReduce(in, false, longPos(), btc, 101000, LongShortMode, Limits{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := BuildReduce(Intent{InstID: "BTC-USDT-SWAP", Side: "short", ClientID: "ctgp4"}, true, longPos(), btc, 101000, LongShortMode, Limits{}); err == nil {
		t.Error("closed the wrong side")
	}
}

func TestAdjust(t *testing.T) {
	in := Intent{InstID: "BTC-USDT-SWAP", Side: "long", SL: 100100, TP: 105000, ClientID: "ctgp6", AlgoID: "ctgt6"} // stop to break-even
	o, err := BuildAdjust(in, longPos(), btc, 101000, LongShortMode, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wantBodies(t, o,
		`/api/v5/trade/order-algo {"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","posSide":"long","ordType":"oco","closeFraction":"1",`+
			`"tpTriggerPx":"105000.0","tpOrdPx":"-1","tpTriggerPxType":"last","slTriggerPx":"100100.0","slOrdPx":"-1","slTriggerPxType":"last","algoClOrdId":"ctgt6"}`,
		`/api/v5/trade/cancel-algos [{"instId":"BTC-USDT-SWAP","algoId":"A1"}] (optional)`)
	for name, mod := range map[string]func(*Intent){
		"wider stop":          func(i *Intent) { i.SL = 97000 },
		"stop above price":    func(i *Intent) { i.SL = 101500 },
		"target below price":  func(i *Intent) { i.TP = 100500 },
		"stop inside noise":   func(i *Intent) { i.SL = 100900 },
		"no take-profit":      func(i *Intent) { i.TP = 0 },
		"foreign TP/SL id":    func(i *Intent) { i.AlgoID = "mine1" },
		"not a USDT-perp":     func(i *Intent) { i.InstID = "BTC-USD-SWAP" },
		"position other side": func(i *Intent) { i.Side = "short"; i.SL, i.TP = 102000, 99000 },
	} {
		in := in
		mod(&in)
		if _, err := BuildAdjust(in, longPos(), btc, 101000, LongShortMode, Limits{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCancelOnlyOwn(t *testing.T) {
	o, err := CancelOrder("BTC-USDT-SWAP", "ctgp12")
	if err != nil {
		t.Fatal(err)
	}
	wantBodies(t, o, `/api/v5/trade/cancel-order {"instId":"BTC-USDT-SWAP","clOrdId":"ctgp12"}`)
	for _, id := range []string{"", "12", "abc", "ctga12", "ctgp", "ctgp1x"} {
		if _, err := CancelOrder("BTC-USDT-SWAP", id); err == nil {
			t.Errorf("cancel %q accepted", id)
		}
	}
	if _, err := CancelAlgos("BTC-USDT-SWAP", nil); err == nil {
		t.Error("empty cancel")
	}
}

// The live client sends nothing outside the allow-list: no other path, no other body key, no order or
// TP/SL without a ctg client id, no cancel of someone else's order. A refused request stops the whole
// order before anything is sent.
func TestAllowList(t *testing.T) {
	ok := []Request{
		{Method: "POST", Path: pathLeverage, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","lever":"5","mgnMode":"cross"}`)},
		{Method: "POST", Path: pathCancelOrder, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","clOrdId":"ctgp3"}`)},
		{Method: "POST", Path: pathCancelAlgos, Body: json.RawMessage(`[{"instId":"BTC-USDT-SWAP","algoId":"9"}]`)},
	}
	for _, r := range ok {
		if err := checkRequest(r); err != nil {
			t.Errorf("%s refused: %v", r.Path, err)
		}
	}
	bad := map[string]Request{
		"withdrawal":         {Method: "POST", Path: "/api/v5/asset/withdrawal", Body: json.RawMessage(`{"ccy":"USDT","amt":"1"}`)},
		"transfer":           {Method: "POST", Path: "/api/v5/asset/transfer", Body: json.RawMessage(`{}`)},
		"batch orders":       {Method: "POST", Path: "/api/v5/trade/batch-orders", Body: json.RawMessage(`[]`)},
		"amend order":        {Method: "POST", Path: "/api/v5/trade/amend-order", Body: json.RawMessage(`{}`)},
		"position mode":      {Method: "POST", Path: "/api/v5/account/set-position-mode", Body: json.RawMessage(`{"posMode":"net_mode"}`)},
		"GET":                {Method: "GET", Path: pathOrder, Body: json.RawMessage(`{}`)},
		"extra key":          {Method: "POST", Path: pathOrder, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"buy","ordType":"market","sz":"1","clOrdId":"ctgp1","ccy":"USDT"}`)},
		"spot":               {Method: "POST", Path: pathOrder, Body: json.RawMessage(`{"instId":"BTC-USDT","tdMode":"cash","side":"buy","ordType":"market","sz":"1","clOrdId":"ctgp1"}`)},
		"cash margin":        {Method: "POST", Path: pathOrder, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","tdMode":"cash","side":"buy","ordType":"market","sz":"1","clOrdId":"ctgp1"}`)},
		"order without id":   {Method: "POST", Path: pathOrder, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"buy","ordType":"market","sz":"1"}`)},
		"cancel foreign":     {Method: "POST", Path: pathCancelOrder, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","ordId":"123"}`)},
		"cancel foreign cid": {Method: "POST", Path: pathCancelOrder, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","clOrdId":"mine"}`)},
		"sized algo":         {Method: "POST", Path: pathOrderAlgo, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","ordType":"conditional","sz":"1","algoClOrdId":"ctgt1"}`)},
		"foreign algo":       {Method: "POST", Path: pathOrderAlgo, Body: json.RawMessage(`{"instId":"BTC-USDT-SWAP","tdMode":"isolated","side":"sell","ordType":"oco","closeFraction":"1","algoClOrdId":"x1"}`)},
		"cancel all algos":   {Method: "POST", Path: pathCancelAlgos, Body: json.RawMessage(`[]`)},
		"not JSON":           {Method: "POST", Path: pathClosePos, Body: json.RawMessage(`nope`)},
	}
	for name, r := range bad {
		if err := checkRequest(r); err == nil {
			t.Errorf("%s allowed", name)
		}
	}
	// every request any builder makes passes the check
	var all []Order
	o1, _ := Build(intent(), btc, 100000, LongShortMode, Limits{})
	o2, _ := BuildAdd(addIntent(), longPos(), btc, 101000, LongShortMode, Limits{})
	o3, _ := BuildReduce(Intent{InstID: "BTC-USDT-SWAP", Side: "long", Fraction: 0.5, ClientID: "ctgp3", AlgoID: "ctgt3"}, false, longPos(), btc, 101000, NetMode, Limits{})
	o4, _ := BuildReduce(Intent{InstID: "BTC-USDT-SWAP", Side: "long", ClientID: "ctgp4"}, true, longPos(), btc, 101000, LongShortMode, Limits{})
	o5, _ := BuildAdjust(Intent{InstID: "BTC-USDT-SWAP", Side: "long", SL: 100100, TP: 105000, ClientID: "ctgp6", AlgoID: "ctgt6"}, longPos(), btc, 101000, LongShortMode, Limits{})
	o6, _ := CancelOrder("BTC-USDT-SWAP", "ctgp7")
	o7, _ := CancelAlgos("BTC-USDT-SWAP", []string{"A1"})
	all = append(all, o1, o2, o3, o4, o5, o6, o7)
	for i, o := range all {
		if len(o.Requests()) == 0 {
			t.Fatalf("order %d empty", i)
		}
		for _, r := range o.Requests() {
			if err := checkRequest(r); err != nil {
				t.Errorf("order %d: %v", i, err)
			}
		}
	}
}

// fakeSeq answers OKX for a whole action and records the paths; failPath answers with an error.
type fakeSeq struct {
	paths    []string
	failPath string
}

func (f *fakeSeq) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.paths = append(f.paths, r.URL.Path)
	io.ReadAll(r.Body)
	switch r.URL.Path {
	case f.failPath:
		w.Write([]byte(`{"code":"1","msg":"failed","data":[{"sCode":"51000","sMsg":"Parameter error"}]}`))
	case pathOrder:
		w.Write([]byte(`{"code":"0","data":[{"ordId":"888","clOrdId":"ctgp2","sCode":"0","sMsg":""}]}`))
	case pathOrderAlgo:
		w.Write([]byte(`{"code":"0","data":[{"algoId":"T1","algoClOrdId":"ctgt2","sCode":"0","sMsg":""}]}`))
	default:
		w.Write([]byte(`{"code":"0","data":[{"sCode":"0"}]}`))
	}
}

func TestLiveAddSequence(t *testing.T) {
	run := func(fail string) (*fakeSeq, Result, error) {
		f := &fakeSeq{failPath: fail}
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		c, _ := New(Live, cfgVia(srv))
		c.(*live).base = "http://okx.invalid"
		o, err := BuildAdd(addIntent(), longPos(), btc, 101000, LongShortMode, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.Place(context.Background(), o)
		return f, res, err
	}
	f, res, err := run("")
	if err != nil || res.OrdID != "888" || res.AlgoID != "T1" || len(f.paths) != 3 || f.paths[0] != pathOrderAlgo || f.paths[2] != pathOrder {
		t.Fatalf("add: %v %+v %v", err, res, f.paths)
	}
	// a failing cancel of the superseded TP/SL is a warning; the add still goes
	f, res, err = run(pathCancelAlgos)
	if err != nil || len(res.Warnings) != 1 || len(f.paths) != 3 || res.OrdID != "888" {
		t.Fatalf("optional failure: %v %+v %v", err, res, f.paths)
	}
	// a failing new TP/SL stops before the add: never more size without the stop on it
	f, res, err = run(pathOrderAlgo)
	if err == nil || len(f.paths) != 1 || res.OrdID != "" {
		t.Fatalf("TP/SL failure: %v %+v %v", err, res, f.paths)
	}
}
