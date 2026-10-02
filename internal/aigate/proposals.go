package aigate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"crypto-trader-go/internal/trade"
)

// Proposal states.
const (
	PropPending  = "pending"
	PropApproved = "approved"
	PropRejected = "rejected"
)

// Proposal is a parameter change suggested by the model. It is applied only when the owner
// approves it (ApproveProposal → ApplyParams).
type Proposal struct {
	ID         int64              `json:"id"`
	Ts         int64              `json:"ts"`
	DecisionID int64              `json:"decisionId"`
	AccountID  int64              `json:"accountId"`
	InstanceID int64              `json:"instanceId"`
	Strategy   string             `json:"strategy"`
	Params     map[string]float64 `json:"params"`  // only the changed parameters
	Current    map[string]float64 `json:"current"` // their values when proposed
	Rationale  string             `json:"rationale"`
	State      string             `json:"state"`
	DecidedAt  int64              `json:"decidedAt,omitempty"`
	Note       string             `json:"note,omitempty"`
}

// propose stores a pending proposal for the decision's instance. An older pending proposal of the
// same instance is superseded (rejected with a note).
func (g *Gate) propose(d trade.Decision, req trade.ReviewRequest) {
	instID := req.Instance.ID
	if instID == 0 {
		instID = req.Signal.InstanceID
	}
	acct := req.Account.ID
	if acct == 0 {
		acct = req.Signal.AccountID
	}
	cur := map[string]float64{}
	for k := range d.ParamChange {
		if v, ok := req.Instance.Params[k]; ok {
			cur[k] = v
		} else {
			for _, p := range req.Strategy.Params {
				if p.Name == k {
					cur[k] = p.Default
				}
			}
		}
	}
	pj, _ := json.Marshal(d.ParamChange)
	cj, _ := json.Marshal(cur)
	now := g.now().UnixMilli()
	tx, err := g.st.DB.Begin()
	if err != nil {
		g.logf("aigate: proposal: %v", err)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO ai_proposals(ts,decision_id,account_id,instance_id,strategy,params,current,rationale,state)
		VALUES(?,?,?,?,?,?,?,?,?)`, now, d.ID, acct, instID, req.Strategy.Name, string(pj), string(cj), d.Rationale, PropPending)
	if err != nil {
		g.logf("aigate: proposal: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`UPDATE ai_proposals SET state=?, decided_at=?, note=? WHERE instance_id=? AND state=? AND id<>?`,
		PropRejected, now, fmt.Sprintf("superseded by #%d", id), instID, PropPending, id); err != nil {
		g.logf("aigate: proposal: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		g.logf("aigate: proposal: %v", err)
		return
	}
	p, err := g.Proposal(id)
	if err != nil {
		return
	}
	g.mu.Lock()
	fn := g.onProposal
	g.mu.Unlock()
	if fn != nil {
		go fn(p)
	}
}

const proposalCols = `id,ts,decision_id,account_id,instance_id,strategy,params,current,rationale,state,decided_at,note`

func scanProposal(sc interface{ Scan(...any) error }) (Proposal, error) {
	var p Proposal
	var pj, cj string
	err := sc.Scan(&p.ID, &p.Ts, &p.DecisionID, &p.AccountID, &p.InstanceID, &p.Strategy, &pj, &cj, &p.Rationale, &p.State,
		&p.DecidedAt, &p.Note)
	if err != nil {
		return p, err
	}
	json.Unmarshal([]byte(pj), &p.Params)
	json.Unmarshal([]byte(cj), &p.Current)
	return p, nil
}

// Proposal returns one proposal.
func (g *Gate) Proposal(id int64) (Proposal, error) {
	p, err := scanProposal(g.st.DB.QueryRow(`SELECT `+proposalCols+` FROM ai_proposals WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Proposals lists proposals, newest first; state "" means all.
func (g *Gate) Proposals(state string, limit int) ([]Proposal, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q, args := `SELECT `+proposalCols+` FROM ai_proposals`, []any{}
	if state != "" {
		q, args = q+` WHERE state=?`, append(args, state)
	}
	rows, err := g.st.DB.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrNotPending is returned when deciding a proposal that was already decided.
var ErrNotPending = errors.New("proposal is not pending")

// ApproveProposal decides a pending proposal. Approval calls ApplyParams first; if that fails the
// proposal stays pending and the error is returned.
func (g *Gate) ApproveProposal(id int64, approve bool) (Proposal, error) {
	g.propMu.Lock()
	defer g.propMu.Unlock()
	p, err := g.Proposal(id)
	if err != nil {
		return p, err
	}
	if p.State != PropPending {
		return p, ErrNotPending
	}
	state, note := PropRejected, "rejected by owner"
	if approve {
		g.mu.Lock()
		apply := g.apply
		g.mu.Unlock()
		if apply == nil {
			return p, errors.New("applying parameters is not wired")
		}
		if err := apply(p.InstanceID, p.Params); err != nil {
			return p, fmt.Errorf("apply: %w", err)
		}
		state, note = PropApproved, "approved by owner"
	}
	res, err := g.st.DB.Exec(`UPDATE ai_proposals SET state=?, decided_at=?, note=? WHERE id=? AND state=?`,
		state, g.now().UnixMilli(), note, id, PropPending)
	if err != nil {
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, ErrNotPending
	}
	return g.Proposal(id)
}
