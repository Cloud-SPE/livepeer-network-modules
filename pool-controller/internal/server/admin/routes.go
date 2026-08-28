package admin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ladder"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokerpush"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/certification"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/settlement"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ui/web"
)

// hostEnrollmentView is a host enrollment with its secret withheld.
//
// BrokerSessionCredential is the 32 random bytes a member's agent
// authenticates to the broker with. It is stored in plaintext because
// the controller has to hand it to the member once and hash it into the
// credential push, but it must never travel back out of an admin read:
// the admin token is an operator's key to the control plane, not to
// every member's runner identity. Redacting at the HTTP boundary rather
// than on the type keeps the field serialisable for the bolt store,
// which uses these same JSON tags.
type hostEnrollmentView struct {
	types.HostEnrollment
	BrokerSessionCredential string `json:"broker_session_credential,omitempty"`
}

func redactHostEnrollment(in types.HostEnrollment) hostEnrollmentView {
	in.BrokerSessionCredential = ""
	return hostEnrollmentView{HostEnrollment: in}
}

func redactHostEnrollments(in []types.HostEnrollment) []hostEnrollmentView {
	out := make([]hostEnrollmentView, 0, len(in))
	for _, item := range in {
		out = append(out, redactHostEnrollment(item))
	}
	return out
}

type Deps struct {
	// Catalog is the curated template catalog, loaded from files.
	Catalog *templates.Catalog
	// Stances overrides how many templates a GPU class runs at once.
	Stances map[string]int
	// Ladder moves placements between trust states.
	Ladder LadderRunner
	// LadderPolicy is the pool's ladder configuration, for the shares
	// an operator gesture has to reproduce.
	LadderPolicy func() ladder.Policy
	// PayoutPolicyPath and PayoutPausePath are payout-policy.json and
	// its kill switch. Empty means no automatic approval, which is the
	// state every pool starts in.
	PayoutPolicyPath  string
	PayoutPausePath   string
	Repo              *repo.StateRepo
	WrapAuth          func(http.HandlerFunc) http.HandlerFunc
	Session           *SessionAuth
	RefreshRendered   func(string) error
	GetOfferingsJSON  func() ([]byte, error)
	GetStateJSON      func() ([]byte, error)
	KillWorkerSession func(string) error
}

type RuntimeApplyInfo struct {
	Mode                  string `json:"apply_mode,omitempty"`
	TimeoutMS             int    `json:"apply_timeout_ms,omitempty"`
	CommandConfigured     bool   `json:"apply_command_configured"`
	BrokerAdminConfigured bool   `json:"broker_admin_configured"`
}

type brokerRuntimeMarkAppliedRequest struct {
	Revision string `json:"revision,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Error    string `json:"error,omitempty"`
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
	mux.HandleFunc("GET /admin/placement", uiPage("placement", "Placement"))
	mux.HandleFunc("GET /admin/ladder", uiPage("ladder", "Ladder"))
	mux.HandleFunc("GET /admin/exceptions", uiPage("exceptions", "Exceptions"))
	mux.HandleFunc("GET /admin/payouts", uiPage("payouts", "Payouts"))
	mux.HandleFunc("GET /admin/audit", uiPage("audit", "Audit"))
	mux.HandleFunc("GET /admin/v1/pool-members", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListPoolMembers()
		writeAdminJSON(w, struct {
			PoolMembers []types.PoolMember `json:"pool_members"`
		}{PoolMembers: items}, err)
	}))
	mux.HandleFunc("GET /admin/v1/host-enrollments", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListHostEnrollments()
		writeAdminJSON(w, struct {
			HostEnrollments []hostEnrollmentView `json:"host_enrollments"`
		}{HostEnrollments: redactHostEnrollments(items)}, err)
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
			Enrollment hostEnrollmentView `json:"enrollment"`
			Killed     []string           `json:"killed_worker_sessions,omitempty"`
		}{Enrollment: redactHostEnrollment(enrollment), Killed: killed}, nil)
	}))
	mux.HandleFunc("GET /admin/v1/hardware-units", auth(func(w http.ResponseWriter, _ *http.Request) {
		items, err := deps.Repo.ListHardwareUnits()
		writeAdminJSON(w, struct {
			HardwareUnits []types.HardwareUnit `json:"hardware_units"`
		}{HardwareUnits: items}, err)
	}))
	// The catalog is read-only over HTTP: it is files in the repo,
	// reviewed in version control. What an operator changes at runtime
	// is the override — enable it, price it, add metadata.
	registerPlacementRoutes(mux, deps, auth)
	registerLadderRoutes(mux, deps, auth)
	registerPayoutPolicyRoutes(mux, deps, auth)
	registerExceptionRoutes(mux, deps, auth)
	mux.HandleFunc("GET /admin/v1/template-catalog", auth(func(w http.ResponseWriter, _ *http.Request) {
		overrides, err := deps.Repo.ListTemplateOverrides()
		if err != nil {
			writeAdminJSON(w, nil, err)
			return
		}
		writeAdminJSON(w, struct {
			Templates []templateCatalogView `json:"templates"`
		}{Templates: catalogViews(deps.Catalog, overrides)}, nil)
	}))
	mux.HandleFunc("PUT /admin/v1/template-overrides/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if _, ok := deps.Catalog.Get(id); !ok {
			http.Error(w, "no template "+id+" in the catalog", http.StatusNotFound)
			return
		}
		var override types.TemplateOverride
		if err := json.NewDecoder(r.Body).Decode(&override); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		override.TemplateID = id
		if err := validateTemplateOverride(override); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := deps.Repo.PutTemplateOverride(override); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		stored, err := deps.Repo.GetTemplateOverride(id)
		writeAdminJSON(w, stored, err)
	}))
	mux.HandleFunc("DELETE /admin/v1/template-overrides/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if err := deps.Repo.DeleteTemplateOverride(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeAdminJSON(w, struct {
			Status     string `json:"status"`
			TemplateID string `json:"template_id"`
		}{Status: "reverted_to_catalog_default", TemplateID: id}, nil)
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
		run, err := certification.New(deps.Repo, deps.Catalog).StartAssignmentCertification(id)
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
		run, err := certification.New(deps.Repo, deps.Catalog).CompleteRun(certification.CompleteRequest{
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
	// The offers a broker is told to serve, derived from the enabled
	// templates. This is the same computation the push performs, so
	// what an operator reads here is exactly what the fleet was sent —
	// there is no stored offer set that could disagree.
	mux.HandleFunc("GET /admin/v1/offers", auth(func(w http.ResponseWriter, _ *http.Request) {
		overrides, err := deps.Repo.ListTemplateOverrides()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeAdminJSON(w, struct {
			Offers []brokeradmin.OfferPush `json:"offers"`
		}{Offers: brokerpush.BuildOffersFromCatalog(deps.Catalog.All(), overrides)}, nil)
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

// templateCatalogView is a catalog template with the pool's decision
// folded in, so a console does not have to join the two itself.
type templateCatalogView struct {
	templates.Template
	Enabled bool `json:"enabled"`
	// EffectivePrice is the override's price when set, otherwise the
	// catalog's suggestion — what this pool would actually charge.
	EffectivePrice  templates.Price `json:"effective_price"`
	PriceOverridden bool            `json:"price_overridden,omitempty"`
	// Extra is the catalog's metadata with the pool's merged over it —
	// what is actually advertised.
	Extra map[string]any `json:"extra,omitempty"`
	// ExtraOverride is the pool's own half, unmerged. It exists because
	// a PUT replaces the whole override record: without seeing what the
	// pool set, a client toggling `enabled` would have to either drop
	// the extra override or echo the merged map back, and echoing it
	// would silently freeze today's catalog values into the pool's
	// state where a later catalog edit could never reach them.
	ExtraOverride map[string]any `json:"extra_override,omitempty"`
	UpdatedAt     *time.Time     `json:"override_updated_at,omitempty"`
}

func catalogViews(catalog *templates.Catalog, overrides []types.TemplateOverride) []templateCatalogView {
	byID := make(map[string]types.TemplateOverride, len(overrides))
	for _, o := range overrides {
		byID[o.TemplateID] = o
	}
	all := catalog.All()
	out := make([]templateCatalogView, 0, len(all))
	for _, tmpl := range all {
		view := templateCatalogView{Template: tmpl, EffectivePrice: tmpl.PriceDefault, Extra: tmpl.Extra}
		if override, ok := byID[tmpl.ID]; ok {
			view.Enabled = override.Enabled
			if override.Price != nil {
				view.EffectivePrice = templates.Price{AmountWei: override.Price.AmountWei, PerUnits: override.Price.PerUnits}
				view.PriceOverridden = true
			}
			view.ExtraOverride = override.Extra
			if len(override.Extra) > 0 {
				merged := make(map[string]any, len(tmpl.Extra)+len(override.Extra))
				for k, v := range tmpl.Extra {
					merged[k] = v
				}
				// The pool's word wins on a key they both set: an
				// override exists precisely to disagree with the catalog.
				for k, v := range override.Extra {
					merged[k] = v
				}
				view.Extra = merged
			}
			updated := override.UpdatedAt
			view.UpdatedAt = &updated
		}
		out = append(out, view)
	}
	return out
}

func validateTemplateOverride(override types.TemplateOverride) error {
	if override.Price != nil {
		if !priceWeiRE.MatchString(override.Price.AmountWei) {
			return fmt.Errorf("price.amount_wei must be a non-negative decimal string (got %q)", override.Price.AmountWei)
		}
		if override.Price.PerUnits == 0 {
			return fmt.Errorf("price.per_units must be > 0")
		}
	}
	for _, reserved := range []string{"protocol", "job", "session"} {
		if _, clash := override.Extra[reserved]; clash {
			return fmt.Errorf("extra.%s is reserved — the declaration owns that key", reserved)
		}
	}
	for key := range override.Extra {
		if strings.HasPrefix(key, "x-") {
			return fmt.Errorf("extra.%s — x-* keys are runner extensions; the template promotes them with extra_from_runner", key)
		}
	}
	return nil
}

var priceWeiRE = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
