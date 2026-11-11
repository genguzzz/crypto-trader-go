package aigate

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The chat agent's memory, two layers (after Letta/MemGPT core vs archival, mem0's update-not-duplicate and
// Claude's memory tool):
//   - profile: the owner's standing preferences and rules ("8 倍杠杆", "不做 meme"), at most maxProfile, sent
//     with every chat turn;
//   - fact / position / event / plan: archival memory, recalled per question (the entries most relevant to
//     the message go with the turn) and searched by the agent's memory_search.
// Recall scores character-bigram overlap (no tokenizer, fine for Chinese) + tag hits + recency + use, in memory.
// The agent writes with memory_save (a near-duplicate of the same kind is updated instead of added) and
// memory_forget; the owner edits in the app. Stored as one JSON setting; notes from before kinds existed read
// as profile.

const notesSetting = "ai.notes"
const maxNotes = 300
const maxProfile = 12

// Kinds of memory; "" in old data means profile.
var noteKinds = map[string]bool{"profile": true, "fact": true, "position": true, "event": true, "plan": true}

// Note is one memory.
type Note struct {
	ID      int64    `json:"id"`
	Ts      int64    `json:"ts"` // last written (ms)
	Text    string   `json:"text"`
	By      string   `json:"by"` // "ai" (agent) or "owner"
	Kind    string   `json:"kind,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Created int64    `json:"created,omitempty"`
	Hits    int      `json:"hits,omitempty"` // times recalled into a turn
}

func (n Note) kind() string {
	if n.Kind == "" {
		return "profile"
	}
	return n.Kind
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

// ---------- scoring ----------

func grams(s string) map[string]bool {
	out := map[string]bool{}
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		latin := true
		for _, r := range word {
			if r > unicode.MaxASCII {
				latin = false
				break
			}
		}
		if latin || len(word) == 1 {
			out[string(word)] = true
		} else {
			for i := 0; i+1 < len(word); i++ {
				out[string(word[i:i+2])] = true
			}
		}
		word = word[:0]
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func overlap(q, d map[string]bool) float64 {
	if len(q) == 0 || len(d) == 0 {
		return 0
	}
	n := 0
	for k := range q {
		if d[k] {
			n++
		}
	}
	return float64(n) / math.Sqrt(float64(len(q)*len(d)))
}

// noteScore: relevance (text overlap + tag hit) decides whether a memory matches; recency and use only order
// the matches (they must not lift an unrelated memory over the threshold).
func (g *Gate) noteScore(n Note, q map[string]bool, query string) (rel, rank float64) {
	rel = overlap(q, grams(n.Text+" "+strings.Join(n.Tags, " ")))
	lq := strings.ToLower(query)
	for _, t := range n.Tags {
		if t != "" && strings.Contains(lq, strings.ToLower(t)) {
			rel += 0.35
			break
		}
	}
	ageDays := float64(g.now().UnixMilli()-n.Ts) / 86400000
	return rel, rel + 0.15*math.Exp(-ageDays/60) + math.Min(0.1, float64(n.Hits)*0.01)
}

// MemoryFor is what a chat turn gets: the profile, and the archival memories most relevant to the question.
func (g *Gate) MemoryFor(query string) map[string][]Note {
	g.notesMu.Lock()
	defer g.notesMu.Unlock()
	ns := g.loadNotesLocked()
	q := grams(query)
	profile := []Note{}
	type scored struct {
		i int
		s float64
	}
	var cands []scored
	for i, n := range ns {
		if n.kind() == "profile" {
			profile = append(profile, n)
		} else if rel, rank := g.noteScore(n, q, query); rel > 0.15 {
			cands = append(cands, scored{i, rank})
		}
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].s > cands[b].s })
	recalled := []Note{}
	for _, c := range cands[:min(6, len(cands))] {
		ns[c.i].Hits++
		recalled = append(recalled, ns[c.i])
	}
	if len(recalled) > 0 {
		if err := g.saveNotesLocked(ns); err != nil {
			g.logf("aigate: memory hits: %v", err)
		}
	}
	return map[string][]Note{"profile": profile, "recalled": recalled}
}

// SearchNotes ranks memories for a query (empty query: newest first), optionally of one kind.
func (g *Gate) SearchNotes(query, kind string, limit int) []Note {
	ns := g.Notes()
	q := grams(query)
	type scored struct {
		n Note
		s float64
	}
	var out []scored
	for _, n := range ns {
		if kind != "" && n.kind() != kind {
			continue
		}
		rel, rank := g.noteScore(n, q, query)
		if strings.TrimSpace(query) != "" && rel <= 0.1 {
			continue
		}
		out = append(out, scored{n, rank})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].s != out[b].s {
			return out[a].s > out[b].s
		}
		return out[a].n.Ts > out[b].n.Ts
	})
	res := []Note{}
	for _, x := range out[:min(max(1, limit), len(out))] {
		res = append(res, x.n)
	}
	return res
}

// AddNote stores a memory. replaces > 0, or a near-duplicate of the same kind, is updated instead of added.
func (g *Gate) AddNote(text, by string, replaces int64) (Note, error) {
	return g.SaveNote(text, by, "profile", nil, replaces)
}

func (g *Gate) SaveNote(text, by, kind string, tags []string, replaces int64) (Note, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > 300 {
		return Note{}, errors.New("note must be 1–300 characters")
	}
	if kind == "" {
		kind = "profile"
	}
	if !noteKinds[kind] {
		return Note{}, errors.New("unknown memory kind " + kind)
	}
	clean := []string{}
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && len(clean) < 6 {
			clean = append(clean, t)
		}
	}
	g.notesMu.Lock()
	defer g.notesMu.Unlock()
	ns := g.loadNotesLocked()
	now := g.now().UnixMilli()
	hit := -1
	for i := range ns {
		if replaces > 0 && ns[i].ID == replaces {
			hit = i
			break
		}
	}
	if hit < 0 && replaces == 0 {
		tg := grams(text)
		for i := range ns {
			if ns[i].kind() == kind && overlap(tg, grams(ns[i].Text)) >= 0.72 {
				hit = i
				break
			}
		}
	}
	if hit >= 0 {
		n := ns[hit]
		n.Text, n.By, n.Kind, n.Ts = text, by, kind, now
		if len(clean) > 0 {
			n.Tags = clean
		}
		ns[hit] = n
		return n, g.saveNotesLocked(ns)
	}
	profiles := 0
	var id int64
	for _, x := range ns {
		id = max(id, x.ID)
		if x.kind() == "profile" {
			profiles++
		}
	}
	if kind == "profile" && profiles >= maxProfile {
		return Note{}, errors.New("profile is full (12): merge or replace one, or save it as a fact")
	}
	n := Note{ID: id + 1, Ts: now, Created: now, Text: text, By: by, Kind: kind, Tags: clean}
	ns = append(ns, n)
	if len(ns) > maxNotes {
		// drop the least useful archival memories: never recalled first, then oldest
		idx := []int{}
		for i, x := range ns {
			if x.kind() != "profile" {
				idx = append(idx, i)
			}
		}
		sort.Slice(idx, func(a, b int) bool {
			if ns[idx[a]].Hits != ns[idx[b]].Hits {
				return ns[idx[a]].Hits < ns[idx[b]].Hits
			}
			return ns[idx[a]].Ts < ns[idx[b]].Ts
		})
		drop := map[int]bool{}
		for _, i := range idx[:min(len(idx), len(ns)-maxNotes)] {
			drop[i] = true
		}
		kept := ns[:0]
		for i, x := range ns {
			if !drop[i] {
				kept = append(kept, x)
			}
		}
		ns = kept
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

// onAgentEvent handles events that change state on the Go side (the agent's memory_save / memory_forget).
func (g *Gate) onAgentEvent(ev chatEvent) {
	var e struct {
		Type     string   `json:"type"`
		Text     string   `json:"text"`
		Kind     string   `json:"kind"`
		Tags     []string `json:"tags"`
		Replaces int64    `json:"replaces"`
		ID       int64    `json:"id"`
	}
	if json.Unmarshal(ev, &e) != nil {
		return
	}
	switch e.Type {
	case "memory":
		if _, err := g.SaveNote(e.Text, "ai", e.Kind, e.Tags, e.Replaces); err != nil {
			g.logf("aigate: memory_save: %v", err)
		}
	case "memory_forget":
		if err := g.DeleteNote(e.ID); err != nil {
			g.logf("aigate: memory_forget: %v", err)
		}
	}
}

func (g *Gate) registerNotes(mux *http.ServeMux) {
	// all memories (profile first, newest first), or ranked by ?q= (and ?kind=, ?limit=) for the agent's memory_search
	mux.HandleFunc("GET /api/v1/ai/notes", func(w http.ResponseWriter, r *http.Request) {
		q, kind := r.URL.Query().Get("q"), r.URL.Query().Get("kind")
		if q != "" || kind != "" {
			writeJSON(w, 200, g.SearchNotes(q, kind, int(max(1, min(qInt(r.URL.Query().Get("limit")), 50)))))
			return
		}
		ns := g.Notes()
		sort.SliceStable(ns, func(a, b int) bool {
			pa, pb := ns[a].kind() == "profile", ns[b].kind() == "profile"
			if pa != pb {
				return pa
			}
			return ns[a].Ts > ns[b].Ts
		})
		writeJSON(w, 200, ns)
	})
	mux.HandleFunc("POST /api/v1/ai/notes", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text     string   `json:"text"`
			Kind     string   `json:"kind"`
			Tags     []string `json:"tags"`
			Replaces int64    `json:"replaces"`
		}
		if !decode(w, r, &in) {
			return
		}
		n, err := g.SaveNote(in.Text, "owner", in.Kind, in.Tags, in.Replaces)
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
