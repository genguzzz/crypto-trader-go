package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// The iOS app (Capacitor, web/ios) bundles the web build and loads it from capacitor://localhost,
// so it calls this API cross-origin. Over-the-air bundles now come from scripts/ios-ota.sh (static
// files on apps.gugenzzz.top); GET /api/v1/app/bundle, which describes the web build embedded in
// this binary (files fetched from /), only bootstraps IPAs 1.0.1–1.0.3, whose built-in web code
// still asks here (docs/ios.md).

// appOrigins are the web view origins of the native app.
var appOrigins = map[string]bool{"capacitor://localhost": true, "ionic://localhost": true}

// withAppCORS lets the native app's web view call the API; browsers stay same-origin only.
func withAppCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !appOrigins[origin] {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CTG-Admin")
			h.Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type bundleFile struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
	Size   int    `json:"size"`
}

type appBundle struct {
	Version string       `json:"version"`
	Runtime string       `json:"runtime"`
	Files   []bundleFile `json:"files"`
}

// bundleManifest hashes the embedded web build once; version and runtime come from the
// bundle.json the web build writes (web/vite.config.ts).
func bundleManifest(web fs.FS) func() (*appBundle, error) {
	return sync.OnceValues(func() (*appBundle, error) {
		var b appBundle
		raw, err := fs.ReadFile(web, "bundle.json")
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		err = fs.WalkDir(web, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
				return err
			}
			data, err := fs.ReadFile(web, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			b.Files = append(b.Files, bundleFile{Path: p, Sha256: hex.EncodeToString(sum[:]), Size: len(data)})
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Path < b.Files[j].Path })
		return &b, nil
	})
}

func (s *Server) appBundle(manifest func() (*appBundle, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := manifest()
		if err != nil {
			httpErr(w, http.StatusServiceUnavailable, "web bundle unavailable: "+err.Error())
			return
		}
		writeJSON(w, b)
	}
}
