package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// adminPaths are writes that store credentials, spend money (model calls) or run submitted code
// (user strategies: save, check, trial, publish). Paper trading itself (accounts, instances,
// confirmations, proposals) needs no key. Until owner login (#1) exists, these writes need the
// owner key; real-money trading (/api/v1/realtrade/) needs it for reads too (ownerReadPaths).
var adminPaths = []string{"/api/v1/interact/config", "/api/v1/interact/test", "/api/v1/ai/credential", "/api/v1/ai/model",
	"/api/v1/ai/test", "/api/v1/ai/memory/", "/api/v1/ai/chat", "/api/v1/ai/chats/", "/api/v1/ai/transcribe", "/api/v1/ai/notes", "/api/v1/ai/notes/", "/api/v1/ai/plans/", "/api/v1/trade/sources", "/api/v1/trade/sources/", "/api/v1/trade/check",
	"/api/v1/trade/trial", "/api/v1/app/logs"}

// ownerReadPaths need the owner key for every method, reads included: the owner's own OKX account and
// the real-trade proposals (approving one places a real order).
var ownerReadPaths = []string{"/api/v1/account/", "/api/v1/realtrade/"}

// adminGate requires header X-CTG-Admin == key for non-GET requests to adminPaths and for any request
// to ownerReadPaths. With no key configured, only direct loopback requests (no reverse proxy headers) pass.
func adminGate(key string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read := r.Method == http.MethodGet || r.Method == http.MethodHead
		if !matches(ownerReadPaths, r.URL.Path) && (read || !needsAdmin(r.URL.Path)) {
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

func needsAdmin(path string) bool { return matches(adminPaths, path) }

func matches(paths []string, path string) bool {
	for _, p := range paths {
		if path == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(path, p)) {
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
