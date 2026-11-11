package autopilot

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/aigate"
	"crypto-trader-go/internal/signals"
)

// StrategyView is a strategy as the app shows it: its definition, the owner's setting and its scans.
type StrategyView struct {
	Key     string             `json:"key"`
	Label   string             `json:"label"`
	Desc    string             `json:"desc"`
	Custom  bool               `json:"custom,omitempty"`
	Enabled bool               `json:"enabled"`
	Params  map[string]float64 `json:"params,omitempty"`
	Defs    []ParamDef         `json:"paramDefs,omitempty"`
	Scans   []ScanView         `json:"scans"`
	Error   string             `json:"error,omitempty"` // why a scan is not running
	Fires24 int                `json:"fires24h"`        // coins it put in the inbox in the last 24 h
}

// ScanView is one running subscription of a strategy.
type ScanView struct {
	SignalID    int64  `json:"signalId"`
	Desc        string `json:"desc"`
	CooldownMin int    `json:"cooldownMin"`
	FireCount   int    `json:"fireCount"`
	LastFiredAt int64  `json:"lastFiredAt,omitempty"`
	Progress    string `json:"progress,omitempty"`
}

// Status is the desk at a glance.
type Status struct {
	Enabled        bool    `json:"enabled"`
	ChatID         int64   `json:"chatId"`
	Running        *RunRow `json:"running,omitempty"`
	LastRun        *RunRow `json:"lastRun,omitempty"`
	NextScheduleAt int64   `json:"nextScheduleAt,omitempty"`
	Pending        int     `json:"pending"` // inbox items waiting for a run
	Runs24h        int     `json:"runs24h"`
	SignalRuns24h  int     `json:"signalRuns24h"`
	Cost24hUSD     float64 `json:"cost24hUsd"`
	Decisions24h   int     `json:"decisions24h"`
	TradeMode      string  `json:"tradeMode,omitempty"`
	LastError      string  `json:"lastError,omitempty"` // why the last run could not start
	Now            int64   `json:"now"`
}

// Status reports the desk's state.
func (s *Service) Status() Status {
	now := s.now()
	cfg := s.Config()
	st := Status{Enabled: cfg.Enabled, ChatID: s.ChatID(), Now: now.UnixMilli(), SignalRuns24h: s.signalRuns(now)}
	st.Running = s.running()
	if r, err := s.runWhere(`status<>? ORDER BY id DESC LIMIT 1`, RunRunning); err == nil {
		st.LastRun = &r
	}
	if t := s.NextSchedule(); !t.IsZero() {
		st.NextScheduleAt = t.UnixMilli()
	}
	day := now.Add(-24 * time.Hour).UnixMilli()
	s.db.QueryRow(`SELECT COUNT(*) FROM desk_inbox WHERE status=?`, InboxPending).Scan(&st.Pending)
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost_usd),0) FROM desk_runs WHERE ts>=? AND status<>?`, day, RunFailed).Scan(&st.Runs24h, &st.Cost24hUSD)
	s.db.QueryRow(`SELECT COUNT(*) FROM desk_decisions WHERE ts>=?`, day).Scan(&st.Decisions24h)
	if s.opts.TradeMode != nil {
		st.TradeMode = s.opts.TradeMode()
	}
	s.mu.Lock()
	st.LastError = s.lastError
	s.mu.Unlock()
	return st
}

// StrategyViews lists every strategy with its scans.
func (s *Service) StrategyViews() []StrategyView {
	cfg := s.Config()
	subs, _ := s.scans.DeskSubs()
	s.mu.Lock()
	errs := map[string]string{}
	for k, v := range s.stratErr {
		errs[k] = v
	}
	s.mu.Unlock()
	fires := map[string]int{}
	if rows, err := s.db.Query(`SELECT strategy, COUNT(*) FROM desk_inbox WHERE ts>=? GROUP BY strategy`, s.now().Add(-24*time.Hour).UnixMilli()); err == nil {
		for rows.Next() {
			var k string
			var n int
			rows.Scan(&k, &n)
			fires[k] = n
		}
		rows.Close()
	}
	fill := func(v *StrategyView, keys ...string) {
		v.Scans = []ScanView{}
		for _, k := range keys {
			v.Fires24 += fires[k]
			if e := errs[k]; e != "" && v.Error == "" {
				v.Error = e
			}
			sub, ok := subs[k]
			if !ok {
				continue
			}
			sc := ScanView{SignalID: sub.ID, Desc: sub.Desc, CooldownMin: sub.CooldownMin, FireCount: sub.FireCount, LastFiredAt: sub.LastFiredAt}
			if full, err := s.scans.Get(sub.ID); err == nil && full.Progress != nil {
				sc.Progress = full.Progress.Text
			}
			v.Scans = append(v.Scans, sc)
		}
	}
	out := []StrategyView{}
	for _, sc := range cfg.Strategies {
		d, ok := strategyDef(sc.Key)
		if !ok {
			continue
		}
		v := StrategyView{Key: d.Key, Label: d.Label, Desc: d.Desc, Enabled: sc.Enabled, Params: sc.Params, Defs: d.Params}
		var keys []string
		for _, sd := range d.build(sc.Params) {
			k := d.Key
			if sd.suffix != "" {
				k += ":" + sd.suffix
			}
			keys = append(keys, k)
		}
		fill(&v, keys...)
		out = append(out, v)
	}
	for _, x := range cfg.Custom {
		v := StrategyView{Key: x.Key, Label: x.Label, Custom: true, Enabled: x.Enabled}
		if spec, err := signals.ParseSpec(string(x.Spec)); err == nil {
			v.Desc = spec.Label()
		}
		fill(&v, x.Key)
		out = append(out, v)
	}
	return out
}

// Register mounts the owner routes (the owner key is required under /api/v1):
//
//	GET    /api/v1/autopilot                 {status, config, strategies, options}
//	PUT    /api/v1/autopilot/config          Config → the same as GET
//	POST   /api/v1/autopilot/run             {note?} → the run (the desk's chat streams it)
//	GET    /api/v1/autopilot/runs?limit&before
//	GET    /api/v1/autopilot/inbox?status&limit
//	GET    /api/v1/autopilot/journal?instId&action&days&runId&reviewed&before&limit
//	GET    /api/v1/autopilot/journal/{id}
//	POST   /api/v1/autopilot/journal/{id}/review   {verdict, review} (the owner's own conclusion)
//	GET    /api/v1/autopilot/memory?status=active|retired|all
//	POST   /api/v1/autopilot/memory          {kind, instId?, text, replaces?}
//	DELETE /api/v1/autopilot/memory/{id}     retire (?reason=)
//	GET    /api/v1/autopilot/stats?days=
//
// Changes are pushed as the stream event "autopilot" {kind}.
func (s *Service) Register(mux *http.ServeMux) {
	overview := func(w http.ResponseWriter) {
		writeJSON(w, http.StatusOK, map[string]any{"status": s.Status(), "config": s.Config(), "strategies": s.StrategyViews(),
			"options": map[string]any{"everyHours": everyHours, "models": aigate.ChatModels, "efforts": aigate.ChatEfforts,
				"actions": actions, "verdicts": verdicts, "memoryKinds": memoryKinds}})
	}
	mux.HandleFunc("GET /api/v1/autopilot", func(w http.ResponseWriter, r *http.Request) { overview(w) })
	mux.HandleFunc("PUT /api/v1/autopilot/config", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid config: "+err.Error())
			return
		}
		if _, err := s.SetConfig(c); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		overview(w)
	})
	mux.HandleFunc("POST /api/v1/autopilot/run", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in)
		run, err := s.RunNow(r.Context(), strings.TrimSpace(in.Note))
		switch {
		case errors.Is(err, ErrDisabled), errors.Is(err, ErrRunning):
			httpErr(w, http.StatusConflict, err.Error())
		case err != nil: // the agent is down or the chat cannot take it now
			httpErr(w, http.StatusServiceUnavailable, err.Error())
		default:
			writeJSON(w, http.StatusOK, run)
		}
	})
	mux.HandleFunc("GET /api/v1/autopilot/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		runs, err := s.Runs(atoi(q.Get("limit")), atoi64(q.Get("before")))
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
	})
	mux.HandleFunc("GET /api/v1/autopilot/inbox", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := atoi(q.Get("limit"))
		if limit <= 0 || limit > 300 {
			limit = 100
		}
		var items []InboxItem
		if st := q.Get("status"); st != "" {
			items = s.inbox(`status=? ORDER BY id DESC LIMIT ?`, st, limit)
		} else {
			items = s.inbox(`1=1 ORDER BY id DESC LIMIT ?`, limit)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("GET /api/v1/autopilot/journal", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ds, err := s.Journal(JournalQuery{InstID: q.Get("instId"), Action: q.Get("action"), Days: atoi(q.Get("days")), RunID: atoi64(q.Get("runId")),
			Before: atoi64(q.Get("before")), Reviewed: q.Get("reviewed"), Limit: atoi(q.Get("limit"))})
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"decisions": ds})
	})
	mux.HandleFunc("GET /api/v1/autopilot/journal/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := s.Decision(atoi64(r.PathValue("id")))
		if err != nil {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("POST /api/v1/autopilot/journal/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Verdict, Review string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		d, err := s.Review(atoi64(r.PathValue("id")), in.Verdict, in.Review)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("GET /api/v1/autopilot/memory", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"memory": s.Memory(r.URL.Query().Get("status")), "max": maxMemory})
	})
	mux.HandleFunc("POST /api/v1/autopilot/memory", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind, InstID, Text string
			Replaces           int64
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		m, err := s.Remember(in.Kind, in.InstID, in.Text, "owner", in.Replaces, 0)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m)
	})
	mux.HandleFunc("DELETE /api/v1/autopilot/memory/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Retire(atoi64(r.PathValue("id")), r.URL.Query().Get("reason")); err != nil {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/autopilot/stats", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Stats(atoi(r.URL.Query().Get("days")))
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
}

func atoi(s string) int     { v, _ := strconv.Atoi(s); return v }
func atoi64(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
