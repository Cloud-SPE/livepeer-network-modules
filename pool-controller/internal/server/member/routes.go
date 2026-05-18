package member

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/backendverify"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Deps struct {
	Repo     *repo.StateRepo
	Verifier *backendverify.Service
}

func Register(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("POST /member/v1/join-requests", func(w http.ResponseWriter, r *http.Request) {
		var req types.JoinRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateJoinRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ID == "" {
			req.ID = fmt.Sprintf("join-%d", time.Now().UTC().UnixNano())
		}
		req.Status = types.JoinRequestPending
		req.SubmittedAt = time.Now().UTC()
		req.ReviewedAt = nil
		if err := deps.Repo.PutJoinRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "join_request_submitted",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   req.ID,
			ResourceType: "join_request",
			Details: map[string]any{
				"member_eth_address": req.MemberEthAddress,
				"requested_backends": len(req.RequestedBackends),
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(req)
	})
	mux.HandleFunc("GET /member/v1/join-requests/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/member/v1/join-requests/")
		if id == "" {
			http.Error(w, "join request id is required", http.StatusBadRequest)
			return
		}
		item, err := deps.Repo.GetJoinRequest(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(item)
	})
	mux.HandleFunc("POST /member/v1/join-requests/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/member/v1/join-requests/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 2 || parts[1] != "refresh" {
			http.Error(w, "expected /member/v1/join-requests/{id}/refresh", http.StatusBadRequest)
			return
		}
		if deps.Verifier == nil {
			http.Error(w, "verifier is not configured", http.StatusInternalServerError)
			return
		}
		results, err := deps.Verifier.VerifyJoinRequest(parts[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := deps.Repo.GetJoinRequest(parts[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "join_request_refreshed",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   parts[0],
			ResourceType: "join_request",
			Details: map[string]any{
				"verified_backends": len(results),
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(item)
	})
}

func validateJoinRequest(req types.JoinRequest) error {
	req.MemberEthAddress = strings.TrimSpace(req.MemberEthAddress)
	req.PayoutMode = strings.TrimSpace(req.PayoutMode)
	if req.MemberEthAddress == "" {
		return fmt.Errorf("member_eth_address is required")
	}
	if len(req.RequestedBackends) == 0 {
		return fmt.Errorf("requested_backends must contain at least one backend")
	}
	switch req.PayoutMode {
	case "", "onchain", "manual":
	default:
		return fmt.Errorf("payout_mode must be onchain or manual")
	}
	for i, backend := range req.RequestedBackends {
		if strings.TrimSpace(backend.ID) == "" {
			return fmt.Errorf("requested_backends[%d].id is required", i)
		}
		if strings.TrimSpace(backend.Transport) == "" {
			return fmt.Errorf("requested_backends[%d].transport is required", i)
		}
		if strings.TrimSpace(backend.URL) == "" {
			return fmt.Errorf("requested_backends[%d].url is required", i)
		}
	}
	return nil
}
