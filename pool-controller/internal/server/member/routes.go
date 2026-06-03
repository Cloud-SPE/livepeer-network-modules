package member

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/poolscope"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/backendverify"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/memberenrollment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Deps struct {
	Repo       *repo.StateRepo
	Verifier   *backendverify.Service
	Enrollment *memberenrollment.Service
	Sessions   *SessionAuth
}

func Register(mux *http.ServeMux, deps Deps) {
	if deps.Enrollment == nil && deps.Repo != nil {
		deps.Enrollment = memberenrollment.New(deps.Repo)
	}
	if deps.Sessions == nil {
		deps.Sessions = NewSessionAuth()
	}
	mux.HandleFunc("POST /member/v1/auth/nonce", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MemberEthAddress string `json:"member_eth_address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := deps.Enrollment.IssueNonce(memberenrollment.NonceIssueRequest{EthAddress: req.MemberEthAddress})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /member/v1/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			NonceID      string `json:"nonce_id"`
			SignatureHex string `json:"signature"`
			DisplayName  string `json:"display_name"`
			Contact      string `json:"contact"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := deps.Enrollment.VerifyNonce(memberenrollment.VerifyRequest{
			NonceID:      req.NonceID,
			SignatureHex: req.SignatureHex,
			DisplayName:  req.DisplayName,
			Contact:      req.Contact,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		sessionID, err := deps.Sessions.Create(result.Member.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		setMemberSessionCookie(w, sessionID)
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "member_eth_verified",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   result.Member.ID,
			ResourceType: "pool_member",
			Details: map[string]any{
				"member_eth_address": result.Member.EthAddress,
			},
		})
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /member/v1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		memberID, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member session is required", http.StatusUnauthorized)
			return
		}
		member, err := deps.Repo.GetPoolMember(memberID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		var req struct {
			HostLabel string `json:"host_label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := deps.Enrollment.CreateEnrollment(memberenrollment.CreateEnrollmentRequest{
			MemberEthAddress: member.EthAddress,
			HostLabel:        req.HostLabel,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "host_enrollment_created",
			OccurredAt:   time.Now().UTC(),
			ResourceID:   result.Enrollment.ID,
			ResourceType: "host_enrollment",
			Details: map[string]any{
				"member_eth_address": result.Enrollment.MemberEthAddress,
				"host_label":         result.Enrollment.HostLabel,
			},
		})
		writeJSON(w, http.StatusOK, struct {
			memberenrollment.CreateEnrollmentResult
			BundleURL string `json:"bundle_url"`
		}{
			CreateEnrollmentResult: result,
			BundleURL:              "/member/v1/enrollments/" + result.Enrollment.ID + "/bundle",
		})
	})
	mux.HandleFunc("GET /member/v1/enrollments/", func(w http.ResponseWriter, r *http.Request) {
		id, action, ok := enrollmentPath(r.URL.Path)
		if !ok || action != "bundle" {
			http.Error(w, "expected /member/v1/enrollments/{id}/bundle", http.StatusBadRequest)
			return
		}
		enrollment, token, ok := enrollmentFromBearer(deps.Enrollment, id, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		assignments := listEnrollmentAssignments(deps.Repo, enrollment.ID)
		templates, _ := deps.Repo.ListTemplateCatalogEntries()
		body, err := memberenrollment.RenderBundleZip(memberenrollment.BundleInput{
			ControllerURL: requestBaseURL(r),
			BrokerURL:     requestBaseURL(r),
			Enrollment:    enrollment,
			Token:         token,
			Assignments:   assignments,
			Templates:     templates,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", enrollment.ID+"-pool-bundle.zip"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("POST /member/v1/enrollments/", func(w http.ResponseWriter, r *http.Request) {
		id, action, ok := enrollmentPath(r.URL.Path)
		if !ok || action != "hardware" {
			http.Error(w, "expected /member/v1/enrollments/{id}/hardware", http.StatusBadRequest)
			return
		}
		enrollment, _, ok := enrollmentFromBearer(deps.Enrollment, id, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		var req struct {
			HardwareUnits []types.HardwareUnit `json:"hardware_units"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		saved := make([]types.HardwareUnit, 0, len(req.HardwareUnits))
		for i, unit := range req.HardwareUnits {
			unit.EnrollmentID = enrollment.ID
			unit.MemberEthAddress = enrollment.MemberEthAddress
			if strings.TrimSpace(unit.ID) == "" {
				unit.ID = fmt.Sprintf("%s-gpu-%d", enrollment.ID, i)
			}
			if unit.State == "" {
				unit.State = types.HardwareUnitRegistered
			}
			if unit.CreatedAt.IsZero() {
				unit.CreatedAt = now
			}
			unit.LastSeenAt = now
			unit.UpdatedAt = now
			if err := deps.Repo.PutHardwareUnit(unit); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			saved = append(saved, unit)
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "host_hardware_reported",
			OccurredAt:   now,
			ResourceID:   enrollment.ID,
			ResourceType: "host_enrollment",
			Details: map[string]any{
				"hardware_units": len(saved),
			},
		})
		writeJSON(w, http.StatusOK, struct {
			HardwareUnits []types.HardwareUnit `json:"hardware_units"`
		}{HardwareUnits: saved})
	})
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
		for j, claim := range backend.ClaimedCapabilities {
			if err := poolscope.EnsureSupportedClaim(claim.CapabilityID, claim.InteractionMode); err != nil {
				return fmt.Errorf("requested_backends[%d].claimed_capabilities[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func memberIDFromRequest(sessions *SessionAuth, r *http.Request) (string, bool) {
	cookie, err := r.Cookie(memberSessionCookieName)
	if err != nil {
		return "", false
	}
	return sessions.MemberID(cookie.Value)
}

func enrollmentPath(path string) (id string, action string, ok bool) {
	path = strings.TrimPrefix(path, "/member/v1/enrollments/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func enrollmentFromBearer(svc *memberenrollment.Service, enrollmentID string, r *http.Request) (types.HostEnrollment, string, bool) {
	if svc == nil {
		return types.HostEnrollment{}, "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		return types.HostEnrollment{}, "", false
	}
	enrollment, err := svc.GetEnrollmentForToken(enrollmentID, token)
	if err != nil {
		return types.HostEnrollment{}, "", false
	}
	return enrollment, token, true
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	}
	host := strings.TrimSpace(r.Host)
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		host = forwardedHost
	}
	return scheme + "://" + host
}

func listEnrollmentAssignments(stateRepo *repo.StateRepo, enrollmentID string) []types.TemplateAssignment {
	if stateRepo == nil {
		return nil
	}
	units, err := stateRepo.ListHardwareUnitsByEnrollment(enrollmentID)
	if err != nil {
		return nil
	}
	var assignments []types.TemplateAssignment
	for _, unit := range units {
		items, err := stateRepo.ListTemplateAssignmentsByHardwareUnit(unit.ID)
		if err != nil {
			continue
		}
		assignments = append(assignments, items...)
	}
	return assignments
}
