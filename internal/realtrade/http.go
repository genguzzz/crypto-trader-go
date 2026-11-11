package realtrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// Register mounts the owner-only routes (the owner key is required for every method, reads included;
// see api/admin.go ownerReadPaths):
//
//	GET  /api/v1/realtrade/proposals?status=pending|open|all&chatId=&limit=
//	GET  /api/v1/realtrade/proposals/{id}            with the audit trail
//	POST /api/v1/realtrade/proposals/{id}/approve    {confirm: true, via: web|app} → final state (executed / failed / invalidated)
//	POST /api/v1/realtrade/proposals/{id}/reject     {via}
//	GET  /api/v1/realtrade/config                    mode, limits, Telegram bot status, triggers
//	PUT  /api/v1/realtrade/triggers                  [{id, enabled, everyMin, ruleIds, cooldownMin, prompt}]
//
// There is no route that changes the mode or the limits: only CTG_TRADE_MODE / CTG_TRADE_* can.
func (s *Service) Register(mux *http.ServeMux) {
	// Public on purpose and data-free: only the id, which the app then loads with the owner key.
	mux.HandleFunc("GET /go/proposal/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		app := fmt.Sprintf("okxhelper://proposal/%d", id)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`+
			`<title>交易提议 #%d</title><body style="background:#0b0b0b;color:#eee;font:16px -apple-system,sans-serif;text-align:center;padding:30vh 20px">`+
			`<p>正在打开 OKX Helper…</p><p><a style="color:#bcff2f" href="%s">没有自动跳转？点这里在 App 中查看 #%d</a></p>`+
			`<script>location.href=%q</script>`, id, app, id, app)
	})
	mux.HandleFunc("GET /api/v1/realtrade/proposals", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		chat, _ := strconv.ParseInt(q.Get("chatId"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		st := q.Get("status")
		if st != "" && st != "all" && st != "open" && statusText[st] == "" {
			httpErr(w, http.StatusBadRequest, "unknown status")
			return
		}
		ps, err := s.List(ListQuery{Status: st, ChatID: chat, Limit: limit})
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"proposals": ps, "now": s.now().UnixMilli(), "mode": s.Mode()})
	})
	mux.HandleFunc("GET /api/v1/realtrade/proposals/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		p, err := s.Get(id)
		if err != nil {
			httpErr(w, errStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	answer := func(approve bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Confirm bool   `json:"confirm"`
				Via     string `json:"via"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
				httpErr(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if in.Via != "app" {
				in.Via = "web"
			}
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			var p Proposal
			var err error
			if approve {
				if !in.Confirm {
					httpErr(w, http.StatusBadRequest, "approval needs confirm: true")
					return
				}
				p, err = s.Approve(r.Context(), id, "owner", in.Via)
			} else {
				p, err = s.Reject(id, "owner", in.Via)
			}
			switch {
			case err == nil:
				writeJSON(w, http.StatusOK, p)
			case errors.Is(err, ErrClosed):
				writeJSON(w, http.StatusConflict, map[string]any{"error": "这笔提议已结束：" + statusText[p.Status], "proposal": p})
			default:
				httpErr(w, errStatus(err), err.Error())
			}
		}
	}
	mux.HandleFunc("POST /api/v1/realtrade/proposals/{id}/approve", answer(true))
	mux.HandleFunc("POST /api/v1/realtrade/proposals/{id}/reject", answer(false))
	mux.HandleFunc("GET /api/v1/realtrade/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Config())
	})
	mux.HandleFunc("PUT /api/v1/realtrade/triggers", func(w http.ResponseWriter, r *http.Request) {
		var in []TriggerDef
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if _, err := s.SaveTriggers(in); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.Config())
	})
}

// ConfigView is GET /api/v1/realtrade/config.
type ConfigView struct {
	Mode     string       `json:"mode"`
	Limits   Limits       `json:"limits"`
	Telegram BotStatus    `json:"telegram"`
	Triggers []TriggerDef `json:"triggers"`
	Account  bool         `json:"account"` // the OKX account is readable (position checks, live close detection)
}

// Config is the read-only view of how real trading is set up.
func (s *Service) Config() ConfigView {
	s.mu.Lock()
	b := s.bot
	s.mu.Unlock()
	return ConfigView{Mode: string(s.Mode()), Limits: s.opts.Limits, Telegram: b.Status(), Triggers: s.Triggers(), Account: s.account() != nil}
}

func errStatus(err error) int {
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
