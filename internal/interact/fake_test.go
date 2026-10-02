package interact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto-trader-go/internal/store"
)

// testToken is a syntactically valid, fake bot token (never a real one).
const testToken = "123456:TESTtokenTESTtokenTESTtoken0000"

// fakeTG is a minimal Telegram Bot API server.
type fakeTG struct {
	srv *httptest.Server

	mu        sync.Mutex
	updates   []map[string]any
	nextUpd   int64
	msgID     int64
	sent      []fakeMsg
	edits     []fakeMsg
	callbacks []map[string]any
	fail      []int // status codes to return for the next sendMessage calls
	attempts  []time.Time
	wake      chan struct{}
}

type fakeMsg struct {
	Chat   int64
	ID     int64
	Text   string
	Markup *keyboard
	Silent bool
	At     time.Time
}

func newFakeTG(t *testing.T) *fakeTG {
	f := &fakeTG{nextUpd: 100, msgID: 1000, wake: make(chan struct{}, 1)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func tgOK(w http.ResponseWriter, result any) {
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func tgFail(w http.ResponseWriter, code int, desc string, retry int) {
	w.WriteHeader(code)
	m := map[string]any{"ok": false, "error_code": code, "description": desc}
	if retry > 0 {
		m["parameters"] = map[string]int{"retry_after": retry}
	}
	json.NewEncoder(w).Encode(m)
}

func (f *fakeTG) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/bot")
	tok, method, _ := strings.Cut(path, "/")
	if tok != testToken {
		tgFail(w, 401, "Unauthorized", 0)
		return
	}
	var p map[string]json.RawMessage
	json.NewDecoder(r.Body).Decode(&p)
	switch method {
	case "getMe":
		tgOK(w, map[string]any{"id": 123456, "is_bot": true, "username": "ctg_test_bot"})
	case "setMyCommands":
		tgOK(w, true)
	case "getUpdates":
		var offset, timeout int64
		json.Unmarshal(p["offset"], &offset)
		json.Unmarshal(p["timeout"], &timeout)
		deadline := time.After(time.Duration(min(timeout, 1)) * time.Second)
		for {
			f.mu.Lock()
			var out []map[string]any
			for _, u := range f.updates {
				if u["update_id"].(int64) >= offset {
					out = append(out, u)
				}
			}
			f.mu.Unlock()
			if len(out) > 0 {
				tgOK(w, out)
				return
			}
			select {
			case <-f.wake:
			case <-deadline:
				tgOK(w, []any{})
				return
			case <-r.Context().Done():
				return
			}
		}
	case "sendMessage":
		f.mu.Lock()
		f.attempts = append(f.attempts, time.Now())
		if len(f.fail) > 0 {
			code := f.fail[0]
			f.fail = f.fail[1:]
			f.mu.Unlock()
			switch code {
			case 429:
				tgFail(w, 429, "Too Many Requests: retry after 1", 1)
			case 400:
				tgFail(w, 400, "Bad Request: can't parse entities", 0)
			default:
				tgFail(w, code, "Internal Server Error", 0)
			}
			return
		}
		f.msgID++
		m := fakeMsg{ID: f.msgID, At: time.Now()}
		json.Unmarshal(p["chat_id"], &m.Chat)
		json.Unmarshal(p["text"], &m.Text)
		json.Unmarshal(p["disable_notification"], &m.Silent)
		if raw, ok := p["reply_markup"]; ok {
			m.Markup = &keyboard{}
			json.Unmarshal(raw, m.Markup)
		}
		f.sent = append(f.sent, m)
		f.mu.Unlock()
		tgOK(w, map[string]any{"message_id": m.ID, "chat": map[string]any{"id": m.Chat, "type": "private"}, "text": m.Text})
	case "editMessageText":
		f.mu.Lock()
		var m fakeMsg
		json.Unmarshal(p["chat_id"], &m.Chat)
		json.Unmarshal(p["message_id"], &m.ID)
		json.Unmarshal(p["text"], &m.Text)
		if raw, ok := p["reply_markup"]; ok {
			m.Markup = &keyboard{}
			json.Unmarshal(raw, m.Markup)
		}
		f.edits = append(f.edits, m)
		f.mu.Unlock()
		tgOK(w, true)
	case "answerCallbackQuery":
		f.mu.Lock()
		cb := map[string]any{}
		for k, v := range p {
			var x any
			json.Unmarshal(v, &x)
			cb[k] = x
		}
		f.callbacks = append(f.callbacks, cb)
		f.mu.Unlock()
		tgOK(w, true)
	default:
		tgFail(w, 404, "Not Found: method "+method, 0)
	}
}

func (f *fakeTG) push(u map[string]any) {
	f.mu.Lock()
	f.nextUpd++
	u["update_id"] = f.nextUpd
	f.updates = append(f.updates, u)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *fakeTG) message(from int64, chatID int64, chatType, text string) {
	f.push(map[string]any{"message": map[string]any{"message_id": 1, "from": map[string]any{"id": from, "first_name": "u", "username": fmt.Sprintf("user%d", from)},
		"chat": map[string]any{"id": chatID, "type": chatType}, "text": text}})
}

func (f *fakeTG) callback(from int64, chatType, data string) {
	f.push(map[string]any{"callback_query": map[string]any{"id": fmt.Sprintf("cb%d", f.nextUpd+1), "from": map[string]any{"id": from, "first_name": "u"},
		"message": map[string]any{"message_id": 5, "chat": map[string]any{"id": from, "type": chatType}}, "data": data}})
}

func (f *fakeTG) sentMsgs() []fakeMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeMsg{}, f.sent...)
}

func (f *fakeTG) editMsgs() []fakeMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeMsg{}, f.edits...)
}

func (f *fakeTG) cbAnswers() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.callbacks...)
}

func (f *fakeTG) failNext(codes ...int) {
	f.mu.Lock()
	f.fail = append(f.fail, codes...)
	f.mu.Unlock()
}

// ---------- hub helpers ----------

type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logBuf) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func (l *logBuf) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	return st
}

func testOpts(f *fakeTG, logs *logBuf) Options {
	o := Options{PollTimeout: time.Second, Backoff: 50 * time.Millisecond, MaxBackoff: time.Second,
		SweepEvery: 50 * time.Millisecond, Logf: logs.logf}
	if f != nil {
		o.TelegramAPI = f.srv.URL
	}
	return o
}

func newHub(t *testing.T, st *store.Store, f *fakeTG, logs *logBuf) *Hub {
	t.Helper()
	h, err := New(st, f.srv.Client(), testOpts(f, logs))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func tgView(enabled bool, token string, allow ...int64) ConfigView {
	var v ConfigView
	v.Telegram.Enabled = enabled
	v.Telegram.BotToken = token
	v.Telegram.AllowUserIDs = allow
	return v
}

// run starts the hub and stops it (waiting for every goroutine) at cleanup.
func run(t *testing.T, h *Hub) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	eventually(t, "hub running", h.running)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("hub did not stop")
		}
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
