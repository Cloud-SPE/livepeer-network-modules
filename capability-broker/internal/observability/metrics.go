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
	"time"

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

	metadataRefreshTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_metadata_refresh_total",
		Help: "Total metadata discovery refresh attempts, labeled by family, provider, and result.",
	}, []string{"family", "provider", "result"})

	metadataRefreshDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_metadata_refresh_duration_seconds",
		Help:    "Wall-clock duration of metadata discovery refresh attempts, labeled by family, provider, and result.",
		Buckets: prometheus.DefBuckets,
	}, []string{"family", "provider", "result"})

	metadataRefreshLastAttemptTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_last_attempt_timestamp_seconds",
		Help: "Unix timestamp of the most recent metadata discovery refresh attempt for a published offering.",
	}, []string{"family", "capability", "offering", "provider"})

	metadataRefreshLastSuccessTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_last_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful metadata discovery refresh for a published offering.",
	}, []string{"family", "capability", "offering", "provider"})

	metadataRefreshCurrentResult = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_current_result",
		Help: "Current metadata discovery result for a published offering. The active result label is 1 and previous results are reset to 0 on transition.",
	}, []string{"family", "capability", "offering", "provider", "result"})

	metadataRefreshConsecutiveFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_consecutive_failures",
		Help: "Number of consecutive unhealthy metadata discovery results for a published offering.",
	}, []string{"family", "capability", "offering", "provider"})
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

// RecordMetadataRefresh emits one metadata discovery refresh outcome.
func RecordMetadataRefresh(
	family, capability, offering, provider, result string,
	previousResult string,
	consecutiveFailures int,
	durationSeconds float64,
	attemptedAt time.Time,
	successAt time.Time,
) {
	family = metadataLabelValue(family)
	capability = metadataLabelValue(capability)
	offering = metadataLabelValue(offering)
	provider = metadataLabelValue(provider)
	result = metadataLabelValue(result)
	previousResult = metadataLabelValue(previousResult)
	metadataRefreshTotal.WithLabelValues(family, provider, result).Inc()
	metadataRefreshDuration.WithLabelValues(family, provider, result).Observe(durationSeconds)
	metadataRefreshLastAttemptTimestamp.WithLabelValues(family, capability, offering, provider).Set(float64(attemptedAt.UTC().Unix()))
	if !successAt.IsZero() {
		metadataRefreshLastSuccessTimestamp.WithLabelValues(family, capability, offering, provider).Set(float64(successAt.UTC().Unix()))
	}
	if previousResult != result {
		metadataRefreshCurrentResult.WithLabelValues(family, capability, offering, provider, previousResult).Set(0)
	}
	metadataRefreshCurrentResult.WithLabelValues(family, capability, offering, provider, result).Set(1)
	metadataRefreshConsecutiveFailures.WithLabelValues(family, capability, offering, provider).Set(float64(consecutiveFailures))
}

func metadataLabelValue(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// MetricsHandler returns a Prometheus scrape handler suitable for mounting at
// /metrics on a separate listener.
func MetricsHandler() http.Handler { return promhttp.Handler() }
