package aigate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestChatStreamPersistResume(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.g.SetAgent(Agent{MCPURL: "http://127.0.0.1:1/mcp", MCPToken: "ctg_in_x"})
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	if m := e.g.configMsgLocked(); m["agent"].(Agent).MCPURL != "http://127.0.0.1:1/mcp" {
		t.Fatalf("config %v", m)
	}
	mux := http.NewServeMux()
	e.g.Register(mux)
	turn := func(body string, session string, reply string) (string, int64) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			m := w.recv()
			if m["type"] != "chat" || m["sessionId"] != session {
				t.Errorf("chat msg %v (want session %q)", m, session)
			}
			w.send(map[string]any{"type": "event", "id": m["id"], "event": map[string]any{"type": "tool", "id": "t1", "label": "K 线"}})
			w.send(map[string]any{"type": "event", "id": m["id"], "event": map[string]any{"type": "text", "delta": reply}})
			w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": reply, "sessionId": "s1", "cost": 0.02,
				"steps": []any{map[string]any{"t": "tool", "id": "t1", "label": "K 线", "ok": true}, map[string]any{"t": "text", "v": reply}}}})
		}()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/ai/chat", strings.NewReader(body)))
		<-done
		var id int64
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			var ev map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil && ev["type"] == "chat" {
				id = int64(ev["chatId"].(float64))
			}
		}
		return rec.Body.String(), id
	}
	out, id := turn(`{"message":"SOL 怎么样","instId":"SOL-USDT-SWAP"}`, "", "偏弱")
	for _, want := range []string{`"type":"chat"`, `"label":"K 线"`, `"delta":"偏弱"`, `"type":"done"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream lacks %s:\n%s", want, out)
		}
	}
	// second turn resumes the stored session
	turn(`{"chatId":`+itoa(id)+`,"message":"那 ETH 呢"}`, "s1", "也弱")
	c, err := e.g.ChatByID(id)
	if err != nil || len(c.Messages) != 4 || c.Messages[3].Content != "也弱" || c.InstID != "SOL-USDT-SWAP" || c.CostUSD < 0.039 {
		t.Fatalf("chat %+v %v", c, err)
	}
	// a finished chat streams "idle" on re-attach
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ai/chats/"+itoa(id)+"/stream", nil))
	if !strings.Contains(rec.Body.String(), `"type":"idle"`) {
		t.Fatalf("reattach %s", rec.Body)
	}
	if list, _ := e.g.Chats(10); len(list) != 1 || list[0].Title != "SOL 怎么样" {
		t.Fatalf("list %+v", list)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestNotesRememberReplaceDelete(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.onAgentEvent(chatEvent(`{"type":"memory","text":"杠杆偏好：多空都用 8 倍"}`))
	n, err := e.g.AddNote("不做 meme 币", "owner", 0)
	if err != nil || n.ID != 2 {
		t.Fatalf("add %+v %v", n, err)
	}
	e.g.onAgentEvent(chatEvent(`{"type":"memory","text":"杠杆偏好：多空都用 10 倍","replaces":1}`))
	ns := e.g.Notes()
	if len(ns) != 2 || ns[0].Text != "杠杆偏好：多空都用 10 倍" || ns[0].By != "ai" || ns[1].By != "owner" {
		t.Fatalf("notes %+v", ns)
	}
	e.g.DeleteNote(2)
	if ns := e.g.Notes(); len(ns) != 1 {
		t.Fatalf("after delete %+v", ns)
	}
	if _, err := e.g.AddNote("  ", "owner", 0); err == nil {
		t.Fatal("empty note accepted")
	}
}

func TestChatInterruptedRetryNotify(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	notified := make(chan string, 2)
	e.g.SetNotify(func(_ context.Context, text string) error { notified <- text; return nil })
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)

	// a turn the worker drops (as on a server restart): error, nobody watching → notified
	id, err := e.g.StartChat(ChatInput{Message: "SOL 能空吗"})
	if err != nil {
		t.Fatal(err)
	}
	m := w.recv()
	w.send(map[string]any{"type": "error", "id": m["id"], "error": "AI worker 已断开"})
	if got := <-notified; !strings.Contains(got, "回答失败") {
		t.Fatalf("notify %q", got)
	}
	waitFor(t, "run ended", func() bool { return e.g.run(id) == nil })

	// an unanswered question (restart before any answer was stored) reads as interrupted
	e.g.st.DB.Exec(`DELETE FROM ai_chat_msgs WHERE chat_id=? AND role='assistant'`, id)
	if c, _ := e.g.ChatByID(id); !c.Interrupted {
		t.Fatalf("not interrupted: %+v", c)
	}

	// retry re-asks the same question without adding it again, and replaces the failed answer
	if _, err := e.g.StartChat(ChatInput{ChatID: id, Retry: true}); err != nil {
		t.Fatal(err)
	}
	m = w.recv()
	if m["message"] != "SOL 能空吗" {
		t.Fatalf("retry message %v", m["message"])
	}
	w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": "能空", "sessionId": "s2"}})
	<-notified
	waitFor(t, "retry ended", func() bool { return e.g.run(id) == nil })
	c, _ := e.g.ChatByID(id)
	if len(c.Messages) != 2 || c.Messages[0].Role != "user" || c.Messages[1].Content != "能空" || c.Interrupted {
		t.Fatalf("after retry %+v", c.Messages)
	}
}

// A signal wakes an existing chat: the message is stored as a signal (not the owner's), the agent resumes
// the chat's session, a busy chat refuses until its turn ends, and the end of a turn is reported.
func TestWakeChat(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	done := make(chan int64, 4)
	e.g.SetOnChatDone(func(id int64) { done <- id })
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	id, err := e.g.StartChat(ChatInput{Message: "AAVE 能多吗"})
	if err != nil {
		t.Fatal(err)
	}
	m := w.recv()
	if err := e.g.WakeChat(id, "⚡ 信号 #1 触发"); !errors.Is(err, ErrChatBusy) {
		t.Fatalf("wake while busy: %v", err)
	}
	w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": "等回踩", "sessionId": "s9"}})
	if got := <-done; got != id {
		t.Fatalf("done hook %d", got)
	}
	if err := e.g.WakeChat(id, "⚡ 信号 #1 触发：AAVE 进入 139.5–140.2"); err != nil {
		t.Fatal(err)
	}
	m = w.recv()
	if m["sessionId"] != "s9" || !strings.HasPrefix(m["message"].(string), "⚡ 信号 #1") {
		t.Fatalf("wake message %v", m)
	}
	w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": "已提议", "sessionId": "s9"}})
	<-done
	c, _ := e.g.ChatByID(id)
	if len(c.Messages) != 4 || c.Messages[2].Role != RoleSignal || c.Messages[3].Content != "已提议" {
		t.Fatalf("messages %+v", c.Messages)
	}
	if t2, ok := e.g.ChatTitle(id); !ok || t2 != "AAVE 能多吗" {
		t.Fatalf("title %q %v", t2, ok)
	}
	if err := e.g.WakeChat(9999, "x"); err == nil {
		t.Fatal("woke a chat that does not exist")
	}
}

// A steer reaches a chat at once: an idle chat starts a turn with it (stored with its role); a chat
// mid-answer gets it injected into the running turn (the worker is told which call, the message is
// stored and streamed then); a turn that cannot take it any more answers ErrChatBusy so the caller
// keeps it queued.
func TestSteerChat(t *testing.T) {
	e := newEnv(t, Options{})
	e.g.SetCredential(KindAPIKey, testKey)
	e.run(t)
	w := e.dial(t, true)
	waitFor(t, "available", e.g.Available)
	if err := e.g.SteerChat(1, RoleUser, "x"); err == nil {
		t.Fatal("a steer as the owner")
	}
	id, err := e.g.StartChat(ChatInput{Message: "BTC 开多"})
	if err != nil {
		t.Fatal(err)
	}
	turn := w.recv()
	_, events := e.g.run(id).subscribe()
	steered := make(chan error, 1)
	go func() { steered <- e.g.SteerChat(id, RoleTrade, "✅ #3 已成交 · 开仓做多 BTC") }()
	m := w.recv()
	if m["type"] != "steer" || m["callId"] != turn["id"] || m["message"] != "✅ #3 已成交 · 开仓做多 BTC" {
		t.Fatalf("steer msg %v (turn %v)", m, turn["id"])
	}
	w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"ok": true}})
	if err := <-steered; err != nil {
		t.Fatalf("steer into a running turn: %v", err)
	}
	if ev := <-events; !strings.Contains(string(ev), `"type":"steer"`) || !strings.Contains(string(ev), `"role":"trade"`) {
		t.Fatalf("stream event %s", ev)
	}
	// the turn is finishing: the worker refuses, the caller retries later
	go func() { steered <- e.g.SteerChat(id, RoleSignal, "⚡ 信号 #2 触发") }()
	m = w.recv()
	w.send(map[string]any{"type": "error", "id": m["id"], "error": "turn is ending"})
	if err := <-steered; !errors.Is(err, ErrChatBusy) {
		t.Fatalf("refused steer: %v", err)
	}
	w.send(map[string]any{"type": "result", "id": turn["id"], "result": map[string]any{"text": "已核对", "sessionId": "s5"}})
	waitFor(t, "turn ended", func() bool { return e.g.run(id) == nil })
	// idle: a new turn in the same session, the message stored with its role
	if err := e.g.SteerChat(id, RoleSignal, "⚡ 信号 #2 触发：BTC 价格上破 103000"); err != nil {
		t.Fatal(err)
	}
	m = w.recv()
	if m["type"] != "chat" || m["sessionId"] != "s5" || !strings.HasPrefix(m["message"].(string), "⚡ 信号 #2") {
		t.Fatalf("new turn %v", m)
	}
	w.send(map[string]any{"type": "result", "id": m["id"], "result": map[string]any{"text": "已减仓提议", "sessionId": "s5"}})
	waitFor(t, "second turn ended", func() bool { return e.g.run(id) == nil })
	c, _ := e.g.ChatByID(id)
	roles := []string{}
	for _, m := range c.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,trade,assistant,signal,assistant" || c.Interrupted {
		t.Fatalf("messages %v", roles)
	}
}

// The app's model menu renders the options as given: 默认 ("") first, then the levels a turn may send.
func TestChatOptions(t *testing.T) {
	g := &Gate{model: "claude-sonnet-5"}
	o := g.ChatOptions()
	efforts := o["efforts"].([]ChatOption)
	if efforts[0].Value != "" || efforts[1].Value != "off" {
		t.Fatalf("efforts start with 默认, then 关: %+v", efforts[:2])
	}
	if slices.Contains(ChatEfforts, "") || !slices.Contains(ChatEfforts, "off") || !slices.Contains(ChatModels, "claude-opus-5-5") {
		t.Fatalf("allowed: %v %v", ChatEfforts, ChatModels)
	}
	if o["defaults"].(map[string]any)["model"] != "claude-sonnet-5" {
		t.Fatalf("defaults: %v", o["defaults"])
	}
}
