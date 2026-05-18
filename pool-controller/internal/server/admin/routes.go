package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/admissionreview"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/assignmentpolicy"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/backendverify"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/offerservice"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/runtimeservice"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/statusservice"
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

type offerMutationRequest = offerservice.Mutation

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

type assignmentPreviewView = assignmentpolicy.PreviewView

type joinRequestPreviewRequest struct {
	JoinRequestID string `json:"join_request_id"`
}

type joinRequestBackendPreview = admissionreview.JoinRequestBackendPreview
type joinRequestClaimPreview = admissionreview.JoinRequestClaimPreview
type joinRequestOfferSuggestion = admissionreview.JoinRequestOfferSuggestion
type joinRequestPreviewView = admissionreview.JoinRequestPreviewView
type assignmentCandidateView = admissionreview.AssignmentCandidateView

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
		item, err := statusservice.SetMemberStatus(deps.Repo, id, req.Status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("member-status-updated"); err != nil {
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
		offers, err := deps.Repo.ListOffers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		view := admissionreview.BuildJoinRequestPreview(item, offers)
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
			if _, err := admissionreview.ApproveJoinRequest(deps.Repo, item, req.Reason, time.Now().UTC()); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
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
		offer, err := offerservice.Create(deps.Repo, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		updated, err := offerservice.Update(deps.Repo, current, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
	mux.HandleFunc("GET /admin/v1/assignment-candidates", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := admissionreview.ListAssignmentCandidates(deps.Repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Candidates []assignmentCandidateView `json:"candidates"`
		}{Candidates: items})
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
		item, err := statusservice.SetBackendStatus(deps.Repo, id, req.Status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("member-backend-status-updated"); err != nil {
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
		view, err := assignmentpolicy.Preview(deps.Repo, req.OfferID, req.MemberBackendID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
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
		if _, err := assignmentpolicy.CreateAssignment(deps.Repo, assignment); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		item, err := statusservice.SetAssignmentStatus(deps.Repo, id, req.Status)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("assignment-status-updated"); err != nil {
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
		_ = json.NewEncoder(w).Encode(runtimeservice.BuildView(desired, applied))
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
		_ = json.NewEncoder(w).Encode(runtimeservice.BuildDiff(desired, applied))
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/mark-applied", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		now := time.Now().UTC()
		applied, err := runtimeservice.MarkApplied(deps.Repo, desired, runtimeservice.MarkRequest{
			Revision: req.Revision,
			Actor:    req.Actor,
		}, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "broker_runtime_mark_applied",
			OccurredAt:   now,
			Actor:        req.Actor,
			ResourceID:   applied.AppliedRevision,
			ResourceType: "broker_runtime",
			Details: map[string]any{
				"desired_revision": desired.Revision,
				"applied_revision": applied.AppliedRevision,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(runtimeservice.BuildView(desired, applied))
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/mark-started", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		now := time.Now().UTC()
		applied, err := runtimeservice.MarkStarted(deps.Repo, desired, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		_ = json.NewEncoder(w).Encode(runtimeservice.BuildView(desired, applied))
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/mark-failed", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		now := time.Now().UTC()
		applied, err := runtimeservice.MarkFailed(deps.Repo, desired, runtimeservice.MarkRequest{
			Actor: req.Actor,
			Error: req.Error,
		}, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		_ = json.NewEncoder(w).Encode(runtimeservice.BuildView(desired, applied))
	}))
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
