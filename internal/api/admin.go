package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Owner access (2026-11-01): every request under /api/v1/ needs the owner key (header X-CTG-Admin =
// CTG_ADMIN_KEY), reads included, because the API now carries the owner's OKX positions, his AI chats
// and real-trade proposals. With no key configured only direct loopback requests (no reverse proxy
// headers) pass. Outside /api/v1/ nothing is gated here: /api/open/v1 and /mcp check their own bearer
// keys (internal/devapi), and the web pages are handled by spa().

// publicExact / publicPrefix are the GET/HEAD exceptions: deploy and monitoring probes, the
// before-deploy "is a chat running" check, and expo-updates, which cannot send the owner key.
var publicExact = map[string]bool{
	"/api/v1/health":        true, // deploy.yml public check, scripts/dev.sh
	"/api/v1/status":        true, // deploy/deploy.sh wait_healthy (curl on the host)
	"/api/v1/ai/busy":       true, // {"running": bool} only
	"/api/v1/expo/manifest": true, // native app OTA (expo-updates)
}
var publicPrefix = []string{"/api/v1/expo/assets/"}

// queryKeyExact / queryKeyPrefix may carry the key as ?key= instead of the header, for clients that
// cannot set headers: EventSource (the live stream) and <img> (coin icons). The parameter is removed
// from the request before it reaches the handler.
var queryKeyExact = map[string]bool{"/api/v1/stream": true}
var queryKeyPrefix = []string{"/api/v1/icons/"}

// agentPath: the AI worker's own reads (ai/agent.py list_chats / read_chat) with the per-process
// agent key (header X-CTG-Agent), so the owner key never has to reach the worker.
var agentPath = regexp.MustCompile(`^/api/v1/ai/chats(/[0-9]+)?$`)

const (
	failWindow   = time.Minute
	failLimit    = 10 // distinct wrong keys per client per window before it is blocked
	failBlockFor = 5 * time.Minute
)

// ownerGate holds the key check and the per-client throttle for wrong keys.
type ownerGate struct {
	key      [32]byte
	set      bool
	agentKey [32]byte
	agentSet bool
	next     http.Handler
	now      func() time.Time

	mu      sync.Mutex
	clients map[string]*failState
	pruned  time.Time
}

// failState counts distinct wrong keys (by hash) in the current window, so one stale key repeated
// by many parallel requests counts once while guessing does not.
type failState struct {
	start   time.Time
	keys    map[[8]byte]bool
	blocked time.Time // until
}

func adminGate(key, agentKey string, next http.Handler) http.Handler {
	g := &ownerGate{next: next, now: time.Now, clients: map[string]*failState{}}
	if key != "" {
		g.key, g.set = sha256.Sum256([]byte(key)), true
	}
	if agentKey != "" {
		g.agentKey, g.agentSet = sha256.Sum256([]byte(agentKey)), true
	}
	return g
}

func (g *ownerGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/v1/") && p != "/api/v1" {
		g.next.ServeHTTP(w, r)
		return
	}
	p = path.Clean(p) // the mux redirects unclean paths; decide on the path it would route
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if read && (publicExact[p] || hasAnyPrefix(p, publicPrefix)) {
		g.next.ServeHTTP(w, r)
		return
	}
	got := r.Header.Get("X-CTG-Admin")
	if q := r.URL.Query(); q.Has("key") {
		if read && got == "" && (queryKeyExact[p] || hasAnyPrefix(p, queryKeyPrefix)) {
			got = q.Get("key")
		}
		q.Del("key") // never pass it on (handlers, logs)
		r.URL.RawQuery = q.Encode()
	}
	if a := r.Header.Get("X-CTG-Agent"); a != "" && g.agentSet && read && agentPath.MatchString(p) {
		sum := sha256.Sum256([]byte(a))
		if subtle.ConstantTimeCompare(sum[:], g.agentKey[:]) == 1 {
			g.next.ServeHTTP(w, r)
			return
		}
	}
	ip := clientIP(r)
	if until := g.blockedUntil(ip); !until.IsZero() {
		w.Header().Set("Retry-After", strconv.Itoa(int(until.Sub(g.now()).Seconds())+1))
		writeAuthErr(w, http.StatusTooManyRequests, `{"error":"too many wrong keys, try again later","code":"rate_limited"}`)
		return
	}
	if !g.set {
		if directLoopback(r) {
			g.next.ServeHTTP(w, r)
			return
		}
	} else if got != "" {
		sum := sha256.Sum256([]byte(got))
		if subtle.ConstantTimeCompare(sum[:], g.key[:]) == 1 {
			g.next.ServeHTTP(w, r)
			return
		}
		g.fail(ip, sum, r)
	}
	writeAuthErr(w, http.StatusUnauthorized, `{"error":"owner key required","code":"admin_required"}`)
}

func writeAuthErr(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	w.Write([]byte(body))
}

func (g *ownerGate) blockedUntil(ip string) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.clients[ip]; s != nil && g.now().Before(s.blocked) {
		return s.blocked
	}
	return time.Time{}
}

// fail records a wrong key from ip and blocks the client once it tried more than failLimit distinct
// keys within failWindow. Logs carry the client, method and path, never the key.
func (g *ownerGate) fail(ip string, sum [32]byte, r *http.Request) {
	now := g.now()
	var h [8]byte
	copy(h[:], sum[:8])
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Sub(g.pruned) > failWindow {
		for k, s := range g.clients {
			if now.Sub(s.start) > failWindow && now.After(s.blocked) {
				delete(g.clients, k)
			}
		}
		g.pruned = now
	}
	s := g.clients[ip]
	if s == nil || now.Sub(s.start) > failWindow {
		s = &failState{start: now, keys: map[[8]byte]bool{}, blocked: timeOr(s)}
		g.clients[ip] = s
	}
	if s.keys[h] {
		return // the same wrong key again (e.g. a stale key in parallel requests): logged once
	}
	s.keys[h] = true
	log.Printf("auth: wrong owner key from %s %s %s (%d distinct in window)", ip, r.Method, r.URL.Path, len(s.keys))
	if len(s.keys) > failLimit {
		s.blocked = now.Add(failBlockFor)
		log.Printf("auth: %s blocked for %s after %d wrong keys within %s", ip, failBlockFor, len(s.keys), failWindow)
	}
}

func timeOr(s *failState) time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.blocked
}

func hasAnyPrefix(p string, prefixes []string) bool {
	for _, x := range prefixes {
		if strings.HasPrefix(p, x) {
			return true
		}
	}
	return false
}

// clientIP is the caller's address: the reverse proxy's X-Real-IP (or the last X-Forwarded-For hop)
// when the request comes from loopback, else the socket peer.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			parts := strings.Split(v, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
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
