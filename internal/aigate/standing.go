package aigate

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Standing chats are conversations the server owns: created once, never deleted from the list, woken
// by the server (SteerChat) as well as answered by the owner. The trading desk (internal/autopilot)
// is the only kind.

// KindDesk marks the trading desk's chat.
const KindDesk = "desk"

// ErrStandingChat: a standing chat cannot be deleted (turn its service off instead).
var ErrStandingChat = errors.New("这是常驻交易台会话，不能删除；可以在交易台设置里停用")

// EnsureChat returns the id of the standing chat of a kind, creating it (no turn is run).
func (g *Gate) EnsureChat(kind, title string) (int64, error) {
	if kind == "" {
		return 0, errors.New("kind is required")
	}
	g.runMu.Lock() // serialises concurrent creation
	defer g.runMu.Unlock()
	var id int64
	err := g.st.DB.QueryRow(`SELECT id FROM ai_chats WHERE kind=? ORDER BY id LIMIT 1`, kind).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := g.now().UnixMilli()
	res, err := g.st.DB.Exec(`INSERT INTO ai_chats(created, updated, title, kind) VALUES(?,?,?,?)`, now, now, title, kind)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StandingChat is the id of the standing chat of a kind (false: not created yet).
func (g *Gate) StandingChat(kind string) (int64, bool) {
	var id int64
	err := g.st.DB.QueryRow(`SELECT id FROM ai_chats WHERE kind=? ORDER BY id LIMIT 1`, kind).Scan(&id)
	return id, err == nil
}

// ChatRunning reports whether a turn is running in the chat.
func (g *Gate) ChatRunning(chat int64) bool { return g.run(chat) != nil }

// Answer is the newest assistant message of a chat.
type Answer struct {
	MsgID     int64
	Ts        int64
	Text      string
	Error     string
	CostUSD   float64
	LatencyMs int64
	Tokens    int64 // input + output + cache tokens of the turn (0 when the agent reported none)
}

// LastAnswer returns the newest assistant message with an id above afterMsg.
func (g *Gate) LastAnswer(chat, afterMsg int64) (Answer, bool) {
	var a Answer
	var usage string
	err := g.st.DB.QueryRow(`SELECT id, ts, content, error, cost_usd, latency_ms, usage FROM ai_chat_msgs
		WHERE chat_id=? AND role='assistant' AND id>? ORDER BY id DESC LIMIT 1`, chat, afterMsg).
		Scan(&a.MsgID, &a.Ts, &a.Text, &a.Error, &a.CostUSD, &a.LatencyMs, &usage)
	if err != nil {
		return a, false
	}
	var u struct{ Input, Output, CacheWrite, CacheRead int64 }
	if usage != "" && json.Unmarshal([]byte(usage), &u) == nil {
		a.Tokens = u.Input + u.Output + u.CacheWrite + u.CacheRead
	}
	return a, true
}

// LastMsgID is the newest message id of a chat (0: none).
func (g *Gate) LastMsgID(chat int64) int64 {
	var id int64
	g.st.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM ai_chat_msgs WHERE chat_id=?`, chat).Scan(&id)
	return id
}

// ResetSession makes the chat's next turn start a fresh agent session: the stored messages stay, the
// model's context starts empty. The desk does this every so many runs so a chat that never ends does
// not carry (and pay for) an ever longer context; what must survive is in its journal and memory.
// Refused while a turn runs.
func (g *Gate) ResetSession(chat int64) error {
	if g.run(chat) != nil {
		return ErrChatBusy
	}
	_, err := g.st.DB.Exec(`UPDATE ai_chats SET session_id='' WHERE id=?`, chat)
	return err
}
