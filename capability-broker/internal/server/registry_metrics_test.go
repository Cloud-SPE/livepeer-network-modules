package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
)

func TestInstrumentRegistryScrapeRecordsOutcome(t *testing.T) {
	before := testutil.ToFloat64(observability.TestRegistryScrapeCounter("offerings", http.StatusOK))

	h := instrumentRegistryScrape("offerings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"capabilities":[]}`))
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/registry/offerings", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"capabilities":[]}` {
		t.Fatalf("body passthrough mismatch: %q", got)
	}

	after := testutil.ToFloat64(observability.TestRegistryScrapeCounter("offerings", http.StatusOK))
	if after-before != 1 {
		t.Fatalf("offerings 200 counter: want +1, got %v", after-before)
	}
}

func TestInstrumentRegistryScrapeCapturesErrorStatus(t *testing.T) {
	before := testutil.ToFloat64(observability.TestRegistryScrapeCounter("health", http.StatusInternalServerError))

	h := instrumentRegistryScrape("health", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "health manager is not available", http.StatusInternalServerError)
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/registry/health", nil))

	after := testutil.ToFloat64(observability.TestRegistryScrapeCounter("health", http.StatusInternalServerError))
	if after-before != 1 {
		t.Fatalf("health 500 counter: want +1, got %v", after-before)
	}
}
