package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestAppCORS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := withAppCORS(ok)

	r := httptest.NewRequest("OPTIONS", "/api/v1/rules", nil)
	r.Header.Set("Origin", "capacitor://localhost")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "capacitor://localhost" || w.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}

	r = httptest.NewRequest("GET", "/api/v1/meta", nil)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin got CORS headers")
	}
}

func TestBundleManifest(t *testing.T) {
	web := fstest.MapFS{
		"bundle.json":       {Data: []byte(`{"version":"abc","runtime":"1"}`)},
		"index.html":        {Data: []byte("<html>")},
		"assets/app-1.js":   {Data: []byte("js")},
		".gitkeep":          {Data: nil},
		"research/a/b.html": {Data: []byte("b")},
	}
	b, err := bundleManifest(web)()
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != "abc" || b.Runtime != "1" || len(b.Files) != 4 {
		t.Fatalf("manifest: %+v", b)
	}
	sum := sha256.Sum256([]byte("js"))
	if b.Files[0].Path != "assets/app-1.js" || b.Files[0].Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("first file: %+v", b.Files[0])
	}
}
