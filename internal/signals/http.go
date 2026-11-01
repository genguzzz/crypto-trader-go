package signals

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Register mounts the owner routes (the owner key is required under /api/v1, reads included):
//
//	GET  /api/v1/signals?status=active|fired|ended|all&instId=&chatId=&kind=chat|owner&limit=   {signals, caps, now}
//	GET  /api/v1/signals/{id}           with the fire history
//	POST /api/v1/signals                {spec, code?, intent, expiresHours?, maxFires?, cooldownMin?, planId?, every?} — the owner's own (wakes a new chat)
//	POST /api/v1/signals/{id}/cancel    {reason?}
//	POST /api/v1/signals/test-script    {code} → ScriptTest (compile + replay; nothing is stored)
//	GET  /api/v1/signals/script-template {code}
//	GET  /go/chat/{id}                  public, data-free bounce to okxhelper://ai?chat=<id> (Telegram buttons take http(s) only)
//
// Changes are pushed as the stream event "signals" {kind, id, chatId}.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /go/chat/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		app := fmt.Sprintf("okxhelper://ai?chat=%d", id)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`+
			`<title>AI 对话 #%d</title><body style="background:#0b0b0b;color:#eee;font:16px -apple-system,sans-serif;text-align:center;padding:30vh 20px">`+
			`<p>正在打开 OKX Helper…</p><p><a style="color:#bcff2f" href="%s">没有自动跳转？点这里在 App 中打开对话 #%d</a></p>`+
			`<script>location.href=%q</script>`, id, app, id, app)
	})
	mux.HandleFunc("GET /api/v1/signals", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		chat, _ := strconv.ParseInt(q.Get("chatId"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		subs, err := s.List(ListQuery{Status: q.Get("status"), InstID: q.Get("instId"), ChatID: chat, Kind: q.Get("kind"), Limit: limit})
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"signals": subs, "caps": s.Caps(), "now": s.now().UnixMilli()})
	})
	mux.HandleFunc("GET /api/v1/signals/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		sub, err := s.Get(id)
		if err != nil {
			httpErr(w, errStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sub)
	})
	mux.HandleFunc("GET /api/v1/signals/script-template", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": ScriptTemplate, "limits": s.limits()})
	})
	mux.HandleFunc("POST /api/v1/signals/test-script", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		t, err := s.TestScript(r.Context(), in.Code)
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, ErrNoScripts) || strings.HasPrefix(err.Error(), "script runner") {
				code = http.StatusServiceUnavailable
			}
			httpErr(w, code, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, t)
	})
	mux.HandleFunc("POST /api/v1/signals", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code         string          `json:"code"`
			Spec         json.RawMessage `json:"spec"`
			Intent       string          `json:"intent"`
			ExpiresHours float64         `json:"expiresHours"`
			MaxFires     int             `json:"maxFires"`
			CooldownMin  int             `json:"cooldownMin"`
			PlanID       int64           `json:"planId"`
			Every        string          `json:"every"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		raw := string(in.Spec)
		if (raw == "" || raw == "null") && in.Code != "" {
			raw = `{"type":"script"}`
		}
		spec, err := ParseSpec(raw)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		spec.fillCode(in.Code)
		sub, err := s.SubscribeContext(r.Context(), SubscribeInput{Spec: spec, Intent: in.Intent, ExpiresHours: in.ExpiresHours, MaxFires: in.MaxFires,
			CooldownMin: in.CooldownMin, PlanID: in.PlanID, Every: in.Every})
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sub)
	})
	mux.HandleFunc("POST /api/v1/signals/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) // the body is optional
		if in.Reason == "" {
			in.Reason = "主人取消"
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		sub, err := s.Cancel(id, "owner", in.Reason)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, sub)
		case errors.Is(err, ErrEnded):
			writeJSON(w, http.StatusConflict, map[string]any{"error": "这个订阅已经结束", "signal": sub})
		default:
			httpErr(w, errStatus(err), err.Error())
		}
	})
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
