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
	// Retry re-runs the chat's last question (after an error or an interrupted turn) instead of
	// adding a new one; Message is ignored.
	Retry bool `json:"retry"`
}

// ChatModels the app may pick per turn.
var ChatModels = []string{"claude-sonnet-5", "claude-opus-5-5"}

var errChatBusy = errors.New("这个对话还在回答中")

// StartChat stores the user's message and starts the agent run; the caller streams it with Attach.
func (g *Gate) StartChat(in ChatInput) (int64, error) {
	in.Message = strings.TrimSpace(in.Message)
	if in.Model != "" && !slices.Contains(ChatModels, in.Model) {
		return 0, errors.New("unknown model")
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
	run := &chatRun{subs: map[chan chatEvent]struct{}{}}
	g.runMu.Lock()
	if g.runs == nil {
		g.runs = map[int64]*chatRun{}
	}
	if _, busy := g.runs[in.ChatID]; busy {
		g.runMu.Unlock()
		return 0, errChatBusy
	}
	g.runs[in.ChatID] = run
	g.runMu.Unlock()
	if in.Retry {
		// drop the failed answer (if any) so the question is answered once
		g.st.DB.Exec(`DELETE FROM ai_chat_msgs WHERE chat_id=? AND role='assistant' AND id > (SELECT MAX(id) FROM ai_chat_msgs WHERE chat_id=? AND role='user')`, in.ChatID, in.ChatID)
	} else if _, err := g.st.DB.Exec(`INSERT INTO ai_chat_msgs(chat_id, ts, role, content) VALUES(?,?,?,?)`, in.ChatID, now, "user", in.Message); err != nil {
		g.endRun(in.ChatID)
		return 0, err
	}
	g.st.DB.Exec(`UPDATE ai_chats SET updated=? WHERE id=?`, now, in.ChatID)

	ctx, cancel := context.WithTimeout(context.Background(), ChatTimeout)
	run.cancel = cancel
	go func() {
		defer cancel()
		defer g.endRun(in.ChatID)
		start := g.now()
		msg := map[string]any{"type": "chat", "chatId": in.ChatID, "sessionId": session, "message": in.Message, "instId": in.InstID, "model": in.Model, "notes": g.Notes()}
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
	err := g.st.DB.QueryRow(`SELECT content FROM ai_chat_msgs WHERE chat_id=? AND role='user' ORDER BY id DESC LIMIT 1`, chat).Scan(&q)
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
	if n := len(c.Messages); !c.Running && n > 0 && c.Messages[n-1].Role == "user" {
		c.Interrupted = true
	}
	return &c, rows.Err()
}

// DeleteChat removes a conversation (not while it runs).
func (g *Gate) DeleteChat(id int64) error {
	if g.run(id) != nil {
		return errChatBusy
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
	id := newID()
	msg["id"] = id
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
