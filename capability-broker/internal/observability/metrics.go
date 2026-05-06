// Package observability provides Prometheus metrics + structured logging
// helpers shared across the broker.
//
// Per the spec's observability section, the broker exposes the following
// counters/histograms (labels: capability, offering, outcome):
//
//	livepeer_mode_requests_total{capability,offering,outcome}
//	livepeer_mode_request_duration_seconds{capability,offering}
//	livepeer_mode_work_units_total{capability,offering}
//
// In addition, the standard Go runtime + process collectors are exposed via
// promauto's default registry.
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_mode_requests_total",
		Help: "Total paid requests received by the broker, labeled by capability, offering, and outcome.",
	}, []string{"capability", "offering", "outcome"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_mode_request_duration_seconds",
		Help:    "Wall-clock duration of paid requests, headers-to-response.",
		Buckets: prometheus.DefBuckets,
	}, []string{"capability", "offering"})

	workUnitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_mode_work_units_total",
		Help: "Sum of actualUnits reported by the extractor across all paid requests.",
	}, []string{"capability", "offering"})
)

// RecordRequest emits one request's metrics.
//
//   - capID, offID — labels for grouping; "" if unknown (e.g., header
//     validation failed before dispatch).
//   - outcome — "success" for 2xx, the Livepeer-Error code if non-2xx with a
//     code header set, "other" otherwise.
//   - durationSeconds — wall-clock time spent in the broker.
//   - workUnits — value emitted in Livepeer-Work-Units (0 if not set).
func RecordRequest(capID, offID, outcome string, durationSeconds float64, workUnits uint64) {
	requestsTotal.WithLabelValues(capID, offID, outcome).Inc()
	if capID != "" || offID != "" {
		requestDuration.WithLabelValues(capID, offID).Observe(durationSeconds)
		if workUnits > 0 {
			workUnitsTotal.WithLabelValues(capID, offID).Add(float64(workUnits))
		}
	}
}

// MetricsHandler returns a Prometheus scrape handler suitable for mounting at
// /metrics on a separate listener.
func MetricsHandler() http.Handler { return promhttp.Handler() }
