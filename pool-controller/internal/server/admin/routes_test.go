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
		Repo:                stateRepo,
		WrapAuth:            func(next http.HandlerFunc) http.HandlerFunc { return next },
		RefreshRendered:     func(string) error { return nil },
		GetDesiredRuntime:   func() (*types.DesiredBrokerRuntime, error) { return desired, nil },
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
		GetBrokerConfig:     func() []byte { return []byte(desired.RenderedYAML) },
		GetMembersJSON:      func() ([]byte, error) { return []byte(`{"members":[]}`), nil },
		GetOfferingsJSON:    func() ([]byte, error) { return []byte(`{"offerings":[]}`), nil },
		GetStateJSON:        func() ([]byte, error) { return []byte(`{}`), nil },
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
