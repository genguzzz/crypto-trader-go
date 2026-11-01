package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestOwnerGate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("key") {
			w.WriteHeader(500) // the query key must never reach a handler
			return
		}
		w.WriteHeader(204)
	})
	cases := []struct {
		name, key, method, path, hdr, remote, realIP string
		want                                         int
	}{
		// public exceptions
		{"health", "k", "GET", "/api/v1/health", "", "1.2.3.4:5", "", 204},
		{"status", "k", "GET", "/api/v1/status", "", "1.2.3.4:5", "", 204},
		{"ai busy", "k", "GET", "/api/v1/ai/busy", "", "1.2.3.4:5", "", 204},
		{"expo manifest", "k", "GET", "/api/v1/expo/manifest", "", "1.2.3.4:5", "", 204},
		{"expo asset", "k", "GET", "/api/v1/expo/assets/1/0b2d-x/bundles/ios.hbc", "", "1.2.3.4:5", "", 204},
		{"head health", "k", "HEAD", "/api/v1/health", "", "1.2.3.4:5", "", 204},
		{"post to public path", "k", "POST", "/api/v1/status", "", "1.2.3.4:5", "", 401},
		{"traversal out of public", "k", "GET", "/api/v1/expo/assets/../../ai/chats", "", "1.2.3.4:5", "", 401},
		{"open api untouched", "k", "GET", "/api/open/v1/universe", "", "1.2.3.4:5", "", 204},
		{"mcp untouched", "k", "POST", "/mcp", "", "1.2.3.4:5", "", 204},
		{"web page untouched", "k", "GET", "/embed/chart/BTC-USDT-SWAP", "", "1.2.3.4:5", "", 204},
		// everything else under /api/v1/ needs the key, reads included
		{"market read", "k", "GET", "/api/v1/universe", "", "1.2.3.4:5", "", 401},
		{"chats read", "k", "GET", "/api/v1/ai/chats", "", "1.2.3.4:5", "", 401},
		{"chat stream", "k", "GET", "/api/v1/ai/chats/3/stream", "", "1.2.3.4:5", "", 401},
		{"paper trading", "k", "POST", "/api/v1/trade/accounts", "", "1.2.3.4:5", "", 401},
		{"app events", "k", "POST", "/api/v1/app/events", "", "1.2.3.4:5", "", 401},
		{"app bundle", "k", "GET", "/api/v1/app/bundle", "", "1.2.3.4:5", "", 401},
		{"bare prefix", "k", "GET", "/api/v1", "", "1.2.3.4:5", "", 401},
		{"wrong key", "k", "GET", "/api/v1/universe", "x", "1.2.3.4:5", "", 401},
		{"right key", "k", "GET", "/api/v1/universe", "k", "1.2.3.4:5", "", 204},
		{"right key write", "k", "POST", "/api/v1/realtrade/proposals/1/approve", "k", "1.2.3.4:5", "", 204},
		{"key prefix is not the key", "kk", "GET", "/api/v1/universe", "k", "1.2.3.4:5", "", 401},
		// query key only where a header is impossible
		{"query key stream", "k", "GET", "/api/v1/stream?key=k", "", "1.2.3.4:5", "", 204},
		{"query key icon", "k", "GET", "/api/v1/icons/btc.png?key=k", "", "1.2.3.4:5", "", 204},
		{"query key wrong", "k", "GET", "/api/v1/stream?key=x", "", "1.2.3.4:5", "", 401},
		{"query key elsewhere", "k", "GET", "/api/v1/ai/chats?key=k", "", "1.2.3.4:5", "", 401},
		{"query key on write", "k", "POST", "/api/v1/stream?key=k", "", "1.2.3.4:5", "", 401},
		{"header with stray query key", "k", "GET", "/api/v1/universe?key=zzz", "k", "1.2.3.4:5", "", 204},
		// no key configured: direct loopback only
		{"no key loopback", "", "GET", "/api/v1/universe", "", "127.0.0.1:9", "", 204},
		{"no key proxied", "", "GET", "/api/v1/universe", "", "127.0.0.1:9", "8.8.8.8", 401},
		{"no key remote", "", "GET", "/api/v1/universe", "", "8.8.8.8:9", "", 401},
		{"no key public", "", "GET", "/api/v1/health", "", "8.8.8.8:9", "", 204},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.RemoteAddr = c.remote
		if c.hdr != "" {
			r.Header.Set("X-CTG-Admin", c.hdr)
		}
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		w := httptest.NewRecorder()
		adminGate(c.key, "", ok).ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d want %d", c.name, w.Code, c.want)
		}
		if w.Code == 401 && !strings.Contains(w.Body.String(), `"admin_required"`) {
			t.Errorf("%s: 401 without code admin_required: %s", c.name, w.Body.String())
		}
	}
}

func TestAgentKey(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := adminGate("k", "agent", ok)
	for _, c := range []struct {
		method, path, agent string
		want                int
	}{
		{"GET", "/api/v1/ai/chats", "agent", 204},
		{"GET", "/api/v1/ai/chats/12", "agent", 204},
		{"GET", "/api/v1/ai/chats/12", "wrong", 401},
		{"GET", "/api/v1/ai/chats/12/stream", "agent", 401},
		{"DELETE", "/api/v1/ai/chats/12", "agent", 401},
		{"GET", "/api/v1/account/positions", "agent", 401},
		{"GET", "/api/v1/realtrade/proposals", "agent", 401},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("X-CTG-Agent", c.agent)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s: got %d want %d", c.method, c.path, w.Code, c.want)
		}
	}
}

func TestOwnerGateThrottle(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	now := time.Unix(1_700_000_000, 0)
	g := adminGate("k", "", ok).(*ownerGate)
	g.now = func() time.Time { return now }
	do := func(key, realIP string) int {
		r := httptest.NewRequest("GET", "/api/v1/universe", nil)
		r.RemoteAddr = "127.0.0.1:1" // behind nginx
		r.Header.Set("X-Real-IP", realIP)
		if key != "" {
			r.Header.Set("X-CTG-Admin", key)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w.Code
	}
	// one stale key repeated (many parallel app requests) and missing keys never block
	for range 50 {
		if c := do("stale", "9.9.9.9"); c != 401 {
			t.Fatalf("stale key: %d", c)
		}
		if c := do("", "9.9.9.9"); c != 401 {
			t.Fatalf("no key: %d", c)
		}
	}
	if c := do("k", "9.9.9.9"); c != 204 {
		t.Fatalf("owner after stale key: %d", c)
	}
	// distinct guesses: the 11th within a minute blocks the client, the right key included
	for i := range failLimit + 1 {
		if c := do(fmt.Sprintf("guess%d", i), "6.6.6.6"); c != 401 {
			t.Fatalf("guess %d: %d", i, c)
		}
	}
	if c := do("k", "6.6.6.6"); c != 429 {
		t.Fatalf("blocked client: %d", c)
	}
	if c := do("k", "7.7.7.7"); c != 204 {
		t.Fatalf("other client: %d", c)
	}
	now = now.Add(failBlockFor + time.Second)
	if c := do("k", "6.6.6.6"); c != 204 {
		t.Fatalf("after the block: %d", c)
	}
	// guesses spread over more than a minute do not add up
	for i := range 3 * failLimit {
		now = now.Add(7 * time.Second)
		if c := do(fmt.Sprintf("slow%d", i), "5.5.5.5"); c != 401 {
			t.Fatalf("slow guess %d: %d", i, c)
		}
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "8.8.8.8:1"
	r.Header.Set("X-Real-IP", "1.1.1.1") // spoofed by a direct client: ignored
	if ip := clientIP(r); ip != "8.8.8.8" {
		t.Fatalf("direct: %s", ip)
	}
	r.RemoteAddr = "127.0.0.1:1"
	if ip := clientIP(r); ip != "1.1.1.1" {
		t.Fatalf("proxied: %s", ip)
	}
	r.Header.Del("X-Real-IP")
	r.Header.Set("X-Forwarded-For", "3.3.3.3, 2.2.2.2")
	if ip := clientIP(r); ip != "2.2.2.2" {
		t.Fatalf("forwarded: %s", ip)
	}
}

func TestWebOff(t *testing.T) {
	web := fstest.MapFS{
		"index.html":                     {Data: []byte("<html>app</html>")},
		"assets/app-1.js":                {Data: []byte("js")},
		"icon-32.png":                    {Data: []byte("png")},
		"research/alt-perps-report.html": {Data: []byte("report")},
	}
	get := func(h http.Handler, p string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		b, _ := io.ReadAll(w.Body)
		return w.Code, string(b)
	}
	off := spa(web, false)
	for _, p := range []string{"/", "/index.html", "/instrument/BTC-USDT-SWAP", "/trading", "/settings"} {
		if c, b := get(off, p); c != 503 || !strings.Contains(b, "网页版已暂停") {
			t.Errorf("off %s: %d %q", p, c, b)
		}
	}
	for p, want := range map[string]string{"/embed/chart/BTC-USDT-SWAP": "app", "/embed/trades/3": "app", "/assets/app-1.js": "js",
		"/icon-32.png": "png", "/research/alt-perps-report.html": "report"} {
		if c, b := get(off, p); c != 200 || !strings.Contains(b, want) {
			t.Errorf("off %s: %d %q", p, c, b)
		}
	}
	if c, _ := get(off, "/api/v1/nope"); c != 404 {
		t.Errorf("off unknown api: %d", c)
	}
	on := spa(web, true)
	for _, p := range []string{"/", "/instrument/BTC-USDT-SWAP", "/embed/chart/X"} {
		if c, b := get(on, p); c != 200 || b != "<html>app</html>" {
			t.Errorf("on %s: %d %q", p, c, b)
		}
	}
}

// The gate wraps the whole server: pages stay reachable, the API does not.
func TestServerGated(t *testing.T) {
	web := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "bundle.json": {Data: []byte(`{"version":"v","runtime":"1"}`)}}
	h := (&Server{Web: web, AdminKey: "k"}).Handler()
	for p, want := range map[string]int{"/": 503, "/embed/chart/BTC-USDT-SWAP": 200, "/api/v1/universe": 401, "/api/v1/app/bundle": 401, "/api/v1/stream": 401} {
		r := httptest.NewRequest("GET", p, nil)
		r.RemoteAddr = "8.8.8.8:1"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: got %d want %d", p, w.Code, want)
		}
	}
}
