package admin

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestBrokerRuntimeApplyFailure(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	desired := &types.DesiredBrokerRuntime{
		Revision:     "rev-fail",
		RenderedYAML: "capabilities: []\n",
		RenderedAt:   time.Now().UTC(),
	}
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:              stateRepo,
		WrapAuth:          func(next http.HandlerFunc) http.HandlerFunc { return next },
		RefreshRendered:   func(string) error { return nil },
		GetDesiredRuntime: func() (*types.DesiredBrokerRuntime, error) { return desired, nil },
		ApplyDesiredRuntime: func(*types.DesiredBrokerRuntime) error {
			applied, _ := stateRepo.GetAppliedBrokerRuntime()
			applied.BrokerLoadedRevision = "rev-older"
			applied.BrokerReloadStatus = "failed"
			applied.BrokerReloadError = "broker rejected reload"
			if err := stateRepo.PutAppliedBrokerRuntime(applied); err != nil {
				t.Fatalf("PutAppliedBrokerRuntime() error = %v", err)
			}
			return fmt.Errorf("reload failed")
		},
		GetBrokerConfig:  func() []byte { return []byte(desired.RenderedYAML) },
		GetMembersJSON:   func() ([]byte, error) { return []byte(`{"members":[]}`), nil },
		GetOfferingsJSON: func() ([]byte, error) { return []byte(`{"offerings":[]}`), nil },
		GetStateJSON:     func() ([]byte, error) { return []byte(`{}`), nil },
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/admin/v1/broker-runtime/apply", "application/json", bytes.NewBufferString(`{"actor":"tester"}`))
	if err != nil {
		t.Fatalf("POST /admin/v1/broker-runtime/apply error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "apply failed: reload failed") {
		t.Fatalf("POST /admin/v1/broker-runtime/apply status=%d body=%s", resp.StatusCode, string(body))
	}

	applied, err := stateRepo.GetAppliedBrokerRuntime()
	if err != nil {
		t.Fatalf("GetAppliedBrokerRuntime() error = %v", err)
	}
	if applied.LastApplyStatus != "failed" || applied.LastApplyError != "reload failed" || applied.AppliedRevision != "" {
		t.Fatalf("applied = %#v", applied)
	}

	items, err := stateRepo.ListAuditEventsFiltered("broker_runtime_apply_failed", "broker_runtime", desired.Revision, 10)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(items) == 0 || items[len(items)-1].Details["error"] != "reload failed" {
		t.Fatalf("audit events = %#v", items)
	}
	last := items[len(items)-1]
	if last.Details["broker_loaded_revision"] != "rev-older" || last.Details["broker_reload_status"] != "failed" || last.Details["broker_reload_error"] != "broker rejected reload" {
		t.Fatalf("audit details = %#v", last.Details)
	}
}

func TestBrokerRuntimeHistory(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	now := time.Now().UTC()
	for _, event := range []types.AuditEvent{
		{
			Kind:         "broker_runtime_mark_started",
			OccurredAt:   now.Add(-3 * time.Minute),
			Actor:        "tester",
			ResourceID:   "rev-1",
			ResourceType: "broker_runtime",
		},
		{
			Kind:         "broker_runtime_apply_failed",
			OccurredAt:   now.Add(-2 * time.Minute),
			Actor:        "tester",
			ResourceID:   "rev-1",
			ResourceType: "broker_runtime",
			Details: map[string]any{
				"desired_revision":       "rev-1",
				"current_revision":       "rev-2",
				"error":                  "reload failed",
				"broker_loaded_revision": "rev-older",
				"broker_reload_status":   "failed",
			},
		},
		{
			Kind:         "broker_runtime_apply_succeeded",
			OccurredAt:   now.Add(-1 * time.Minute),
			Actor:        "tester",
			ResourceID:   "rev-2",
			ResourceType: "broker_runtime",
			Details: map[string]any{
				"desired_revision":         "rev-2",
				"applied_revision":         "rev-2",
				"broker_reload_attempt_id": "reload-2",
				"broker_loaded_revision":   "rev-2",
				"broker_reload_status":     "applied",
			},
		},
		{
			Kind:         "offer_created",
			OccurredAt:   now,
			ResourceID:   "offer-1",
			ResourceType: "offer",
		},
	} {
		if err := stateRepo.AppendAuditEvent(event); err != nil {
			t.Fatalf("AppendAuditEvent() error = %v", err)
		}
	}

	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
		GetDesiredRuntime: func() (*types.DesiredBrokerRuntime, error) {
			return &types.DesiredBrokerRuntime{Revision: "rev-2"}, nil
		},
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/v1/broker-runtime/history?limit=2")
	if err != nil {
		t.Fatalf("GET /admin/v1/broker-runtime/history error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/v1/broker-runtime/history status=%d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"items":[`) || !strings.Contains(string(body), `"kind":"broker_runtime_apply_succeeded"`) || !strings.Contains(string(body), `"broker_reload_attempt_id":"reload-2"`) || !strings.Contains(string(body), `"broker_loaded_revision":"rev-2"`) {
		t.Fatalf("GET /admin/v1/broker-runtime/history body=%s", string(body))
	}
	if strings.Contains(string(body), `"kind":"offer_created"`) || strings.Contains(string(body), `"kind":"broker_runtime_mark_started"`) {
		t.Fatalf("history should be filtered/limited, body=%s", string(body))
	}
}

func TestApprovePayoutBatchMaterializesExportedIntents(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	now := time.Now().UTC()
	if err := stateRepo.PutPayoutBatch(types.PayoutBatch{
		ID:                 "batch-window-1",
		SettlementWindowID: "window-1",
		Status:             types.PayoutBatchPendingApproval,
		TotalAmountWei:     "300",
		LineItems: []types.PayoutLineItem{
			{
				MemberEthAddress:     "0x1111111111111111111111111111111111111111",
				DestinationAddress:   "0x2222222222222222222222222222222222222222",
				CapabilityID:         "openai:chat-completions",
				OfferingID:           "default",
				AttributedRevenueWei: "300",
				AmountWei:            "300",
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("PutPayoutBatch() error = %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/admin/v1/payout-batches/batch-window-1/approve", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST approve error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST approve status=%d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"status":"approved"`) || !strings.Contains(string(body), `"status":"exported"`) {
		t.Fatalf("POST approve body=%s", string(body))
	}
	intent, err := stateRepo.GetPayoutIntent("payout-batch-window-1-0000")
	if err != nil {
		t.Fatalf("GetPayoutIntent() error = %v", err)
	}
	if intent.Status != "exported" || intent.AmountWei != "300" || intent.ChainID != 42161 || intent.Asset != "native_eth" {
		t.Fatalf("intent = %+v", intent)
	}
}

func TestTemplateAssignmentCertificationRoutes(t *testing.T) {
	stateRepo := seedAdminCertificationRepo(t)
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/admin/v1/template-assignments/assign-1/certification/start", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("start certification error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"running"`) {
		t.Fatalf("start status=%d body=%s", resp.StatusCode, string(body))
	}
	runs, err := stateRepo.ListCertificationRuns()
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListCertificationRuns() = %#v err=%v", runs, err)
	}
	resp, err = http.Post(server.URL+"/admin/v1/certification-runs/"+runs[0].ID+"/complete", "application/json", bytes.NewBufferString(`{"passed":true}`))
	if err != nil {
		t.Fatalf("complete certification error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"passed"`) {
		t.Fatalf("complete status=%d body=%s", resp.StatusCode, string(body))
	}
	assignment, _ := stateRepo.GetTemplateAssignment("assign-1")
	if assignment.State != types.TemplateAssignmentProbationary {
		t.Fatalf("assignment state = %s", assignment.State)
	}
}

func TestSettlementWindowCloseRoute(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	if err := stateRepo.SaveWorkReceipt(types.WorkReceipt{
		ID:                   "work-1",
		CreatedAt:            time.Now().UTC(),
		RoundID:              "100",
		RequestID:            "req-1",
		CapabilityID:         "openai:chat-completions",
		OfferingID:           "default",
		MemberEthAddress:     "0x1111111111111111111111111111111111111111",
		AttributedRevenueWei: "1000",
		Status:               "accepted",
	}); err != nil {
		t.Fatalf("SaveWorkReceipt() error = %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }})
	server := httptest.NewServer(mux)
	defer server.Close()
	resp, err := http.Post(server.URL+"/admin/v1/settlement-windows/close", "application/json", bytes.NewBufferString(`{"window_id":"window-98","round_ids":["100"],"confirmed_revenue_wei":"500"}`))
	if err != nil {
		t.Fatalf("close window error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"settlement_scale_ppm":500000`) || !strings.Contains(string(body), `"status":"pending_approval"`) {
		t.Fatalf("close status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestHostEnrollmentRevokeKillsWorkerSessions(t *testing.T) {
	stateRepo := seedAdminCertificationRepo(t)
	now := time.Now().UTC()
	if err := stateRepo.PutHostEnrollment(types.HostEnrollment{ID: "host-1", MemberEthAddress: "0x1111111111111111111111111111111111111111", Status: types.HostEnrollmentActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("PutHostEnrollment() error = %v", err)
	}
	var killed []string
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
		KillWorkerSession: func(id string) error {
			killed = append(killed, id)
			return nil
		},
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	resp, err := http.Post(server.URL+"/admin/v1/host-enrollments/host-1/revoke", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("revoke error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"revoked"`) {
		t.Fatalf("revoke status=%d body=%s", resp.StatusCode, string(body))
	}
	if len(killed) != 1 || killed[0] != "assign-1" {
		t.Fatalf("killed = %#v", killed)
	}
}

func seedAdminCertificationRepo(t *testing.T) *repo.StateRepo {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	now := time.Now().UTC()
	if err := stateRepo.PutHardwareUnit(types.HardwareUnit{ID: "gpu-1", EnrollmentID: "host-1", MemberEthAddress: "0x1111111111111111111111111111111111111111", GPUUUID: "GPU-1", State: types.HardwareUnitRegistered, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("PutHardwareUnit() error = %v", err)
	}
	if err := stateRepo.PutTemplateCatalogEntry(types.TemplateCatalogEntry{ID: "chat-4090", CapabilityID: "openai:chat-completions", OfferingID: "default", InteractionMode: "http-stream@v0", Status: types.TemplateStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("PutTemplateCatalogEntry() error = %v", err)
	}
	if err := stateRepo.PutTemplateAssignment(types.TemplateAssignment{ID: "assign-1", HardwareUnitID: "gpu-1", HostEnrollmentID: "host-1", MemberEthAddress: "0x1111111111111111111111111111111111111111", TemplateID: "chat-4090", State: types.TemplateAssignmentPending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	return stateRepo
}
