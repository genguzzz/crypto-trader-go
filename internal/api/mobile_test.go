package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	updOld = "11111111-1111-4111-8111-111111111111"
	updNew = "22222222-2222-4222-8222-222222222222"
)

// writeUpdate lays out one exported update the way mobile/scripts/ota.sh uploads it.
func writeUpdate(t *testing.T, root, rv, id, createdAt string) {
	t.Helper()
	dir := filepath.Join(root, "updates", rv, id)
	files := map[string]string{
		"_expo/static/js/ios/entry-abc.hbc": "bundle-" + id,
		"assets/0123abcd":                   "png-bytes",
		"metadata.json":                     `{"version":0,"bundler":"metro","fileMetadata":{"ios":{"bundle":"_expo/static/js/ios/entry-abc.hbc","assets":[{"path":"assets/0123abcd","ext":"png"}]}}}`,
		"expoConfig.json":                   `{"name":"OKX Helper","slug":"okx-helper"}`,
		"update.json":                       `{"createdAt":"` + createdAt + `","message":"msg ` + id[:4] + `","commit":"abc123"}`,
	}
	for p, body := range files {
		f := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func manifestReq(h http.Handler, rv, current, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/v1/expo/manifest", nil)
	r.Host = "ctg.example"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("expo-platform", "ios")
	r.Header.Set("expo-protocol-version", "1")
	if rv != "" {
		r.Header.Set("expo-runtime-version", rv)
	}
	if current != "" {
		r.Header.Set("expo-current-update-id", current)
	}
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func mobileMux(dir string) http.Handler {
	mux := http.NewServeMux()
	(&Mobile{Dir: dir}).Register(mux)
	return mux
}

func TestExpoManifestMultipart(t *testing.T) {
	dir := t.TempDir()
	writeUpdate(t, dir, "1", updOld, "2026-10-30T10:00:00.000Z")
	writeUpdate(t, dir, "1", updNew, "2026-10-31T10:00:00.000Z")
	os.MkdirAll(filepath.Join(dir, "updates", "1", ".incoming-x"), 0o755) // an upload in progress is ignored
	h := mobileMux(dir)

	w := manifestReq(h, "1", "", "multipart/mixed,application/expo+json")
	if w.Code != 200 || w.Header().Get("expo-protocol-version") != "1" || w.Header().Get("expo-sfv-version") != "0" {
		t.Fatalf("status %d headers %v", w.Code, w.Header())
	}
	mt, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type %q: %v", w.Header().Get("Content-Type"), err)
	}
	part, err := multipart.NewReader(w.Body, params["boundary"]).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.FormName() != "manifest" {
		t.Fatalf("part name %q", part.FormName())
	}
	var man expoManifest
	raw, _ := io.ReadAll(part)
	if err := json.Unmarshal(raw, &man); err != nil {
		t.Fatal(err)
	}
	if man.ID != updNew || man.RuntimeVersion != "1" || man.CreatedAt != "2026-10-31T10:00:00.000Z" {
		t.Fatalf("manifest: %+v", man)
	}
	sum := sha256.Sum256([]byte("bundle-" + updNew))
	if man.LaunchAsset.Hash != base64.RawURLEncoding.EncodeToString(sum[:]) || man.LaunchAsset.ContentType != "application/javascript" {
		t.Fatalf("launch asset: %+v", man.LaunchAsset)
	}
	wantURL := "https://ctg.example/api/v1/expo/assets/1/" + updNew + "/_expo/static/js/ios/entry-abc.hbc"
	if man.LaunchAsset.URL != wantURL {
		t.Fatalf("launch url %q", man.LaunchAsset.URL)
	}
	if len(man.Assets) != 1 || man.Assets[0].FileExtension != ".png" || man.Assets[0].ContentType != "image/png" || len(man.Assets[0].Key) != 32 {
		t.Fatalf("assets: %+v", man.Assets)
	}
	if cfg, _ := man.Extra["expoClient"].(map[string]any); cfg["name"] != "OKX Helper" {
		t.Fatalf("extra: %+v", man.Extra)
	}

	// the asset URL serves the same bytes
	r := httptest.NewRequest("GET", strings.TrimPrefix(man.LaunchAsset.URL, "https://ctg.example"), nil)
	aw := httptest.NewRecorder()
	h.ServeHTTP(aw, r)
	if aw.Code != 200 || aw.Body.String() != "bundle-"+updNew || aw.Header().Get("Content-Type") != "application/javascript" {
		t.Fatalf("asset: %d %q %v", aw.Code, aw.Body.String(), aw.Header())
	}
}

func TestExpoManifestJSONAndNoUpdate(t *testing.T) {
	dir := t.TempDir()
	writeUpdate(t, dir, "1", updOld, "2026-10-30T10:00:00.000Z")
	h := mobileMux(dir)

	w := manifestReq(h, "1", "", "application/expo+json")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/expo+json" || !strings.Contains(w.Body.String(), updOld) {
		t.Fatalf("json manifest: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if w := manifestReq(h, "1", updOld, ""); w.Code != http.StatusNoContent { // already running the latest
		t.Fatalf("current: %d", w.Code)
	}
	if w := manifestReq(h, "2", "", ""); w.Code != http.StatusNoContent { // nothing for this runtime
		t.Fatalf("other runtime: %d", w.Code)
	}
	if w := manifestReq(h, "", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("missing runtime: %d", w.Code)
	}
	if w := manifestReq(h, "../x", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad runtime: %d", w.Code)
	}
}

func TestExpoAssetPathsStayInside(t *testing.T) {
	dir := t.TempDir()
	writeUpdate(t, dir, "1", updOld, "2026-10-30T10:00:00.000Z")
	os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o644)
	h := mobileMux(dir)
	for _, p := range []string{
		"/api/v1/expo/assets/1/" + updOld + "/../../../secret",
		"/api/v1/expo/assets/1/" + updOld + "/%2e%2e/%2e%2e/%2e%2e/secret",
		"/api/v1/expo/assets/1/not-a-uuid/metadata.json",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code == 200 {
			t.Fatalf("%s served", p)
		}
	}
}

func TestAppEvents(t *testing.T) {
	dir := t.TempDir()
	h := mobileMux(dir)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/app/events", strings.NewReader(`{"events":[{"e":"screen","p":{"path":"/"}},"junk",{"e":"tap"}]}`)))
	if w.Code != 200 {
		t.Fatalf("events: %d %s", w.Code, w.Body.String())
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "events"))
	if len(ents) != 1 {
		t.Fatalf("event files: %v", ents)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events", ents[0].Name()))
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("lines %d: %s", n, b)
	}
}
