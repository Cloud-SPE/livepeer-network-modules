package member

import (
	"encoding/json"
	"net/http"
	"net/url"
)

func registerRegionalRoutes(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/pool", func(w http.ResponseWriter, r *http.Request) {
		terms, err := deps.Repo.ListRegionalTerms()
		if err != nil {
			http.Error(w, "regional terms unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pool_id": deps.Repo.PoolID(), "terms": terms})
	})
	mux.HandleFunc("POST /member/v1/terms/accept", func(w http.ResponseWriter, r *http.Request) {
		// Strict origin validation protects cookie-authorized regional join actions.
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") {
			http.Error(w, "same-origin request required", http.StatusForbidden)
			return
		}
		wallet, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member authentication required", http.StatusUnauthorized)
			return
		}
		var req struct {
			PoolID  string `json:"pool_id"`
			Version string `json:"terms_version"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			http.Error(w, "invalid terms acceptance", http.StatusBadRequest)
			return
		}
		accepted, err := deps.Repo.AcceptRegionalTerms(req.PoolID, wallet, req.Version)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if deps.RefreshTerms != nil {
			if err := deps.RefreshTerms(); err != nil {
				http.Error(w, "terms accepted; broker synchronization pending, retry to refresh", http.StatusServiceUnavailable)
				return
			}
		}
		writeJSON(w, http.StatusOK, accepted)
	})
}
