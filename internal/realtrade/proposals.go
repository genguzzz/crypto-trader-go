// Package realtrade turns the agent's trade ideas into real OKX orders only with the owner's approval
// (owner decision 2026-11-01, docs/trading-design.md §7). A TradeProposal comes from a chat (the
// owner-only MCP tool propose_trade), a signal or a schedule (Trigger → a headless agent run recorded
// as a normal chat, which may call propose_trade). Every proposal waits for the owner: the first
// answer from any channel (the dedicated Telegram trade bot, the web, the app) wins, expiry never
// approves, and approval re-validates against the live price before internal/okxorder places the one
// allowed order (market + TP + SL, isolated). CTG_TRADE_MODE decides dry run (default) or live.
//
//	pending → approved → executing → executed (dryrun | live) → closed (TP / SL, detected)
//	                               → failed       (OKX or network error; the request is kept)
//	                   → invalidated              (re-validation failed; nothing sent)
//	pending → rejected | expired
package realtrade

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const schema = `
CREATE TABLE IF NOT EXISTS trade_proposals(id INTEGER PRIMARY KEY AUTOINCREMENT, created INTEGER NOT NULL, updated INTEGER NOT NULL,
  source TEXT NOT NULL, trigger_label TEXT NOT NULL DEFAULT '', chat_id INTEGER NOT NULL DEFAULT 0,
  inst_id TEXT NOT NULL, side TEXT NOT NULL, margin REAL NOT NULL, leverage INTEGER NOT NULL,
  contracts REAL NOT NULL DEFAULT 0, notional REAL NOT NULL DEFAULT 0, ref_px REAL NOT NULL, sl REAL NOT NULL, tp REAL NOT NULL,
  rationale TEXT NOT NULL DEFAULT '', invalidation TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL,
  status TEXT NOT NULL, status_at INTEGER NOT NULL, mode TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '', via TEXT NOT NULL DEFAULT '', exec_px REAL NOT NULL DEFAULT 0, ord_id TEXT NOT NULL DEFAULT '',
  request TEXT NOT NULL DEFAULT '', response TEXT NOT NULL DEFAULT '',
  closed_at INTEGER NOT NULL DEFAULT 0, close_kind TEXT NOT NULL DEFAULT '', close_px REAL NOT NULL DEFAULT 0, pnl REAL NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS trade_proposals_status ON trade_proposals(status, expires_at);
CREATE INDEX IF NOT EXISTS trade_proposals_chat ON trade_proposals(chat_id);
CREATE TABLE IF NOT EXISTS trade_proposal_events(id INTEGER PRIMARY KEY AUTOINCREMENT, proposal_id INTEGER NOT NULL, ts INTEGER NOT NULL,
  kind TEXT NOT NULL, text TEXT NOT NULL, by TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS trade_proposal_events_p ON trade_proposal_events(proposal_id, id);
CREATE TABLE IF NOT EXISTS trade_trigger_runs(chat_id INTEGER PRIMARY KEY, trigger_id TEXT NOT NULL, kind TEXT NOT NULL,
  label TEXT NOT NULL, ts INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS trade_trigger_runs_t ON trade_trigger_runs(trigger_id, ts);
CREATE TABLE IF NOT EXISTS trade_tg_msgs(proposal_id INTEGER PRIMARY KEY, chat INTEGER NOT NULL, message INTEGER NOT NULL);
`

// Sources and statuses.
const (
	SourceChat     = "chat"
	SourceSignal   = "signal"
	SourceSchedule = "schedule"

	StatusPending     = "pending"
	StatusApproved    = "approved"
	StatusExecuting   = "executing"
	StatusExecuted    = "executed"
	StatusFailed      = "failed"
	StatusRejected    = "rejected"
	StatusExpired     = "expired"
	StatusInvalidated = "invalidated"
)

// Proposal is one trade the agent asked the owner to approve, with its audit trail.
type Proposal struct {
	ID           int64   `json:"id"`
	Created      int64   `json:"created"`
	Updated      int64   `json:"updated"`
	Source       string  `json:"source"`  // chat | signal | schedule
	Trigger      string  `json:"trigger"` // trigger label ("" for a chat)
	ChatID       int64   `json:"chatId"`
	InstID       string  `json:"instId"`
	Side         string  `json:"side"` // long | short
	MarginUSDT   float64 `json:"marginUsdt"`
	Leverage     int     `json:"leverage"`
	Contracts    float64 `json:"contracts"`    // at creation (re-sized at execution)
	NotionalUSDT float64 `json:"notionalUsdt"` // at creation
	RefPx        float64 `json:"refPx"`        // last price at creation
	SL           float64 `json:"sl"`
	TP           float64 `json:"tp"`
	SLPct        float64 `json:"slPct"` // distance from RefPx, %
	TPPct        float64 `json:"tpPct"`
	RR           float64 `json:"rr"`
	Rationale    string  `json:"rationale"`
	Invalidation string  `json:"invalidation"`
	ExpiresAt    int64   `json:"expiresAt"`
	Status       string  `json:"status"`
	Mode         string  `json:"mode"` // dryrun | live (at creation; at execution once executed)
	StatusAt     int64   `json:"statusAt"`
	Error        string  `json:"error,omitempty"`
	By           string  `json:"by,omitempty"`  // who resolved it: telegram:<id>, owner, timeout
	Via          string  `json:"via,omitempty"` // telegram | web | app | timeout
	ExecPx       float64 `json:"execPx,omitempty"`
	OrdID        string  `json:"ordId,omitempty"`
	// Request / Response: the exact OKX requests ([{method, path, body}]) and answers.
	Request   json.RawMessage `json:"request,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	ClosedAt  int64           `json:"closedAt,omitempty"`
	CloseKind string          `json:"closeKind,omitempty"` // tp | sl | other
	ClosePx   float64         `json:"closePx,omitempty"`
	PnlUSDT   float64         `json:"pnlUsdt,omitempty"` // dry run: estimate before fees
	Events    []AuditEvent    `json:"events,omitempty"`
}

// AuditEvent is one entry of a proposal's trail.
type AuditEvent struct {
	Ts   int64  `json:"ts"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	By   string `json:"by,omitempty"`
}

// Open reports whether the proposal still waits or is on its way to the exchange.
func (p Proposal) Open() bool {
	return p.Status == StatusPending || p.Status == StatusApproved || p.Status == StatusExecuting
}

var (
	ErrNotFound = errors.New("proposal not found")
	ErrClosed   = errors.New("proposal is no longer pending")
)

const cols = `id, created, updated, source, trigger_label, chat_id, inst_id, side, margin, leverage, contracts, notional, ref_px, sl, tp,
  rationale, invalidation, expires_at, status, status_at, mode, error, resolved_by, via, exec_px, ord_id, request, response,
  closed_at, close_kind, close_px, pnl`

func scan(row interface{ Scan(...any) error }) (Proposal, error) {
	var p Proposal
	var req, resp string
	err := row.Scan(&p.ID, &p.Created, &p.Updated, &p.Source, &p.Trigger, &p.ChatID, &p.InstID, &p.Side, &p.MarginUSDT, &p.Leverage,
		&p.Contracts, &p.NotionalUSDT, &p.RefPx, &p.SL, &p.TP, &p.Rationale, &p.Invalidation, &p.ExpiresAt, &p.Status, &p.StatusAt,
		&p.Mode, &p.Error, &p.By, &p.Via, &p.ExecPx, &p.OrdID, &req, &resp, &p.ClosedAt, &p.CloseKind, &p.ClosePx, &p.PnlUSDT)
	if req != "" {
		p.Request = json.RawMessage(req)
	}
	if resp != "" {
		p.Response = json.RawMessage(resp)
	}
	if p.RefPx > 0 && p.SL > 0 && p.TP > 0 {
		p.SLPct = abs(p.RefPx-p.SL) / p.RefPx * 100
		p.TPPct = abs(p.TP-p.RefPx) / p.RefPx * 100
		if p.SLPct > 0 {
			p.RR = p.TPPct / p.SLPct
		}
	}
	return p, err
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func (s *Service) get(id int64) (Proposal, error) {
	p, err := scan(s.db.QueryRow(`SELECT `+cols+` FROM trade_proposals WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Get returns a proposal with its audit trail.
func (s *Service) Get(id int64) (Proposal, error) {
	p, err := s.get(id)
	if err != nil {
		return p, err
	}
	rows, err := s.db.Query(`SELECT ts, kind, text, by FROM trade_proposal_events WHERE proposal_id=? ORDER BY id`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Events = []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.Ts, &e.Kind, &e.Text, &e.By); err != nil {
			return p, err
		}
		p.Events = append(p.Events, e)
	}
	return p, rows.Err()
}

// ListQuery filters List: Status pending | open (pending, approved, executing) | all.
type ListQuery struct {
	Status string
	ChatID int64
	Limit  int
}

// List returns proposals newest first.
func (s *Service) List(q ListQuery) ([]Proposal, error) {
	where, args := []string{"1=1"}, []any{}
	switch q.Status {
	case "", "all":
	case "open":
		where = append(where, "status IN ('pending','approved','executing')")
	default:
		where = append(where, "status=?")
		args = append(args, q.Status)
	}
	if q.ChatID > 0 {
		where = append(where, "chat_id=?")
		args = append(args, q.ChatID)
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 50
	}
	rows, err := s.db.Query(`SELECT `+cols+` FROM trade_proposals WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// transition moves a proposal from one of from to status (first caller wins: a conditional UPDATE).
// set adds column assignments ("col=?" → value). It reports whether this call made the change.
func (s *Service) transition(id int64, from []string, status string, set map[string]any, extra string, extraArgs ...any) (bool, error) {
	now := s.now().UnixMilli()
	q := `UPDATE trade_proposals SET status=?, status_at=?, updated=?`
	args := []any{status, now, now}
	for k, v := range set {
		q += ", " + k + "=?"
		args = append(args, v)
	}
	q += ` WHERE id=? AND status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(from)), ",") + `)`
	args = append(args, id)
	for _, f := range from {
		args = append(args, f)
	}
	if extra != "" {
		q += " AND " + extra
		args = append(args, extraArgs...)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Service) audit(id int64, kind, text, by string) {
	if _, err := s.db.Exec(`INSERT INTO trade_proposal_events(proposal_id, ts, kind, text, by) VALUES(?,?,?,?,?)`,
		id, s.now().UnixMilli(), kind, text, by); err != nil {
		s.logf("realtrade: audit %d: %v", id, err)
	}
}
