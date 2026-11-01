package okxacct

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Register mounts the owner-only read route (the owner key is required even for GET, see api/admin.go).
//
//	GET /api/v1/account/positions?instId=   open swap positions with TP/SL and equity;
//	                                         {enabled:false, positions:[]} when not configured
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/account/positions", func(w http.ResponseWriter, r *http.Request) {
		snap, err := s.Snapshot(r.Context(), strings.ToUpper(r.URL.Query().Get("instId")))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(snap)
	})
}
