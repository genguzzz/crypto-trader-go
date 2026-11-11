package aigate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The desk's chat: created once without a turn, listed first, not deletable, loadable by its tail,
// woken by the server with a model of its own, and its agent session can be restarted.
func TestStandingChat(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.g.SetAgent(Agent{MCPURL: "http://127.0.0.1:1/mcp", MCPToken: "ctg_in_x"})
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	if _, ok := e.g.StandingChat(KindDesk); ok {
		t.Fatal("a desk chat exists before it was created")
	}
	id, err := e.g.EnsureChat(KindDesk, "交易台 · 全自动")
	if err != nil || id == 0 {
		t.Fatal(id, err)
	}
	if again, _ := e.g.EnsureChat(KindDesk, "x"); again != id {
		t.Fatalf("EnsureChat made a second chat %d", again)
	}
	if got, ok := e.g.StandingChat(KindDesk); !ok || got != id {
		t.Fatalf("StandingChat %d %v", got, ok)
	}
	turn := func(start func() error, wantModel, wantSession, reply string) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			m := w.recv()
			if m["type"] != "chat" || m["model"] != wantModel || m["sessionId"] != wantSession {
				t.Errorf("chat msg model %v session %v (want %q, %q)", m["model"], m["sessionId"], wantModel, wantSession)
			}
			w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": reply, "sessionId": "s1", "cost": 0.03,
				"usage": map[string]any{"input": 100, "output": 20, "cacheRead": 3000, "cacheWrite": 50}}})
		}()
		if err := start(); err != nil {
			t.Fatal(err)
		}
		<-done
		waitFor(t, "turn end", func() bool { return !e.g.ChatRunning(id) })
	}
	before := e.g.LastMsgID(id)
	turn(func() error {
		return e.g.SteerChatWith(id, RoleSignal, "🤖 交易台 #1 · 定时巡检", "claude-opus-5-5", "high")
	}, "claude-opus-5-5", "", "没有机会")
	a, ok := e.g.LastAnswer(id, before)
	if !ok || a.Text != "没有机会" || a.CostUSD != 0.03 || a.Tokens != 3170 {
		t.Fatalf("answer %+v %v", a, ok)
	}
	if _, ok := e.g.LastAnswer(id, a.MsgID); ok {
		t.Fatal("LastAnswer returned a message at or before afterMsg")
	}
	// an ordinary chat made later is listed after the desk
	_, err = e.g.StartChat(ChatInput{Message: "SOL 怎么样"})
	if err != nil {
		t.Fatal(err)
	}
	m := w.recv()
	w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": "偏弱", "sessionId": "s9"}})
	waitFor(t, "idle", func() bool { return !e.g.Busy() })
	list, _ := e.g.Chats(10)
	if len(list) != 2 || list[0].ID != id || list[0].Kind != KindDesk || list[1].Kind != "" {
		t.Fatalf("list %+v", list)
	}
	// the tail: only the newest messages, with the count of older ones
	c, err := e.g.ChatTail(id, 1)
	if err != nil || len(c.Messages) != 1 || c.Messages[0].Role != RoleAssistant || c.Truncated != 1 || c.Kind != KindDesk {
		t.Fatalf("tail %+v %v", c, err)
	}
	if c, _ := e.g.ChatByID(id); len(c.Messages) != 2 || c.Truncated != 0 {
		t.Fatalf("full %+v", c)
	}
	mux := http.NewServeMux()
	e.g.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/chats/"+itoa(id)+"?last=1", nil))
	if !strings.Contains(rec.Body.String(), `"truncated":1`) || !strings.Contains(rec.Body.String(), `"kind":"desk"`) {
		t.Fatalf("GET ?last=1: %s", rec.Body)
	}
	// it cannot be deleted
	if err := e.g.DeleteChat(id); !errors.Is(err, ErrStandingChat) {
		t.Fatalf("delete: %v", err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/ai/chats/"+itoa(id), nil))
	if rec.Code != 409 {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	// the next turn resumes the session; after ResetSession it starts a fresh one
	turn(func() error { return e.g.SteerChat(id, RoleSignal, "🤖 交易台 #2") }, "", "s1", "还是没有")
	if err := e.g.ResetSession(id); err != nil {
		t.Fatal(err)
	}
	turn(func() error { return e.g.SteerChat(id, RoleSignal, "🤖 交易台 #3") }, "", "", "新的上下文")
	if c, _ := e.g.ChatByID(id); len(c.Messages) != 6 {
		t.Fatalf("messages were lost by the session reset: %d", len(c.Messages))
	}
}

// "Answer ready" notes can be limited to some chats (the desk's runs are not pushed one by one).
func TestNotifyFilter(t *testing.T) {
	e := newEnv(t, Options{})
	keep := map[int64]bool{2: true}
	var mu sync.Mutex
	var got []string
	e.g.SetNotify(func(_ context.Context, text string) error {
		mu.Lock()
		got = append(got, text)
		mu.Unlock()
		return nil
	})
	e.g.SetNotifyFilter(func(chat int64) bool { return keep[chat] })
	e.g.notifyDone(1, "a", "")
	e.g.notifyDone(2, "b", "")
	waitFor(t, "note", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasSuffix(got[0], "b") {
		t.Fatalf("notes %v", got)
	}
}
