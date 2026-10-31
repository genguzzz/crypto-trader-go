package aigate

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

// The owner's preferences and long-term notes for the chat agent ("8 倍杠杆", "不做 meme"…). Every
// chat turn receives them; the agent adds or replaces one with its remember tool, the owner edits
// them in the app. Stored as one JSON setting.

const notesSetting = "ai.notes"
const maxNotes = 50

// Note is one remembered preference or fact.
type Note struct {
	ID   int64  `json:"id"`
	Ts   int64  `json:"ts"`
	Text string `json:"text"`
	By   string `json:"by"` // "ai" (remember tool) or "owner"
}

func (g *Gate) Notes() []Note {
	g.notesMu.Lock()
	defer g.notesMu.Unlock()
	return g.loadNotesLocked()
}

func (g *Gate) loadNotesLocked() []Note {
	out := []Note{}
	if raw, ok := g.st.Setting(notesSetting); ok && raw != "" {
		json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// AddNote stores a note; replaces > 0 overwrites that note instead (the agent updating a preference).
func (g *Gate) AddNote(text, by string, replaces int64) (Note, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > 300 {
		return Note{}, errors.New("note must be 1–300 characters")
	}
	g.notesMu.Lock()
	defer g.notesMu.Unlock()
	ns := g.loadNotesLocked()
	n := Note{Ts: g.now().UnixMilli(), Text: text, By: by}
	for _, x := range ns {
		n.ID = max(n.ID, x.ID)
	}
	n.ID++
	replaced := false
	for i := range ns {
		if replaces > 0 && ns[i].ID == replaces {
			n.ID = replaces
			ns[i] = n
			replaced = true
		}
	}
	if !replaced {
		if len(ns) >= maxNotes {
			return Note{}, errors.New("too many notes; delete some first")
		}
		ns = append(ns, n)
	}
	return n, g.saveNotesLocked(ns)
}

func (g *Gate) DeleteNote(id int64) error {
	g.notesMu.Lock()
	defer g.notesMu.Unlock()
	ns := g.loadNotesLocked()
	out := ns[:0]
	for _, x := range ns {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return g.saveNotesLocked(out)
}

func (g *Gate) saveNotesLocked(ns []Note) error {
	b, _ := json.Marshal(ns)
	return g.st.PutSetting(notesSetting, string(b))
}

// onAgentEvent handles events that change state on the Go side (the agent's remember tool).
func (g *Gate) onAgentEvent(ev chatEvent) {
	var e struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Replaces int64  `json:"replaces"`
	}
	if json.Unmarshal(ev, &e) == nil && e.Type == "memory" {
		if _, err := g.AddNote(e.Text, "ai", e.Replaces); err != nil {
			g.logf("aigate: remember: %v", err)
		}
	}
}

func (g *Gate) registerNotes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/ai/notes", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, g.Notes()) })
	mux.HandleFunc("POST /api/v1/ai/notes", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text     string `json:"text"`
			Replaces int64  `json:"replaces"`
		}
		if !decode(w, r, &in) {
			return
		}
		n, err := g.AddNote(in.Text, "owner", in.Replaces)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, n)
	})
	mux.HandleFunc("DELETE /api/v1/ai/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.DeleteNote(qInt(r.PathValue("id"))); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}
