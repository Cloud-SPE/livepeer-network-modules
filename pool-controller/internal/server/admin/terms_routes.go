package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func registerTermsRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /admin/v1/regional-terms", auth(func(w http.ResponseWriter, r *http.Request) {
		terms, err := deps.Repo.ListRegionalTerms()
		writeAdminJSON(w, terms, err)
	}))
	mux.HandleFunc("GET /admin/v1/terms-publications", auth(func(w http.ResponseWriter, r *http.Request) {
		publications, err := deps.Repo.TermsPublications()
		writeAdminJSON(w, publications, err)
	}))
	mux.HandleFunc("POST /admin/v1/regional-terms", auth(func(w http.ResponseWriter, r *http.Request) {
		if deps.PublishTerms == nil {
			http.Error(w, "regional terms publication unavailable", 503)
			return
		}
		var req struct {
			SupersedesVersion string              `json:"supersedes_version,omitempty"`
			Terms             types.RegionalTerms `json:"terms"`
			Reason            string              `json:"reason"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || req.Terms.PoolID != deps.Repo.PoolID() {
			http.Error(w, "invalid regional terms publication", 400)
			return
		}
		actor := strings.TrimSpace(actorFromRequest(r))
		if actor == "" {
			actor = "operator"
		}
		if err := deps.PublishTerms(r.Context(), req.Terms, actor, req.Reason, req.SupersedesVersion); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		terms, err := deps.Repo.ListRegionalTerms()
		writeAdminJSON(w, terms, err)
	}))
}
