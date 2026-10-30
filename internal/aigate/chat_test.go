package aigate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
