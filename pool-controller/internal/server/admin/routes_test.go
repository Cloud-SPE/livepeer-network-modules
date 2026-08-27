package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

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
	stateRepo, catalog := seedAdminCertificationRepo(t)
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, Catalog: catalog, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }, RefreshRendered: func(string) error { return nil }})
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
	Register(mux, Deps{Repo: stateRepo, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }, RefreshRendered: func(string) error { return nil }})
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
	stateRepo, catalog := seedAdminCertificationRepo(t)
	now := time.Now().UTC()
	if err := stateRepo.PutHostEnrollment(types.HostEnrollment{ID: "host-1", MemberEthAddress: "0x1111111111111111111111111111111111111111", Status: types.HostEnrollmentActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("PutHostEnrollment() error = %v", err)
	}
	var killed []string
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		Catalog:  catalog,
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

func seedAdminCertificationRepo(t *testing.T) (*repo.StateRepo, *templates.Catalog) {
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
	if err := stateRepo.PutTemplateAssignment(types.TemplateAssignment{ID: "assign-1", HardwareUnitID: "gpu-1", HostEnrollmentID: "host-1", MemberEthAddress: "0x1111111111111111111111111111111111111111", TemplateID: "chat-4090", State: types.TemplateAssignmentPending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	return stateRepo, loadAdminCatalog(t, `id: chat-4090
capability: openai:chat-completions
offering_id: default
protocol: paid-job/v1
price_default:
  amount_wei: "5"
  per_units: 1
extra:
  provider: vllm
  tier: gold
stacking:
  primary: true
`)
}

// loadAdminCatalog builds the catalog the way the controller does, from
// files, so a route test cannot serve a template the loader would have
// refused at boot.
func loadAdminCatalog(t *testing.T, bodies ...string) *templates.Catalog {
	t.Helper()
	dir := t.TempDir()
	for i, body := range bodies {
		name := filepath.Join(dir, fmt.Sprintf("tmpl-%02d.yaml", i))
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("templates.Load() error = %v", err)
	}
	return catalog
}

// catalogViewsFromServer reads the joined catalog view the console uses.
func catalogViewsFromServer(t *testing.T, baseURL string) []struct {
	ID              string          `json:"id"`
	Enabled         bool            `json:"enabled"`
	EffectivePrice  templates.Price `json:"effective_price"`
	PriceOverridden bool            `json:"price_overridden"`
	Extra           map[string]any  `json:"extra"`
	UpdatedAt       *time.Time      `json:"override_updated_at"`
} {
	t.Helper()
	resp, err := http.Get(baseURL + "/admin/v1/template-catalog")
	if err != nil {
		t.Fatalf("GET /admin/v1/template-catalog error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("template-catalog status=%d body=%s", resp.StatusCode, string(body))
	}
	var view struct {
		Templates []struct {
			ID              string          `json:"id"`
			Enabled         bool            `json:"enabled"`
			EffectivePrice  templates.Price `json:"effective_price"`
			PriceOverridden bool            `json:"price_overridden"`
			Extra           map[string]any  `json:"extra"`
			UpdatedAt       *time.Time      `json:"override_updated_at"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("json.Unmarshal(template-catalog) error = %v body=%s", err, string(body))
	}
	return view.Templates
}

// The catalog view is the join an operator actually reads: the reviewed
// template plus this pool's own decision about it.
func TestTemplateCatalogViewMergesOverride(t *testing.T) {
	stateRepo, catalog := seedAdminCertificationRepo(t)
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, Catalog: catalog, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }})
	server := httptest.NewServer(mux)
	defer server.Close()

	// With no override the pool has made no decision: not enabled, and
	// the catalog's suggested price stands as the effective one.
	views := catalogViewsFromServer(t, server.URL)
	if len(views) != 1 || views[0].ID != "chat-4090" {
		t.Fatalf("catalog views = %+v", views)
	}
	if views[0].Enabled || views[0].PriceOverridden || views[0].UpdatedAt != nil {
		t.Fatalf("an untouched template already reads as decided: %+v", views[0])
	}
	if views[0].EffectivePrice.AmountWei != "5" || views[0].EffectivePrice.PerUnits != 1 {
		t.Fatalf("effective price without an override = %+v", views[0].EffectivePrice)
	}

	// An override that sets no price keeps the catalog's; its extra
	// merges key by key, and the pool's word wins where they collide.
	resp := putOverride(t, server.URL, "chat-4090", `{"enabled":true,"extra":{"tier":"silver","region":"eu"}}`)
	if resp != http.StatusOK {
		t.Fatalf("PUT override status = %d", resp)
	}
	views = catalogViewsFromServer(t, server.URL)
	if !views[0].Enabled {
		t.Fatalf("override did not enable the template: %+v", views[0])
	}
	if views[0].PriceOverridden || views[0].EffectivePrice.AmountWei != "5" {
		t.Fatalf("a priceless override changed the effective price: %+v", views[0])
	}
	if views[0].Extra["provider"] != "vllm" {
		t.Fatalf("merge dropped a catalog key the override never mentioned: %+v", views[0].Extra)
	}
	if views[0].Extra["tier"] != "silver" || views[0].Extra["region"] != "eu" {
		t.Fatalf("override did not win the merge: %+v", views[0].Extra)
	}
	if views[0].UpdatedAt == nil || views[0].UpdatedAt.IsZero() {
		t.Fatalf("override view carries no updated_at: %+v", views[0])
	}

	// A priced override is what the pool charges, and says so.
	if code := putOverride(t, server.URL, "chat-4090", `{"enabled":true,"price":{"amount_wei":"42","per_units":1000}}`); code != http.StatusOK {
		t.Fatalf("PUT priced override status = %d", code)
	}
	views = catalogViewsFromServer(t, server.URL)
	if !views[0].PriceOverridden || views[0].EffectivePrice.AmountWei != "42" || views[0].EffectivePrice.PerUnits != 1000 {
		t.Fatalf("priced override = %+v", views[0])
	}

	// Deleting returns the template to the catalog's default, which is
	// not the same as disabling it.
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/admin/v1/template-overrides/chat-4090", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE override error = %v", err)
	}
	delBody, _ := io.ReadAll(delResp.Body)
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK || !strings.Contains(string(delBody), `"status":"reverted_to_catalog_default"`) {
		t.Fatalf("DELETE override status=%d body=%s", delResp.StatusCode, string(delBody))
	}
	views = catalogViewsFromServer(t, server.URL)
	if views[0].PriceOverridden || views[0].EffectivePrice.AmountWei != "5" || views[0].Extra["tier"] != "gold" {
		t.Fatalf("delete did not revert to the catalog default: %+v", views[0])
	}
}

// An override names a template the catalog defines; anything else is a
// typo that would otherwise sit in the database describing nothing.
func TestTemplateOverrideRejectsUnknownTemplate(t *testing.T) {
	stateRepo, catalog := seedAdminCertificationRepo(t)
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, Catalog: catalog, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }})
	server := httptest.NewServer(mux)
	defer server.Close()

	if code := putOverride(t, server.URL, "chat-4091", `{"enabled":true}`); code != http.StatusNotFound {
		t.Fatalf("PUT unknown template status = %d, want 404", code)
	}
	stored, err := stateRepo.ListTemplateOverrides()
	if err != nil || len(stored) != 0 {
		t.Fatalf("a rejected override was persisted: %#v err=%v", stored, err)
	}
}

func putOverride(t *testing.T, baseURL, id, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/admin/v1/template-overrides/"+id, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT override error = %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// GET /admin/v1/offers derives its answer the same way the push does,
// so what an operator reads here is exactly what the fleet was sent.
// The three states a template can be in have to be distinguishable on
// this surface, because it is the only place an operator sees them.
func TestOffersEndpointServesTheDerivedSet(t *testing.T) {
	stateRepo, catalog := seedAdminCertificationRepo(t)
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, Catalog: catalog, WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next }})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Not adopted: the template exists in the catalog and is not sold.
	if offers := offersFromServer(t, server.URL); len(offers) != 0 {
		t.Fatalf("an unadopted template is being offered: %+v", offers)
	}

	if status := putOverride(t, server.URL, "chat-4090", `{"enabled":true,"price":{"amount_wei":"9","per_units":2}}`); status != http.StatusOK {
		t.Fatalf("PUT override status = %d", status)
	}
	offers := offersFromServer(t, server.URL)
	if len(offers) != 1 || offers[0].OfferingID != "default" || offers[0].Disabled {
		t.Fatalf("offers after enabling = %+v", offers)
	}
	if offers[0].Price.AmountWei != "9" || offers[0].Price.PerUnits != 2 {
		t.Fatalf("the pool's price did not reach the derived offer: %+v", offers[0].Price)
	}

	// Disabled is not removed. The broker keeps the offer and its
	// frozen runner shape; dropping it from the set would delete both,
	// so re-enabling would silently start a fresh freeze.
	if status := putOverride(t, server.URL, "chat-4090", `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("PUT override status = %d", status)
	}
	offers = offersFromServer(t, server.URL)
	if len(offers) != 1 {
		t.Fatalf("disabling a template dropped its offer instead of disabling it: %+v", offers)
	}
	if !offers[0].Disabled {
		t.Fatalf("offer = %+v, want disabled", offers[0])
	}
}

func offersFromServer(t *testing.T, baseURL string) []brokeradmin.OfferPush {
	t.Helper()
	resp, err := http.Get(baseURL + "/admin/v1/offers")
	if err != nil {
		t.Fatalf("GET /admin/v1/offers error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("offers status=%d body=%s", resp.StatusCode, string(body))
	}
	var view struct {
		Offers []brokeradmin.OfferPush `json:"offers"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("json.Unmarshal(offers) error = %v body=%s", err, string(body))
	}
	return view.Offers
}
