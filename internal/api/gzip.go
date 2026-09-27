package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

var gzPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed); return w }}

type gzWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (w *gzWriter) Write(b []byte) (int, error) { return w.gz.Write(b) }
func (w *gzWriter) WriteHeader(code int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

// withGzip compresses JSON/HTML/JS/CSS responses (not SSE, not images). Snapshots shrink ~8–10×,
// which matters over slow/proxied links where every KB costs.
func withGzip(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.URL.Path == "/api/v1/stream" || strings.HasPrefix(r.URL.Path, "/api/v1/icons/") {
			h.ServeHTTP(w, r)
			return
		}
		gz := gzPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() { gz.Close(); gzPool.Put(gz) }()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		h.ServeHTTP(&gzWriter{ResponseWriter: w, gz: gz}, r)
	})
}
