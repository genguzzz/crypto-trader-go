package interact

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"crypto-trader-go/internal/trade"
)

// WebChannel serves prompts to the web UI. Open prompts live in interact_prompts and are listed
// over REST; answers posted from the page resolve them like any other channel. Changes are signalled
// through the hub's OnChange hook (forward it to SSE). It does not show notifications: the trading
// pages already refresh from their own SSE events.
type WebChannel struct{ h *Hub }

func (w *WebChannel) Name() string                              { return "web" }
func (w *WebChannel) Notify(context.Context, trade.Event) error { return nil }
func (w *WebChannel) Ask(context.Context, trade.Prompt) error   { w.h.changed(); return nil }
func (w *WebChannel) Resolve(context.Context, string, string, string) error {
	w.h.changed()
	return nil
}

// Run has nothing to receive: answers arrive through Hub.Register's POST handler.
func (w *WebChannel) Run(ctx context.Context, _ chan<- Answer) { <-ctx.Done() }

// ---------- REST ----------

// Register mounts the interaction API on mux:
//
//	GET  /api/v1/interact/config           masked config + bot status
//	PUT  /api/v1/interact/config           masked/empty token = unchanged
//	POST /api/v1/interact/test             test message + test prompt
//	GET  /api/v1/interact/prompts?state=open|all&limit=
//	POST /api/v1/interact/prompts/{id}     {choice}
func (h *Hub) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/interact/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, h.Config())
	})
	mux.HandleFunc("PUT /api/v1/interact/config", func(w http.ResponseWriter, r *http.Request) {
		var in ConfigView
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		out, err := h.SaveConfig(in)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/v1/interact/test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		res, id, err := h.Test(ctx)
		if err != nil {
			httpErr(w, http.StatusConflict, err.Error())
			return
		}
		ok, some := true, false // every enabled channel delivered, and there was one
		for _, v := range res {
			if v != "未启用" {
				ok, some = ok && v == "ok", true
			}
		}
		ok = ok && some
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "channels": res, "promptId": id})
	})
	mux.HandleFunc("GET /api/v1/interact/prompts", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state == "" {
			state = StateOpen
		}
		if state != StateOpen && state != "all" {
			httpErr(w, http.StatusBadRequest, "state must be open or all")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		out, err := h.Prompts(state, limit)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"prompts": out, "now": time.Now().UnixMilli()})
	})
	mux.HandleFunc("POST /api/v1/interact/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Choice string `json:"choice"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		rec, err := h.Answer(r.PathValue("id"), in.Choice, byWeb)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, rec)
		case errors.Is(err, ErrNotFound):
			httpErr(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBadChoice):
			httpErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrClosed):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "prompt": rec})
		default:
			httpErr(w, http.StatusInternalServerError, err.Error())
		}
	})
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
