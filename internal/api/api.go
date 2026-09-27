// Package api serves the HTTP snapshot + SSE delta API and the embedded web UI.
// Public market data only; there are no account or order endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"crypto-trader-go/internal/features"
	"crypto-trader-go/internal/market"
	"crypto-trader-go/internal/rules"
)

type Server struct {
	E     *market.Engine
	Web   fs.FS        // built SPA (index.html + assets)
	Icons http.Handler // coin icon proxy (optional)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.E.Status()) })
	mux.HandleFunc("GET /api/v1/meta", s.meta)
	mux.HandleFunc("GET /api/v1/universe", s.universe)
	mux.HandleFunc("GET /api/v1/instruments/{id}", s.instrument)
	mux.HandleFunc("GET /api/v1/catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"items": s.E.Catalog()})
	})
	mux.HandleFunc("GET /api/v1/storage", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.E.Retention()) })
	mux.HandleFunc("GET /api/v1/candles", s.candles)
	mux.HandleFunc("GET /api/v1/compare", s.compare)
	mux.HandleFunc("GET /api/v1/orderbook", func(w http.ResponseWriter, r *http.Request) {
		ob, err := s.E.OrderBook(r.Context(), r.URL.Query().Get("instId"))
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSON(w, ob)
	})
	mux.HandleFunc("GET /api/v1/series", s.series)
	mux.HandleFunc("GET /api/v1/rules", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.E.Rules()) })
	mux.HandleFunc("POST /api/v1/rules", s.saveRule)
	mux.HandleFunc("DELETE /api/v1/rules/{id}", s.deleteRule)
	mux.HandleFunc("GET /api/v1/rules/{id}/track", s.track)
	mux.HandleFunc("PUT /api/v1/watchlist/{id}", func(w http.ResponseWriter, r *http.Request) { s.watch(w, r, true) })
	mux.HandleFunc("DELETE /api/v1/watchlist/{id}", func(w http.ResponseWriter, r *http.Request) { s.watch(w, r, false) })
	mux.HandleFunc("GET /api/v1/positions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{"connected": false, "reason": "只读账户接入属于 I4，尚未启用；这不代表空仓"})
	})
	mux.HandleFunc("GET /api/v1/turnovers", s.turnovers)
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.E.Settings()) })
	mux.HandleFunc("PUT /api/v1/settings", s.putSettings)
	mux.HandleFunc("GET /api/v1/stream", s.stream)
	if s.Icons != nil {
		mux.Handle("GET /api/v1/icons/{ccy}", s.Icons)
	}
	if s.Web != nil {
		mux.Handle("/", spa(s.Web))
	}
	return logRequests(withGzip(mux))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	snap, phase := s.E.Snapshot(), s.E.Phase() // cheap: no DB, no runtime stats
	ok := phase == "live" && time.Now().UnixMilli()-snap.ComputedAt < 3*60_000
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	writeJSON(w, map[string]any{"ok": ok, "phase": phase, "asOfMinute": snap.AsOfMinute, "coverage": snap.Coverage})
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"fields": features.Fields, "rules": s.E.Rules(), "settings": s.E.Settings(), "categories": map[string]string{"1": "加密", "3": "股票/ETF", "4": "商品"}})
}

// universe returns the snapshot. scope=active (default) | watch | all. Scores are always present
// for active and watched rows; percentiles are relative to the active pool.
func (s *Server) universe(w http.ResponseWriter, r *http.Request) {
	snap := s.E.Snapshot()
	scope := r.URL.Query().Get("scope")
	out := *snap
	out.Rows = nil
	for _, row := range snap.Rows {
		switch scope {
		case "all":
		case "watch":
			if !row.Watch {
				continue
			}
		default:
			if !row.Active && !row.Watch {
				continue
			}
		}
		out.Rows = append(out.Rows, row)
	}
	sort.SliceStable(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if a.Active != b.Active {
			return a.Active
		}
		return a.Turnover12h > b.Turnover12h
	})
	writeJSON(w, out)
}

func (s *Server) instrument(w http.ResponseWriter, r *http.Request) {
	info, ok := s.E.Instrument(r.PathValue("id"))
	if !ok {
		httpErr(w, 404, "unknown instrument")
		return
	}
	writeJSON(w, info)
}

func atoiDefault(s string, d int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return d
}

func (s *Server) candles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, bar, limit := q.Get("instId"), q.Get("bar"), atoiDefault(q.Get("limit"), 300)
	if bar == "" {
		bar = "15m"
	}
	var (
		cs  []market.Candle
		err error
	)
	if q.Get("market") == "spot" {
		cs, err = s.E.SpotCandles(r.Context(), id, bar, min(limit, 1000))
	} else {
		cs, err = s.E.Candles(id, bar, limit)
	}
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, cs)
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bar := q.Get("bar")
	if bar == "" {
		bar = "15m"
	}
	pts, ids, err := s.E.Compare(q.Get("instId"), bar, atoiDefault(q.Get("limit"), 300))
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ids": ids, "points": pts})
}

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pts, err := s.E.Series(q.Get("instId"), q.Get("field"), atoiDefault(q.Get("hours"), 24))
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, pts)
}

func (s *Server) saveRule(w http.ResponseWriter, r *http.Request) {
	var rule rules.Rule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&rule); err != nil {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	saved, err := s.E.SaveRule(rule)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, saved)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.E.DeleteRule(r.PathValue("id")); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// turnovers is a compact list for the settings-page estimate: [instId, category, turnover12h, live(0/1)].
func (s *Server) turnovers(w http.ResponseWriter, r *http.Request) {
	snap := s.E.Snapshot()
	rows := make([][4]any, 0, len(snap.Rows))
	for _, x := range snap.Rows {
		if !x.Eligible { // watched instrument outside the recording universe
			continue
		}
		live := 0
		if x.Status == "live" {
			live = 1
		}
		rows = append(rows, [4]any{x.InstID, x.Category, x.Turnover12h, live})
	}
	writeJSON(w, map[string]any{"asOfMinute": snap.AsOfMinute, "rows": rows})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in market.Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		httpErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	out, err := s.E.UpdateSettings(r.Context(), in)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, out)
}

func (s *Server) track(w http.ResponseWriter, r *http.Request) {
	rep, ok := s.E.Track(r.PathValue("id"))
	if !ok {
		httpErr(w, 404, "unknown rule")
		return
	}
	writeJSON(w, rep)
}

func (s *Server) watch(w http.ResponseWriter, r *http.Request, on bool) {
	if err := s.E.SetWatch(r.PathValue("id"), on); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// stream is a one-way SSE feed: "universe" (new snapshot seq → client refetches), "prices" (every 2s),
// and a heartbeat. Clients always GET the snapshot first and on any gap simply refetch.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := s.E.Subscribe()
	defer s.E.Unsubscribe(ch)
	snap := s.E.Snapshot()
	fmt.Fprintf(w, "event: hello\ndata: {\"seq\":%d}\n\n", snap.Seq)
	fl.Flush()
	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			fl.Flush()
		case ev := <-ch:
			b, _ := json.Marshal(ev.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			fl.Flush()
		}
	}
}

func spa(web fs.FS) http.Handler {
	files := http.FileServer(http.FS(web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(p, "api/") { // unknown API route: never fall back to the SPA
			httpErr(w, http.StatusNotFound, "not found")
			return
		}
		if p != "" {
			if _, err := fs.Stat(web, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(web, "index.html")
		if err != nil {
			http.Error(w, "web UI not built (run make web)", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeHTTP(w, r)
	})
}
