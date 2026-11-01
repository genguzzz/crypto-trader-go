package devapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/store"
)

func newTestService(t *testing.T) (*Service, *http.ServeMux, *time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	s, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_800_000_000_000)
	s.now = func() time.Time { return now }
	mux := http.NewServeMux()
	s.Mount(mux, nil, nil) // engine-free endpoints only (fields, usage)
	return s, mux, &now
}

func TestBucket(t *testing.T) {
	var b bucket
	t0 := time.Unix(0, 0)
	for i := 0; i < 5; i++ {
		if ok, _, _ := b.take(t0, 60, 5, 1); !ok {
			t.Fatalf("request %d within burst rejected", i)
		}
	}
	ok, _, retry := b.take(t0, 60, 5, 1)
	if ok || retry != time.Second {
		t.Fatalf("6th request: ok=%v retry=%v, want rejected with 1s retry", ok, retry)
	}
	if ok, _, _ := b.take(t0.Add(2*time.Second), 60, 5, 2); !ok {
		t.Fatal("2 tokens refilled after 2s at 1/s")
	}
	if got := b.peek(t0.Add(time.Hour), 60, 5); got != 5 {
		t.Fatalf("refill capped at burst: got %v", got)
	}
}

func TestCreateValidate(t *testing.T) {
	s, _, _ := newTestService(t)
	for _, in := range []CreateInput{
		{Name: ""},
		{Name: "x", Kind: "bogus"},
		{Name: "x", RatePerMin: MaxRatePerMin + 1},
		{Name: "x", Kind: KindToken},                    // token needs a TTL
		{Name: "x", Kind: KindToken, TTLHours: 31 * 24}, // too long
	} {
		if _, err := s.Create(in); err == nil {
			t.Errorf("%+v accepted", in)
		}
	}
	c, err := s.Create(CreateInput{Name: "agent", RatePerMin: 90})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Secret, "ctg_sk_") || c.Prefix != c.Secret[:12] || c.Burst != 30 || c.ExpiresAt != 0 {
		t.Fatalf("unexpected key %+v", c)
	}
	tok, err := s.Create(CreateInput{Name: "tmp", Kind: KindToken, TTLHours: 1})
	if err != nil || !strings.HasPrefix(tok.Secret, "ctg_at_") || tok.ExpiresAt == 0 {
		t.Fatalf("token %+v %v", tok, err)
	}
	// secrets are never listed, and survive a restart as hashes only
	b, _ := json.Marshal(s.Keys())
	if strings.Contains(string(b), c.Secret) || strings.Contains(string(b), hashSecret(c.Secret)) {
		t.Fatal("secret or hash leaked in key list")
	}
	s2, err := New(s.st)
	if err != nil || len(s2.Keys()) != 2 {
		t.Fatalf("reload: %v %d", err, len(s2.Keys()))
	}
}

func get(mux http.Handler, path, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestRESTAuthAndLimits(t *testing.T) {
	s, mux, now := newTestService(t)
	if w := get(mux, OpenPrefix+"fields", ""); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no key: %d", w.Code)
	}
	if w := get(mux, OpenPrefix+"fields", "ctg_sk_wrong"); w.Code != 401 {
		t.Fatalf("bad key: %d", w.Code)
	}
	c, _ := s.Create(CreateInput{Name: "a", RatePerMin: 60, Burst: 3})
	for i := 0; i < 3; i++ {
		w := get(mux, OpenPrefix+"fields", c.Secret)
		if w.Code != 200 {
			t.Fatalf("call %d: %d %s", i, w.Code, w.Body)
		}
		if got := w.Header().Get("X-RateLimit-Remaining"); got != []string{"2", "1", "0"}[i] {
			t.Fatalf("call %d remaining %s", i, got)
		}
	}
	w := get(mux, OpenPrefix+"fields", c.Secret)
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), "retryAfterMs") {
		t.Fatalf("over limit: %d %v %s", w.Code, w.Header(), w.Body)
	}
	// usage is free and reports the empty bucket
	w = get(mux, OpenPrefix+"me", c.Secret)
	var u Usage
	json.Unmarshal(w.Body.Bytes(), &u)
	if w.Code != 200 || u.Remaining != 0 || u.Limited != 1 || u.Requests != 4 || u.Key.Name != "a" {
		t.Fatalf("usage: %d %+v", w.Code, u)
	}
	*now = now.Add(time.Second)
	if w := get(mux, OpenPrefix+"fields", c.Secret); w.Code != 200 {
		t.Fatalf("after refill: %d", w.Code)
	}
	if w := get(mux, OpenPrefix+"me?x=1", c.Secret); w.Code != 200 {
		t.Fatalf("unknown params are ignored: %d", w.Code)
	}
	// revoke, expiry, global switch
	s.Update(c.ID, UpdateInput{Revoke: true})
	if w := get(mux, OpenPrefix+"fields", c.Secret); w.Code != 401 || !strings.Contains(w.Body.String(), "revoked") {
		t.Fatalf("revoked: %d %s", w.Code, w.Body)
	}
	tok, _ := s.Create(CreateInput{Name: "t", Kind: KindToken, TTLHours: 1})
	*now = now.Add(2 * time.Hour)
	if w := get(mux, OpenPrefix+"fields", tok.Secret); w.Code != 401 || !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("expired: %d %s", w.Code, w.Body)
	}
	k2, _ := s.Create(CreateInput{Name: "b"})
	s.SetConfig(Config{Enabled: false, GlobalPerMin: 100})
	if w := get(mux, OpenPrefix+"fields", k2.Secret); w.Code != 503 {
		t.Fatalf("disabled: %d", w.Code)
	}
	if len(s.Log()) == 0 || s.Log()[0].Status != 503 {
		t.Fatalf("log newest first: %+v", s.Log())
	}
}

func TestGlobalLimit(t *testing.T) {
	s, mux, _ := newTestService(t)
	s.SetConfig(Config{Enabled: true, GlobalPerMin: 60}) // global burst = 10
	a, _ := s.Create(CreateInput{Name: "a", RatePerMin: 600, Burst: 100})
	n := 0
	for i := 0; i < 20; i++ {
		if w := get(mux, OpenPrefix+"fields", a.Secret); w.Code == 200 {
			n++
		}
	}
	if n != 10 {
		t.Fatalf("global ceiling let %d through, want 10", n)
	}
}

func TestParamValidation(t *testing.T) {
	ep := &Endpoint{Params: []Param{
		{Name: "id", Type: "string", Required: true},
		{Name: "n", Type: "integer", Default: 5, Min: 1, Max: 10},
		{Name: "b", Type: "boolean", Default: false},
		{Name: "e", Type: "string", Enum: []string{"x", "y"}},
	}}
	bind := func(m map[string]string) (Args, error) {
		return ep.bind(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
	}
	if _, err := bind(map[string]string{}); err == nil {
		t.Error("missing required accepted")
	}
	a, err := bind(map[string]string{"id": "X", "b": "1"})
	if err != nil || a.Int("n") != 5 || !a.Bool("b") {
		t.Errorf("defaults: %v %v", a, err)
	}
	for _, bad := range []map[string]string{{"id": "X", "n": "11"}, {"id": "X", "n": "a"}, {"id": "X", "b": "maybe"}, {"id": "X", "e": "z"}} {
		if _, err := bind(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func mcpPost(mux http.Handler, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", MCPPath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestMCP(t *testing.T) {
	s, mux, _ := newTestService(t)
	if w := mcpPost(mux, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	k, _ := s.Create(CreateInput{Name: "agent", RatePerMin: 60, Burst: 2})
	w := mcpPost(mux, k.Secret, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	var init struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	json.Unmarshal(w.Body.Bytes(), &init)
	if w.Code != 200 || init.Result.ProtocolVersion != "2025-03-26" || init.Result.Capabilities["tools"] == nil {
		t.Fatalf("initialize: %d %s", w.Code, w.Body)
	}
	if w := mcpPost(mux, k.Secret, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); w.Code != 202 {
		t.Fatalf("notification: %d", w.Code)
	}
	w = mcpPost(mux, k.Secret, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var list struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Result.Tools) != len(s.eps) || list.Result.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("tools/list: %s", w.Body)
	}
	call := func(id int, name, args string) (bool, string) {
		w := mcpPost(mux, k.Secret, `{"jsonrpc":"2.0","id":`+string(rune('0'+id))+`,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		var r struct {
			Result struct {
				Content []struct{ Text string } `json:"content"`
				IsError bool                    `json:"isError"`
			} `json:"result"`
		}
		json.Unmarshal(w.Body.Bytes(), &r)
		if w.Code != 200 || len(r.Result.Content) != 1 {
			t.Fatalf("tools/call %s: %d %s", name, w.Code, w.Body)
		}
		return r.Result.IsError, r.Result.Content[0].Text
	}
	if isErr, txt := call(3, "list_fields", `{}`); isErr || !strings.Contains(txt, "ret_1h") {
		t.Fatalf("list_fields: %v %s", isErr, txt)
	}
	if isErr, _ := call(4, "nope", `{}`); !isErr {
		t.Fatal("unknown tool not an error")
	}
	call(5, "list_fields", `{}`)
	if isErr, txt := call(6, "list_fields", `{}`); !isErr || !strings.Contains(txt, "retryAfterMs") {
		t.Fatalf("rate limit inside a tool result: %v %s", isErr, txt)
	}
	if isErr, txt := call(7, "get_usage", `{}`); isErr || !strings.Contains(txt, `"remaining":0`) {
		t.Fatalf("usage stays free: %v %s", isErr, txt)
	}
	if w := mcpPost(mux, k.Secret, `{"jsonrpc":"2.0","id":8,"method":"resources/list"}`); !strings.Contains(w.Body.String(), "-32601") {
		t.Fatalf("unknown method: %s", w.Body)
	}
	r := httptest.NewRequest("GET", MCPPath, nil)
	gw := httptest.NewRecorder()
	mux.ServeHTTP(gw, r)
	if gw.Code != 405 {
		t.Fatalf("GET /mcp: %d", gw.Code)
	}
}

// Owner endpoints (the owner's OKX positions) are invisible to external keys and served to OwnerKey only.
func TestOwnerEndpoint(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	s, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	var gotInst string
	s.SetPositions(func(_ context.Context, inst string) (any, error) {
		gotInst = inst
		return map[string]any{"enabled": true, "positions": []any{map[string]any{"instId": "BTC-USDT-SWAP", "avgPx": 1.23456789}}}, nil
	})
	mux := http.NewServeMux()
	s.Mount(mux, nil, nil)
	ext, _ := s.Create(CreateInput{Name: "ext", RatePerMin: 60, Burst: 10})
	tools := func(key string) string {
		return mcpPost(mux, key, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`).Body.String()
	}
	call := func(key string) string {
		return mcpPost(mux, key, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_positions","arguments":{"instId":"btc-usdt-swap"}}}`).Body.String()
	}
	for _, key := range []string{ext.Secret, s.InternalKey("sandbox", 60, 10)} {
		if strings.Contains(tools(key), "get_positions") {
			t.Fatal("owner tool listed to a non-owner key")
		}
		if out := call(key); !strings.Contains(out, "unknown tool") || gotInst != "" {
			t.Fatalf("owner tool served to a non-owner key: %s", out)
		}
	}
	if w := get(mux, OpenPrefix+"positions", ext.Secret); w.Code == 200 {
		t.Fatalf("owner endpoint on the open REST API: %d", w.Code)
	}
	if strings.Contains(get(mux, OpenPrefix+"openapi.json", "").Body.String(), "get_positions") {
		t.Fatal("owner endpoint in the OpenAPI document")
	}
	owner := s.OwnerKey("ai-agent", 60, 10)
	if !strings.Contains(tools(owner), "get_positions") {
		t.Fatal("owner tool not listed to the owner key")
	}
	if out := call(owner); !strings.Contains(out, `\"avgPx\":1.23457`) || gotInst != "BTC-USDT-SWAP" {
		t.Fatalf("owner call: %q %s", gotInst, out)
	}
}

func TestOpenAPI(t *testing.T) {
	_, mux, _ := newTestService(t)
	w := get(mux, OpenPrefix+"openapi.json", "")
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || w.Code != 200 || doc.OpenAPI != "3.1.0" {
		t.Fatalf("openapi: %d %v", w.Code, err)
	}
	for _, p := range []string{"/universe", "/instruments/{instId}", "/candles", "/rules/{ruleId}/track", "/me"} {
		if doc.Paths[p]["get"] == nil {
			t.Errorf("missing %s", p)
		}
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS header missing")
	}
}

// Truncating a normalized series must renormalize to the first returned bar.
func TestRebaseAfterTail(t *testing.T) {
	pts := []market.ComparePoint{
		{T: 1, V: map[string]float64{"A": 100, "B": 100}},
		{T: 2, V: map[string]float64{"A": 110, "B": 95}},
		{T: 3, V: map[string]float64{"A": 121, "B": 90.25}},
	}
	got := rebase(tail(pts, 2))
	if len(got) != 2 || got[0].T != 2 || got[0].V["A"] != 100 || got[0].V["B"] != 100 || math.Abs(got[1].V["A"]-110) > 1e-9 || math.Abs(got[1].V["B"]-95) > 1e-9 {
		t.Fatalf("rebase: %+v", got)
	}
	if pts[1].V["A"] != 110 {
		t.Fatal("input mutated")
	}
}
