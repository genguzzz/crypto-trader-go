package autopilot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"crypto-trader-go/internal/realtrade"
	"crypto-trader-go/internal/signals"
)

// The journal is the desk's record of what it decided: one row per coin per decision, stamped by the
// server with the price and fact-panel values at that moment, then followed: the price 1 h / 4 h /
// 24 h later, the best and worst excursion within 24 h, and the fate of the linked proposal. Returns
// are signed by the decision's side (a short that fell is positive), so "was the call right" can be
// counted for trades and for the ones it passed on alike. The agent writes a review when the outcome
// is in; lessons go to desk_memory.

// Decision actions.
var actions = []string{"open", "add", "reduce", "close", "adjust", "hold", "watch", "skip"}

var actionText = map[string]string{"open": "开仓", "add": "加仓", "reduce": "减仓", "close": "平仓", "adjust": "调整止盈止损",
	"hold": "持有不动", "watch": "观望等待", "skip": "不做"}

var convictions = map[string]string{"low": "低", "medium": "中", "high": "高", "低": "低", "中": "中", "高": "高"}

var verdicts = []string{"right", "wrong", "mixed", "na"}

var verdictText = map[string]string{"right": "判断正确", "wrong": "判断错误", "mixed": "部分正确", "na": "无法判定"}

// Decision is one journal row.
type Decision struct {
	ID           int64   `json:"id"`
	Ts           int64   `json:"ts"`
	RunID        int64   `json:"runId,omitempty"`
	ChatID       int64   `json:"chatId,omitempty"`
	InstID       string  `json:"instId"`
	Action       string  `json:"action"`
	Side         string  `json:"side,omitempty"` // long | short | ""
	Conviction   string  `json:"conviction,omitempty"`
	Thesis       string  `json:"thesis"`
	Entry        float64 `json:"entry,omitempty"`
	SL           float64 `json:"sl,omitempty"`
	TP           float64 `json:"tp,omitempty"`
	Invalidation string  `json:"invalidation,omitempty"`
	Tags         string  `json:"tags,omitempty"`
	Strategy     string  `json:"strategy,omitempty"` // the scans that put the coin in front of the desk
	Trigger      string  `json:"trigger,omitempty"`  // schedule | signal | manual | "" (the owner's own question)
	ProposalID   int64   `json:"proposalId,omitempty"`
	Px           float64 `json:"px"` // last price when recorded
	// Snapshot: the fact-panel values and BTC's price at that moment.
	Snapshot json.RawMessage `json:"snapshot,omitempty"`
	// Outcome (nil = not due yet or no data). Ret* in percent, signed by Side (unsigned price change when there is no side).
	Px1h   *float64 `json:"px1h,omitempty"`
	Px4h   *float64 `json:"px4h,omitempty"`
	Px24h  *float64 `json:"px24h,omitempty"`
	Ret1h  *float64 `json:"ret1h,omitempty"`
	Ret4h  *float64 `json:"ret4h,omitempty"`
	Ret24h *float64 `json:"ret24h,omitempty"`
	MFE24h *float64 `json:"mfe24h,omitempty"` // best move for the side within 24 h, %
	MAE24h *float64 `json:"mae24h,omitempty"` // worst move against it, % (≤ 0)
	// OutcomeAt: when the 24 h follow-up was closed.
	OutcomeAt      int64   `json:"outcomeAt,omitempty"`
	ProposalStatus string  `json:"proposalStatus,omitempty"`
	PnlUSDT        float64 `json:"pnlUsdt,omitempty"`
	ClosedAt       int64   `json:"closedAt,omitempty"`
	CloseKind      string  `json:"closeKind,omitempty"`
	Review         string  `json:"review,omitempty"`
	Verdict        string  `json:"verdict,omitempty"`
	ReviewedAt     int64   `json:"reviewedAt,omitempty"`
}

const decisionCols = `id, ts, run_id, chat_id, inst_id, action, side, conviction, thesis, entry, sl, tp, invalidation, tags, strategy, trigger,
	proposal_id, px, snapshot, px_1h, px_4h, px_24h, ret_1h, ret_4h, ret_24h, mfe_24h, mae_24h, outcome_at, proposal_status, pnl_usdt,
	closed_at, close_kind, review, verdict, reviewed_at`

func scanDecision(row interface{ Scan(...any) error }) (Decision, error) {
	var d Decision
	var snap string
	var f [8]sql.NullFloat64
	err := row.Scan(&d.ID, &d.Ts, &d.RunID, &d.ChatID, &d.InstID, &d.Action, &d.Side, &d.Conviction, &d.Thesis, &d.Entry, &d.SL, &d.TP,
		&d.Invalidation, &d.Tags, &d.Strategy, &d.Trigger, &d.ProposalID, &d.Px, &snap, &f[0], &f[1], &f[2], &f[3], &f[4], &f[5], &f[6], &f[7],
		&d.OutcomeAt, &d.ProposalStatus, &d.PnlUSDT, &d.ClosedAt, &d.CloseKind, &d.Review, &d.Verdict, &d.ReviewedAt)
	if err != nil {
		return d, err
	}
	if snap != "" {
		d.Snapshot = json.RawMessage(snap)
	}
	for i, p := range []**float64{&d.Px1h, &d.Px4h, &d.Px24h, &d.Ret1h, &d.Ret4h, &d.Ret24h, &d.MFE24h, &d.MAE24h} {
		if f[i].Valid {
			v := f[i].Float64
			*p = &v
		}
	}
	return d, nil
}

// line is the one-line form used in briefs and tool results.
func (d Decision) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s #%d %s %s", time.UnixMilli(d.Ts).In(tz8).Format("01-02 15:04"), d.ID, coin(d.InstID), actionText[d.Action])
	if d.Side != "" {
		b.WriteString(sideText[d.Side])
	}
	if d.Conviction != "" {
		b.WriteString("（" + convictions[d.Conviction] + "）")
	}
	fmt.Fprintf(&b, " @%s：%s", fmtPx(d.Px), clip(d.Thesis, 90))
	var out []string
	for _, x := range []struct {
		l string
		v *float64
	}{{"1h", d.Ret1h}, {"4h", d.Ret4h}, {"24h", d.Ret24h}} {
		if x.v != nil {
			out = append(out, fmt.Sprintf("%s %+.1f%%", x.l, *x.v))
		}
	}
	if d.ProposalID > 0 {
		st := d.ProposalStatus
		if st == "" {
			st = "pending"
		}
		p := fmt.Sprintf("提议 #%d %s", d.ProposalID, st)
		if d.ClosedAt > 0 {
			p += fmt.Sprintf("，已平仓 %+.2f USDT", d.PnlUSDT)
		}
		out = append(out, p)
	}
	if len(out) > 0 {
		b.WriteString("（" + strings.Join(out, "，") + "）")
	}
	if d.Verdict != "" {
		b.WriteString(" → " + verdictText[d.Verdict])
	}
	return b.String()
}

// JournalInput is one decision to record.
type JournalInput struct {
	ChatID       int64
	InstID       string
	Action       string
	Side         string
	Conviction   string
	Thesis       string
	Entry        float64
	SL           float64
	TP           float64
	Invalidation string
	Tags         string
	ProposalID   int64
}

// Record stores a decision with the market state now.
func (s *Service) Record(in JournalInput) (Decision, error) {
	inst := signals.NormalizeInst(in.InstID)
	if inst == "" || !s.mkt.Known(inst) {
		return Decision{}, fmt.Errorf("unknown instrument %q (OKX USDT perpetual ids like AAVE-USDT-SWAP)", in.InstID)
	}
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	if !slices.Contains(actions, in.Action) {
		return Decision{}, fmt.Errorf("action must be one of %s", strings.Join(actions, ", "))
	}
	in.Side = strings.ToLower(strings.TrimSpace(in.Side))
	switch in.Side {
	case "", "long", "short":
	default:
		return Decision{}, errors.New("side must be long, short or empty (no directional view)")
	}
	if in.Side == "" && slices.Contains([]string{"open", "add", "reduce", "close", "adjust"}, in.Action) {
		return Decision{}, fmt.Errorf("side is required for %s (the position's side)", in.Action)
	}
	conv := ""
	if c := strings.ToLower(strings.TrimSpace(in.Conviction)); c != "" {
		t, ok := convictions[c]
		if !ok {
			return Decision{}, errors.New("conviction must be low, medium or high")
		}
		conv = map[string]string{"低": "low", "中": "medium", "高": "high"}[t]
	}
	in.Thesis = strings.TrimSpace(in.Thesis)
	if n := utf8.RuneCountInString(in.Thesis); n < 6 || n > 600 {
		return Decision{}, errors.New("thesis is required: why, in one or two sentences (6–600 characters)")
	}
	for _, v := range []float64{in.Entry, in.SL, in.TP} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return Decision{}, errors.New("entry / sl / tp are prices (0 = none)")
		}
	}
	now := s.now()
	px, ts, ok := s.mkt.Quote(inst)
	if !ok || now.Sub(time.UnixMilli(ts)) > 5*time.Minute {
		px = 0 // unknown stays unknown: the follow-up then has nothing to measure from
	}
	snap := map[string]any{}
	if vs, ok := s.mkt.Values(inst); ok {
		f := map[string]float64{}
		for k, v := range vs {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				f[k] = v
			}
		}
		snap["fields"] = f
	}
	if bpx, bts, ok := s.mkt.Quote("BTC-USDT-SWAP"); ok && now.Sub(time.UnixMilli(bts)) <= 5*time.Minute {
		snap["btcPx"] = bpx
	}
	raw, _ := json.Marshal(snap)
	runID, trigger := int64(0), ""
	strategy := ""
	if s.IsDeskChat(in.ChatID) {
		runID, trigger = s.currentRun()
		if runID > 0 {
			var keys []string
			for _, it := range s.inbox(`run_id=? AND inst_id=? ORDER BY id`, runID, inst) {
				if !slices.Contains(keys, it.Strategy) {
					keys = append(keys, it.Strategy)
				}
			}
			strategy = strings.Join(keys, ",")
		}
	}
	status := ""
	if in.ProposalID > 0 {
		p, err := s.trades.Get(in.ProposalID)
		if err != nil {
			return Decision{}, fmt.Errorf("proposal %d not found", in.ProposalID)
		}
		if p.InstID != inst {
			return Decision{}, fmt.Errorf("proposal %d is for %s, not %s", in.ProposalID, p.InstID, inst)
		}
		status = p.Status
	}
	res, err := s.db.Exec(`INSERT INTO desk_decisions(ts, run_id, chat_id, inst_id, action, side, conviction, thesis, entry, sl, tp, invalidation,
		tags, strategy, trigger, proposal_id, px, snapshot, proposal_status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now.UnixMilli(), runID, in.ChatID, inst, in.Action, in.Side, conv, in.Thesis, in.Entry, in.SL, in.TP, clip(strings.TrimSpace(in.Invalidation), 300),
		clip(strings.TrimSpace(in.Tags), 120), strategy, trigger, in.ProposalID, px, string(raw), status)
	if err != nil {
		return Decision{}, err
	}
	id, _ := res.LastInsertId()
	s.changed("journal")
	return s.Decision(id)
}

// Decision returns one journal row.
func (s *Service) Decision(id int64) (Decision, error) {
	d, err := scanDecision(s.db.QueryRow(`SELECT `+decisionCols+` FROM desk_decisions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, fmt.Errorf("decision %d not found", id)
	}
	return d, err
}

// JournalQuery filters the journal (newest first).
type JournalQuery struct {
	InstID   string
	Action   string // one action, or "trades" (open/add/reduce/close/adjust), "passes" (hold/watch/skip)
	Days     int
	RunID    int64
	Before   int64  // id
	Reviewed string // "yes" | "no"
	Limit    int
}

// Journal lists decisions.
func (s *Service) Journal(q JournalQuery) ([]Decision, error) {
	where, args := []string{"1=1"}, []any{}
	if q.InstID != "" {
		where = append(where, "inst_id=?")
		args = append(args, signals.NormalizeInst(q.InstID))
	}
	switch q.Action {
	case "":
	case "trades":
		where = append(where, "action IN ('open','add','reduce','close','adjust')")
	case "passes":
		where = append(where, "action IN ('hold','watch','skip')")
	default:
		if !slices.Contains(actions, q.Action) {
			return nil, fmt.Errorf("unknown action %q", q.Action)
		}
		where = append(where, "action=?")
		args = append(args, q.Action)
	}
	if q.Days > 0 {
		where = append(where, "ts>=?")
		args = append(args, s.now().Add(-time.Duration(q.Days)*24*time.Hour).UnixMilli())
	}
	if q.RunID > 0 {
		where = append(where, "run_id=?")
		args = append(args, q.RunID)
	}
	if q.Before > 0 {
		where = append(where, "id<?")
		args = append(args, q.Before)
	}
	switch q.Reviewed {
	case "yes":
		where = append(where, "reviewed_at>0")
	case "no":
		where = append(where, "reviewed_at=0")
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 50
	}
	rows, err := s.db.Query(`SELECT `+decisionCols+` FROM desk_decisions WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// reviewsDue are decisions whose outcome is in (24 h followed, or their position closed) and that
// have no review yet, oldest first; passes are only worth a review when the coin then moved.
func (s *Service) reviewsDue(limit int) []Decision {
	rows, err := s.db.Query(`SELECT `+decisionCols+` FROM desk_decisions WHERE reviewed_at=0 AND ts>=? AND (closed_at>0 OR
		(outcome_at>0 AND (action IN ('open','add','reduce','close','adjust') OR ABS(COALESCE(ret_24h,0))>=5)))
		ORDER BY id LIMIT ?`, s.now().Add(-14*24*time.Hour).UnixMilli(), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		if d, err := scanDecision(rows); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// Review stores the agent's (or the owner's) conclusion on a decision.
func (s *Service) Review(id int64, verdict, text string) (Decision, error) {
	verdict = strings.ToLower(strings.TrimSpace(verdict))
	if !slices.Contains(verdicts, verdict) {
		return Decision{}, fmt.Errorf("verdict must be one of %s", strings.Join(verdicts, ", "))
	}
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n < 6 || n > 800 {
		return Decision{}, errors.New("review is required: what happened and why the call was right or wrong (6–800 characters)")
	}
	res, err := s.db.Exec(`UPDATE desk_decisions SET review=?, verdict=?, reviewed_at=? WHERE id=?`, text, verdict, s.now().UnixMilli(), id)
	if err != nil {
		return Decision{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Decision{}, fmt.Errorf("decision %d not found", id)
	}
	s.changed("journal")
	return s.Decision(id)
}

// ---------- follow-up ----------

var horizons = []struct {
	col string
	d   time.Duration
}{{"1h", time.Hour}, {"4h", 4 * time.Hour}, {"24h", 24 * time.Hour}}

// follow fills in what happened after each decision: horizon prices as they come due, the 24 h
// excursions, and the linked proposal's status and result. Runs each minute; cheap when nothing is due.
func (s *Service) follow() {
	now := s.now()
	// a proposal is followed for 30 days: an order that never became a position has nothing more to report
	rows, err := s.db.Query(`SELECT `+decisionCols+` FROM desk_decisions WHERE (outcome_at=0 AND ts<=?) OR (proposal_id>0 AND closed_at=0 AND ts>=? AND
		proposal_status NOT IN ('rejected','expired','failed','invalidated')) ORDER BY id LIMIT 200`, now.Add(-time.Hour).UnixMilli(), now.Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return
	}
	var ds []Decision
	for rows.Next() {
		if d, err := scanDecision(rows); err == nil {
			ds = append(ds, d)
		}
	}
	rows.Close()
	changed := false
	for _, d := range ds {
		if d.OutcomeAt == 0 && s.followPrice(d, now) {
			changed = true
		}
		if d.ProposalID > 0 && d.ClosedAt == 0 && s.followProposal(d) {
			changed = true
		}
	}
	if changed {
		s.changed("journal")
	}
}

// signed is the percent change from px to v in the direction of side.
func signed(side string, px, v float64) float64 {
	r := (v/px - 1) * 100
	if side == "short" {
		r = -r
	}
	return math.Round(r*100) / 100
}

func (s *Service) followPrice(d Decision, now time.Time) bool {
	if !(d.Px > 0) { // nothing to measure from
		s.db.Exec(`UPDATE desk_decisions SET outcome_at=? WHERE id=?`, now.UnixMilli(), d.ID)
		return false
	}
	changed := false
	have := map[string]bool{"1h": d.Px1h != nil, "4h": d.Px4h != nil, "24h": d.Px24h != nil}
	for _, h := range horizons {
		at := d.Ts + h.d.Milliseconds()
		if have[h.col] || now.UnixMilli() < at+90_000 {
			continue
		}
		// the close of the minute that contains the horizon (a missing bar: the nearest within 5 minutes before)
		bars := s.mkt.Bars1m(d.InstID, at-5*60_000, at+60_000)
		if len(bars) == 0 {
			continue
		}
		px := bars[len(bars)-1].C
		s.db.Exec(`UPDATE desk_decisions SET px_`+h.col+`=?, ret_`+h.col+`=? WHERE id=?`, px, signed(d.Side, d.Px, px), d.ID)
		have[h.col], changed = true, true
	}
	end := d.Ts + 24*time.Hour.Milliseconds()
	if now.UnixMilli() < end+90_000 {
		return changed
	}
	if !have["24h"] && now.UnixMilli() < end+2*time.Hour.Milliseconds() {
		return changed // the bar may still arrive (backfill)
	}
	var mfe, mae sql.NullFloat64
	if bars := s.mkt.Bars1m(d.InstID, d.Ts/60_000*60_000, end); len(bars) > 0 {
		hi, lo := 0.0, math.Inf(1)
		for _, b := range bars {
			hi, lo = math.Max(hi, b.H), math.Min(lo, b.L)
		}
		up, down := signed("long", d.Px, hi), signed("long", d.Px, lo)
		if d.Side == "short" {
			up, down = -down, -up
		}
		mfe, mae = sql.NullFloat64{Float64: math.Max(up, 0), Valid: true}, sql.NullFloat64{Float64: math.Min(down, 0), Valid: true}
	}
	s.db.Exec(`UPDATE desk_decisions SET mfe_24h=?, mae_24h=?, outcome_at=? WHERE id=?`, mfe, mae, now.UnixMilli(), d.ID)
	return true
}

func (s *Service) followProposal(d Decision) bool {
	p, err := s.trades.Get(d.ProposalID)
	if err != nil {
		return false
	}
	// an action on a position (add, reduce, …) is settled with the position it belongs to
	closedAt, kind, pnl := p.ClosedAt, p.CloseKind, p.PnlUSDT
	if p.PosID > 0 && p.PosID != p.ID {
		if root, err := s.trades.Get(p.PosID); err == nil {
			closedAt, kind, pnl = root.ClosedAt, root.CloseKind, root.PnlUSDT
		}
	}
	if p.Status == d.ProposalStatus && closedAt == d.ClosedAt {
		return false
	}
	s.db.Exec(`UPDATE desk_decisions SET proposal_status=?, closed_at=?, close_kind=?, pnl_usdt=? WHERE id=?`, p.Status, closedAt, kind, pnl, d.ID)
	return true
}

// ---------- memory ----------

// Memory kinds.
var memoryKinds = []string{"rule", "lesson", "regime", "coin"}

var memoryKindText = map[string]string{"rule": "规则", "lesson": "教训", "regime": "市场状态", "coin": "币种"}

const (
	maxMemory     = 60
	MemoryActive  = "active"
	MemoryRetired = "retired"
)

// MemoryItem is one long-term memory of the desk.
type MemoryItem struct {
	ID             int64  `json:"id"`
	Ts             int64  `json:"ts"`
	Updated        int64  `json:"updated"`
	Kind           string `json:"kind"`
	InstID         string `json:"instId,omitempty"`
	Text           string `json:"text"`
	By             string `json:"by"` // ai | owner
	SourceDecision int64  `json:"sourceDecision,omitempty"`
	Status         string `json:"status"`
	RetiredReason  string `json:"retiredReason,omitempty"`
}

func (m MemoryItem) line() string {
	t := "[" + memoryKindText[m.Kind]
	if m.InstID != "" {
		t += " " + coin(m.InstID)
	}
	return fmt.Sprintf("%s #%d] %s", t, m.ID, m.Text)
}

const memoryCols = `id, ts, updated, kind, inst_id, text, by, source_decision, status, retired_reason`

func (s *Service) memory(where string, args ...any) []MemoryItem {
	rows, err := s.db.Query(`SELECT `+memoryCols+` FROM desk_memory WHERE `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []MemoryItem{}
	for rows.Next() {
		var m MemoryItem
		if rows.Scan(&m.ID, &m.Ts, &m.Updated, &m.Kind, &m.InstID, &m.Text, &m.By, &m.SourceDecision, &m.Status, &m.RetiredReason) == nil {
			out = append(out, m)
		}
	}
	return out
}

// Memory lists the desk's memory ("active", "retired" or "all").
func (s *Service) Memory(status string) []MemoryItem {
	if status == "" || status == "all" {
		return s.memory(`1=1 ORDER BY status, updated DESC LIMIT 300`)
	}
	return s.memory(`status=? ORDER BY updated DESC LIMIT 300`, status)
}

// memoryFor picks what a brief carries: the owner's and the agent's rules first, then what is about
// the coins in play, then the newest of the rest.
func (s *Service) memoryFor(insts []string, limit int) []MemoryItem {
	all := s.memory(`status=? ORDER BY updated DESC`, MemoryActive)
	rank := func(m MemoryItem) int {
		switch {
		case m.Kind == "rule":
			return 0
		case m.InstID != "" && slices.Contains(insts, m.InstID):
			return 1
		case m.Kind == "coin":
			return 3 // another coin's note: least relevant to this run
		}
		return 2
	}
	slices.SortStableFunc(all, func(a, b MemoryItem) int { return rank(a) - rank(b) })
	return all[:min(len(all), limit)]
}

// Remember adds a memory, or rewrites one (replaces > 0).
func (s *Service) Remember(kind, instID, text, by string, replaces, sourceDecision int64) (MemoryItem, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !slices.Contains(memoryKinds, kind) {
		return MemoryItem{}, fmt.Errorf("kind must be one of %s", strings.Join(memoryKinds, ", "))
	}
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n < 6 || n > 300 {
		return MemoryItem{}, errors.New("text is one reusable statement (6–300 characters)")
	}
	inst := ""
	if strings.TrimSpace(instID) != "" {
		inst = signals.NormalizeInst(instID)
		if !s.mkt.Known(inst) {
			return MemoryItem{}, fmt.Errorf("unknown instrument %q", instID)
		}
	}
	if kind == "coin" && inst == "" {
		return MemoryItem{}, errors.New("kind coin needs instId")
	}
	now := s.now().UnixMilli()
	if replaces > 0 {
		res, err := s.db.Exec(`UPDATE desk_memory SET kind=?, inst_id=?, text=?, updated=?, by=? WHERE id=? AND status=?`, kind, inst, text, now, by, replaces, MemoryActive)
		if err != nil {
			return MemoryItem{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return MemoryItem{}, fmt.Errorf("memory %d not found or retired", replaces)
		}
		s.changed("memory")
		return s.memory(`id=?`, replaces)[0], nil
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM desk_memory WHERE status=?`, MemoryActive).Scan(&n)
	if n >= maxMemory {
		return MemoryItem{}, fmt.Errorf("the desk keeps at most %d memories — rewrite one (replaces) or retire one first (desk_recall what=memory lists them)", maxMemory)
	}
	res, err := s.db.Exec(`INSERT INTO desk_memory(ts, updated, kind, inst_id, text, by, source_decision, status) VALUES(?,?,?,?,?,?,?,?)`,
		now, now, kind, inst, text, by, max(sourceDecision, 0), MemoryActive)
	if err != nil {
		return MemoryItem{}, err
	}
	id, _ := res.LastInsertId()
	s.changed("memory")
	return s.memory(`id=?`, id)[0], nil
}

// Retire takes a memory out of the briefs (kept for the record).
func (s *Service) Retire(id int64, reason string) error {
	res, err := s.db.Exec(`UPDATE desk_memory SET status=?, retired_reason=?, updated=? WHERE id=? AND status=?`, MemoryRetired, clip(strings.TrimSpace(reason), 200),
		s.now().UnixMilli(), id, MemoryActive)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("memory %d not found or already retired", id)
	}
	s.changed("memory")
	return nil
}

// ---------- statistics ----------

// Bucket is the record of a group of decisions.
type Bucket struct {
	Key       string   `json:"key"`
	N         int      `json:"n"`
	Followed  int      `json:"followed"`            // with a 4 h outcome
	WinRate4h *float64 `json:"winRate4h,omitempty"` // share with ret_4h > 0
	AvgRet1h  *float64 `json:"avgRet1h,omitempty"`
	AvgRet4h  *float64 `json:"avgRet4h,omitempty"`
	AvgRet24h *float64 `json:"avgRet24h,omitempty"`
}

// Stats summarise the journal over a period.
type Stats struct {
	Days      int     `json:"days"`
	Decisions int     `json:"decisions"`
	Runs      int     `json:"runs"`
	CostUSD   float64 `json:"costUsd"`
	Proposals struct {
		Made     int     `json:"made"`
		Executed int     `json:"executed"`
		Rejected int     `json:"rejected"`
		Expired  int     `json:"expired"`
		Closed   int     `json:"closed"`
		Wins     int     `json:"wins"`
		PnlUSDT  float64 `json:"pnlUsdt"`
	} `json:"proposals"`
	// Directional: decisions with a side (trades and passes alike), signed by that side.
	Directional  Bucket         `json:"directional"`
	ByAction     []Bucket       `json:"byAction"`
	ByConviction []Bucket       `json:"byConviction"`
	ByStrategy   []Bucket       `json:"byStrategy"`
	ByTrigger    []Bucket       `json:"byTrigger"`
	Verdicts     map[string]int `json:"verdicts"`
}

type acc struct {
	n, followed, wins int
	s1, s4, s24       float64
	n1, n24           int
}

func (a *acc) add(d Decision) {
	a.n++
	if d.Side == "" {
		return
	}
	if d.Ret1h != nil {
		a.s1 += *d.Ret1h
		a.n1++
	}
	if d.Ret4h != nil {
		a.followed++
		a.s4 += *d.Ret4h
		if *d.Ret4h > 0 {
			a.wins++
		}
	}
	if d.Ret24h != nil {
		a.s24 += *d.Ret24h
		a.n24++
	}
}

func (a *acc) bucket(key string) Bucket {
	b := Bucket{Key: key, N: a.n, Followed: a.followed}
	r := func(v float64) *float64 { v = math.Round(v*100) / 100; return &v }
	if a.followed > 0 {
		b.WinRate4h, b.AvgRet4h = r(float64(a.wins)/float64(a.followed)), r(a.s4/float64(a.followed))
	}
	if a.n1 > 0 {
		b.AvgRet1h = r(a.s1 / float64(a.n1))
	}
	if a.n24 > 0 {
		b.AvgRet24h = r(a.s24 / float64(a.n24))
	}
	return b
}

// Stats computes the desk's record over the last days (1–365).
func (s *Service) Stats(days int) (Stats, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	since := s.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	st := Stats{Days: days, Verdicts: map[string]int{}}
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost_usd),0) FROM desk_runs WHERE ts>=? AND status<>?`, since, RunFailed).Scan(&st.Runs, &st.CostUSD)
	rows, err := s.db.Query(`SELECT `+decisionCols+` FROM desk_decisions WHERE ts>=? ORDER BY id`, since)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	var dir acc
	groups := map[string]map[string]*acc{"action": {}, "conviction": {}, "strategy": {}, "trigger": {}}
	add := func(g, k string, d Decision) {
		if k == "" {
			k = "—"
		}
		if groups[g][k] == nil {
			groups[g][k] = &acc{}
		}
		groups[g][k].add(d)
	}
	seenProposal := map[int64]bool{}
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return st, err
		}
		st.Decisions++
		if d.Side != "" {
			dir.add(d)
		}
		add("action", d.Action, d)
		add("conviction", d.Conviction, d)
		add("trigger", d.Trigger, d)
		if d.Strategy == "" {
			add("strategy", "", d)
		}
		for _, k := range strings.Split(d.Strategy, ",") {
			if k != "" {
				add("strategy", k, d)
			}
		}
		if d.Verdict != "" {
			st.Verdicts[d.Verdict]++
		}
		if d.ProposalID > 0 && !seenProposal[d.ProposalID] {
			seenProposal[d.ProposalID] = true
			p := &st.Proposals
			p.Made++
			switch d.ProposalStatus {
			case realtrade.StatusExecuted:
				p.Executed++
			case realtrade.StatusRejected:
				p.Rejected++
			case realtrade.StatusExpired:
				p.Expired++
			}
			if d.ClosedAt > 0 && d.Action == "open" {
				p.Closed++
				p.PnlUSDT += d.PnlUSDT
				if d.PnlUSDT > 0 {
					p.Wins++
				}
			}
		}
	}
	st.Proposals.PnlUSDT = math.Round(st.Proposals.PnlUSDT*100) / 100
	st.CostUSD = math.Round(st.CostUSD*1000) / 1000
	st.Directional = dir.bucket("directional")
	flat := func(g string) []Bucket {
		out := make([]Bucket, 0, len(groups[g]))
		for k, a := range groups[g] {
			out = append(out, a.bucket(k))
		}
		slices.SortFunc(out, func(a, b Bucket) int {
			if a.N != b.N {
				return b.N - a.N
			}
			return strings.Compare(a.Key, b.Key)
		})
		return out
	}
	st.ByAction, st.ByConviction, st.ByStrategy, st.ByTrigger = flat("action"), flat("conviction"), flat("strategy"), flat("trigger")
	return st, rows.Err()
}

// ---------- runs ----------

// RunRow is one run of the desk.
type RunRow struct {
	ID         int64    `json:"id"`
	Ts         int64    `json:"ts"`
	Trigger    string   `json:"trigger"`
	Label      string   `json:"label"`
	ChatID     int64    `json:"chatId"`
	Status     string   `json:"status"`
	Error      string   `json:"error,omitempty"`
	Signals    int      `json:"signals"`
	InstIDs    []string `json:"instIds"`
	NewSession bool     `json:"newSession,omitempty"`
	FinishedAt int64    `json:"finishedAt,omitempty"`
	AnswerMsg  int64    `json:"answerMsg,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	CostUSD    float64  `json:"costUsd,omitempty"`
	Tokens     int64    `json:"tokens,omitempty"`
	LatencyMs  int64    `json:"latencyMs,omitempty"`
	Decisions  int      `json:"decisions"`
	Proposals  int      `json:"proposals"`
	afterMsg   int64
}

const runCols = `id, ts, trigger, label, chat_id, after_msg, status, error, signals, insts, new_session, finished_at, answer_msg, summary,
	cost_usd, tokens, latency_ms, decisions, proposals`

func scanRun(row interface{ Scan(...any) error }) (RunRow, error) {
	var r RunRow
	var insts string
	var ns int
	err := row.Scan(&r.ID, &r.Ts, &r.Trigger, &r.Label, &r.ChatID, &r.afterMsg, &r.Status, &r.Error, &r.Signals, &insts, &ns, &r.FinishedAt,
		&r.AnswerMsg, &r.Summary, &r.CostUSD, &r.Tokens, &r.LatencyMs, &r.Decisions, &r.Proposals)
	r.NewSession = ns == 1
	r.InstIDs = []string{}
	if insts != "" {
		r.InstIDs = strings.Split(insts, ",")
	}
	return r, err
}

func (s *Service) runWhere(where string, args ...any) (RunRow, error) {
	return scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM desk_runs WHERE `+where, args...))
}

func (s *Service) runByID(id int64) (RunRow, error) { return s.runWhere(`id=?`, id) }

// Runs lists the newest runs.
func (s *Service) Runs(limit int, before int64) ([]RunRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	q, args := `SELECT `+runCols+` FROM desk_runs`, []any{}
	if before > 0 {
		q += ` WHERE id<?`
		args = append(args, before)
	}
	rows, err := s.db.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunRow{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- the agent's tools (devapi, owner key only; chat = the X-Ctg-Chat of the call) ----------

func (s *Service) deskOnly(chat int64, tool string) error {
	if !s.IsDeskChat(chat) {
		return fmt.Errorf("%s works only in the trading desk conversation (交易台); here use desk_recall to read its journal", tool)
	}
	return nil
}

// JournalTool is desk_journal.
func (s *Service) JournalTool(_ context.Context, chat int64, a map[string]string) (any, error) {
	if err := s.deskOnly(chat, "desk_journal"); err != nil {
		return nil, err
	}
	f := func(k string) float64 { v, _ := strconv.ParseFloat(a[k], 64); return v }
	pid, _ := strconv.ParseInt(a["proposalId"], 10, 64)
	d, err := s.Record(JournalInput{ChatID: chat, InstID: a["instId"], Action: a["action"], Side: a["side"], Conviction: a["conviction"], Thesis: a["thesis"],
		Entry: f("entry"), SL: f("sl"), TP: f("tp"), Invalidation: a["invalidation"], Tags: a["tags"], ProposalID: pid})
	if err != nil {
		return nil, err
	}
	return map[string]any{"decisionId": d.ID, "recorded": d.line(), "note": "Stored with the price and fact-panel values now; the server follows the price " +
		"1 h / 4 h / 24 h later and the proposal's outcome. You will be asked to review it when the outcome is in."}, nil
}

// ReviewTool is desk_review.
func (s *Service) ReviewTool(_ context.Context, chat int64, a map[string]string) (any, error) {
	if err := s.deskOnly(chat, "desk_review"); err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(a["decisionId"], 10, 64)
	d, err := s.Review(id, a["verdict"], a["review"])
	if err != nil {
		return nil, err
	}
	return map[string]any{"decisionId": d.ID, "reviewed": d.line()}, nil
}

// RememberTool is desk_remember (text = add or rewrite; retire = take one out).
func (s *Service) RememberTool(_ context.Context, chat int64, a map[string]string) (any, error) {
	if err := s.deskOnly(chat, "desk_remember"); err != nil {
		return nil, err
	}
	if id, _ := strconv.ParseInt(a["retire"], 10, 64); id > 0 {
		if err := s.Retire(id, a["reason"]); err != nil {
			return nil, err
		}
		return map[string]any{"retired": id}, nil
	}
	rep, _ := strconv.ParseInt(a["replaces"], 10, 64)
	src, _ := strconv.ParseInt(a["decisionId"], 10, 64)
	m, err := s.Remember(a["kind"], a["instId"], a["text"], "ai", rep, src)
	if err != nil {
		return nil, err
	}
	return map[string]any{"memoryId": m.ID, "stored": m.line()}, nil
}

// RecallTool is desk_recall: the journal, the memory, the runs or the statistics (any conversation).
func (s *Service) RecallTool(_ context.Context, _ int64, a map[string]string) (any, error) {
	days, _ := strconv.Atoi(a["days"])
	limit, _ := strconv.Atoi(a["limit"])
	switch a["what"] {
	case "", "journal":
		if id, _ := strconv.ParseInt(a["decisionId"], 10, 64); id > 0 {
			return s.Decision(id)
		}
		if limit <= 0 || limit > 60 {
			limit = 20
		}
		ds, err := s.Journal(JournalQuery{InstID: a["instId"], Action: a["action"], Days: days, Reviewed: a["reviewed"], Limit: limit})
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]any, 0, len(ds))
		for _, d := range ds {
			r := map[string]any{"decisionId": d.ID, "line": d.line()}
			if d.Invalidation != "" {
				r["invalidation"] = d.Invalidation
			}
			if d.MFE24h != nil && d.MAE24h != nil {
				r["mfe24h"], r["mae24h"] = *d.MFE24h, *d.MAE24h
			}
			if d.Review != "" {
				r["review"] = d.Review
			}
			rows = append(rows, r)
		}
		return map[string]any{"decisions": rows, "note": "ret values are percent, signed by the decision's side; decisionId gives the full row with the market snapshot"}, nil
	case "memory":
		ms := s.Memory(MemoryActive)
		rows := make([]string, len(ms))
		for i, m := range ms {
			rows[i] = m.line()
		}
		return map[string]any{"memory": rows, "max": maxMemory}, nil
	case "stats":
		return s.Stats(days)
	case "runs":
		return s.Runs(limit, 0)
	}
	return nil, errors.New("what must be journal, memory, stats or runs")
}
