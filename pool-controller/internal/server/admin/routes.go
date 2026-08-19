package admin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/admissionreview"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/assignmentpolicy"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/backendverify"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/certification"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/offerservice"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/runtimeservice"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/settlement"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/statusservice"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ui/web"
)

type Deps struct {
	Repo                *repo.StateRepo
	WrapAuth            func(http.HandlerFunc) http.HandlerFunc
	Session             *SessionAuth
	RefreshRendered     func(string) error
	GetDesiredRuntime   func() (*types.DesiredBrokerRuntime, error)
	GetRuntimeApplyInfo func() RuntimeApplyInfo
	ApplyDesiredRuntime func(*types.DesiredBrokerRuntime) error
	Verifier            *backendverify.Service
	GetBrokerConfig     func() []byte
	GetMembersJSON      func() ([]byte, error)
	GetOfferingsJSON    func() ([]byte, error)
	GetStateJSON        func() ([]byte, error)
	KillWorkerSession   func(string) error
}

type RuntimeApplyInfo struct {
	Mode                  string `json:"apply_mode,omitempty"`
	TimeoutMS             int    `json:"apply_timeout_ms,omitempty"`
	CommandConfigured     bool   `json:"apply_command_configured"`
	BrokerAdminConfigured bool   `json:"broker_admin_configured"`
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

type runtimeView struct {
	runtimeservice.View
	RuntimeApplyInfo
}

type runtimeHistoryItem struct {
	Kind                  string         `json:"kind"`
	Status                string         `json:"status"`
	OccurredAt            time.Time      `json:"occurred_at"`
	Actor                 string         `json:"actor,omitempty"`
	ResourceID            string         `json:"resource_id,omitempty"`
	DesiredRevision       string         `json:"desired_revision,omitempty"`
	CurrentRevision       string         `json:"current_revision,omitempty"`
	AppliedRevision       string         `json:"applied_revision,omitempty"`
	BrokerReloadAttemptID string         `json:"broker_reload_attempt_id,omitempty"`
	BrokerLoadedRevision  string         `json:"broker_loaded_revision,omitempty"`
	BrokerReloadStatus    string         `json:"broker_reload_status,omitempty"`
	BrokerReloadError     string         `json:"broker_reload_error,omitempty"`
	Error                 string         `json:"error,omitempty"`
	Details               map[string]any `json:"details,omitempty"`
}

func Register(mux *http.ServeMux, deps Deps) {
	auth := deps.WrapAuth

	pages, err := loadTemplates()
	if err != nil {
		// Templates are embedded, so a load failure is a programming error.
		panic(fmt.Sprintf("admin: load templates: %v", err))
	}
	assets, err := fs.Sub(web.FS, "assets")
	if err != nil {
		panic(fmt.Sprintf("admin: assets sub: %v", err))
	}
	mux.Handle("GET /admin/assets/", http.StripPrefix("/admin/assets/", versionedAssetHandler(assets, uiVersion)))

	// requireSession gates the operator UI pages when login is enabled (an
	// admin token is configured). In open mode it is a pass-through.
	requireSession := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if deps.Session == nil || !deps.Session.Enabled() {
				next(w, r)
				return
			}
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			actor, ok := deps.Session.Actor(cookie.Value)
			if !ok {
				clearSessionCookie(w)
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			next(w, withActor(r, actor))
		}
	}

	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) {
		if deps.Session == nil || !deps.Session.Enabled() {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			if _, ok := deps.Session.Actor(cookie.Value); ok {
				http.Redirect(w, r, "/admin", http.StatusSeeOther)
				return
			}
		}
		renderLogin(w, pages["login"], loginPageData{Version: uiVersion}, http.StatusOK)
	})
	mux.HandleFunc("POST /admin/login", func(w http.ResponseWriter, r *http.Request) {
		if deps.Session == nil || !deps.Session.Enabled() {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			renderLogin(w, pages["login"], loginPageData{Version: uiVersion, Error: "could not parse form"}, http.StatusBadRequest)
			return
		}
		id, err := deps.Session.Login(r.PostForm.Get("admin_token"), r.PostForm.Get("actor"))
		if err != nil {
			renderLogin(w, pages["login"], loginPageData{Version: uiVersion, Error: err.Error()}, http.StatusUnauthorized)
			return
		}
		setSessionCookie(w, id)
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /admin/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(SessionCookieName); err == nil && deps.Session != nil {
			deps.Session.Logout(cookie.Value)
		}
		clearSessionCookie(w)
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	})

	uiPage := func(page, title string) http.HandlerFunc {
		return requireSession(func(w http.ResponseWriter, r *http.Request) {
			renderPage(w, pages[page], pageHeader{Title: title, ActivePage: page, Version: uiVersion, Actor: actorFromRequest(r)})
		})
	}
	mux.HandleFunc("GET /admin", uiPage("overview", "Overview"))
	mux.HandleFunc("GET /admin/pool", uiPage("pool", "Pool"))
	mux.HandleFunc("GET /admin/offers", uiPage("offers", "Offers"))
	mux.HandleFunc("GET /admin/join-requests", uiPage("join-requests", "Join requests"))
	mux.HandleFunc("GET /admin/members", uiPage("members", "Members & backends"))
	mux.HandleFunc("GET /admin/assignments", uiPage("assignments", "Assignments"))
	mux.HandleFunc("GET /admin/broker-runtime", uiPage("broker-runtime", "Broker runtime"))
	mux.HandleFunc("GET /admin/audit", uiPage("audit", "Audit"))
	mux.HandleFunc("GET /admin/v1/broker-config", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.GetBrokerConfig == nil {
			http.Error(w, "broker config reader is not configured", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(deps.GetBrokerConfig())
	}))
	mux.HandleFunc("GET /admin/v1/pool-members", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListPoolMembers()
		writeAdminJSON(w, struct {
			PoolMembers []types.PoolMember `json:"pool_members"`
		}{PoolMembers: items}, err)
	}))
	mux.HandleFunc("GET /admin/v1/host-enrollments", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListHostEnrollments()
		writeAdminJSON(w, struct {
			HostEnrollments []types.HostEnrollment `json:"host_enrollments"`
		}{HostEnrollments: items}, err)
	}))
	mux.HandleFunc("POST /admin/v1/host-enrollments/{id}/revoke", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			http.Error(w, "host enrollment id is required", http.StatusBadRequest)
			return
		}
		enrollment, err := deps.Repo.RevokeHostEnrollment(id, "operator_revoke", time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		killed := killEnrollmentAssignments(deps.Repo, id, deps.KillWorkerSession)
		writeAdminJSON(w, struct {
			Enrollment types.HostEnrollment `json:"enrollment"`
			Killed     []string             `json:"killed_worker_sessions,omitempty"`
		}{Enrollment: enrollment, Killed: killed}, nil)
	}))
	mux.HandleFunc("GET /admin/v1/hardware-units", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListHardwareUnits()
		writeAdminJSON(w, struct {
			HardwareUnits []types.HardwareUnit `json:"hardware_units"`
		}{HardwareUnits: items}, err)
	}))
	mux.HandleFunc("GET /admin/v1/template-catalog", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListTemplateCatalogEntries()
		writeAdminJSON(w, struct {
			Templates []types.TemplateCatalogEntry `json:"templates"`
		}{Templates: items}, err)
	}))
	mux.HandleFunc("POST /admin/v1/template-catalog", auth(func(w http.ResponseWriter, r *http.Request) {
		var item types.TemplateCatalogEntry
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		if item.Status == "" {
			item.Status = types.TemplateStatusActive
		}
		if err := deps.Repo.PutTemplateCatalogEntry(item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeAdminJSON(w, item, nil)
	}))
	mux.HandleFunc("GET /admin/v1/template-assignments", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListTemplateAssignments()
		writeAdminJSON(w, struct {
			Assignments []types.TemplateAssignment `json:"assignments"`
		}{Assignments: items}, err)
	}))
	mux.HandleFunc("POST /admin/v1/template-assignments", auth(func(w http.ResponseWriter, r *http.Request) {
		var item types.TemplateAssignment
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		if item.State == "" {
			item.State = types.TemplateAssignmentPending
		}
		if item.Role == "" {
			item.Role = types.TemplateAssignmentPrimary
		}
		if err := deps.Repo.PutTemplateAssignment(item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("template-assignment-created"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeAdminJSON(w, item, nil)
	}))
	mux.HandleFunc("POST /admin/v1/template-assignments/{id}/certification/start", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		run, err := certification.New(deps.Repo).StartAssignmentCertification(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("certification-started"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeAdminJSON(w, run, nil)
	}))
	mux.HandleFunc("GET /admin/v1/certification-runs", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListCertificationRuns()
		writeAdminJSON(w, struct {
			CertificationRuns []types.CertificationRun `json:"certification_runs"`
		}{CertificationRuns: items}, err)
	}))
	mux.HandleFunc("POST /admin/v1/certification-runs/{id}/complete", auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Passed        bool                        `json:"passed"`
			Results       []types.CertificationResult `json:"results,omitempty"`
			FailureReason string                      `json:"failure_reason,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		run, err := certification.New(deps.Repo).CompleteRun(certification.CompleteRequest{
			RunID:         strings.TrimSpace(r.PathValue("id")),
			Passed:        req.Passed,
			Results:       req.Results,
			FailureReason: req.FailureReason,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.RefreshRendered("certification-completed"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeAdminJSON(w, run, nil)
	}))
	mux.HandleFunc("GET /admin/v1/settlement-windows", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListSettlementWindows()
		writeAdminJSON(w, struct {
			SettlementWindows []types.SettlementWindow `json:"settlement_windows"`
		}{SettlementWindows: items}, err)
	}))
	mux.HandleFunc("POST /admin/v1/settlement-windows/close", auth(func(w http.ResponseWriter, r *http.Request) {
		var req settlement.CloseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		window, batch, err := settlement.New(deps.Repo).CloseWindow(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeAdminJSON(w, struct {
			Window types.SettlementWindow `json:"window"`
			Batch  types.PayoutBatch      `json:"batch"`
		}{Window: window, Batch: batch}, nil)
	}))
	mux.HandleFunc("GET /admin/v1/payout-batches", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListPayoutBatches()
		writeAdminJSON(w, struct {
			PayoutBatches []types.PayoutBatch `json:"payout_batches"`
		}{PayoutBatches: items}, err)
	}))
	mux.HandleFunc("POST /admin/v1/payout-batches/{id}/approve", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			http.Error(w, "batch id is required", http.StatusBadRequest)
			return
		}
		batch, err := deps.Repo.GetPayoutBatch(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if batch.Status != types.PayoutBatchPendingApproval {
			http.Error(w, "only pending_approval payout batches can be approved", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		actor := strings.TrimSpace(actorFromRequest(r))
		if actor == "" {
			actor = "operator"
		}
		intents := materializePayoutIntents(batch, now)
		for _, intent := range intents {
			if err := deps.Repo.SavePayoutIntent(intent); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		batch.Status = types.PayoutBatchApproved
		batch.ApprovedBy = actor
		batch.ApprovedAt = now
		batch.UpdatedAt = now
		if err := deps.Repo.PutPayoutBatch(batch); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "payout_batch_approved",
			OccurredAt:   now,
			Actor:        actor,
			ResourceID:   batch.ID,
			ResourceType: "payout_batch",
			Details: map[string]any{
				"settlement_window_id": batch.SettlementWindowID,
				"payout_intents":       len(intents),
			},
		})
		writeAdminJSON(w, struct {
			Batch   types.PayoutBatch    `json:"batch"`
			Intents []types.PayoutIntent `json:"intents"`
		}{Batch: batch, Intents: intents}, nil)
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
		current, err := deps.Repo.GetMember(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "member_status_updated",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   id,
			ResourceType: "member",
			Details: map[string]any{
				"from_status": current.Status,
				"to_status":   item.Status,
			},
		})
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
				"capability_id": offer.CapabilityID,
				"offering_id":   offer.OfferingID,
				"protocol":      offer.Protocol,
				"status":        offer.Status,
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
				"capability_id": updated.CapabilityID,
				"offering_id":   updated.OfferingID,
				"protocol":      updated.Protocol,
				"status":        updated.Status,
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
		current, err := deps.Repo.GetMemberBackend(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "member_backend_status_updated",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   id,
			ResourceType: "member_backend",
			Details: map[string]any{
				"from_status": current.Status,
				"to_status":   item.Status,
			},
		})
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
		current, err := deps.Repo.GetAssignment(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "assignment_status_updated",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   id,
			ResourceType: "assignment",
			Details: map[string]any{
				"from_status": current.Status,
				"to_status":   item.Status,
			},
		})
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
		_ = json.NewEncoder(w).Encode(buildRuntimeView(desired, applied, deps.GetRuntimeApplyInfo))
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
	mux.HandleFunc("GET /admin/v1/broker-runtime/history", auth(func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			var parsed int
			if _, err := fmt.Sscanf(raw, "%d", &parsed); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		events, err := deps.Repo.ListAuditEventsFiltered("", "broker_runtime", "", 200)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items := buildRuntimeHistory(events, limit)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Items []runtimeHistoryItem `json:"items"`
		}{Items: items})
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
		_ = json.NewEncoder(w).Encode(buildRuntimeView(desired, applied, deps.GetRuntimeApplyInfo))
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
		_ = json.NewEncoder(w).Encode(buildRuntimeView(desired, applied, deps.GetRuntimeApplyInfo))
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
		_ = json.NewEncoder(w).Encode(buildRuntimeView(desired, applied, deps.GetRuntimeApplyInfo))
	}))
	mux.HandleFunc("POST /admin/v1/broker-runtime/apply", auth(func(w http.ResponseWriter, r *http.Request) {
		desired, err := deps.GetDesiredRuntime()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var req brokerRuntimeMarkAppliedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		now := time.Now().UTC()
		applied, status, err := runtimeservice.Apply(deps.Repo, desired, runtimeservice.MarkRequest{
			Revision: req.Revision,
			Actor:    req.Actor,
			Error:    req.Error,
		}, now, deps.ApplyDesiredRuntime)
		currentDesired := desired
		if latest, latestErr := deps.GetDesiredRuntime(); latestErr == nil && latest != nil {
			currentDesired = latest
		}
		if status == "failed" {
			details := map[string]any{
				"error":            applied.LastApplyError,
				"desired_revision": desired.Revision,
				"current_revision": currentDesired.Revision,
			}
			if applied.BrokerReloadAttemptID != "" {
				details["broker_reload_attempt_id"] = applied.BrokerReloadAttemptID
			}
			if applied.BrokerLoadedRevision != "" {
				details["broker_loaded_revision"] = applied.BrokerLoadedRevision
			}
			if applied.BrokerReloadStatus != "" {
				details["broker_reload_status"] = applied.BrokerReloadStatus
			}
			if applied.BrokerReloadError != "" {
				details["broker_reload_error"] = applied.BrokerReloadError
			}
			_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
				Kind:         "broker_runtime_apply_failed",
				OccurredAt:   now,
				Actor:        req.Actor,
				ResourceID:   desired.Revision,
				ResourceType: "broker_runtime",
				Details:      details,
			})
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		details := map[string]any{
			"desired_revision": currentDesired.Revision,
			"applied_revision": applied.AppliedRevision,
		}
		if applied.BrokerReloadAttemptID != "" {
			details["broker_reload_attempt_id"] = applied.BrokerReloadAttemptID
		}
		if applied.BrokerLoadedRevision != "" {
			details["broker_loaded_revision"] = applied.BrokerLoadedRevision
		}
		if applied.BrokerReloadStatus != "" {
			details["broker_reload_status"] = applied.BrokerReloadStatus
		}
		if applied.BrokerReloadError != "" {
			details["broker_reload_error"] = applied.BrokerReloadError
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "broker_runtime_apply_succeeded",
			OccurredAt:   now,
			Actor:        req.Actor,
			ResourceID:   applied.AppliedRevision,
			ResourceType: "broker_runtime",
			Details:      details,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(buildRuntimeView(currentDesired, applied, deps.GetRuntimeApplyInfo))
	}))
}

func buildRuntimeView(desired *types.DesiredBrokerRuntime, applied types.AppliedBrokerRuntime, infoFn func() RuntimeApplyInfo) runtimeView {
	view := runtimeView{
		View: runtimeservice.BuildView(desired, applied),
	}
	if infoFn != nil {
		view.RuntimeApplyInfo = infoFn()
	}
	return view
}

func buildRuntimeHistory(events []types.AuditEvent, limit int) []runtimeHistoryItem {
	if limit <= 0 {
		limit = 20
	}
	items := make([]runtimeHistoryItem, 0, limit)
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if !isRuntimeHistoryKind(event.Kind) {
			continue
		}
		item := runtimeHistoryItem{
			Kind:       event.Kind,
			Status:     runtimeHistoryStatus(event.Kind),
			OccurredAt: event.OccurredAt,
			Actor:      event.Actor,
			ResourceID: event.ResourceID,
			Details:    event.Details,
		}
		if event.Details != nil {
			item.DesiredRevision = stringDetail(event.Details, "desired_revision")
			item.CurrentRevision = stringDetail(event.Details, "current_revision")
			item.AppliedRevision = stringDetail(event.Details, "applied_revision")
			item.BrokerReloadAttemptID = stringDetail(event.Details, "broker_reload_attempt_id")
			item.BrokerLoadedRevision = stringDetail(event.Details, "broker_loaded_revision")
			item.BrokerReloadStatus = stringDetail(event.Details, "broker_reload_status")
			item.BrokerReloadError = stringDetail(event.Details, "broker_reload_error")
			item.Error = stringDetail(event.Details, "error")
		}
		if item.AppliedRevision == "" && event.Kind == "broker_runtime_mark_applied" {
			item.AppliedRevision = event.ResourceID
		}
		if item.DesiredRevision == "" && (event.Kind == "broker_runtime_mark_started" || event.Kind == "broker_runtime_mark_failed") {
			item.DesiredRevision = event.ResourceID
		}
		items = append(items, item)
		if len(items) >= limit {
			break
		}
	}
	return items
}

func isRuntimeHistoryKind(kind string) bool {
	switch kind {
	case "broker_runtime_mark_started",
		"broker_runtime_mark_failed",
		"broker_runtime_mark_applied",
		"broker_runtime_apply_failed",
		"broker_runtime_apply_succeeded":
		return true
	default:
		return false
	}
}

func runtimeHistoryStatus(kind string) string {
	switch kind {
	case "broker_runtime_mark_started":
		return "started"
	case "broker_runtime_mark_failed", "broker_runtime_apply_failed":
		return "failed"
	case "broker_runtime_mark_applied", "broker_runtime_apply_succeeded":
		return "applied"
	default:
		return ""
	}
}

func stringDetail(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	value, ok := details[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func writeAdminJSON(w http.ResponseWriter, value any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func materializePayoutIntents(batch types.PayoutBatch, now time.Time) []types.PayoutIntent {
	intents := make([]types.PayoutIntent, 0, len(batch.LineItems))
	for i, line := range batch.LineItems {
		if strings.TrimSpace(line.AmountWei) == "" || strings.TrimSpace(line.AmountWei) == "0" {
			continue
		}
		id := fmt.Sprintf("payout-%s-%04d", batch.ID, i)
		destination := strings.TrimSpace(line.DestinationAddress)
		if destination == "" {
			destination = line.MemberEthAddress
		}
		intents = append(intents, types.PayoutIntent{
			ID:                 id,
			CreatedAt:          now,
			RoundReceiptID:     batch.SettlementWindowID,
			RoundID:            batch.SettlementWindowID,
			MemberEthAddress:   line.MemberEthAddress,
			DestinationAddress: destination,
			ChainID:            42161,
			Asset:              "native_eth",
			AmountWei:          line.AmountWei,
			Status:             "exported",
			ExportedAt:         now,
		})
	}
	return intents
}

func killEnrollmentAssignments(stateRepo *repo.StateRepo, enrollmentID string, kill func(string) error) []string {
	if stateRepo == nil || kill == nil {
		return nil
	}
	units, err := stateRepo.ListHardwareUnitsByEnrollment(enrollmentID)
	if err != nil {
		return nil
	}
	var killed []string
	for _, unit := range units {
		assignments, err := stateRepo.ListTemplateAssignmentsByHardwareUnit(unit.ID)
		if err != nil {
			continue
		}
		for _, assignment := range assignments {
			if err := kill(assignment.ID); err == nil {
				killed = append(killed, assignment.ID)
			}
		}
	}
	return killed
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
