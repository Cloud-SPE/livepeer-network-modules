package main

import (
	"encoding/json"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"net/http"
	"strconv"
)

func registerRevenueSources(mux *http.ServeMux, state *runtimeState) {
	mux.HandleFunc("GET /admin/v1/revenue-sources", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var round *int64
		if raw := r.URL.Query().Get("round"); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				http.Error(w, "invalid round", 400)
				return
			}
			round = &n
		}
		sources, err := state.repo.RevenueSources(round)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			PoolID  string `json:"pool_id"`
			Sources any    `json:"sources"`
		}{state.repo.PoolID(), sources})
	}))
	mux.HandleFunc("POST /admin/v1/revenue-sources", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Source     revenue.Source `json:"source"`
			StartRound int64          `json:"start_round"`
			Reason     string         `json:"reason"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			http.Error(w, "invalid source registration", 400)
			return
		}
		if err := state.repo.RegisterRevenueSource(req.Source, req.StartRound, req.Reason, servicePrincipal(r).ID); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /admin/v1/revenue-sources/{source}/drain", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Round  int64  `json:"round"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
			http.Error(w, "invalid source drain", 400)
			return
		}
		if err := state.repo.DrainRevenueSource(r.PathValue("source"), req.Round, req.Reason, servicePrincipal(r).ID); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /admin/v1/revenue-sources/{source}/retire", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AfterRound int64  `json:"after_round"`
			Reason     string `json:"reason"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || req.Reason == "" {
			http.Error(w, "retirement round and audit reason required", 400)
			return
		}
		all, err := state.repo.RevenueSources(nil)
		if err != nil {
			http.Error(w, "source registry unavailable", 503)
			return
		}
		var public revenue.Source
		found := false
		for _, record := range all {
			if record.Source.SourceID == r.PathValue("source") {
				if record.RetiredAfterRound != nil {
					if *record.RetiredAfterRound == req.AfterRound {
						w.WriteHeader(204)
					} else {
						http.Error(w, "retirement round immutable", 409)
					}
					return
				}
				public = record.Source
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "source not found", 404)
			return
		}
		cfg, _, _ := state.Snapshot()
		var local revenue.Source
		found = false
		for _, source := range cfg.RevenueSources {
			if source.SourceID == public.SourceID {
				local = source
				source.TokenFile = ""
				source.CAFile = ""
				if source != public {
					http.Error(w, "source reader identity differs from registry", 409)
					return
				}
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "historical source reader credentials unavailable", 503)
			return
		}
		proof, err := revenue.CollectDrain(r.Context(), local)
		if err != nil {
			http.Error(w, "source retirement proof unavailable", 503)
			return
		}
		actor := servicePrincipal(r).ID
		if actor == "" {
			actor = "local-operator"
		}
		if err = state.repo.RetireRevenueSource(public.SourceID, req.AfterRound, req.Reason, proof, actor); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(204)
	}))

}
