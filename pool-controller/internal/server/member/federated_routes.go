package member

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func registerFederatedRoutes(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/regional-report", func(w http.ResponseWriter, r *http.Request) {
		wallet, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member authentication required", 401)
			return
		}
		report, err := deps.Repo.RegionalMemberReport(wallet)
		if err != nil {
			http.Error(w, "regional accounting unavailable", 503)
			return
		}
		writeJSON(w, 200, report)
	})
	mux.HandleFunc("GET /member/v1/membership", func(w http.ResponseWriter, r *http.Request) {
		wallet, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member authentication required", 401)
			return
		}
		terms, err := deps.Repo.ListRegionalTerms()
		if err != nil {
			http.Error(w, "regional terms unavailable", 503)
			return
		}
		var member *types.PoolMember
		record, err := deps.Repo.GetPoolMember(wallet)
		if err == nil {
			member = &record
		} else if !errors.Is(err, repo.ErrNotFound) {
			http.Error(w, "regional membership unavailable", 503)
			return
		}
		acceptances := []types.TermsAcceptance{}
		for _, term := range terms {
			accepted, err := deps.Repo.RequireTermsAcceptance(wallet, term.Version)
			if err == nil {
				acceptances = append(acceptances, accepted)
			} else if !errors.Is(err, repo.ErrTermsNotAccepted) {
				http.Error(w, "regional acceptance unavailable", 503)
				return
			}
		}
		writeJSON(w, 200, map[string]any{"pool_id": deps.Repo.PoolID(), "wallet": wallet, "joined": len(acceptances) > 0, "member": member, "terms": terms, "acceptances": acceptances})
	})
	mux.HandleFunc("POST /member/v1/join", func(w http.ResponseWriter, r *http.Request) {
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") {
			http.Error(w, "same-origin request required", 403)
			return
		}
		wallet, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member authentication required", 401)
			return
		}
		var req struct {
			PoolID  string `json:"pool_id"`
			Version string `json:"terms_version"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			http.Error(w, "invalid regional join", 400)
			return
		}
		accepted, err := deps.Repo.JoinRegionalTerms(req.PoolID, wallet, req.Version)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		if deps.RefreshTerms != nil {
			if err := deps.RefreshTerms(); err != nil {
				http.Error(w, "membership accepted; broker synchronization pending", 503)
				return
			}
		}
		writeJSON(w, 200, accepted)
	})
}
