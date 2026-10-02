package aigate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"crypto-trader-go/internal/trade"
)

// MemoryDoc is one version of a strategy's experience document. Versions are append-only; the
// latest one is attached to every review of the strategy.
type MemoryDoc struct {
	Strategy        string  `json:"strategy"`
	Version         int     `json:"version"`
	Ts              int64   `json:"ts"`
	Doc             string  `json:"doc"`
	Source          string  `json:"source"`          // reflect
	ThroughDecision int64   `json:"throughDecision"` // newest audit id it was distilled from
	Decisions       int     `json:"decisions"`       // how many decisions were read
	Model           string  `json:"model,omitempty"`
	CostUSD         float64 `json:"costUsd"`
	AuditID         int64   `json:"auditId,omitempty"`
}

const memoryCols = `strategy,version,ts,doc,source,through_decision,decisions,model,cost_usd,audit_id`

func scanMemory(sc interface{ Scan(...any) error }) (MemoryDoc, error) {
	var m MemoryDoc
	err := sc.Scan(&m.Strategy, &m.Version, &m.Ts, &m.Doc, &m.Source, &m.ThroughDecision, &m.Decisions, &m.Model, &m.CostUSD, &m.AuditID)
	return m, err
}

func (g *Gate) latestMemory(strategy string) (MemoryDoc, bool) {
	m, err := scanMemory(g.st.DB.QueryRow(`SELECT `+memoryCols+` FROM ai_memory WHERE strategy=? ORDER BY version DESC LIMIT 1`, strategy))
	return m, err == nil
}

// Memory returns a version of the strategy's document (0 = latest) and the list of versions
// (newest first, without the text).
func (g *Gate) Memory(strategy string, version int) (*MemoryDoc, []MemoryDoc, error) {
	rows, err := g.st.DB.Query(`SELECT `+memoryCols+` FROM ai_memory WHERE strategy=? ORDER BY version DESC`, strategy)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var cur *MemoryDoc
	list := []MemoryDoc{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, nil, err
		}
		if cur == nil && (version == 0 || m.Version == version) {
			c := m
			cur = &c
		}
		m.Doc = ""
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if version != 0 && cur == nil {
		return nil, list, ErrNotFound
	}
	return cur, list, nil
}

// reflectDecision is one past decision as given to the reflection prompt.
type reflectDecision struct {
	ID           int64     `json:"id"`
	Ts           int64     `json:"ts"`
	InstID       string    `json:"instId"`
	SignalAction string    `json:"signalAction"`
	SignalReason string    `json:"signalReason,omitempty"`
	Price        float64   `json:"price,omitempty"`
	Action       string    `json:"action"`
	SizeScale    float64   `json:"sizeScale"`
	Confidence   float64   `json:"confidence"`
	Rationale    string    `json:"rationale"`
	Outcomes     []Outcome `json:"outcomes,omitempty"`
}

// ErrNoDecisions is returned by Reflect when the strategy has no reviewed signals yet.
var ErrNoDecisions = errors.New("该策略还没有 AI 决策记录")

// Reflect asks the worker to distil the last ReflectN decisions of the strategy and their outcomes
// into a new version of its experience document.
func (g *Gate) Reflect(ctx context.Context, strategy string) (MemoryDoc, error) {
	if strategy == "" {
		return MemoryDoc{}, errors.New("strategy required")
	}
	g.reflectMu.Lock()
	defer g.reflectMu.Unlock()
	if !g.Available() {
		return MemoryDoc{}, ErrUnavailable
	}
	rows, err := g.st.DB.Query(`SELECT id, ts, inst_id, signal_action, decision, request FROM ai_decisions
		WHERE kind='review' AND error='' AND strategy=? ORDER BY id DESC LIMIT ?`, strategy, g.opts.ReflectN)
	if err != nil {
		return MemoryDoc{}, err
	}
	var ds []DecisionRecord
	var items []reflectDecision
	description, timeframe := "", ""
	for rows.Next() {
		var it reflectDecision
		var dec, req string
		if err := rows.Scan(&it.ID, &it.Ts, &it.InstID, &it.SignalAction, &dec, &req); err != nil {
			rows.Close()
			return MemoryDoc{}, err
		}
		var d trade.Decision
		json.Unmarshal([]byte(dec), &d)
		it.Action, it.SizeScale, it.Confidence, it.Rationale = d.Action, d.SizeScale, d.Confidence, d.Rationale
		var rq trade.ReviewRequest
		if json.Unmarshal([]byte(req), &rq) == nil {
			it.SignalReason, it.Price = rq.Signal.Reason, rq.Signal.Price
			if description == "" {
				description, timeframe = rq.Strategy.Description, rq.Strategy.Timeframe
			}
		}
		items = append(items, it)
		ds = append(ds, DecisionRecord{ID: it.ID})
	}
	rows.Close()
	if len(items) == 0 {
		return MemoryDoc{}, ErrNoDecisions
	}
	if err := g.attachOutcomes(ds); err != nil {
		return MemoryDoc{}, err
	}
	for i := range items {
		items[i].Outcomes = ds[i].Outcomes
	}
	// oldest first reads naturally
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	prev, hasPrev := g.latestMemory(strategy)
	reqBody := map[string]any{"strategy": strategy, "description": description, "timeframe": timeframe,
		"decisions": items, "currentVersion": 0, "current": ""}
	if hasPrev {
		reqBody["currentVersion"], reqBody["current"] = prev.Version, prev.Doc
	}
	body, _ := json.Marshal(reqBody)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.opts.ReflectTimeout)
		defer cancel()
	}
	g.mu.Lock()
	model := g.model
	g.mu.Unlock()
	row := auditRow{Kind: "reflect", Strategy: strategy, Request: string(body), RequestSHA: sha256hex(body), Model: model,
		MemoryVersion: prev.Version}
	start := g.now()
	resp, err := g.call(ctx, map[string]any{"type": "reflect", "request": json.RawMessage(body)})
	row.LatencyMs = g.now().Sub(start).Milliseconds()
	if err != nil {
		row.Error = g.errText(err)
		g.writeAudit(&row)
		g.noteCall(row.Error)
		return MemoryDoc{}, fmt.Errorf("aigate: reflect: %s", row.Error)
	}
	var r struct {
		Memory string `json:"memory"`
		callMeta
	}
	json.Unmarshal(resp.Result, &r)
	r.Memory = strings.TrimSpace(r.Memory)
	row.Decision, row.CostUSD, row.PromptVersion = string(resp.Result), r.CostUSD, r.PromptVersion
	if r.Model != "" {
		row.Model = r.Model
	}
	if r.Memory == "" {
		row.Error = "empty memory document"
		g.writeAudit(&row)
		g.noteCall(row.Error)
		return MemoryDoc{}, errors.New("aigate: reflect: " + row.Error)
	}
	if err := g.writeAudit(&row); err != nil {
		return MemoryDoc{}, err
	}
	g.noteCall("")
	m := MemoryDoc{Strategy: strategy, Version: prev.Version + 1, Ts: g.now().UnixMilli(), Doc: truncRunes(r.Memory, 12000),
		Source: "reflect", ThroughDecision: items[len(items)-1].ID, Decisions: len(items), Model: row.Model, CostUSD: r.CostUSD,
		AuditID: row.ID}
	if _, err := g.st.DB.Exec(`INSERT INTO ai_memory(`+memoryCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.Strategy, m.Version, m.Ts,
		m.Doc, m.Source, m.ThroughDecision, m.Decisions, m.Model, m.CostUSD, m.AuditID); err != nil {
		return MemoryDoc{}, err
	}
	g.logf("aigate: memory %s v%d from %d decisions", strategy, m.Version, m.Decisions)
	return m, nil
}

// strategiesDue lists strategies with successful reviews newer than their latest memory version.
func (g *Gate) strategiesDue() ([]string, error) {
	rows, err := g.st.DB.Query(`SELECT d.strategy FROM ai_decisions d
		LEFT JOIN (SELECT strategy, MAX(through_decision) AS through FROM ai_memory GROUP BY strategy) m ON m.strategy=d.strategy
		WHERE d.kind='review' AND d.error='' AND d.strategy<>'' GROUP BY d.strategy
		HAVING MAX(d.id) > COALESCE(MAX(m.through), 0) ORDER BY d.strategy`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// nextReflect is the next 00:05 UTC+8 strictly after t.
func nextReflect(t time.Time) time.Time {
	d := dayStart(t).Add(5 * time.Minute)
	if !d.After(t) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

func (g *Gate) reflectLoop(ctx context.Context) {
	for {
		wait := time.Until(nextReflect(time.Now()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		g.reflectAll(ctx)
	}
}

func (g *Gate) reflectAll(ctx context.Context) {
	names, err := g.strategiesDue()
	if err != nil {
		g.logf("aigate: reflect schedule: %v", err)
		return
	}
	for _, s := range names {
		if ctx.Err() != nil {
			return
		}
		if !g.Available() {
			g.logf("aigate: reflect skipped (AI unavailable)")
			return
		}
		if _, err := g.Reflect(ctx, s); err != nil {
			g.logf("aigate: reflect %s: %v", s, err)
		}
	}
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
