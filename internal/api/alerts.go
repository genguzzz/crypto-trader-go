package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"crypto-trader-go/internal/alerts"
)

// alertRoutes: monitoring rules, delivery channels (secrets masked) and history.
func (s *Server) alertRoutes(mux *http.ServeMux) {
	a := s.Alerts
	mux.HandleFunc("GET /api/v1/alerts/rules", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"rules": a.Rules(), "types": a.Types()})
	})
	save := func(w http.ResponseWriter, r *http.Request, id int64) {
		var in alerts.Rule
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			httpErr(w, 400, "invalid JSON: "+err.Error())
			return
		}
		in.ID = id
		out, err := a.SaveRule(in)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, out)
	}
	mux.HandleFunc("POST /api/v1/alerts/rules", func(w http.ResponseWriter, r *http.Request) { save(w, r, 0) })
	mux.HandleFunc("PUT /api/v1/alerts/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			httpErr(w, 400, "invalid id")
			return
		}
		save(w, r, id)
	})
	mux.HandleFunc("DELETE /api/v1/alerts/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err == nil {
			err = a.DeleteRule(id)
		}
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/alerts/channels", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.ChannelView()) })
	mux.HandleFunc("PUT /api/v1/alerts/channels", func(w http.ResponseWriter, r *http.Request) {
		var in alerts.ChannelView
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
			httpErr(w, 400, "invalid JSON")
			return
		}
		out, err := a.SaveChannels(in)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("DELETE /api/v1/alerts/channels/{ch}", func(w http.ResponseWriter, r *http.Request) {
		out, err := a.ClearChannel(r.PathValue("ch"))
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("POST /api/v1/alerts/channels/{ch}/test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		if err := a.Test(ctx, r.PathValue("ch")); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/alerts/history", func(w http.ResponseWriter, r *http.Request) {
		evs, err := a.History(atoiDefault(r.URL.Query().Get("limit"), 100))
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		writeJSON(w, evs)
	})
}
