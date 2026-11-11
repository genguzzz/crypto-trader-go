package realtrade

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBarkMessage(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	p := Proposal{ID: 12, Kind: KindOpen, InstID: "ENA-USDT-SWAP", Side: "long", Mode: "live", MarginUSDT: 120, Leverage: 8,
		MarginMode: "isolated", RefPx: 0.42, SL: 0.41, TP: 0.45, Rationale: "回踩企稳", ExpiresAt: now.Add(15 * time.Minute).UnixMilli()}
	m := barkMessage("proposed", p, "https://x/go/proposal/12", now)
	if !strings.Contains(m.Title, "#12 待审核") || !strings.Contains(m.Title, "ENA") || m.Level != "timeSensitive" || m.URL == "" {
		t.Fatalf("proposed: %+v", m)
	}
	if !strings.Contains(m.Body, "回踩企稳") || !strings.Contains(m.Body, "18:15 前审核") {
		t.Fatalf("body: %s", m.Body)
	}
	p.Status, p.Error = StatusFailed, "insufficient margin"
	if m := barkMessage(StatusFailed, p, "", now); m.Level != "active" || !strings.Contains(m.Body, "下单失败") {
		t.Fatalf("failed: %+v", m)
	}
}

func TestBarkSend(t *testing.T) {
	var got barkPush
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer srv.Close()
	b := NewBark(&Service{}, []string{srv.URL + "/devkey"}, srv.Client())
	if err := b.send(t.Context(), srv.URL+"/devkey", barkPush{Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if path != "/devkey" || got.Title != "t" {
		t.Fatalf("path %s push %+v", path, got)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"code":400,"message":"failed to get device token"}`))
	}))
	defer bad.Close()
	if err := b.send(t.Context(), bad.URL+"/nokey", barkPush{}); err == nil || !strings.Contains(err.Error(), "device token") {
		t.Fatalf("want the server's error, got %v", err)
	}
}

func TestBarkFromEnv(t *testing.T) {
	t.Setenv("CTG_BARK_URL", "")
	if BarkFromEnv(&Service{}, nil) != nil {
		t.Fatal("unset must be nil")
	}
	t.Setenv("CTG_BARK_URL", " https://bark.example/k1/ , junk, http://x/k2")
	b := BarkFromEnv(&Service{}, nil)
	if b == nil || len(b.targets) != 2 || b.targets[0] != "https://bark.example/k1" {
		t.Fatalf("%+v", b)
	}
}
