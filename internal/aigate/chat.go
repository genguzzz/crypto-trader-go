package aigate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Interactive analysis chat: multi-turn Claude Agent SDK sessions run by the worker with the ctg MCP
// data tools, web search and a sandboxed shell (ai/README.md "Chat agent"). A run is detached from
// the HTTP request: closing the page does not stop it, the final answer is stored, and a client can
// re-attach to a running chat and replay its events.

const chatSchema = `
CREATE TABLE IF NOT EXISTS ai_chats(id INTEGER PRIMARY KEY AUTOINCREMENT, created INTEGER NOT NULL, updated INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '', inst_id TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
  cost_usd REAL NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ai_chat_msgs(id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, ts INTEGER NOT NULL,
  role TEXT NOT NULL, content TEXT NOT NULL, steps TEXT NOT NULL DEFAULT '[]', cost_usd REAL NOT NULL DEFAULT 0,
  latency_ms INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ai_chat_msgs_chat ON ai_chat_msgs(chat_id, id);
`

// ChatTimeout bounds one agent run (several tool calls, a sub-analysis or two).
const ChatTimeout = 10 * time.Minute

// Agent is what the worker needs to give the chat agent its tools: the ctg MCP endpoint and an
// in-memory key for it (devapi.InternalKey).
type Agent struct {
	MCPURL   string `json:"mcpUrl"`
	MCPToken string `json:"mcpToken"`
	// SandboxToken is the same kind of key, handed to the sandboxed shell for the public ctg API.
	SandboxToken string `json:"sandboxToken"`
	// SessionsToken lets the worker read the owner's chats (GET /api/v1/ai/chats[/id], header
	// X-CTG-Agent) for its list_chats / read_chat tools; random per process, memory only.
	SessionsToken string `json:"sessionsToken"`
}

// SetNotify sets where "answer ready" messages go when nobody was watching a chat finish.
func (g *Gate) SetNotify(f func(ctx context.Context, text string) error) {
	g.mu.Lock()
	g.notify = f
	g.mu.Unlock()
}

// SetAgent configures the chat agent's tools and pushes them to a connected worker.
func (g *Gate) SetAgent(a Agent) {
	g.mu.Lock()
	g.agent = a
	g.mu.Unlock()
	g.pushConfig()
}

// Chat is one conversation.
type Chat struct {
	ID      int64   `json:"id"`
	Created int64   `json:"created"`
	Updated int64   `json:"updated"`
	Title   string  `json:"title"`
	InstID  string  `json:"instId,omitempty"`
	CostUSD float64 `json:"costUsd"`
	Running bool    `json:"running"`
	// Interrupted: the last turn has no answer and nothing is running (the server restarted mid-answer).
	Interrupted bool        `json:"interrupted,omitempty"`
	Messages    []ChatMsg   `json:"messages,omitempty"`
	Events      []chatEvent `json:"events,omitempty"` // a running chat: events so far
}

// ChatMsg is one stored turn. Steps are the tool calls of an assistant turn.
type ChatMsg struct {
	ID        int64           `json:"id"`
	Ts        int64           `json:"ts"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Steps     json.RawMessage `json:"steps"`
	CostUSD   float64         `json:"costUsd,omitempty"`
	LatencyMs int64           `json:"latencyMs,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type chatEvent = json.RawMessage

// chatRun is a running agent turn: its events so far and the clients streaming it.
type chatRun struct {
	mu     sync.Mutex
	events []chatEvent
	subs   map[chan chatEvent]struct{}
	done   bool
	cancel context.CancelFunc
	callID string // the worker call running the turn: a steer names it
}

func (r *chatRun) publish(ev chatEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	for ch := range r.subs {
		select {
		case ch <- ev:
		default: // a stalled client misses deltas; the stored answer is complete
		}
	}
}

// finish ends the run; it reports whether a client was streaming it at that moment.
func (r *chatRun) finish() (watched bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	watched = len(r.subs) > 0
	r.done = true
	for ch := range r.subs {
		close(ch)
	}
	r.subs = nil
	return watched
}

// subscribe returns the events so far and a channel for the rest (closed at the end).
func (r *chatRun) subscribe() ([]chatEvent, chan chatEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan chatEvent, 1024)
	past := append([]chatEvent(nil), r.events...)
	if r.done {
		close(ch)
	} else {
		r.subs[ch] = struct{}{}
	}
	return past, ch
}

func (r *chatRun) unsubscribe(ch chan chatEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[ch]; ok {
		delete(r.subs, ch)
		close(ch)
	}
}

func evJSON(v any) chatEvent { b, _ := json.Marshal(v); return b }

// ChatInput starts a turn. ChatID 0 opens a new chat.
type ChatInput struct {
	ChatID  int64  `json:"chatId"`
	Message string `json:"message"`
	InstID  string `json:"instId"` // the instrument page the owner is on (context only)
	Model   string `json:"model"`  // one of ChatModels; empty = the configured model
	// Effort (low / medium / high) and WebSearch for this turn, picked in the app; empty / nil = the configured ones.
	Effort    string `json:"effort"`
	WebSearch *bool  `json:"webSearch"`
	// Retry re-runs the chat's last question (after an error or an interrupted turn) instead of
	// adding a new one; Message is ignored.
	Retry bool `json:"retry"`
	// Title names a new chat instead of its first words (server-started runs: "信号 · …").
	Title string `json:"-"`
	// Role stores the message as RoleSignal instead of a question the owner typed (server-started runs).
	Role string `json:"-"`
}

// Message roles. A signal message is injected by the server when a subscribed signal fires
// (internal/signals); a trade message when an approved trade proposal of the chat ran or its position
// closed (internal/realtrade). The agent answers them like a question; the app shows them as system
// messages, not as the owner.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSignal    = "signal"
	RoleTrade     = "trade"
)

// ChatOption is one choice in the app's model menu.
type ChatOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// The app's model menu comes from here (GET /api/v1/ai/options), so every app shows the same choices.
// Effort "" (默认) sends nothing: the agent's configured effort applies; "off" turns thinking off.
var (
	chatModelOptions = []ChatOption{
		{"claude-sonnet-5", "Sonnet", "快，日常分析够用"},
		{"claude-opus-5-5", "Opus", "最强，慢一些、更费 token"},
	}
	chatEffortOptions = []ChatOption{
		{"", "默认", "不指定，用服务器的设置"},
		{"off", "关", "不思考，最快最省，适合查数据"},
		{"low", "低", "想得少，省 token，适合简单问答"},
		{"medium", "中", "平衡，大多数行情分析"},
		{"high", "高", "想得最久，复杂的多空研判和复盘"},
	}
	ChatModels  = values(chatModelOptions)
	ChatEfforts = values(chatEffortOptions[1:])
)

func values(o []ChatOption) []string {
	out := make([]string, len(o))
	for i, x := range o {
		out[i] = x.Value
	}
	return out
}

// ChatOptions is what the app's model menu shows.
func (g *Gate) ChatOptions() map[string]any {
	return map[string]any{
		"models":    chatModelOptions,
		"efforts":   chatEffortOptions,
		"webSearch": map[string]any{"default": true, "hint": "查新闻、公告时搜网页；关掉更省"},
		"defaults":  map[string]any{"model": g.currentModel(), "effort": ""},
	}
}

// ErrChatBusy: the chat is answering; a new turn has to wait until it ends.
var ErrChatBusy = errors.New("这个对话还在回答中")

// StartChat stores the user's message and starts the agent run; the caller streams it with Attach.
func (g *Gate) StartChat(in ChatInput) (int64, error) {
	in.Message = strings.TrimSpace(in.Message)
	if in.Model != "" && !slices.Contains(ChatModels, in.Model) {
		return 0, errors.New("unknown model")
	}
	if in.Effort != "" && !slices.Contains(ChatEfforts, in.Effort) {
		return 0, errors.New("unknown effort")
	}
	if in.Retry {
		if in.ChatID == 0 || g.run(in.ChatID) != nil {
			return 0, errors.New("nothing to retry")
		}
		q, err := g.lastQuestion(in.ChatID)
		if err != nil {
			return 0, err
		}
		in.Message = q
	}
	if in.Message == "" {
		return 0, errors.New("empty message")
	}
	if utf8.RuneCountInString(in.Message) > 8000 {
		return 0, errors.New("message too long")
	}
	role := RoleUser
	if in.Role == RoleSignal || in.Role == RoleTrade {
		role = in.Role
	}
	g.mu.Lock()
	ready := g.cred.configured() && g.w != nil && g.w.info.Ready
	g.mu.Unlock()
	if !ready {
		return 0, ErrUnavailable
	}
	now := g.now().UnixMilli()
	var session string
	if in.ChatID == 0 {
		title := []rune(in.Message)
		if len(title) > 40 {
			title = append(title[:40], '…')
		}
		if in.Title != "" {
			title = []rune(in.Title)
		}
		res, err := g.st.DB.Exec(`INSERT INTO ai_chats(created, updated, title, inst_id) VALUES(?,?,?,?)`, now, now, string(title), in.InstID)
		if err != nil {
			return 0, err
		}
		in.ChatID, _ = res.LastInsertId()
	} else {
		err := g.st.DB.QueryRow(`SELECT session_id FROM ai_chats WHERE id=?`, in.ChatID).Scan(&session)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("chat not found")
		} else if err != nil {
			return 0, err
		}
	}
	run := &chatRun{subs: map[chan chatEvent]struct{}{}, callID: newID()}
	g.runMu.Lock()
	if g.runs == nil {
		g.runs = map[int64]*chatRun{}
	}
	if _, busy := g.runs[in.ChatID]; busy {
		g.runMu.Unlock()
		return 0, ErrChatBusy
	}
	g.runs[in.ChatID] = run
	g.runMu.Unlock()
	if in.Retry {
		// drop the failed answer (if any) so the question is answered once
		g.st.DB.Exec(`DELETE FROM ai_chat_msgs WHERE chat_id=? AND role='assistant' AND id > (SELECT MAX(id) FROM ai_chat_msgs WHERE chat_id=? AND role IN ('user','signal','trade'))`, in.ChatID, in.ChatID)
	} else if _, err := g.st.DB.Exec(`INSERT INTO ai_chat_msgs(chat_id, ts, role, content) VALUES(?,?,?,?)`, in.ChatID, now, role, in.Message); err != nil {
		g.endRun(in.ChatID)
		return 0, err
	}
	g.st.DB.Exec(`UPDATE ai_chats SET updated=? WHERE id=?`, now, in.ChatID)

	ctx, cancel := context.WithTimeout(context.Background(), ChatTimeout)
	run.cancel = cancel
	go func() {
		defer cancel()
		defer g.chatDone(in.ChatID) // after endRun: the chat takes a new turn again
		defer g.endRun(in.ChatID)
		start := g.now()
		msg := map[string]any{"type": "chat", "id": run.callID, "chatId": in.ChatID, "sessionId": session, "message": in.Message, "instId": in.InstID, "model": in.Model, "turnEffort": in.Effort, "webSearch": in.WebSearch, "notes": g.MemoryFor(in.Message)}
		resp, err := g.callStream(ctx, msg, func(ev chatEvent) { g.onAgentEvent(ev); run.publish(ev) })
		lat := g.now().Sub(start).Milliseconds()
		var out struct {
			Text      string          `json:"text"`
			SessionID string          `json:"sessionId"`
			Cost      float64         `json:"cost"`
			Steps     json.RawMessage `json:"steps"`
		}
		errText := ""
		if err != nil {
			errText = g.errText(err)
			if errors.Is(err, context.Canceled) {
				errText = "已停止"
			}
		} else {
			json.Unmarshal(resp.Result, &out)
		}
		if len(out.Steps) == 0 {
			out.Steps = json.RawMessage("[]")
		}
		ts := g.now().UnixMilli()
		g.st.DB.Exec(`INSERT INTO ai_chat_msgs(chat_id, ts, role, content, steps, cost_usd, latency_ms, error) VALUES(?,?,?,?,?,?,?,?)`,
			in.ChatID, ts, "assistant", out.Text, string(out.Steps), out.Cost, lat, errText)
		if out.SessionID != "" {
			g.st.DB.Exec(`UPDATE ai_chats SET updated=?, session_id=?, cost_usd=cost_usd+? WHERE id=?`, ts, out.SessionID, out.Cost, in.ChatID)
		} else {
			g.st.DB.Exec(`UPDATE ai_chats SET updated=? WHERE id=?`, ts, in.ChatID)
		}
		model := in.Model
		if model == "" {
			model = g.currentModel()
		}
		row := auditRow{Kind: "chat", InstID: in.InstID, Model: model, CostUSD: out.Cost, LatencyMs: lat, Error: errText}
		g.writeAudit(&row)
		if errText != "" {
			run.publish(evJSON(map[string]any{"type": "error", "error": errText}))
		} else {
			g.savePlans(in.ChatID, out.Text, confidenceOf(out.Text))
			run.publish(evJSON(map[string]any{"type": "done", "text": out.Text, "cost": out.Cost, "latencyMs": lat}))
		}
		if !run.finish() && errText != "已停止" {
			g.notifyDone(in.ChatID, out.Text, errText)
		}
	}()
	return in.ChatID, nil
}

func (g *Gate) lastQuestion(chat int64) (string, error) {
	var q string
	err := g.st.DB.QueryRow(`SELECT content FROM ai_chat_msgs WHERE chat_id=? AND role IN ('user','signal','trade') ORDER BY id DESC LIMIT 1`, chat).Scan(&q)
	if err != nil {
		return "", errors.New("chat has no question")
	}
	return q, nil
}

// notifyDone tells the owner (Telegram, when configured) that an answer nobody was watching is ready.
func (g *Gate) notifyDone(chat int64, text, errText string) {
	g.mu.Lock()
	f := g.notify
	g.mu.Unlock()
	if f == nil {
		return
	}
	var title string
	g.st.DB.QueryRow(`SELECT title FROM ai_chats WHERE id=?`, chat).Scan(&title)
	msg := "OKX Helper AI · " + title + "\n"
	if errText != "" {
		msg += "回答失败：" + errText
	} else {
		r := []rune(strings.TrimSpace(text))
		if len(r) > 600 {
			r = append(r[:600], '…')
		}
		msg += string(r)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := f(ctx, msg); err != nil {
			g.logf("aigate: notify: %v", err)
		}
	}()
}

// WakeChat continues an existing chat with a server-injected signal message (RoleSignal): the agent
// resumes the chat's session with its full history. ErrChatBusy while a turn runs, ErrUnavailable
// while the agent is down; the caller retries later (internal/signals queues it).
func (g *Gate) WakeChat(chat int64, message string) error {
	if chat <= 0 {
		return errors.New("chat not found")
	}
	if g.run(chat) != nil {
		return ErrChatBusy
	}
	_, err := g.StartChat(ChatInput{ChatID: chat, Message: message, Role: RoleSignal})
	return err
}

// steerTimeout bounds the worker's acknowledgement of a steer.
const steerTimeout = 10 * time.Second

// SteerChat delivers a server message (RoleSignal or RoleTrade) to a chat now: an idle chat starts a
// turn with it; a chat that is answering gets it injected into the running turn — the worker hands it
// to the agent's session as a further user message, answered before the turn ends — and it is stored
// and streamed at that point. ErrChatBusy only when the running turn can no longer take it (it is
// finishing, or the worker does not steer), ErrUnavailable while the agent is down: the caller keeps
// it queued and retries when the turn ends (SetOnChatDone).
func (g *Gate) SteerChat(chat int64, role, message string) error {
	message = strings.TrimSpace(message)
	switch {
	case chat <= 0:
		return errors.New("chat not found")
	case role != RoleSignal && role != RoleTrade:
		return errors.New("a steer is a signal or trade message")
	case message == "" || utf8.RuneCountInString(message) > 8000:
		return errors.New("empty or too long message")
	}
	for try := 0; try < 2; try++ {
		r := g.run(chat)
		if r == nil {
			_, err := g.StartChat(ChatInput{ChatID: chat, Message: message, Role: role})
			if errors.Is(err, ErrChatBusy) {
				continue // a turn started meanwhile: steer into it
			}
			return err
		}
		return g.steer(r, chat, role, message)
	}
	return ErrChatBusy
}

func (g *Gate) steer(r *chatRun, chat int64, role, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), steerTimeout)
	defer cancel()
	if _, err := g.call(ctx, map[string]any{"type": "steer", "callId": r.callID, "message": message}); err != nil {
		if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWorkerGone) {
			return ErrUnavailable
		}
		g.logf("aigate: steer chat %d: %v (retried after the turn)", chat, err)
		return ErrChatBusy
	}
	now := g.now().UnixMilli()
	if _, err := g.st.DB.Exec(`INSERT INTO ai_chat_msgs(chat_id, ts, role, content) VALUES(?,?,?,?)`, chat, now, role, message); err != nil {
		g.logf("aigate: store steer %d: %v", chat, err)
	}
	g.st.DB.Exec(`UPDATE ai_chats SET updated=? WHERE id=?`, now, chat)
	r.publish(evJSON(map[string]any{"type": "steer", "role": role, "text": message}))
	return nil
}

// ChatTitle returns a chat's title; false when there is no such chat.
func (g *Gate) ChatTitle(chat int64) (string, bool) {
	var t string
	err := g.st.DB.QueryRow(`SELECT title FROM ai_chats WHERE id=?`, chat).Scan(&t)
	return t, err == nil
}

// SetOnChatDone registers a hook called (in the run's goroutine) after any chat turn ended; must not block.
func (g *Gate) SetOnChatDone(f func(chatID int64)) {
	g.mu.Lock()
	g.onDone = f
	g.mu.Unlock()
}

func (g *Gate) chatDone(id int64) {
	g.mu.Lock()
	f := g.onDone
	g.mu.Unlock()
	if f != nil {
		f(id)
	}
}

func (g *Gate) endRun(id int64) {
	g.runMu.Lock()
	delete(g.runs, id)
	g.runMu.Unlock()
}

func (g *Gate) run(id int64) *chatRun {
	g.runMu.Lock()
	defer g.runMu.Unlock()
	return g.runs[id]
}

// Busy reports whether any chat turn is running (a restart would interrupt it).
func (g *Gate) Busy() bool {
	g.runMu.Lock()
	defer g.runMu.Unlock()
	return len(g.runs) > 0
}

// StopChat cancels a running turn.
func (g *Gate) StopChat(id int64) bool {
	if r := g.run(id); r != nil && r.cancel != nil {
		r.cancel()
		return true
	}
	return false
}

func (g *Gate) currentModel() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.model
}

// Chats lists recent conversations.
func (g *Gate) Chats(limit int) ([]Chat, error) {
	rows, err := g.st.DB.Query(`SELECT id, created, updated, title, inst_id, cost_usd FROM ai_chats ORDER BY updated DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ID, &c.Created, &c.Updated, &c.Title, &c.InstID, &c.CostUSD); err != nil {
			return nil, err
		}
		c.Running = g.run(c.ID) != nil
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChatByID returns a conversation with its messages (and the events so far when running).
func (g *Gate) ChatByID(id int64) (*Chat, error) {
	var c Chat
	err := g.st.DB.QueryRow(`SELECT id, created, updated, title, inst_id, cost_usd FROM ai_chats WHERE id=?`, id).
		Scan(&c.ID, &c.Created, &c.Updated, &c.Title, &c.InstID, &c.CostUSD)
	if err != nil {
		return nil, err
	}
	rows, err := g.st.DB.Query(`SELECT id, ts, role, content, steps, cost_usd, latency_ms, error FROM ai_chat_msgs WHERE chat_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	c.Messages = []ChatMsg{}
	for rows.Next() {
		var m ChatMsg
		var steps string
		if err := rows.Scan(&m.ID, &m.Ts, &m.Role, &m.Content, &steps, &m.CostUSD, &m.LatencyMs, &m.Error); err != nil {
			return nil, err
		}
		m.Steps = json.RawMessage(steps)
		c.Messages = append(c.Messages, m)
	}
	c.Running = g.run(id) != nil
	if n := len(c.Messages); !c.Running && n > 0 && c.Messages[n-1].Role != RoleAssistant {
		c.Interrupted = true
	}
	return &c, rows.Err()
}

// DeleteChat removes a conversation (not while it runs).
func (g *Gate) DeleteChat(id int64) error {
	if g.run(id) != nil {
		return ErrChatBusy
	}
	if _, err := g.st.DB.Exec(`DELETE FROM ai_chat_msgs WHERE chat_id=?`, id); err != nil {
		return err
	}
	_, err := g.st.DB.Exec(`DELETE FROM ai_chats WHERE id=?`, id)
	return err
}

// streamChat writes a running chat's events as Server-Sent Events until it ends or the client leaves.
func (g *Gate) streamChat(w http.ResponseWriter, r *http.Request, id int64) {
	fl, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	write := func(ev chatEvent) {
		fmt.Fprintf(w, "data: %s\n\n", ev)
	}
	write(evJSON(map[string]any{"type": "chat", "chatId": id}))
	run := g.run(id)
	if run == nil {
		write(evJSON(map[string]any{"type": "idle"}))
		if fl != nil {
			fl.Flush()
		}
		return
	}
	past, ch := run.subscribe()
	defer run.unsubscribe(ch)
	for _, ev := range past {
		write(ev)
	}
	if fl != nil {
		fl.Flush()
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			write(ev)
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
		}
		if fl != nil {
			fl.Flush()
		}
	}
}

// callStream is call for a streaming request: "event" messages go to onEvent until the final
// result or error.
func (g *Gate) callStream(ctx context.Context, msg map[string]any, onEvent func(chatEvent)) (inbound, error) {
	g.mu.Lock()
	w := g.w
	g.mu.Unlock()
	if w == nil {
		return inbound{}, ErrUnavailable
	}
	id, _ := msg["id"].(string) // a chat turn brings its own (steers name it)
	if id == "" {
		id = newID()
		msg["id"] = id
	}
	if dl, ok := ctx.Deadline(); ok {
		msg["deadlineMs"] = dl.UnixMilli()
	}
	ch := make(chan inbound, 1024)
	w.mu.Lock()
	w.pending[id] = ch
	w.mu.Unlock()
	drop := func() {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
	}
	if err := w.send(msg); err != nil {
		drop()
		g.drop(w)
		return inbound{}, ErrWorkerGone
	}
	for {
		select {
		case m := <-ch:
			switch m.Type {
			case "event":
				onEvent(m.Event)
				continue
			case "error":
				return m, errors.New(m.Error)
			}
			return m, nil
		case <-w.done:
			return inbound{}, ErrWorkerGone
		case <-ctx.Done():
			drop()
			w.send(map[string]string{"type": "cancel", "id": id})
			return inbound{}, ctx.Err()
		}
	}
}

// ---- voice: Groq Whisper (direct, no proxy: only Anthropic traffic must use the residential proxy) ----

const groqURL = "https://api.groq.com/openai/v1/audio/transcriptions"

// Transcribe sends one recording to Groq and returns the text. The key is CTG_GROQ_KEY.
func Transcribe(ctx context.Context, audio io.Reader, filename string) (string, error) {
	key := os.Getenv("CTG_GROQ_KEY")
	if key == "" {
		return "", errors.New("语音转写未配置（CTG_GROQ_KEY）")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, io.LimitReader(audio, 20<<20)); err != nil {
		return "", err
	}
	mw.WriteField("model", "whisper-large-v3-turbo")
	mw.WriteField("response_format", "json")
	mw.WriteField("prompt", "以下是关于加密货币合约交易的普通话提问，请使用简体中文和标点。常见词：BTC、ETH、SOL、USDT、永续、合约、资金费率、持仓量、OI、爆仓、清算、多空、做多、做空、K线、杠杆。")
	mw.Close()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, groqURL, &body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return "", fmt.Errorf("groq: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("groq: HTTP %d %s", resp.StatusCode, strings.ReplaceAll(string(raw[:min(len(raw), 300)]), key, "***"))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}
