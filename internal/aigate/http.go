package aigate

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Register mounts /api/v1/ai/* (same access as the rest of the owner's web UI).
//
//	GET    /api/v1/ai/status
//	PUT    /api/v1/ai/credential            {kind: api_key|oauth_token, value}
//	DELETE /api/v1/ai/credential
//	PUT    /api/v1/ai/model                 {model}
//	POST   /api/v1/ai/test
//	GET    /api/v1/ai/decisions?accountId&instanceId&kind&strategy&limit&before
//	GET    /api/v1/ai/decisions/{id}        (with the full request / context pack)
//	GET    /api/v1/ai/proposals?state=
//	POST   /api/v1/ai/proposals/{id}        {approve}
//	GET    /api/v1/ai/memory/{strategy}?version=
//	POST   /api/v1/ai/memory/{strategy}/reflect
//	POST   /api/v1/ai/chat                  {chatId?, message, instId?} → SSE of the new turn
//	GET    /api/v1/ai/busy                  {running} — public (no owner key): checked before deploys
//	GET    /api/v1/ai/chats                 recent conversations
//	GET    /api/v1/ai/chats/{id}            messages (+ running)
//	GET    /api/v1/ai/chats/{id}/stream     SSE: re-attach to a running turn (replays its events)
//	POST   /api/v1/ai/chats/{id}/stop
//	DELETE /api/v1/ai/chats/{id}
//	POST   /api/v1/ai/transcribe            multipart "audio" → {text} (Groq Whisper)
//	GET    /api/v1/ai/notes                 the owner's preferences / long-term notes for the chat agent
//	POST   /api/v1/ai/notes                 {text, replaces?}
//	DELETE /api/v1/ai/notes/{id}
func (g *Gate) Register(mux *http.ServeMux) {
	g.planRoutes(mux)
	g.registerNotes(mux)
	mux.HandleFunc("POST /api/v1/ai/chat", func(w http.ResponseWriter, r *http.Request) {
		var in ChatInput
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			writeErr(w, 400, "invalid JSON")
			return
		}
		id, err := g.StartChat(in)
		switch {
		case errors.Is(err, ErrUnavailable):
			writeErr(w, 503, err.Error())
			return
		case errors.Is(err, errChatBusy):
			writeErr(w, 409, err.Error())
			return
		case err != nil:
			writeErr(w, 400, err.Error())
			return
		}
		g.streamChat(w, r, id)
	})
	mux.HandleFunc("GET /api/v1/ai/busy", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"running": g.Busy()})
	})
	mux.HandleFunc("GET /api/v1/ai/chats", func(w http.ResponseWriter, r *http.Request) {
		cs, err := g.Chats(100)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, cs)
	})
	mux.HandleFunc("GET /api/v1/ai/chats/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, err := g.ChatByID(qInt(r.PathValue("id")))
		if err != nil {
			writeErr(w, 404, "chat not found")
			return
		}
		writeJSON(w, 200, c)
	})
	mux.HandleFunc("GET /api/v1/ai/chats/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		g.streamChat(w, r, qInt(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/v1/ai/chats/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"stopped": g.StopChat(qInt(r.PathValue("id")))})
	})
	mux.HandleFunc("DELETE /api/v1/ai/chats/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.DeleteChat(qInt(r.PathValue("id"))); err != nil {
			writeErr(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/ai/transcribe", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
		f, hdr, err := r.FormFile("audio")
		if err != nil {
			writeErr(w, 400, "missing audio")
			return
		}
		defer f.Close()
		text, err := Transcribe(r.Context(), f, hdr.Filename)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"text": text})
	})
	mux.HandleFunc("GET /api/v1/ai/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, g.Status()) })
	mux.HandleFunc("PUT /api/v1/ai/credential", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Kind, Value string }
		if !decode(w, r, &in) {
			return
		}
		st, err := g.SetCredential(in.Kind, in.Value)
		in.Value = ""
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("DELETE /api/v1/ai/credential", func(w http.ResponseWriter, r *http.Request) {
		st, err := g.DeleteCredential()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("PUT /api/v1/ai/model", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Model string }
		if !decode(w, r, &in) {
			return
		}
		st, err := g.SetModel(in.Model)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("POST /api/v1/ai/test", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, g.Test(r.Context())) })
	mux.HandleFunc("GET /api/v1/ai/decisions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		out, err := g.Decisions(DecisionQuery{AccountID: qInt(q.Get("accountId")), InstanceID: qInt(q.Get("instanceId")),
			Before: qInt(q.Get("before")), Limit: int(qInt(q.Get("limit"))), Kind: q.Get("kind"), Strategy: q.Get("strategy")})
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/ai/decisions/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := g.Decision(qInt(r.PathValue("id")))
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, 200, d)
	})
	mux.HandleFunc("GET /api/v1/ai/proposals", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		switch state {
		case "", PropPending, PropApproved, PropRejected:
		default:
			writeErr(w, 400, "state must be pending, approved or rejected")
			return
		}
		out, err := g.Proposals(state, int(qInt(r.URL.Query().Get("limit"))))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("POST /api/v1/ai/proposals/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Approve *bool `json:"approve"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Approve == nil {
			writeErr(w, 400, "approve (bool) required")
			return
		}
		p, err := g.ApproveProposal(qInt(r.PathValue("id")), *in.Approve)
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, 200, p)
	})
	mux.HandleFunc("GET /api/v1/ai/memory/{strategy}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("strategy")
		cur, versions, err := g.Memory(name, int(qInt(r.URL.Query().Get("version"))))
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"strategy": name, "current": cur, "versions": versions})
	})
	mux.HandleFunc("POST /api/v1/ai/memory/{strategy}/reflect", func(w http.ResponseWriter, r *http.Request) {
		m, err := g.Reflect(r.Context(), r.PathValue("strategy"))
		if err != nil {
			writeErr(w, statusOf(err), err.Error())
			return
		}
		writeJSON(w, 200, m)
	})
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoDecisions):
		return 404
	case errors.Is(err, ErrNotPending):
		return 409
	case errors.Is(err, ErrUnavailable):
		return 503
	}
	if strings.HasPrefix(err.Error(), "apply:") || strings.Contains(err.Error(), "not wired") {
		return 502
	}
	return 400
}

func qInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(v); err != nil {
		writeErr(w, 400, "invalid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
