package api

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mobile serves the native iOS app (Expo, mobile/ on branch ios):
//
//   - Expo Updates protocol v1 (over-the-air JS bundles): GET /api/v1/expo/manifest and the update's
//     files under /api/v1/expo/assets/{runtime}/{id}/…. mobile/scripts/ota.sh exports a bundle and
//     copies it over ssh into <Dir>/updates/<runtimeVersion>/<updateId>/ (expo export output plus
//     update.json); the newest directory for the requested runtime is served. Update directories are
//     immutable, so their manifests are cached. These two are public (expo-updates sends no owner key).
//   - usage events (POST /api/v1/app/events, appended to <Dir>/events/YYYY-MM-DD.jsonl) and
//     log uploads (POST /api/v1/app/logs, <Dir>/logs/<id>.log); owner key, like all of /api/v1/.
type Mobile struct {
	Dir string // e.g. data/mobile next to the database

	mu        sync.Mutex
	manifests map[string]*expoManifest // by update directory
	eventsMu  sync.Mutex
}

func (m *Mobile) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/expo/manifest", m.manifest)
	mux.HandleFunc("GET /api/v1/expo/assets/{rv}/{id}/{file...}", m.asset)
	mux.HandleFunc("POST /api/v1/app/events", m.events)
	mux.HandleFunc("POST /api/v1/app/logs", m.uploadLog)
}

var (
	safeName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	uuidRe   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type expoAsset struct {
	Hash          string `json:"hash"`
	Key           string `json:"key"`
	ContentType   string `json:"contentType"`
	FileExtension string `json:"fileExtension,omitempty"`
	URL           string `json:"url"`
}

type expoManifest struct {
	ID             string            `json:"id"`
	CreatedAt      string            `json:"createdAt"`
	RuntimeVersion string            `json:"runtimeVersion"`
	LaunchAsset    expoAsset         `json:"launchAsset"`
	Assets         []expoAsset       `json:"assets"`
	Metadata       map[string]string `json:"metadata"`
	Extra          map[string]any    `json:"extra"`
}

// updateInfo is update.json, written by ota.sh next to the export.
type updateInfo struct {
	CreatedAt string `json:"createdAt"`
	Message   string `json:"message"`
	Commit    string `json:"commit"`
}

// exportMetadata is metadata.json from `expo export`.
type exportMetadata struct {
	FileMetadata map[string]struct {
		Bundle string `json:"bundle"`
		Assets []struct {
			Path string `json:"path"`
			Ext  string `json:"ext"`
		} `json:"assets"`
	} `json:"fileMetadata"`
}

func (m *Mobile) manifest(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("expo-protocol-version", "1")
	h.Set("expo-sfv-version", "0")
	h.Set("Cache-Control", "private, max-age=0")
	platform := r.Header.Get("expo-platform")
	rv := r.Header.Get("expo-runtime-version")
	current := r.Header.Get("expo-current-update-id")
	if platform != "ios" && platform != "android" {
		httpErr(w, http.StatusBadRequest, "missing or bad expo-platform header")
		return
	}
	if p := r.Header.Get("expo-protocol-version"); p != "" && p != "1" {
		httpErr(w, http.StatusNotAcceptable, "protocol v1 only")
		return
	}
	if !safeName.MatchString(rv) {
		httpErr(w, http.StatusBadRequest, "missing or bad expo-runtime-version header")
		return
	}
	man, err := m.latest(rv, platform, baseURL(r))
	served := ""
	if man != nil {
		served = man.ID
	}
	log.Printf("expo manifest platform=%s runtime=%s current=%s served=%s", platform, rv, current, served)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if man == nil || strings.EqualFold(man.ID, current) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := json.Marshal(man)
	accept := r.Header.Get("Accept")
	if !strings.Contains(accept, "multipart/mixed") && (strings.Contains(accept, "application/expo+json") || strings.Contains(accept, "application/json")) {
		h.Set("Content-Type", "application/expo+json")
		w.Write(body)
		return
	}
	boundary := "ctg-" + randomHex(12)
	h.Set("Content-Type", "multipart/mixed; boundary="+boundary)
	fmt.Fprintf(w, "--%s\r\ncontent-disposition: form-data; name=\"manifest\"\r\ncontent-type: application/json; charset=utf-8\r\n\r\n%s\r\n--%s--\r\n", boundary, body, boundary)
}

// latest returns the newest update for a runtime and platform, or nil when there is none.
func (m *Mobile) latest(rv, platform, base string) (*expoManifest, error) {
	dir := filepath.Join(m.Dir, "updates", rv)
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type cand struct {
		id   string
		info updateInfo
	}
	var cs []cand
	for _, e := range ents {
		if !e.IsDir() || !uuidRe.MatchString(e.Name()) { // ".incoming-…" while ota.sh uploads
			continue
		}
		var info updateInfo
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), "update.json"))
		if err != nil || json.Unmarshal(raw, &info) != nil || info.CreatedAt == "" {
			continue // incomplete
		}
		cs = append(cs, cand{e.Name(), info})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].info.CreatedAt > cs[j].info.CreatedAt })
	for _, c := range cs {
		man, err := m.build(rv, c.id, c.info, platform, base)
		if err != nil {
			log.Printf("expo update %s/%s unusable: %v", rv, c.id, err)
			continue
		}
		if man != nil {
			return man, nil
		}
	}
	return nil, nil
}

func (m *Mobile) build(rv, id string, info updateInfo, platform, base string) (*expoManifest, error) {
	key := strings.Join([]string{rv, id, platform, base}, "|")
	m.mu.Lock()
	if man, ok := m.manifests[key]; ok {
		m.mu.Unlock()
		return man, nil
	}
	m.mu.Unlock()

	dir := filepath.Join(m.Dir, "updates", rv, id)
	var meta exportMetadata
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	fm, ok := meta.FileMetadata[platform]
	if !ok {
		return nil, nil // this update has no bundle for the platform
	}
	urlBase := fmt.Sprintf("%s/api/v1/expo/assets/%s/%s/", base, rv, id)
	describe := func(rel string, launch bool, ext string) (expoAsset, error) {
		rel = filepath.ToSlash(rel)
		if !localPath(rel) {
			return expoAsset{}, fmt.Errorf("bad asset path %q", rel)
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return expoAsset{}, err
		}
		sha := sha256.Sum256(b)
		sum := md5.Sum(b)
		a := expoAsset{Hash: base64.RawURLEncoding.EncodeToString(sha[:]), Key: hex.EncodeToString(sum[:]), URL: urlBase + rel}
		if launch {
			a.ContentType = "application/javascript"
		} else {
			a.ContentType = assetType(ext)
			a.FileExtension = "." + ext
		}
		return a, nil
	}
	launch, err := describe(fm.Bundle, true, "")
	if err != nil {
		return nil, err
	}
	man := &expoManifest{ID: id, CreatedAt: info.CreatedAt, RuntimeVersion: rv, LaunchAsset: launch, Assets: []expoAsset{}, Metadata: map[string]string{},
		Extra: map[string]any{"message": info.Message, "commit": info.Commit}}
	for _, a := range fm.Assets {
		d, err := describe(a.Path, false, a.Ext)
		if err != nil {
			return nil, err
		}
		man.Assets = append(man.Assets, d)
	}
	var cfg any
	if raw, err := os.ReadFile(filepath.Join(dir, "expoConfig.json")); err == nil && json.Unmarshal(raw, &cfg) == nil {
		man.Extra["expoClient"] = cfg
	}
	m.mu.Lock()
	if m.manifests == nil {
		m.manifests = map[string]*expoManifest{}
	}
	m.manifests[key] = man
	m.mu.Unlock()
	return man, nil
}

func (m *Mobile) asset(w http.ResponseWriter, r *http.Request) {
	rv, id, file := r.PathValue("rv"), r.PathValue("id"), r.PathValue("file")
	if !safeName.MatchString(rv) || !uuidRe.MatchString(id) || !localPath(file) {
		httpErr(w, http.StatusNotFound, "not found")
		return
	}
	p := filepath.Join(m.Dir, "updates", rv, id, filepath.FromSlash(file))
	f, err := os.Open(p)
	if err != nil {
		httpErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		httpErr(w, http.StatusNotFound, "not found")
		return
	}
	ct := "application/octet-stream"
	if strings.HasSuffix(file, ".hbc") || strings.HasSuffix(file, ".js") {
		ct = "application/javascript"
	} else if t := mime.TypeByExtension(path.Ext(file)); t != "" {
		ct = t
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// localPath accepts a relative slash path that stays inside its directory.
func localPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, "\\") && filepath.IsLocal(filepath.FromSlash(p)) && !strings.Contains(p, "..")
}

func assetType(ext string) string {
	if t := mime.TypeByExtension("." + ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// baseURL is the public origin the request came in on (nginx sets Host and X-Forwarded-Proto).
func baseURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme != "http" && scheme != "https" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	return scheme + "://" + r.Host
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- usage events and log uploads ----------

const (
	maxEventBody   = 256 << 10
	maxEventsDaily = 20 << 20
	maxLogBody     = 900 << 10 // nginx allows 1 MB
)

// events appends a batch of usage events ({"events":[…]}) as JSON lines to today's file.
func (m *Mobile) events(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEventBody)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	dir := filepath.Join(m.Dir, "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	file := filepath.Join(dir, time.Now().UTC().Format("2006-01-02")+".jsonl")
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	if st, err := os.Stat(file); err == nil && st.Size() > maxEventsDaily {
		writeJSON(w, map[string]any{"ok": true, "dropped": len(in.Events)})
		return
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	var sb strings.Builder
	for _, e := range in.Events {
		var obj map[string]any
		if json.Unmarshal(e, &obj) != nil { // one compact object per line, nothing else
			continue
		}
		b, _ := json.Marshal(obj)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	f.WriteString(sb.String())
	writeJSON(w, map[string]any{"ok": true})
}

// uploadLog stores a log upload ({"log": "...", "meta": {...}}) and returns its id. Owner key only.
func (m *Mobile) uploadLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Log  string         `json:"log"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLogBody)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	dir := filepath.Join(m.Dir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := time.Now().UTC().Format("20060102-150405") + "-" + randomHex(3)
	meta, _ := json.Marshal(in.Meta)
	if err := os.WriteFile(filepath.Join(dir, id+".log"), []byte(in.Log), 0o644); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	os.WriteFile(filepath.Join(dir, id+".json"), meta, 0o644)
	log.Printf("app log uploaded id=%s bytes=%d", id, len(in.Log))
	writeJSON(w, map[string]string{"id": id})
}
