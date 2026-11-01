// Package aigate is the optional AI review layer: it implements trade.Reviewer by forwarding each
// signal's context pack to a Python worker (Claude Agent SDK) over a Unix socket, one stateless model
// call per signal. It owns the credential (encrypted at rest when CTG_SECRET_KEY is set, masked in
// the API, never logged), the append-only audit log, the per-strategy experience documents
// ("memory", distilled from the audit log) and parameter-change proposals that only the owner can
// approve. Hard limits stay in the order layer (internal/trade); the model can only answer
// execute/skip with a size scale, stop/target suggestions and a rationale.
// See docs/trading-design.md §4–§5.
package aigate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"crypto-trader-go/internal/store"
	"crypto-trader-go/internal/trade"
)

// DefaultModel is the Claude model used until the owner picks another one.
const DefaultModel = "claude-sonnet-5"

const modelSetting = "ai.model"

const schema = `
CREATE TABLE IF NOT EXISTS ai_decisions(id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL,
  account_id INTEGER NOT NULL DEFAULT 0, instance_id INTEGER NOT NULL DEFAULT 0, signal_id INTEGER NOT NULL DEFAULT 0,
  strategy TEXT NOT NULL DEFAULT '', inst_id TEXT NOT NULL DEFAULT '', signal_action TEXT NOT NULL DEFAULT '',
  request TEXT NOT NULL DEFAULT '', request_sha256 TEXT NOT NULL DEFAULT '', memory_version INTEGER NOT NULL DEFAULT 0,
  prompt_version TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '',
  decision TEXT NOT NULL DEFAULT '{}', meta TEXT NOT NULL DEFAULT '{}', cost_usd REAL NOT NULL DEFAULT 0,
  latency_ms INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ai_decisions_account ON ai_decisions(account_id, id);
CREATE INDEX IF NOT EXISTS ai_decisions_instance ON ai_decisions(instance_id, id);
CREATE INDEX IF NOT EXISTS ai_decisions_strategy ON ai_decisions(strategy, id);
CREATE INDEX IF NOT EXISTS ai_decisions_ts ON ai_decisions(ts);
CREATE TABLE IF NOT EXISTS ai_outcomes(decision_id INTEGER NOT NULL, ts INTEGER NOT NULL, result TEXT NOT NULL,
  PRIMARY KEY(decision_id, ts)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS ai_memory(strategy TEXT NOT NULL, version INTEGER NOT NULL, ts INTEGER NOT NULL,
  doc TEXT NOT NULL, source TEXT NOT NULL, through_decision INTEGER NOT NULL DEFAULT 0, decisions INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '', cost_usd REAL NOT NULL DEFAULT 0, audit_id INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(strategy, version)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS ai_proposals(id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL,
  decision_id INTEGER NOT NULL, account_id INTEGER NOT NULL, instance_id INTEGER NOT NULL, strategy TEXT NOT NULL,
  params TEXT NOT NULL, current TEXT NOT NULL, rationale TEXT NOT NULL, state TEXT NOT NULL,
  decided_at INTEGER NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS ai_proposals_state ON ai_proposals(state, id);
`

// Options configure the gate. Zero values pick the defaults.
type Options struct {
	SocketPath     string        // default $CTG_RUN_DIR/ai.sock (CTG_RUN_DIR default data/run)
	SocketMode     os.FileMode   // default 0660 (ctg and the worker share uid/gid 65532)
	Model          string        // default model when the owner has not chosen one (DefaultModel)
	SecretKey      string        // at-rest key for the credential; default env CTG_SECRET_KEY
	ReviewTimeout  time.Duration // used when Review's ctx has no deadline (default 90 s)
	TestTimeout    time.Duration // default 90 s
	ReflectTimeout time.Duration // default 5 min
	ReflectN       int           // decisions fed to a reflection (default 60)
	Heartbeat      time.Duration // ping interval (default 15 s)
	Now            func() time.Time
	Logf           func(format string, args ...any) // default log.Printf; never receives secrets
}

// Gate is the AI review layer. It implements trade.Reviewer.
type Gate struct {
	st     *store.Store
	opts   Options
	sock   string
	sealer *sealer

	mu         sync.Mutex
	cred       credential
	credStored stored
	credProb   string
	model      string
	w          *workerConn
	lastCallAt int64
	lastError  string
	onProposal func(Proposal)
	apply      func(instanceID int64, params map[string]float64) error

	agent   Agent
	notesMu sync.Mutex
	notify  func(ctx context.Context, text string) error
	runMu   sync.Mutex
	runs    map[int64]*chatRun
	onDone  func(chatID int64) // a chat turn ended (signals retry their queued wake-ups)
	onPlan  func(chatID int64, instID string, planID int64)

	candles Candles1m // plan tracker's price source

	reflectMu sync.Mutex
	propMu    sync.Mutex // serializes proposal decisions
}

var _ trade.Reviewer = (*Gate)(nil)

// New creates the gate's tables and loads the stored credential and model.
func New(st *store.Store, opts Options) (*Gate, error) {
	if opts.SocketMode == 0 {
		opts.SocketMode = 0o660
	}
	if opts.Model == "" {
		opts.Model = DefaultModel
	}
	if opts.SecretKey == "" {
		opts.SecretKey = os.Getenv("CTG_SECRET_KEY")
	}
	if opts.ReviewTimeout <= 0 {
		opts.ReviewTimeout = 90 * time.Second
	}
	if opts.TestTimeout <= 0 {
		opts.TestTimeout = 90 * time.Second
	}
	if opts.ReflectTimeout <= 0 {
		opts.ReflectTimeout = 5 * time.Minute
	}
	if opts.ReflectN <= 0 {
		opts.ReflectN = 60
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 15 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if _, err := st.DB.Exec(schema + chatSchema + planSchema); err != nil {
		return nil, fmt.Errorf("aigate schema: %w", err)
	}
	g := &Gate{st: st, opts: opts, sock: SocketPath(opts), sealer: newSealer(opts.SecretKey), model: opts.Model}
	g.opts.SecretKey = "" // only the derived cipher is kept
	if raw, ok := st.Setting(credSetting); ok && raw != "" {
		g.cred, g.credStored, g.credProb = decodeCredential(raw, g.sealer)
		if g.credProb != "" {
			g.logf("aigate: %s", g.credProb)
		}
	}
	if m, ok := st.Setting(modelSetting); ok && m != "" {
		g.model = m
	}
	return g, nil
}

func (g *Gate) now() time.Time                  { return g.opts.Now() }
func (g *Gate) logf(format string, args ...any) { g.opts.Logf(format, args...) }

// SetOnProposal registers a hook called (in its own goroutine) for every new parameter proposal,
// e.g. to notify the owner through the interaction layer.
func (g *Gate) SetOnProposal(fn func(p Proposal)) {
	g.mu.Lock()
	g.onProposal = fn
	g.mu.Unlock()
}

// SetApplyParams wires approval to the order layer: fn receives only the changed parameters and
// must create a new instance config version.
func (g *Gate) SetApplyParams(fn func(instanceID int64, params map[string]float64) error) {
	g.mu.Lock()
	g.apply = fn
	g.mu.Unlock()
}

// Available reports whether a ready worker is connected and a usable credential is configured.
func (g *Gate) Available() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.w != nil && g.w.info.Ready && g.cred.configured()
}

// ---------- credential & model ----------

// SetCredential validates and stores the credential and pushes it to the worker.
func (g *Gate) SetCredential(kind, value string) (Status, error) {
	c, err := validateCredential(kind, value)
	if err != nil {
		return Status{}, err
	}
	now := g.now().UnixMilli()
	raw, err := encodeCredential(c, g.sealer, now)
	if err != nil {
		return Status{}, errors.New("cannot store credential")
	}
	if err := g.st.PutSetting(credSetting, raw); err != nil {
		return Status{}, fmt.Errorf("store: %w", err)
	}
	g.mu.Lock()
	g.cred, g.credProb = c, ""
	g.credStored = stored{Kind: c.Kind, UpdatedAt: now, Enc: "plain"}
	if g.sealer != nil {
		g.credStored.Enc = "aes-gcm"
	}
	g.lastError = ""
	g.mu.Unlock()
	g.logf("aigate: credential updated (kind %s)", c.Kind)
	g.pushConfig()
	return g.Status(), nil
}

// DeleteCredential removes the credential (stored and in the worker).
func (g *Gate) DeleteCredential() (Status, error) {
	if _, err := g.st.DB.Exec(`DELETE FROM settings WHERE key=?`, credSetting); err != nil {
		return Status{}, err
	}
	g.mu.Lock()
	g.cred, g.credStored, g.credProb = credential{}, stored{}, ""
	g.mu.Unlock()
	g.logf("aigate: credential deleted")
	g.pushConfig()
	return g.Status(), nil
}

var modelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,62}(\[1m\])?$`)

// SetModel changes the model (a Claude model id or alias, e.g. claude-sonnet-5, sonnet).
func (g *Gate) SetModel(model string) (Status, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = g.opts.Model
	}
	if !modelRe.MatchString(model) {
		return Status{}, errors.New("模型名称无效")
	}
	if err := g.st.PutSetting(modelSetting, model); err != nil {
		return Status{}, err
	}
	g.mu.Lock()
	g.model = model
	g.mu.Unlock()
	g.pushConfig()
	return g.Status(), nil
}

// Status is the masked view served by GET /api/v1/ai/status.
type Status struct {
	Configured       bool        `json:"configured"`
	Kind             string      `json:"kind,omitempty"`
	Masked           string      `json:"masked,omitempty"`
	Storage          string      `json:"storage,omitempty"` // encrypted | plain
	StorageNote      string      `json:"storageNote,omitempty"`
	UpdatedAt        int64       `json:"updatedAt,omitempty"`
	CredentialError  string      `json:"credentialError,omitempty"`
	Model            string      `json:"model"`
	WorkerOnline     bool        `json:"workerOnline"`
	WorkerReady      bool        `json:"workerReady"`
	ProxyOK          bool        `json:"proxyOk"`
	ProxyError       string      `json:"proxyError,omitempty"`
	Worker           *WorkerInfo `json:"worker,omitempty"`
	Available        bool        `json:"available"`
	LastCallAt       int64       `json:"lastCallAt,omitempty"`
	LastError        string      `json:"lastError,omitempty"`
	CostToday        float64     `json:"costToday"` // USD, UTC+8 day; an estimate under an OAuth (subscription) token
	CallsToday       int         `json:"callsToday"`
	PendingProposals int         `json:"pendingProposals"`
}

var cst = time.FixedZone("UTC+8", 8*3600)

func dayStart(t time.Time) time.Time {
	t = t.In(cst)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, cst)
}

// Status reports configuration and worker health without revealing the secret.
func (g *Gate) Status() Status {
	g.mu.Lock()
	s := Status{Configured: g.cred.configured(), Model: g.model, LastCallAt: g.lastCallAt, LastError: g.lastError,
		CredentialError: g.credProb}
	if s.Configured {
		s.Kind, s.Masked, s.UpdatedAt = g.cred.Kind, mask(g.cred.Value), g.credStored.UpdatedAt
		if g.credStored.Enc == "aes-gcm" {
			s.Storage = "encrypted"
		} else {
			s.Storage = "plain"
			s.StorageNote = "CTG_SECRET_KEY is not set: the credential is stored unencrypted in the database"
		}
	}
	if g.w != nil {
		info := g.w.info
		s.Worker, s.WorkerOnline, s.WorkerReady, s.ProxyOK, s.ProxyError = &info, true, info.Ready, info.ProxyOK, info.ProxyError
	}
	s.Available = s.WorkerOnline && s.WorkerReady && s.Configured
	g.mu.Unlock()
	from := dayStart(g.now()).UnixMilli()
	g.st.DB.QueryRow(`SELECT COALESCE(SUM(cost_usd),0), COUNT(*) FROM ai_decisions WHERE ts>=?`, from).Scan(&s.CostToday, &s.CallsToday)
	g.st.DB.QueryRow(`SELECT COUNT(*) FROM ai_proposals WHERE state='pending'`).Scan(&s.PendingProposals)
	return s
}

func (g *Gate) noteCall(errText string) {
	g.mu.Lock()
	g.lastCallAt = g.now().UnixMilli()
	g.lastError = errText
	g.mu.Unlock()
}

// secretValue returns the credential value for scrubbing.
func (g *Gate) secretValue() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cred.Value
}

// ---------- review ----------

// workerDecision is the decision payload from the worker (trade.Decision without id).
type workerDecision struct {
	Action      string             `json:"action"`
	SizeScale   float64            `json:"sizeScale"`
	StopLoss    float64            `json:"stopLoss"`
	TakeProfit  float64            `json:"takeProfit"`
	Confidence  float64            `json:"confidence"`
	Rationale   string             `json:"rationale"`
	ParamChange map[string]float64 `json:"paramChange"`
	Model       string             `json:"model"`
	CostUSD     float64            `json:"costUsd"`
	LatencyMs   int64              `json:"latencyMs"`
}

type callMeta struct {
	PromptVersion string  `json:"promptVersion"`
	Model         string  `json:"model"`
	CostUSD       float64 `json:"costUsd"`
	LatencyMs     int64   `json:"latencyMs"`
}

// Review asks the worker for a decision. It attaches the latest memory document of the strategy
// (unless the request already carries one), writes one audit row whatever the outcome and returns
// the decision with its audit id. Parameter changes become pending proposals; they are never
// applied here.
func (g *Gate) Review(ctx context.Context, req trade.ReviewRequest) (trade.Decision, error) {
	memVer := 0
	if req.Memory == "" && req.Strategy.Name != "" {
		if m, ok := g.latestMemory(req.Strategy.Name); ok {
			req.Memory, memVer = m.Doc, m.Version
		}
	}
	req.Strategy.Code = ""
	body, err := json.Marshal(req)
	if err != nil {
		return trade.Decision{}, fmt.Errorf("aigate: encode request: %w", err)
	}
	row := auditRow{Kind: "review", AccountID: req.Signal.AccountID, InstanceID: req.Signal.InstanceID,
		SignalID: req.Signal.ID, Strategy: req.Strategy.Name, InstID: req.Signal.InstID, SignalAction: string(req.Signal.Action),
		Request: string(body), RequestSHA: sha256hex(body), MemoryVersion: memVer}
	if req.Signal.AccountID == 0 {
		row.AccountID = req.Account.ID
	}
	if row.InstanceID == 0 {
		row.InstanceID = req.Instance.ID
	}

	if !g.Available() {
		return trade.Decision{}, ErrUnavailable
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.opts.ReviewTimeout)
		defer cancel()
	}
	g.mu.Lock()
	row.Model = g.model
	g.mu.Unlock()
	start := g.now()
	resp, err := g.call(ctx, map[string]any{"type": "review", "request": json.RawMessage(body)})
	row.LatencyMs = g.now().Sub(start).Milliseconds()
	if err != nil {
		row.Error = g.errText(err)
		g.writeAudit(&row)
		g.noteCall(row.Error)
		return trade.Decision{}, fmt.Errorf("aigate: %s", row.Error)
	}
	var wd workerDecision
	var meta callMeta
	json.Unmarshal(resp.Meta, &meta)
	if err := json.Unmarshal(resp.Decision, &wd); err != nil {
		row.Error = "malformed decision from worker"
		row.Meta = string(resp.Meta)
		g.writeAudit(&row)
		g.noteCall(row.Error)
		return trade.Decision{}, errors.New("aigate: " + row.Error)
	}
	d, notes := sanitize(wd, req)
	if wd.LatencyMs > 0 {
		d.LatencyMs = wd.LatencyMs
	} else {
		d.LatencyMs = row.LatencyMs
	}
	if d.Model == "" {
		d.Model = firstNonEmpty(meta.Model, row.Model)
	}
	row.Model, row.CostUSD, row.Action, row.PromptVersion = d.Model, d.CostUSD, d.Action, meta.PromptVersion
	row.Meta = string(resp.Meta)
	dj, _ := json.Marshal(struct {
		trade.Decision
		Raw   json.RawMessage `json:"raw"`
		Notes []string        `json:"notes,omitempty"`
	}{d, resp.Decision, notes})
	row.Decision = string(dj)
	if err := g.writeAudit(&row); err != nil {
		g.noteCall("audit write failed")
		return trade.Decision{}, fmt.Errorf("aigate: audit: %w", err)
	}
	d.ID = row.ID
	g.noteCall("")
	if len(d.ParamChange) > 0 {
		g.propose(d, req)
	}
	return d, nil
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func (g *Gate) errText(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return truncRunes(scrub(err.Error(), g.secretValue()), 500)
}

// sanitize clamps the model's numbers and drops suggestions that contradict the signal (a stop on
// the wrong side of the price, unknown or non-tunable parameters). It does not enforce account
// limits: the order layer does.
func sanitize(w workerDecision, req trade.ReviewRequest) (trade.Decision, []string) {
	var notes []string
	d := trade.Decision{Action: strings.ToLower(strings.TrimSpace(w.Action)), Model: w.Model, CostUSD: w.CostUSD,
		Rationale: strings.TrimSpace(w.Rationale)}
	if d.Action != "execute" {
		if d.Action != "skip" {
			notes = append(notes, fmt.Sprintf("unknown action %q treated as skip", w.Action))
		}
		d.Action = "skip"
	}
	d.Confidence = clamp01(w.Confidence)
	if d.Action == "execute" {
		d.SizeScale = clamp01(w.SizeScale)
		if d.SizeScale == 0 {
			notes = append(notes, "execute with size 0 treated as skip")
			d.Action = "skip"
		}
	}
	if d.Action == "execute" {
		px := req.Signal.Price
		long := req.Signal.Action == trade.OpenLong
		short := req.Signal.Action == trade.OpenShort
		if ok(w.StopLoss) {
			if px > 0 && ((long && w.StopLoss >= px) || (short && w.StopLoss <= px)) {
				notes = append(notes, "stop on the wrong side of the price ignored")
			} else if long || short {
				d.StopLoss = w.StopLoss
			}
		}
		if ok(w.TakeProfit) {
			if px > 0 && ((long && w.TakeProfit <= px) || (short && w.TakeProfit >= px)) {
				notes = append(notes, "target on the wrong side of the price ignored")
			} else if long || short {
				d.TakeProfit = w.TakeProfit
			}
		}
	}
	d.Rationale = truncRunes(d.Rationale, 2000)
	if len(w.ParamChange) > 0 {
		specs := map[string]trade.ParamSpec{}
		for _, p := range req.Strategy.Params {
			specs[p.Name] = p
		}
		keys := make([]string, 0, len(w.ParamChange))
		for k := range w.ParamChange {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := w.ParamChange[k]
			p, found := specs[k]
			switch {
			case !found:
				notes = append(notes, "unknown parameter "+k+" dropped")
			case !p.AITunable:
				notes = append(notes, "parameter "+k+" is not AI-tunable")
			case math.IsNaN(v) || math.IsInf(v, 0) || (p.Min != nil && v < *p.Min) || (p.Max != nil && v > *p.Max):
				notes = append(notes, "parameter "+k+" out of range")
			case req.Instance.Params != nil && req.Instance.Params[k] == v:
			default:
				if d.ParamChange == nil {
					d.ParamChange = map[string]float64{}
				}
				d.ParamChange[k] = v
			}
		}
	}
	return d, notes
}

func ok(x float64) bool { return x > 0 && !math.IsNaN(x) && !math.IsInf(x, 0) }

func clamp01(x float64) float64 {
	if math.IsNaN(x) || x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// ---------- audit ----------

type auditRow struct {
	ID            int64
	Kind          string
	AccountID     int64
	InstanceID    int64
	SignalID      int64
	Strategy      string
	InstID        string
	SignalAction  string
	Request       string
	RequestSHA    string
	MemoryVersion int
	PromptVersion string
	Model         string
	Action        string
	Decision      string
	Meta          string
	CostUSD       float64
	LatencyMs     int64
	Error         string
}

func (g *Gate) writeAudit(r *auditRow) error {
	if r.Decision == "" {
		r.Decision = "{}"
	}
	if r.Meta == "" {
		r.Meta = "{}"
	}
	res, err := g.st.DB.Exec(`INSERT INTO ai_decisions(ts,kind,account_id,instance_id,signal_id,strategy,inst_id,signal_action,
		request,request_sha256,memory_version,prompt_version,model,action,decision,meta,cost_usd,latency_ms,error)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.now().UnixMilli(), r.Kind, r.AccountID, r.InstanceID, r.SignalID, r.Strategy, r.InstID, r.SignalAction,
		r.Request, r.RequestSHA, r.MemoryVersion, r.PromptVersion, r.Model, r.Action, r.Decision, r.Meta, r.CostUSD,
		r.LatencyMs, r.Error)
	if err != nil {
		g.logf("aigate: audit write: %v", err)
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// DecisionRecord is one audit row.
type DecisionRecord struct {
	ID            int64           `json:"id"`
	Ts            int64           `json:"ts"`
	Kind          string          `json:"kind"` // review | test | reflect
	AccountID     int64           `json:"accountId"`
	InstanceID    int64           `json:"instanceId"`
	SignalID      int64           `json:"signalId"`
	Strategy      string          `json:"strategy"`
	InstID        string          `json:"instId"`
	SignalAction  string          `json:"signalAction"`
	RequestSHA256 string          `json:"requestSha256"`
	MemoryVersion int             `json:"memoryVersion"`
	PromptVersion string          `json:"promptVersion"`
	Model         string          `json:"model"`
	Action        string          `json:"action"`
	Decision      json.RawMessage `json:"decision"`
	Meta          json.RawMessage `json:"meta,omitempty"`
	CostUSD       float64         `json:"costUsd"`
	LatencyMs     int64           `json:"latencyMs"`
	Error         string          `json:"error,omitempty"`
	Outcomes      []Outcome       `json:"outcomes,omitempty"`
	Request       json.RawMessage `json:"request,omitempty"` // only for single-record reads
}

type Outcome struct {
	Ts     int64  `json:"ts"`
	Result string `json:"result"`
}

// DecisionQuery filters Decisions. Zero values mean "any"; Limit defaults to 50 (max 500).
type DecisionQuery struct {
	AccountID, InstanceID, Before int64
	Kind, Strategy                string
	Limit                         int
}

const decisionCols = `id,ts,kind,account_id,instance_id,signal_id,strategy,inst_id,signal_action,request_sha256,
	memory_version,prompt_version,model,action,decision,meta,cost_usd,latency_ms,error`

func scanDecision(sc interface{ Scan(...any) error }, extra ...any) (DecisionRecord, error) {
	var r DecisionRecord
	var dec, meta string
	args := []any{&r.ID, &r.Ts, &r.Kind, &r.AccountID, &r.InstanceID, &r.SignalID, &r.Strategy, &r.InstID, &r.SignalAction,
		&r.RequestSHA256, &r.MemoryVersion, &r.PromptVersion, &r.Model, &r.Action, &dec, &meta, &r.CostUSD, &r.LatencyMs, &r.Error}
	if err := sc.Scan(append(args, extra...)...); err != nil {
		return r, err
	}
	r.Decision, r.Meta = json.RawMessage(dec), json.RawMessage(meta)
	return r, nil
}

// Decisions lists audit rows, newest first.
func (g *Gate) Decisions(q DecisionQuery) ([]DecisionRecord, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	where, args := []string{"1=1"}, []any{}
	if q.AccountID > 0 {
		where, args = append(where, "account_id=?"), append(args, q.AccountID)
	}
	if q.InstanceID > 0 {
		where, args = append(where, "instance_id=?"), append(args, q.InstanceID)
	}
	if q.Before > 0 {
		where, args = append(where, "id<?"), append(args, q.Before)
	}
	if q.Kind != "" {
		where, args = append(where, "kind=?"), append(args, q.Kind)
	}
	if q.Strategy != "" {
		where, args = append(where, "strategy=?"), append(args, q.Strategy)
	}
	rows, err := g.st.DB.Query(`SELECT `+decisionCols+` FROM ai_decisions WHERE `+strings.Join(where, " AND ")+
		` ORDER BY id DESC LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DecisionRecord{}
	for rows.Next() {
		r, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, g.attachOutcomes(out)
}

func (g *Gate) attachOutcomes(rs []DecisionRecord) error {
	if len(rs) == 0 {
		return nil
	}
	idx := map[int64]int{}
	ids := make([]string, len(rs))
	for i, r := range rs {
		idx[r.ID] = i
		ids[i] = fmt.Sprint(r.ID)
	}
	rows, err := g.st.DB.Query(`SELECT decision_id, ts, result FROM ai_outcomes WHERE decision_id IN (` +
		strings.Join(ids, ",") + `) ORDER BY ts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var o Outcome
		if err := rows.Scan(&id, &o.Ts, &o.Result); err != nil {
			return err
		}
		rs[idx[id]].Outcomes = append(rs[idx[id]].Outcomes, o)
	}
	return rows.Err()
}

// ErrNotFound is returned for unknown ids.
var ErrNotFound = errors.New("not found")

// Decision returns one audit row including the full request (context pack) for replay.
func (g *Gate) Decision(id int64) (DecisionRecord, error) {
	var req string
	r, err := scanDecision(g.st.DB.QueryRow(`SELECT `+decisionCols+`, request FROM ai_decisions WHERE id=?`, id), &req)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if req != "" {
		r.Request = json.RawMessage(req)
	}
	rs := []DecisionRecord{r}
	err = g.attachOutcomes(rs)
	return rs[0], err
}

// RecordOutcome appends what happened after a decision (e.g. "+1.2% in 6h", "stopped -0.8%",
// "skipped; price +0.4% in 4h"). Reflections read these.
func (g *Gate) RecordOutcome(decisionID int64, result string) error {
	result = strings.TrimSpace(result)
	if decisionID <= 0 || result == "" {
		return errors.New("decision id and result required")
	}
	result = truncRunes(result, 500)
	_, err := g.st.DB.Exec(`INSERT OR REPLACE INTO ai_outcomes VALUES(?,?,?)`, decisionID, g.now().UnixMilli(), result)
	return err
}

// ---------- test ----------

// TestResult is the answer of POST /api/v1/ai/test.
type TestResult struct {
	OK        bool    `json:"ok"`
	Model     string  `json:"model,omitempty"`
	Reply     string  `json:"reply,omitempty"`
	CostUSD   float64 `json:"costUsd"`
	LatencyMs int64   `json:"latencyMs"`
	Error     string  `json:"error,omitempty"`
}

// Test makes a minimal model call through the worker to verify credential, proxy and model.
func (g *Gate) Test(ctx context.Context) TestResult {
	g.mu.Lock()
	configured, online, ready, model := g.cred.configured(), g.w != nil, g.w != nil && g.w.info.Ready, g.model
	proxyErr := ""
	if g.w != nil {
		proxyErr = g.w.info.ProxyError
	}
	g.mu.Unlock()
	switch {
	case !configured:
		return TestResult{Error: "尚未配置 AI 凭证"}
	case !online:
		return TestResult{Error: "AI worker 未连接"}
	case !ready && proxyErr != "":
		return TestResult{Error: "AI worker is not ready: " + proxyErr}
	case !ready:
		return TestResult{Error: "AI worker is not ready"}
	}
	ctx, cancel := context.WithTimeout(ctx, g.opts.TestTimeout)
	defer cancel()
	row := auditRow{Kind: "test", Model: model}
	start := g.now()
	resp, err := g.call(ctx, map[string]any{"type": "test"})
	row.LatencyMs = g.now().Sub(start).Milliseconds()
	out := TestResult{LatencyMs: row.LatencyMs, Model: model}
	if err != nil {
		row.Error = g.errText(err)
		out.Error = row.Error
	} else {
		var r struct {
			Reply string `json:"reply"`
			callMeta
		}
		json.Unmarshal(resp.Result, &r)
		out.OK, out.Reply, out.CostUSD = true, truncRunes(r.Reply, 200), r.CostUSD
		if r.Model != "" {
			out.Model = r.Model
		}
		if r.LatencyMs > 0 {
			out.LatencyMs = r.LatencyMs
		}
		row.Model, row.CostUSD, row.PromptVersion = out.Model, r.CostUSD, r.PromptVersion
		row.Decision = string(resp.Result)
	}
	g.writeAudit(&row)
	g.noteCall(row.Error)
	return out
}
