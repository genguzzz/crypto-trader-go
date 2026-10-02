package interact

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"crypto-trader-go/internal/trade"
)

const schema = `
CREATE TABLE IF NOT EXISTS interact_outbox(id INTEGER PRIMARY KEY AUTOINCREMENT, channel TEXT NOT NULL, kind TEXT NOT NULL,
  payload TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS interact_outbox_ch ON interact_outbox(channel, id);
CREATE TABLE IF NOT EXISTS interact_prompts(id TEXT PRIMARY KEY, short TEXT NOT NULL UNIQUE, event TEXT NOT NULL,
  options TEXT NOT NULL, deadline INTEGER NOT NULL, dflt TEXT NOT NULL, state TEXT NOT NULL, choice TEXT NOT NULL DEFAULT '',
  answered_by TEXT NOT NULL DEFAULT '', answered_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS interact_prompts_state ON interact_prompts(state, deadline);
CREATE INDEX IF NOT EXISTS interact_prompts_created ON interact_prompts(created_at);
CREATE TABLE IF NOT EXISTS interact_messages(prompt_id TEXT NOT NULL, channel TEXT NOT NULL, chat TEXT NOT NULL,
  message TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(prompt_id, channel, chat)) WITHOUT ROWID;
`

// Prompt states.
const (
	StateOpen     = "open"
	StateAnswered = "answered" // a person chose
	StateExpired  = "expired"  // deadline (default applied, by "timeout") or withdrawn (by "cancelled")
)

// Answerer ids besides "telegram:<user id>".
const (
	byWeb       = "web"
	byTimeout   = "timeout"
	byCancelled = "cancelled"
)

// Record is a persisted prompt (API form).
type Record struct {
	ID         string               `json:"id"`
	Short      string               `json:"-"`
	Event      trade.Event          `json:"event"`
	Options    []trade.PromptOption `json:"options"`
	Deadline   int64                `json:"deadline"` // ms
	Default    string               `json:"default"`
	State      string               `json:"state"`
	Choice     string               `json:"choice,omitempty"`
	By         string               `json:"by,omitempty"`
	AnsweredAt int64                `json:"answeredAt,omitempty"`
	CreatedAt  int64                `json:"createdAt"`
}

func (r Record) label(key string) string {
	for _, o := range r.Options {
		if o.Key == key {
			if o.Label != "" {
				return o.Label
			}
			return o.Key
		}
	}
	if key == "" {
		return "无"
	}
	return key
}

func (r Record) has(key string) bool {
	for _, o := range r.Options {
		if o.Key == key {
			return true
		}
	}
	return false
}

const recordCols = `id,short,event,options,deadline,dflt,state,choice,answered_by,answered_at,created_at`

func scanRecord(sc interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var ev, opts string
	if err := sc.Scan(&r.ID, &r.Short, &ev, &opts, &r.Deadline, &r.Default, &r.State, &r.Choice, &r.By, &r.AnsweredAt, &r.CreatedAt); err != nil {
		return r, err
	}
	json.Unmarshal([]byte(ev), &r.Event)
	json.Unmarshal([]byte(opts), &r.Options)
	return r, nil
}

func (h *Hub) record(id string) (Record, bool) {
	r, err := scanRecord(h.db.QueryRow(`SELECT `+recordCols+` FROM interact_prompts WHERE id=? OR short=?`, id, id))
	return r, err == nil
}

// insertPrompt stores a new prompt; it reports false when one with the same id already exists.
func (h *Hub) insertPrompt(r Record) (bool, error) {
	ev, _ := json.Marshal(r.Event)
	opts, _ := json.Marshal(r.Options)
	res, err := h.db.Exec(`INSERT INTO interact_prompts(`+recordCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		r.ID, r.Short, string(ev), string(opts), r.Deadline, r.Default, r.State, r.Choice, r.By, r.AnsweredAt, r.CreatedAt)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// closePrompt moves an open prompt to its final state. Only the first call wins.
func (h *Hub) closePrompt(id, state, choice, by string, at int64) (bool, error) {
	res, err := h.db.Exec(`UPDATE interact_prompts SET state=?, choice=?, answered_by=?, answered_at=? WHERE id=? AND state=?`,
		state, choice, by, at, id, StateOpen)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Prompts lists prompts newest first. state: "open" or "" / "all".
func (h *Hub) Prompts(state string, limit int) ([]Record, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + recordCols + ` FROM interact_prompts`
	var args []any
	if state != "" && state != "all" {
		q += ` WHERE state=?`
		args = append(args, state)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := h.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// dueOpen returns ids of open prompts whose deadline passed.
func (h *Hub) dueOpen(now int64) ([]Record, error) {
	rows, err := h.db.Query(`SELECT `+recordCols+` FROM interact_prompts WHERE state=? AND deadline<=?`, StateOpen, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- channel message ids (to edit a prompt once it is closed) ----------

func (h *Hub) putMessage(promptID, channel, chat, message string) error {
	_, err := h.db.Exec(`INSERT OR REPLACE INTO interact_messages VALUES(?,?,?,?,?)`, promptID, channel, chat, message, time.Now().UnixMilli())
	return err
}

type sentMessage struct{ Chat, Message string }

func (h *Hub) messages(promptID, channel string) ([]sentMessage, error) {
	rows, err := h.db.Query(`SELECT chat, message FROM interact_messages WHERE prompt_id=? AND channel=?`, promptID, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sentMessage
	for rows.Next() {
		var m sentMessage
		if err := rows.Scan(&m.Chat, &m.Message); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- outbox ----------

const (
	obEvent   = "event"
	obAsk     = "ask"
	obResolve = "resolve"
)

type obRow struct {
	ID        int64
	Kind      string
	Payload   string
	CreatedAt int64
}

func (h *Hub) enqueue(channels []string, kind, payload string) error {
	if len(channels) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	for _, c := range channels {
		if _, err := tx.Exec(`INSERT INTO interact_outbox(channel,kind,payload,created_at) VALUES(?,?,?,?)`, c, kind, payload, now); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (h *Hub) pending(channel string, limit int) ([]obRow, error) {
	rows, err := h.db.Query(`SELECT id,kind,payload,created_at FROM interact_outbox WHERE channel=? ORDER BY id LIMIT ?`, channel, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []obRow
	for rows.Next() {
		var r obRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Payload, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (h *Hub) backlog(channel string) int {
	var n int
	h.db.QueryRow(`SELECT COUNT(*) FROM interact_outbox WHERE channel=?`, channel).Scan(&n)
	return n
}

func (h *Hub) dequeue(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := h.db.Exec(`DELETE FROM interact_outbox WHERE id IN (`+ph+`)`, args...)
	return err
}

func (h *Hub) dropChannel(channel string) {
	if _, err := h.db.Exec(`DELETE FROM interact_outbox WHERE channel=?`, channel); err != nil {
		h.logf("interact: drop %s outbox: %v", channel, err)
	}
}

// prune drops old closed prompts, their message ids and stale outbox rows.
func (h *Hub) prune(now time.Time) {
	cut := now.Add(-promptKeep).UnixMilli()
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM interact_prompts WHERE state<>'open' AND created_at<?`, cut},
		{`DELETE FROM interact_messages WHERE created_at<?`, now.Add(-messageKeep).UnixMilli()},
		{`DELETE FROM interact_outbox WHERE created_at<?`, now.Add(-outboxKeep).UnixMilli()},
	} {
		if _, err := h.db.Exec(q.sql, q.arg); err != nil {
			h.logf("interact: prune: %v", err)
		}
	}
}

// ---------- ids ----------

const shortAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

func newShort() string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = shortAlphabet[int(b[i])%len(shortAlphabet)]
	}
	return string(b)
}
