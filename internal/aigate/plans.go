package aigate

// AI trade plans: every ```plan block in a chat answer is stored and then followed on 1m candles, so
// the K-line can draw the levels while the idea is alive and drop them once it is settled.
//
//	pending  → entry zone touched            → active
//	pending  → stop crossed first             → invalid   (作废: the idea broke before a fill)
//	pending  → TP1 reached first              → missed    (错过: it ran without us)
//	active   → TP1                            → tp1       (still open towards TP2)
//	active / tp1 → stop                       → stopped
//	active / tp1 → TP2                        → done
//	any open → past its holding time          → expired
//	any open → a newer plan for the same coin → replaced
//	any open → the owner dismisses it         → dismissed
//
// Within one 1m candle that touches both the stop and a target, the stop counts first (conservative).

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const planSchema = `
CREATE TABLE IF NOT EXISTS ai_plans(id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, created INTEGER NOT NULL,
  inst_id TEXT NOT NULL, side TEXT NOT NULL, entry_lo REAL NOT NULL, entry_hi REAL NOT NULL, stop REAL NOT NULL,
  tp1 REAL NOT NULL, tp2 REAL NOT NULL DEFAULT 0, leverage REAL NOT NULL DEFAULT 0, hold TEXT NOT NULL DEFAULT '',
  invalidate TEXT NOT NULL DEFAULT '', confidence TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending', status_at INTEGER NOT NULL DEFAULT 0, status_px REAL NOT NULL DEFAULT 0,
  entered_at INTEGER NOT NULL DEFAULT 0, checked_to INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS ai_plans_inst ON ai_plans(inst_id, status);
`

// Plan is one stored trade idea and where it stands.
type Plan struct {
	ID         int64   `json:"id"`
	ChatID     int64   `json:"chatId"`
	Created    int64   `json:"created"`
	InstID     string  `json:"instId"`
	Side       string  `json:"side"` // long | short
	EntryLo    float64 `json:"entryLo"`
	EntryHi    float64 `json:"entryHi"`
	Stop       float64 `json:"stop"`
	TP1        float64 `json:"tp1"`
	TP2        float64 `json:"tp2,omitempty"`
	Leverage   float64 `json:"leverage,omitempty"`
	Hold       string  `json:"hold,omitempty"`
	Invalidate string  `json:"invalidate,omitempty"`
	Confidence string  `json:"confidence,omitempty"`
	Expires    int64   `json:"expires"`
	Status     string  `json:"status"`
	StatusAt   int64   `json:"statusAt,omitempty"`
	StatusPx   float64 `json:"statusPx,omitempty"`
	EnteredAt  int64   `json:"enteredAt,omitempty"`
}

func isOpen(s string) bool { return s == "pending" || s == "active" || s == "tp1" }

// Candles1m returns recent confirmed-or-forming 1m candles for an instrument, oldest first.
type Candles1m func(instID string, limit int) ([]Bar, error)

// Bar is one candle as the tracker needs it.
type Bar struct {
	T       int64
	H, L, C float64
}

// SetCandles gives the plan tracker its price source (the market engine's 1m candles).
func (g *Gate) SetCandles(f Candles1m) {
	g.mu.Lock()
	g.candles = f
	g.mu.Unlock()
}

var planBlock = regexp.MustCompile("(?s)```plan\\s*\\n(.*?)\\n?```")

// savePlans stores the plan blocks of a finished answer; a new plan replaces the open ones for its coin.
func (g *Gate) savePlans(chatID int64, text, confidence string) {
	now := g.now().UnixMilli()
	for _, m := range planBlock.FindAllStringSubmatch(text, -1) {
		var raw struct {
			InstID     string    `json:"instId"`
			Side       string    `json:"side"`
			Entry      []float64 `json:"entry"`
			Stop       float64   `json:"stop"`
			TP         []float64 `json:"tp"`
			Leverage   float64   `json:"leverage"`
			Hold       string    `json:"hold"`
			Invalidate string    `json:"invalidate"`
		}
		if json.Unmarshal([]byte(m[1]), &raw) != nil || raw.InstID == "" || len(raw.Entry) == 0 || raw.Stop <= 0 || len(raw.TP) == 0 {
			continue
		}
		if raw.Side != "long" && raw.Side != "short" {
			continue
		}
		lo, hi := raw.Entry[0], raw.Entry[len(raw.Entry)-1]
		if lo > hi {
			lo, hi = hi, lo
		}
		tp2 := 0.0
		if len(raw.TP) > 1 {
			tp2 = raw.TP[1]
		}
		inst := strings.ToUpper(raw.InstID)
		g.st.DB.Exec(`UPDATE ai_plans SET status='replaced', status_at=? WHERE inst_id=? AND status IN ('pending','active','tp1')`, now, inst)
		g.st.DB.Exec(`INSERT INTO ai_plans(chat_id, created, inst_id, side, entry_lo, entry_hi, stop, tp1, tp2, leverage, hold, invalidate,
			confidence, expires, checked_to) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			chatID, now, inst, raw.Side, lo, hi, raw.Stop, raw.TP[0], tp2, raw.Leverage, raw.Hold, raw.Invalidate,
			confidence, now+holdMs(raw.Hold), now)
	}
}

var verdictConf = regexp.MustCompile("(?s)```verdict\\s*\\n.*?\"confidence\"\\s*:\\s*\"([^\"]+)\"")

func confidenceOf(text string) string {
	if m := verdictConf.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

var holdNum = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:[-–~至到]\s*(\d+(?:\.\d+)?))?\s*(分钟|min|小时|h|天|日|d|周|w)`)

// holdMs turns the plan's holding time ("1–3 天", "4–8 小时", "日内") into how long it stays on the chart:
// the upper end of the range, clamped to 2 h … 14 d; 3 d when it can't be read.
func holdMs(hold string) int64 {
	const h = int64(time.Hour / time.Millisecond)
	d := 72 * h
	if m := holdNum.FindStringSubmatch(strings.ToLower(hold)); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		if m[2] != "" {
			n, _ = strconv.ParseFloat(m[2], 64)
		}
		unit := map[string]int64{"分钟": h / 60, "min": h / 60, "小时": h, "h": h, "天": 24 * h, "日": 24 * h, "d": 24 * h, "周": 168 * h, "w": 168 * h}[m[3]]
		d = int64(n * float64(unit))
	} else if strings.Contains(hold, "日内") || strings.Contains(hold, "数小时") {
		d = 24 * h
	}
	return max(2*h, min(d, 14*24*h))
}

// step advances one plan through one candle; it reports whether anything changed.
func (p *Plan) step(b Bar) bool {
	long := p.Side == "long"
	hitStop := (long && b.L <= p.Stop) || (!long && b.H >= p.Stop)
	hit := func(px float64) bool { return px > 0 && ((long && b.H >= px) || (!long && b.L <= px)) }
	set := func(s string, px float64) bool { p.Status, p.StatusAt, p.StatusPx = s, b.T, px; return true }
	switch p.Status {
	case "pending":
		inZone := b.L <= p.EntryHi && b.H >= p.EntryLo
		switch {
		case hitStop && !inZone:
			return set("invalid", p.Stop)
		case inZone:
			p.EnteredAt = b.T
			px := p.EntryHi
			if long {
				px = p.EntryLo
			}
			if hitStop { // filled and stopped in the same minute
				return set("stopped", p.Stop)
			}
			set("active", px)
			if hit(p.TP1) {
				return set("tp1", p.TP1)
			}
			return true
		case hit(p.TP1):
			return set("missed", p.TP1)
		}
	case "active", "tp1":
		switch {
		case hitStop:
			return set("stopped", p.Stop)
		case hit(p.TP2):
			return set("done", p.TP2)
		case p.Status == "active" && hit(p.TP1):
			if p.TP2 <= 0 {
				return set("done", p.TP1)
			}
			return set("tp1", p.TP1)
		}
	}
	return false
}

// trackPlans walks every open plan over the 1m candles since it was last checked.
func (g *Gate) trackPlans() {
	g.mu.Lock()
	f := g.candles
	g.mu.Unlock()
	if f == nil {
		return
	}
	now := g.now().UnixMilli()
	g.st.DB.Exec(`UPDATE ai_plans SET status='expired', status_at=? WHERE status IN ('pending','active','tp1') AND expires < ?`, now, now)
	plans, checked := g.loadPlans(`status IN ('pending','active','tp1')`)
	for i := range plans {
		p := &plans[i]
		need := int((now-checked[p.ID])/60_000) + 2
		bars, err := f(p.InstID, max(2, min(need, 1440)))
		if err != nil || len(bars) == 0 {
			continue
		}
		upTo := checked[p.ID]
		changed := false
		for _, b := range bars {
			if b.T+60_000 <= checked[p.ID] { // fully seen already
				continue
			}
			if p.step(b) {
				changed = true
			}
			upTo = b.T
			if !isOpen(p.Status) {
				break
			}
		}
		if changed {
			g.st.DB.Exec(`UPDATE ai_plans SET status=?, status_at=?, status_px=?, entered_at=?, checked_to=? WHERE id=?`,
				p.Status, p.StatusAt, p.StatusPx, p.EnteredAt, upTo, p.ID)
		} else {
			g.st.DB.Exec(`UPDATE ai_plans SET checked_to=? WHERE id=?`, upTo, p.ID)
		}
	}
}

func (g *Gate) loadPlans(where string, args ...any) ([]Plan, map[int64]int64) {
	rows, err := g.st.DB.Query(`SELECT id, chat_id, created, inst_id, side, entry_lo, entry_hi, stop, tp1, tp2, leverage, hold, invalidate,
		confidence, expires, status, status_at, status_px, entered_at, checked_to FROM ai_plans WHERE `+where+` ORDER BY id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	var out []Plan
	checked := map[int64]int64{}
	for rows.Next() {
		var p Plan
		var c int64
		if rows.Scan(&p.ID, &p.ChatID, &p.Created, &p.InstID, &p.Side, &p.EntryLo, &p.EntryHi, &p.Stop, &p.TP1, &p.TP2, &p.Leverage,
			&p.Hold, &p.Invalidate, &p.Confidence, &p.Expires, &p.Status, &p.StatusAt, &p.StatusPx, &p.EnteredAt, &c) == nil {
			out = append(out, p)
			checked[p.ID] = c
		}
	}
	return out, checked
}

func (g *Gate) planLoop(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		g.trackPlans()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (g *Gate) planRoutes(mux *http.ServeMux) {
	// open plans for a coin (the chart), or the latest plans of all coins; ?recent=<hours> adds settled ones
	mux.HandleFunc("GET /api/v1/ai/plans", func(w http.ResponseWriter, r *http.Request) {
		where, args := "1=1", []any{}
		if inst := strings.ToUpper(r.URL.Query().Get("instId")); inst != "" {
			where, args = "inst_id=?", append(args, inst)
		}
		if h, err := strconv.ParseFloat(r.URL.Query().Get("recent"), 64); err == nil && h > 0 {
			where += " AND (status IN ('pending','active','tp1') OR status_at > ?)"
			args = append(args, g.now().UnixMilli()-int64(h*3600_000))
		} else if r.URL.Query().Get("all") != "1" {
			where += " AND status IN ('pending','active','tp1')"
		}
		ps, _ := g.loadPlans(where, args...)
		if ps == nil {
			ps = []Plan{}
		}
		writeJSON(w, 200, ps)
	})
	mux.HandleFunc("DELETE /api/v1/ai/plans/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad id"})
			return
		}
		g.st.DB.Exec(`UPDATE ai_plans SET status='dismissed', status_at=? WHERE id=? AND status IN ('pending','active','tp1')`, g.now().UnixMilli(), id)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}
