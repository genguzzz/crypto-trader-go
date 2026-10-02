package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// adminPrefixes are API areas whose writes move simulated money, answer trade confirmations or
// store credentials. Until owner login (#1) exists, writes there need the owner key.
var adminPrefixes = []string{"/api/v1/trade/", "/api/v1/interact/", "/api/v1/ai/"}

// adminGate requires header X-CTG-Admin == key for non-GET requests under adminPrefixes.
// With no key configured, only direct loopback requests (no reverse proxy headers) may write.
func adminGate(key string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || !needsAdmin(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if key != "" {
			got := r.Header.Get("X-CTG-Admin")
			if subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		} else if directLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"owner key required","code":"admin_required"}`))
	})
}

func needsAdmin(path string) bool {
	for _, p := range adminPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func directLoopback(r *http.Request) bool {
	if r.Header.Get("X-Real-IP") != "" || r.Header.Get("X-Forwarded-For") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
