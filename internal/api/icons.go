package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ccyRe = regexp.MustCompile(`^[a-z0-9]{1,20}$`)

// Icons proxies OKX's public currency icons with a disk cache (so the NAS / offline browsers work).
// Misses are remembered for a day and answered with 404; the UI then shows a letter avatar.
type Icons struct {
	Dir    string
	Client *http.Client
	mu     sync.Mutex
	miss   map[string]time.Time
}

func (ic *Icons) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ccy := strings.ToLower(strings.TrimSuffix(r.PathValue("ccy"), ".png"))
	if !ccyRe.MatchString(ccy) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(ic.Dir, ccy+".png")
	if b, err := os.ReadFile(path); err == nil {
		writePNG(w, b)
		return
	}
	ic.mu.Lock()
	if ic.miss == nil {
		ic.miss = map[string]time.Time{}
	}
	if t, ok := ic.miss[ccy]; ok && time.Since(t) < 24*time.Hour {
		ic.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	ic.mu.Unlock()
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://static.okx.com/cdn/oksupport/asset/currency/icon/"+ccy+".png", nil)
	resp, err := ic.Client.Do(req)
	if err != nil {
		http.Error(w, "icon fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		ic.mu.Lock()
		ic.miss[ccy] = time.Now()
		ic.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	os.MkdirAll(ic.Dir, 0o755)
	os.WriteFile(path, b, 0o644)
	writePNG(w, b)
}

func writePNG(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(b)
}
