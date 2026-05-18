package admin

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/backendverify"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/compat"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ui/adminpage"
)

type Deps struct {
	Repo              *repo.StateRepo
	WrapAuth          func(http.HandlerFunc) http.HandlerFunc
	RefreshRendered   func(string) error
	GetDesiredRuntime func() (*types.DesiredBrokerRuntime, error)
	Verifier          *backendverify.Service
	GetBrokerConfig   func() []byte
	GetMembersJSON    func() ([]byte, error)
	GetOfferingsJSON  func() ([]byte, error)
	GetStateJSON      func() ([]byte, error)
}

type offerMutationRequest struct {
	ID              string          `json:"id"`
	CapabilityID    string          `json:"capability_id"`
	OfferingID      string          `json:"offering_id"`
	InteractionMode string          `json:"interaction_mode"`
	WorkUnit        config.WorkUnit `json:"work_unit"`
	Price           config.Price    `json:"price"`
	Extra           map[string]any  `json:"extra,omitempty"`
	Constraints     map[string]any  `json:"constraints,omitempty"`
	Status          string          `json:"status,omitempty"`
}

type assignmentMutationRequest struct {
	ID              string `json:"id"`
	OfferID         string `json:"offer_id"`
	MemberBackendID string `json:"member_backend_id"`
	Notes           string `json:"notes,omitempty"`
	Status          string `json:"status,omitempty"`
}

type memberStatusRequest struct {
	Status string `json:"status"`
}

type backendStatusRequest struct {
	Status string `json:"status"`
}

type joinRequestReviewRequest struct {
	Reason string `json:"reason,omitempty"`
}

type brokerRuntimeMarkAppliedRequest struct {
	Revision string `json:"revision,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Error    string `json:"error,omitempty"`
}

type assignmentPreviewRequest struct {
	OfferID         string `json:"offer_id"`
	MemberBackendID string `json:"member_backend_id"`
}

type assignmentPreviewView struct {
	Compatible         bool                 `json:"compatible"`
	Reasons            []string             `json:"reasons,omitempty"`
	Checks             []compat.CheckResult `json:"checks,omitempty"`
	MatchedClaim       *types.ClaimedOffer  `json:"matched_claim,omitempty"`
	OfferFound         bool                 `json:"offer_found"`
	BackendFound       bool                 `json:"backend_found"`
	MemberFound        bool                 `json:"member_found"`
	OfferStatus        string               `json:"offer_status,omitempty"`
	BackendStatus      string               `json:"backend_status,omitempty"`
	VerificationStatus string               `json:"verification_status,omitempty"`
	MemberStatus       string               `json:"member_status,omitempty"`
}

type joinRequestPreviewRequest struct {
	JoinRequestID string `json:"join_request_id"`
}

type joinRequestBackendPreview struct {
	BackendID          string                   `json:"backend_id"`
	Transport          string                   `json:"transport,omitempty"`
	URL                string                   `json:"url,omitempty"`
	VerificationStatus types.VerificationStatus `json:"verification_status,omitempty"`
	VerificationError  string                   `json:"verification_error,omitempty"`
	ClaimCount         int                      `json:"claim_count"`
	Approavable        bool                     `json:"approvable"`
	Reasons            []string                 `json:"reasons,omitempty"`
}

type joinRequestPreviewView struct {
	JoinRequestID   string                      `json:"join_request_id"`
	Status          types.JoinRequestStatus     `json:"status"`
	Approavable     bool                        `json:"approvable"`
	BackendPreviews []joinRequestBackendPreview `json:"backend_previews"`
	Reasons         []string                    `json:"reasons,omitempty"`
}

func Register(mux *http.ServeMux, deps Deps) {
	auth := deps.WrapAuth
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(adminpage.HTML())
	})
	mux.HandleFunc("GET /admin/v1/broker-config", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.GetBrokerConfig == nil {
			http.Error(w, "broker config reader is not configured", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(deps.GetBrokerConfig())
	}))
	mux.HandleFunc("GET /admin/v1/members", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.GetMembersJSON == nil {
			http.Error(w, "members reader is not configured", http.StatusInternalServerError)
			return
		}
		body, err := deps.GetMembersJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	mux.HandleFunc("GET /admin/v1/offers", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListOffers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Offers []types.Offer `json:"offers"`
		}{Offers: items})
	}))
	mux.HandleFunc("GET /admin/v1/offerings", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.GetOfferingsJSON == nil {
			http.Error(w, "offerings reader is not configured", http.StatusInternalServerError)
			return
		}
		body, err := deps.GetOfferingsJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	mux.HandleFunc("GET /admin/v1/state", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.GetStateJSON == nil {
			http.Error(w, "state reader is not configured", http.StatusInternalServerError)
			return
		}
		body, err := deps.GetStateJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	mux.HandleFunc("GET /admin/v1/audit-events", auth(func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		resourceType := strings.TrimSpace(r.URL.Query().Get("resource_type"))
		resourceID := strings.TrimSpace(r.URL.Query().Get("resource_id"))
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			var parsed int
			if _, err := fmt.Sscanf(raw, "%d", &parsed); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		items, err := deps.Repo.ListAuditEventsFiltered(kind, resourceType, resourceID, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Events []types.AuditEvent `json:"events"`
		}{Events: items})
	}))
	mux.HandleFunc("PATCH /admin/v1/members/", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/v1/members/")
		if id == "" {
			http.Error(w, "member id is required", http.StatusBadRequest)
			return
		}
		var req memberStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status := types.MemberStatus(strings.TrimSpace(req.Status))
		switch status {
		case types.MemberStatusActive, types.MemberStatusSuspended:
		default:
			http.Error(w, "status must be active or suspended", http.StatusBadRequest)
			return
		}
		if err := deps.Repo.SetMemberStatus(id, status); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("member-status-updated"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		item, err := deps.Repo.GetMember(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(item)
	}))
	mux.HandleFunc("GET /admin/v1/join-requests", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListJoinRequests()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			JoinRequests []types.JoinRequest `json:"join_requests"`
		}{JoinRequests: items})
	}))
	mux.HandleFunc("POST /admin/v1/join-request-preview", auth(func(w http.ResponseWriter, r *http.Request) {
		var req joinRequestPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := deps.Repo.GetJoinRequest(strings.TrimSpace(req.JoinRequestID))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		view := previewJoinRequest(item)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(view)
	}))
	mux.HandleFunc("POST /admin/v1/join-requests/", auth(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin/v1/join-requests/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 2 {
			http.Error(w, "expected /admin/v1/join-requests/{id}/approve, reject, or refresh", http.StatusBadRequest)
			return
		}
		id, action := parts[0], parts[1]
		item, err := deps.Repo.GetJoinRequest(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		var req joinRequestReviewRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch action {
		case "refresh":
			if deps.Verifier == nil {
				http.Error(w, "verifier is not configured", http.StatusInternalServerError)
				return
			}
			results, err := deps.Verifier.VerifyJoinRequest(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
				Kind:         "join_request_refreshed",
				OccurredAt:   time.Now().UTC(),
				ResourceID:   id,
				ResourceType: "join_request",
				Details: map[string]any{
					"verified_backends": len(results),
				},
			})
		case "approve":
			preview := previewJoinRequest(item)
			if !preview.Approavable {
				http.Error(w, strings.Join(preview.Reasons, "; "), http.StatusBadRequest)
				return
			}
			member, backends := memberAndBackendsFromJoinRequest(item, time.Now().UTC())
			if err := deps.Repo.PutMember(member); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, backend := range backends {
				if err := deps.Repo.PutMemberBackend(backend); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			if err := deps.Repo.SetJoinRequestStatus(id, types.JoinRequestApproved, req.Reason); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = deps.Repo.AppendAuditEvent(types.AuditEvent{Kind: "join_request_approved", OccurredAt: time.Now().UTC(), ResourceID: id, ResourceType: "join_request"})
			if err := deps.RefreshRendered("join-request-approved"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		case "reject":
			if err := deps.Repo.SetJoinRequestStatus(id, types.JoinRequestRejected, req.Reason); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = deps.Repo.AppendAuditEvent(types.AuditEvent{Kind: "join_request_rejected", OccurredAt: time.Now().UTC(), ResourceID: id, ResourceType: "join_request", Details: map[string]any{"reason": req.Reason}})
		default:
			http.Error(w, "action must be approve or reject", http.StatusBadRequest)
			return
		}
		updated, err := deps.Repo.GetJoinRequest(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updated)
	}))
	mux.HandleFunc("POST /admin/v1/offers", auth(func(w http.ResponseWriter, r *http.Request) {
		var req offerMutationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		offer, err := offerFromRequest(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := ensureUniquePublicOffer(deps.Repo, offer); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.Repo.PutOffer(offer); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "offer_created",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   offer.ID,
			ResourceType: "offer",
			Details: map[string]any{
				"capability_id":    offer.CapabilityID,
				"offering_id":      offer.OfferingID,
				"interaction_mode": offer.InteractionMode,
				"status":           offer.Status,
			},
		})
		if err := deps.RefreshRendered("offer-created"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(offer)
	}))
	mux.HandleFunc("PATCH /admin/v1/offers/", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/v1/offers/")
		if id == "" {
			http.Error(w, "offer id is required", http.StatusBadRequest)
			return
		}
		current, err := deps.Repo.GetOffer(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		var req offerMutationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updated, err := updatedOfferFromRequest(current, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := ensureUniquePublicOffer(deps.Repo, updated); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.Repo.PutOffer(updated); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "offer_updated",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   updated.ID,
			ResourceType: "offer",
			Details: map[string]any{
				"capability_id":    updated.CapabilityID,
				"offering_id":      updated.OfferingID,
				"interaction_mode": updated.InteractionMode,
				"status":           updated.Status,
			},
		})
		if err := deps.RefreshRendered("offer-updated"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updated)
	}))
	mux.HandleFunc("GET /admin/v1/member-backends", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListMemberBackends()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Backends []types.MemberBackend `json:"backends"`
		}{Backends: items})
	}))
	mux.HandleFunc("PATCH /admin/v1/member-backends/", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/v1/member-backends/")
		if id == "" {
			http.Error(w, "backend id is required", http.StatusBadRequest)
			return
		}
		var req backendStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status := types.BackendStatus(strings.TrimSpace(req.Status))
		switch status {
		case types.BackendStatusActive, types.BackendStatusDraining, types.BackendStatusDisabled:
		default:
			http.Error(w, "status must be active, draining, or disabled", http.StatusBadRequest)
			return
		}
		if err := deps.Repo.SetMemberBackendStatus(id, status); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("member-backend-status-updated"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		item, err := deps.Repo.GetMemberBackend(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(item)
	}))
	mux.HandleFunc("POST /admin/v1/member-backends/", auth(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin/v1/member-backends/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 2 || parts[1] != "verify" {
			http.Error(w, "expected /admin/v1/member-backends/{id}/verify", http.StatusBadRequest)
			return
		}
		if deps.Verifier == nil {
			http.Error(w, "verifier is not configured", http.StatusInternalServerError)
			return
		}
		result, err := deps.Verifier.VerifyMemberBackend(parts[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "member_backend_verified",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   parts[0],
			ResourceType: "member_backend",
			Details: map[string]any{
				"verification_status": result.VerificationStatus,
				"verification_error":  result.VerificationError,
			},
		})
		item, err := deps.Repo.GetMemberBackend(parts[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(item)
	}))
	mux.HandleFunc("GET /admin/v1/assignments", auth(func(w http.ResponseWriter, _ *http.Request) {
		assignments, err := deps.Repo.ListAssignments()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Assignments []types.Assignment `json:"assignments"`
		}{Assignments: assignments})
	}))
	mux.HandleFunc("POST /admin/v1/assignment-preview", auth(func(w http.ResponseWriter, r *http.Request) {
		var req assignmentPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		view := assignmentPreviewView{}
		offer, err := deps.Repo.GetOffer(strings.TrimSpace(req.OfferID))
		if err == nil {
			view.OfferFound = true
			view.OfferStatus = string(offer.Status)
		}
		backend, err := deps.Repo.GetMemberBackend(strings.TrimSpace(req.MemberBackendID))
		if err == nil {
			view.BackendFound = true
			view.BackendStatus = string(backend.Status)
			view.VerificationStatus = string(backend.VerificationStatus)
			member, memberErr := deps.Repo.GetMember(backend.MemberID)
			if memberErr == nil {
				view.MemberFound = true
				view.MemberStatus = string(member.Status)
			} else {
				view.Reasons = append(view.Reasons, memberErr.Error())
			}
			if view.OfferFound {
				check := compat.Check(offer, backend)
				view.Compatible = check.Compatible
				view.Reasons = append(view.Reasons, check.Reasons...)
				view.Checks = append(view.Checks, check.Checks...)
				view.MatchedClaim = check.MatchedClaim
			}
		}
		if !view.OfferFound {
			view.Reasons = append(view.Reasons, "offer not found")
		}
		if !view.BackendFound {
			view.Reasons = append(view.Reasons, "backend not found")
		}
		if view.OfferFound && offer.Status != types.OfferStatusActive {
			view.Reasons = append(view.Reasons, "offer must be active")
			view.Compatible = false
		}
		if view.BackendFound && backend.Status != types.BackendStatusActive {
			view.Reasons = append(view.Reasons, "backend must be active")
			view.Compatible = false
		}
		if view.BackendFound && backend.VerificationStatus != types.VerificationPassing {
			view.Reasons = append(view.Reasons, "backend verification must be passing")
			view.Compatible = false
		}
		if view.MemberFound {
			member, _ := deps.Repo.GetMember(backend.MemberID)
			if member.Status != types.MemberStatusActive {
				view.Reasons = append(view.Reasons, "member must be active")
				view.Compatible = false
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(view)
	}))
	mux.HandleFunc("POST /admin/v1/assignments", auth(func(w http.ResponseWriter, r *http.Request) {
		var req assignmentMutationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		assignment, err := assignmentFromRequest(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		offer, err := deps.Repo.GetOffer(assignment.OfferID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		backend, err := deps.Repo.GetMemberBackend(assignment.MemberBackendID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		member, err := deps.Repo.GetMember(backend.MemberID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if offer.Status != types.OfferStatusActive {
			http.Error(w, "offer must be active", http.StatusBadRequest)
			return
		}
		if backend.Status != types.BackendStatusActive {
			http.Error(w, "backend must be active", http.StatusBadRequest)
			return
		}
		if member.Status != types.MemberStatusActive {
			http.Error(w, "member must be active", http.StatusBadRequest)
			return
		}
		check := compat.Check(offer, backend)
		if !check.Compatible {
			http.Error(w, strings.Join(check.Reasons, "; "), http.StatusBadRequest)
			return
		}
		if err := deps.Repo.PutAssignment(assignment); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := deps.RefreshRendered("assignment-created"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(assignment)
	}))
	mux.HandleFunc("PATCH /admin/v1/assignments/", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/v1/assignments/")
		if id == "" {
			http.Error(w, "assignment id is required", http.StatusBadRequest)
			return
		}
		var req assignmentMutationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status := types.AssignmentStatus(strings.TrimSpace(req.Status))
		switch status {
		case types.AssignmentStatusActive, types.AssignmentStatusDraining, types.AssignmentStatusDisabled:
		default:
			http.Error(w, "status must be active, draining, or disabled", http.StatusBadRequest)
			return
		}
		if err := deps.Repo.SetAssignmentStatus(id, status); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("assignment-status-updated"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		item, err := deps.Repo.GetAssignment(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(item)
	}))
	mux.HandleFunc("DELETE /admin/v1/assignments/", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/admin/v1/assignments/")
		if id == "" {
			http.Error(w, "assignment id is required", http.StatusBadRequest)
			return
		}
		if err := deps.Repo.DeleteAssignment(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("assignment-deleted"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /admin/v1/broker-runtime", auth(func(w http.ResponseWriter, _ *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		applied, _ := deps.Repo.GetAppliedBrokerRuntime()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildBrokerRuntimeView(desired, applied))
	}))
	mux.HandleFunc("GET /admin/v1/broker-runtime/diff", auth(func(w http.ResponseWriter, _ *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		applied, _ := deps.Repo.GetAppliedBrokerRuntime()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			DesiredRevision string `json:"desired_revision,omitempty"`
			AppliedRevision string `json:"applied_revision,omitempty"`
			Dirty           bool   `json:"dirty"`
		}{
			DesiredRevision: revisionOf(desired),
			AppliedRevision: applied.AppliedRevision,
			Dirty:           revisionOf(desired) != applied.AppliedRevision,
		})
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/mark-applied", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if desired == nil || desired.Revision == "" {
			http.Error(w, "desired broker runtime is not available", http.StatusBadRequest)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		revision := strings.TrimSpace(req.Revision)
		if revision == "" {
			revision = desired.Revision
		}
		if revision != desired.Revision {
			http.Error(w, "revision must match current desired revision", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		applied := types.AppliedBrokerRuntime{
			DesiredRevision:     desired.Revision,
			AppliedRevision:     revision,
			LastApplyStartedAt:  now,
			LastApplyFinishedAt: now,
			LastApplyStatus:     "applied",
		}
		if err := deps.Repo.PutAppliedBrokerRuntime(applied); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "broker_runtime_mark_applied",
			OccurredAt:   now,
			Actor:        req.Actor,
			ResourceID:   revision,
			ResourceType: "broker_runtime",
			Details: map[string]any{
				"desired_revision": desired.Revision,
				"applied_revision": revision,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildBrokerRuntimeView(desired, applied))
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/mark-started", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if desired == nil || desired.Revision == "" {
			http.Error(w, "desired broker runtime is not available", http.StatusBadRequest)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		now := time.Now().UTC()
		applied, _ := deps.Repo.GetAppliedBrokerRuntime()
		applied.DesiredRevision = desired.Revision
		applied.LastApplyStartedAt = now
		applied.LastApplyStatus = "started"
		applied.LastApplyError = ""
		if err := deps.Repo.PutAppliedBrokerRuntime(applied); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "broker_runtime_mark_started",
			OccurredAt:   now,
			Actor:        req.Actor,
			ResourceID:   desired.Revision,
			ResourceType: "broker_runtime",
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildBrokerRuntimeView(desired, applied))
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/mark-failed", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if desired == nil || desired.Revision == "" {
			http.Error(w, "desired broker runtime is not available", http.StatusBadRequest)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		now := time.Now().UTC()
		applied, _ := deps.Repo.GetAppliedBrokerRuntime()
		applied.DesiredRevision = desired.Revision
		applied.LastApplyFinishedAt = now
		applied.LastApplyStatus = "failed"
		applied.LastApplyError = strings.TrimSpace(req.Error)
		if err := deps.Repo.PutAppliedBrokerRuntime(applied); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "broker_runtime_mark_failed",
			OccurredAt:   now,
			Actor:        req.Actor,
			ResourceID:   desired.Revision,
			ResourceType: "broker_runtime",
			Details: map[string]any{
				"error": applied.LastApplyError,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildBrokerRuntimeView(desired, applied))
	}))
}

type brokerRuntimeView struct {
	DesiredRevision     string    `json:"desired_revision,omitempty"`
	AppliedRevision     string    `json:"applied_revision,omitempty"`
	Dirty               bool      `json:"dirty"`
	LastApplyStartedAt  time.Time `json:"last_apply_started_at,omitempty"`
	LastApplyFinishedAt time.Time `json:"last_apply_finished_at,omitempty"`
	LastApplyStatus     string    `json:"last_apply_status,omitempty"`
	LastApplyError      string    `json:"last_apply_error,omitempty"`
	OfferCount          int       `json:"offer_count,omitempty"`
	MemberCount         int       `json:"member_count,omitempty"`
	BackendCount        int       `json:"backend_count,omitempty"`
	AssignmentCount     int       `json:"assignment_count,omitempty"`
}

func buildBrokerRuntimeView(desired *types.DesiredBrokerRuntime, applied types.AppliedBrokerRuntime) brokerRuntimeView {
	return brokerRuntimeView{
		DesiredRevision:     revisionOf(desired),
		AppliedRevision:     applied.AppliedRevision,
		Dirty:               revisionOf(desired) != applied.AppliedRevision,
		LastApplyStartedAt:  applied.LastApplyStartedAt,
		LastApplyFinishedAt: applied.LastApplyFinishedAt,
		LastApplyStatus:     applied.LastApplyStatus,
		LastApplyError:      applied.LastApplyError,
		OfferCount:          countFromDesired(desired, "offer"),
		MemberCount:         countFromDesired(desired, "member"),
		BackendCount:        countFromDesired(desired, "backend"),
		AssignmentCount:     countFromDesired(desired, "assignment"),
	}
}

func revisionOf(desired *types.DesiredBrokerRuntime) string {
	if desired == nil {
		return ""
	}
	return desired.Revision
}

func countFromDesired(desired *types.DesiredBrokerRuntime, kind string) int {
	if desired == nil {
		return 0
	}
	switch kind {
	case "offer":
		return desired.OfferCount
	case "member":
		return desired.MemberCount
	case "backend":
		return desired.BackendCount
	case "assignment":
		return desired.AssignmentCount
	default:
		return 0
	}
}

func offerFromRequest(req offerMutationRequest) (types.Offer, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.CapabilityID = strings.TrimSpace(req.CapabilityID)
	req.OfferingID = strings.TrimSpace(req.OfferingID)
	req.InteractionMode = strings.TrimSpace(req.InteractionMode)
	if req.ID == "" {
		return types.Offer{}, fmt.Errorf("id is required")
	}
	if req.CapabilityID == "" || req.OfferingID == "" || req.InteractionMode == "" {
		return types.Offer{}, fmt.Errorf("capability_id, offering_id, and interaction_mode are required")
	}
	if req.WorkUnit.Name == "" || len(req.WorkUnit.Extractor) == 0 {
		return types.Offer{}, fmt.Errorf("work_unit.name and work_unit.extractor are required")
	}
	if req.Price.AmountWei == "" || req.Price.PerUnits == 0 {
		return types.Offer{}, fmt.Errorf("price.amount_wei and price.per_units > 0 are required")
	}
	status := types.OfferStatusActive
	if strings.TrimSpace(req.Status) != "" {
		status = types.OfferStatus(strings.TrimSpace(req.Status))
	}
	offer := types.Offer{
		ID:              req.ID,
		CapabilityID:    req.CapabilityID,
		OfferingID:      req.OfferingID,
		InteractionMode: req.InteractionMode,
		WorkUnit:        req.WorkUnit,
		Price:           req.Price,
		Extra:           req.Extra,
		Constraints:     req.Constraints,
		Status:          status,
	}
	return offer, validateOffer(offer)
}

func updatedOfferFromRequest(current types.Offer, req offerMutationRequest) (types.Offer, error) {
	if strings.TrimSpace(req.CapabilityID) != "" {
		current.CapabilityID = strings.TrimSpace(req.CapabilityID)
	}
	if strings.TrimSpace(req.OfferingID) != "" {
		current.OfferingID = strings.TrimSpace(req.OfferingID)
	}
	if strings.TrimSpace(req.InteractionMode) != "" {
		current.InteractionMode = strings.TrimSpace(req.InteractionMode)
	}
	if req.WorkUnit.Name != "" {
		current.WorkUnit = req.WorkUnit
	}
	if req.Price.AmountWei != "" {
		current.Price = req.Price
	}
	if req.Extra != nil {
		current.Extra = req.Extra
	}
	if req.Constraints != nil {
		current.Constraints = req.Constraints
	}
	if strings.TrimSpace(req.Status) != "" {
		current.Status = types.OfferStatus(strings.TrimSpace(req.Status))
	}
	if current.CapabilityID == "" || current.OfferingID == "" || current.InteractionMode == "" {
		return types.Offer{}, fmt.Errorf("capability_id, offering_id, and interaction_mode are required")
	}
	if current.WorkUnit.Name == "" || len(current.WorkUnit.Extractor) == 0 {
		return types.Offer{}, fmt.Errorf("work_unit.name and work_unit.extractor are required")
	}
	if current.Price.AmountWei == "" || current.Price.PerUnits == 0 {
		return types.Offer{}, fmt.Errorf("price.amount_wei and price.per_units > 0 are required")
	}
	return current, validateOffer(current)
}

func validateOffer(offer types.Offer) error {
	switch offer.Status {
	case types.OfferStatusActive, types.OfferStatusDisabled:
	default:
		return fmt.Errorf("status must be active or disabled")
	}
	extractorType, _ := offer.WorkUnit.Extractor["type"].(string)
	if strings.TrimSpace(extractorType) == "" {
		return fmt.Errorf("work_unit.extractor.type is required")
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(offer.Price.AmountWei), 10)
	if !ok {
		return fmt.Errorf("price.amount_wei must be a base-10 integer string")
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("price.amount_wei must be > 0")
	}
	return nil
}

func ensureUniquePublicOffer(repo *repo.StateRepo, offer types.Offer) error {
	if repo == nil {
		return nil
	}
	items, err := repo.ListOffers()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == offer.ID {
			continue
		}
		if item.CapabilityID == offer.CapabilityID && item.OfferingID == offer.OfferingID && item.InteractionMode == offer.InteractionMode {
			return fmt.Errorf("offer %q conflicts with existing offer %q for %s/%s %s", offer.ID, item.ID, offer.CapabilityID, offer.OfferingID, offer.InteractionMode)
		}
	}
	return nil
}

func assignmentFromRequest(req assignmentMutationRequest) (types.Assignment, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.OfferID = strings.TrimSpace(req.OfferID)
	req.MemberBackendID = strings.TrimSpace(req.MemberBackendID)
	if req.ID == "" {
		return types.Assignment{}, fmt.Errorf("id is required")
	}
	if req.OfferID == "" || req.MemberBackendID == "" {
		return types.Assignment{}, fmt.Errorf("offer_id and member_backend_id are required")
	}
	status := types.AssignmentStatusActive
	if strings.TrimSpace(req.Status) != "" {
		status = types.AssignmentStatus(strings.TrimSpace(req.Status))
	}
	return types.Assignment{
		ID:              req.ID,
		OfferID:         req.OfferID,
		MemberBackendID: req.MemberBackendID,
		Status:          status,
		Notes:           req.Notes,
	}, nil
}

func previewJoinRequest(item types.JoinRequest) joinRequestPreviewView {
	view := joinRequestPreviewView{
		JoinRequestID:   item.ID,
		Status:          item.Status,
		Approavable:     true,
		BackendPreviews: make([]joinRequestBackendPreview, 0, len(item.RequestedBackends)),
	}
	if item.Status != types.JoinRequestPending {
		view.Approavable = false
		view.Reasons = append(view.Reasons, "join request must be pending for approval")
	}
	if strings.TrimSpace(item.MemberEthAddress) == "" {
		view.Approavable = false
		view.Reasons = append(view.Reasons, "member_eth_address is required")
	}
	if len(item.RequestedBackends) == 0 {
		view.Approavable = false
		view.Reasons = append(view.Reasons, "requested_backends must contain at least one backend")
	}
	for _, backend := range item.RequestedBackends {
		backendView := joinRequestBackendPreview{
			BackendID:          backend.ID,
			Transport:          backend.Transport,
			URL:                backend.URL,
			VerificationStatus: backend.VerificationStatus,
			VerificationError:  backend.VerificationError,
			ClaimCount:         len(backend.ClaimedCapabilities),
			Approavable:        true,
		}
		if strings.TrimSpace(backend.ID) == "" {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend id is required")
		}
		if strings.TrimSpace(backend.Transport) == "" {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend transport is required")
		}
		if strings.TrimSpace(backend.URL) == "" {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend url is required")
		}
		if backend.VerificationStatus != types.VerificationPassing {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend verification must be passing")
		}
		if len(backend.ClaimedCapabilities) == 0 {
			backendView.Approavable = false
			backendView.Reasons = append(backendView.Reasons, "backend must claim at least one capability")
		}
		if !backendView.Approavable {
			view.Approavable = false
			if len(backendView.Reasons) > 0 {
				view.Reasons = append(view.Reasons, backend.ID+": "+strings.Join(backendView.Reasons, "; "))
			}
		}
		view.BackendPreviews = append(view.BackendPreviews, backendView)
	}
	if !view.Approavable && len(view.Reasons) == 0 {
		view.Reasons = append(view.Reasons, "one or more requested backends are not approvable")
	}
	return view
}

func memberAndBackendsFromJoinRequest(req types.JoinRequest, now time.Time) (types.MemberRecord, []types.MemberBackend) {
	now = now.UTC()
	memberID := fmt.Sprintf("member-%d", now.UnixNano())
	payoutMode := req.PayoutMode
	if payoutMode == "" {
		payoutMode = "onchain"
	}
	member := types.MemberRecord{
		ID:                  memberID,
		EthAddress:          req.MemberEthAddress,
		DisplayName:         req.DisplayName,
		PayoutMode:          payoutMode,
		Status:              types.MemberStatusActive,
		SourceJoinRequestID: req.ID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	backends := make([]types.MemberBackend, 0, len(req.RequestedBackends))
	for _, requested := range req.RequestedBackends {
		backends = append(backends, types.MemberBackend{
			ID:                  requested.ID,
			MemberID:            memberID,
			Transport:           requested.Transport,
			URL:                 requested.URL,
			Auth:                requested.Auth,
			HealthProbe:         requested.HealthProbe,
			ClaimedCapabilities: requested.ClaimedCapabilities,
			VerificationStatus:  requested.VerificationStatus,
			VerificationError:   requested.VerificationError,
			LastVerifiedAt:      requested.LastVerifiedAt,
			Status:              types.BackendStatusActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		})
	}
	return member, backends
}
