package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipSkipsEventStreams(t *testing.T) {
	h := withGzip(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("%s: writer can't flush", r.URL.Path)
		}
		w.Write([]byte("data: {}\n\n"))
	}))
	for _, p := range []string{"/api/v1/stream", "/api/v1/ai/chat", "/api/v1/ai/chats/7/stream"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", p, nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		h.ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s compressed", p)
		}
	}
	if isStream("/api/v1/ai/chats/7") {
		t.Error("chat detail is not a stream")
	}
}
