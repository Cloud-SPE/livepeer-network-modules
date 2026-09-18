package server

import (
	"encoding/json"
	"errors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/regionalterms"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func (s *Server) handleTermsPolicy(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil || cfg.ServiceAuthFile == "" || s.workAccounting == nil {
		http.Error(w, "regional terms policy unavailable", 503)
		return
	}
	if _, err := (serviceauth.Verifier{Path: cfg.ServiceAuthFile}).Authorize(r, cfg.PoolID, cfg.ServiceResource, "controller", "pool-admin"); err != nil {
		http.Error(w, "unauthorized service", 401)
		return
	}
	if r.Method == http.MethodGet {
		policy, err := s.workAccounting.Store.TermsPolicy()
		if errors.Is(err, workledger.ErrTermsPolicyMissing) {
			store := s.workAccounting.Store
			adminJSON(w, 200, regionalterms.Policy{PoolID: store.PoolID, SourceID: store.SourceID, BrokerID: store.BrokerID, Paused: true})
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		adminJSON(w, 200, policy)
		return
	}
	var req struct {
		PoolID           string `json:"pool_id"`
		ExpectedRevision uint64 `json:"expected_revision"`
		Action           string `json:"action"`
		Version          string `json:"version"`
		EffectiveRound   uint64 `json:"effective_round"`
		Reason           string `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.PoolID != cfg.PoolID {
		http.Error(w, "invalid regional terms request", 400)
		return
	}
	switch req.Action {
	case "pause":
		policy, err := s.workAccounting.PauseTerms(req.ExpectedRevision, req.Reason)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		adminJSON(w, 200, policy)
	case "activate":
		policy, err := s.workAccounting.ActivateTerms(r.Context(), req.ExpectedRevision, req.Version, req.EffectiveRound)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		adminJSON(w, 200, policy)
	default:
		http.Error(w, "expected pause or activate", 400)
	}
}
