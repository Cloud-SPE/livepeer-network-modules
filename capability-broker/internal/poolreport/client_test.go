package poolreport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHTTPClientReportBackendOutcome(t *testing.T) {
	var gotAuth string
	var gotOutcome BackendOutcome
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if got := r.URL.Path; got != outcomesPath {
			t.Fatalf("path = %q, want %q", got, outcomesPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotOutcome); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, time.Second, config.AuthConfig{
		Method:    "bearer",
		SecretRef: "env://POOL_TOKEN",
	}, backend.NewAuthApplier(backend.NewEnvSecretResolver()))
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	t.Setenv("POOL_TOKEN", "top-secret")

	at := time.Now().UTC().Truncate(time.Second)
	err = client.ReportBackendOutcome(context.Background(), BackendOutcome{
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "shared",
		MemberEthAddress: "0xabc",
		Outcome:          OutcomeSuccess,
		LatencyMetricMS:  123,
		OccurredAt:       at,
	})
	if err != nil {
		t.Fatalf("ReportBackendOutcome() error = %v", err)
	}
	if gotAuth != "Bearer top-secret" {
		t.Fatalf("Authorization = %q, want Bearer top-secret", gotAuth)
	}
	if gotOutcome.BackendID != "backend-a" || gotOutcome.Outcome != OutcomeSuccess || !gotOutcome.OccurredAt.Equal(at) {
		t.Fatalf("outcome = %#v", gotOutcome)
	}
}

func TestReportBestEffortRecordsEmitMetrics(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		before := testutil.ToFloat64(observability.TestBackendOutcomeEmitCounter("success", "success"))
		// Wait on the reporter's own signal, not on the stub being
		// called. The metric is recorded AFTER the client returns, so a
		// test that closed its own channel inside the stub resumed one
		// statement too early and read the counter before the emit.
		done := ReportBestEffort(stubClientFunc(func(context.Context, BackendOutcome) error {
			return nil
		}), BackendOutcome{Outcome: OutcomeSuccess})
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for success emit")
		}
		after := testutil.ToFloat64(observability.TestBackendOutcomeEmitCounter("success", "success"))
		if after != before+1 {
			t.Fatalf("success emit delta = %v; want 1", after-before)
		}
	})

	t.Run("error", func(t *testing.T) {
		before := testutil.ToFloat64(observability.TestBackendOutcomeEmitCounter("backend_failure", "error"))
		done := ReportBestEffort(stubClientFunc(func(context.Context, BackendOutcome) error {
			return errors.New("boom")
		}), BackendOutcome{Outcome: OutcomeBackendFailure})
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for error emit")
		}
		after := testutil.ToFloat64(observability.TestBackendOutcomeEmitCounter("backend_failure", "error"))
		if after != before+1 {
			t.Fatalf("error emit delta = %v; want 1", after-before)
		}
	})
}

// A nil client is a broker with no pool to report to. It must not
// block a caller that waits on the signal, or a standalone deployment
// would hang wherever reporting is wired in.
func TestReportBestEffortWithNoClientCompletes(t *testing.T) {
	select {
	case <-ReportBestEffort(nil, BackendOutcome{Outcome: OutcomeSuccess}):
	case <-time.After(5 * time.Second):
		t.Fatal("a nil client never signalled completion")
	}
}

type stubClientFunc func(context.Context, BackendOutcome) error

func (f stubClientFunc) ReportBackendOutcome(ctx context.Context, outcome BackendOutcome) error {
	return f(ctx, outcome)
}
