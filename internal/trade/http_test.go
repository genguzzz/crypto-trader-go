package trade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type client struct {
	t   *testing.T
	srv *httptest.Server
}

func (c client) do(method, path, body string, wantCode int) map[string]any {
	c.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	req, _ := http.NewRequest(method, c.srv.URL+"/api/v1/trade"+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		c.t.Fatalf("%s %s: %d %s, want %d", method, path, resp.StatusCode, raw, wantCode)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		c.t.Fatalf("%s %s: invalid JSON %q", method, path, raw)
	}
	if m, ok := out.(map[string]any); ok {
		return m
	}
	return map[string]any{"list": out}
}

func TestHTTP(t *testing.T) {
	md := newFakeMD(t0)
	md.setQuote(btc, btcQuote(100000))
	s, _ := newSvc(t, md)
	r := &fakeRunner{versions: []StrategyVersion{testVersion}}
	s.SetRunner(r)
	var changes atomic.Int32
	s.SetOnChange(func(id int64, kind string) { changes.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := client{t, srv}

	acc := c.do("POST", "/accounts", `{"name":"纸面","mode":"live","deposit":1000}`, 201)
	id := int64(acc["id"].(float64))
	if acc["equity"].(float64) != 1000 || acc["limits"].(map[string]any)["maxPositions"].(float64) != 5 {
		t.Fatalf("account %v", acc)
	}
	for _, bad := range []string{
		`{"name":"x","mode":"real","deposit":1}`,
		`{"name":"","mode":"live","deposit":1}`,
		`{"name":"x","mode":"live","deposit":-5}`,
		`{"name":"x","mode":"backtest","deposit":100,"from":5,"to":1}`,
		`{"name":"x","mode":"live","deposit":100,"limits":{"maxPositions":0}}`,
		`{"name":"x","mode":"live","deposit":100,"bogus":1}`,
	} {
		e := c.do("POST", "/accounts", bad, 400)
		if e["error"] == "" {
			t.Fatalf("%s: no error message", bad)
		}
	}
	if e := c.do("POST", "/accounts", `{"name":"x","mode":"real","deposit":1}`, 400); !strings.Contains(e["error"].(string), "实盘") {
		t.Fatalf("real mode: %v", e)
	}
	if l := c.do("GET", "/accounts", "", 200)["list"].([]any); len(l) != 1 {
		t.Fatalf("accounts %v", l)
	}
	c.do("GET", "/accounts/999", "", 404)
	c.do("GET", "/accounts/abc", "", 400)
	p := c.do("PATCH", fmt.Sprintf("/accounts/%d", id), `{"name":"改名"}`, 200)
	if p["name"] != "改名" {
		t.Fatalf("patch %v", p)
	}
	d := c.do("POST", fmt.Sprintf("/accounts/%d/deposit", id), `{"amount":500}`, 200)
	if d["deposit"].(float64) != 1500 || d["equity"].(float64) != 1500 {
		t.Fatalf("deposit %v", d)
	}
	c.do("POST", fmt.Sprintf("/accounts/%d/deposit", id), `{"amount":0}`, 400)
	det := c.do("GET", fmt.Sprintf("/accounts/%d", id), "", 200)
	if len(det["positions"].([]any)) != 0 || len(det["orders"].([]any)) != 0 {
		t.Fatalf("detail %v", det)
	}

	rel := c.do("POST", "/strategies/reload", "", 200)
	if len(rel["strategies"].([]any)) != 1 { // Run's discovery may have loaded it already
		t.Fatalf("reload %v", rel)
	}
	strats := c.do("GET", "/strategies", "", 200)["list"].([]any)
	if len(strats) != 1 || strats[0].(map[string]any)["latest"].(map[string]any)["code"] != nil {
		t.Fatalf("strategies %v", strats)
	}
	if v := c.do("GET", "/strategies/"+testVersion.ID, "", 200); v["code"] != "pass" {
		t.Fatalf("version %v", v)
	}
	c.do("GET", "/strategies/nope", "", 404)

	c.do("POST", fmt.Sprintf("/accounts/%d/instances", id), `{"versionId":"`+testVersion.ID+`","params":{"fast":100}}`, 400)
	c.do("POST", fmt.Sprintf("/accounts/%d/instances", id), `{"versionId":"`+testVersion.ID+`","universe":{"static":["bad"]}}`, 400)
	in := c.do("POST", fmt.Sprintf("/accounts/%d/instances", id), `{"versionId":"`+testVersion.ID+`","params":{"fast":20},"exec":"auto"}`, 201)
	iid := int64(in["id"].(float64))
	if in["state"] != "created" || in["params"].(map[string]any)["fast"].(float64) != 20 {
		t.Fatalf("instance %v", in)
	}
	up := c.do("PATCH", fmt.Sprintf("/instances/%d", iid), `{"params":{"fast":30}}`, 200)
	if up["configVersion"].(float64) != 2 {
		t.Fatalf("patched %v", up)
	}
	for _, a := range []struct{ action, state string }{{"start", "running"}, {"pause", "paused"}, {"resume", "running"}, {"stop", "stopped"}} {
		x := c.do("POST", fmt.Sprintf("/instances/%d/%s", iid, a.action), "", 200)
		if x["state"] != a.state {
			t.Fatalf("%s → %v", a.action, x)
		}
	}
	c.do("POST", fmt.Sprintf("/instances/%d/pause", iid), "", 409)
	c.do("POST", fmt.Sprintf("/instances/%d/explode", iid), "", 404)
	if l := c.do("GET", fmt.Sprintf("/accounts/%d/instances", id), "", 200)["list"].([]any); len(l) != 1 {
		t.Fatalf("instances %v", l)
	}

	for _, path := range []string{"fills", "orders", "signals", "ledger", "equity"} {
		c.do("GET", fmt.Sprintf("/accounts/%d/%s?limit=10", id, path), "", 200)
	}
	c.do("GET", fmt.Sprintf("/accounts/%d/fills?limit=x", id), "", 400)
	led := c.do("GET", fmt.Sprintf("/accounts/%d/ledger", id), "", 200)["list"].([]any)
	if len(led) != 2 {
		t.Fatalf("ledger %v", led)
	}
	rep := c.do("GET", fmt.Sprintf("/accounts/%d/report", id), "", 200)
	if rep["trades"].(float64) != 0 || rep["signals"].(map[string]any)["ordered"].(float64) != 0 {
		t.Fatalf("report %v", rep)
	}
	c.do("POST", fmt.Sprintf("/accounts/%d/close", id), "", 400)
	c.do("POST", fmt.Sprintf("/backtests/%d/run", id), "", 400)

	bt := c.do("POST", "/accounts", fmt.Sprintf(`{"name":"bt","mode":"backtest","deposit":1000,"from":%d,"to":%d}`, t0, t0+hourMs), 201)
	btID := int64(bt["id"].(float64))
	if bt["backtest"].(map[string]any)["state"] != "idle" {
		t.Fatalf("backtest account %v", bt)
	}
	if e := c.do("POST", fmt.Sprintf("/backtests/%d/run", btID), "", 400); !strings.Contains(e["error"].(string), "实例") {
		t.Fatalf("run without instances %v", e)
	}
	if changes.Load() == 0 {
		t.Fatal("no change callbacks")
	}
}
