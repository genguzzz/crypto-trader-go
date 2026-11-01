package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGzipJSONButNotSSE(t *testing.T) {
	h := withGzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.Repeat(`{"a":1}`, 200))
	}))
	req := httptest.NewRequest("GET", "/api/v1/universe", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("expected gzip")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if len(b) != 1400 {
		t.Fatalf("round trip length %d", len(b))
	}
	sse := httptest.NewRequest("GET", "/api/v1/stream", nil)
	sse.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, sse)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("SSE must not be gzipped")
	}
}

func TestUnknownAPIRouteIs404(t *testing.T) {
	h := spa(fstest.MapFS{"index.html": {Data: []byte("<html>")}}, true)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/rules/x", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("spa fallback broken: %d", rec.Code)
	}
}
