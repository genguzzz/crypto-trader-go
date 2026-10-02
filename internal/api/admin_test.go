package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminGate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	cases := []struct {
		name, key, method, path, hdr, remote, realIP string
		want                                         int
	}{
		{"get open", "k", "GET", "/api/v1/trade/accounts", "", "1.2.3.4:5", "", 204},
		{"other area open", "k", "POST", "/api/v1/rules", "", "1.2.3.4:5", "", 204},
		{"write needs key", "k", "POST", "/api/v1/trade/accounts", "", "1.2.3.4:5", "", 401},
		{"wrong key", "k", "PUT", "/api/v1/ai/credential", "x", "1.2.3.4:5", "", 401},
		{"right key", "k", "PUT", "/api/v1/ai/credential", "k", "1.2.3.4:5", "", 204},
		{"no key loopback", "", "POST", "/api/v1/interact/test", "", "127.0.0.1:9", "", 204},
		{"no key proxied", "", "POST", "/api/v1/interact/test", "", "127.0.0.1:9", "8.8.8.8", 401},
		{"no key remote", "", "POST", "/api/v1/interact/test", "", "8.8.8.8:9", "", 401},
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
		adminGate(c.key, ok).ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d want %d", c.name, w.Code, c.want)
		}
	}
}
