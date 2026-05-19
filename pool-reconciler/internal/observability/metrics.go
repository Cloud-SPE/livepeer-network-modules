// Package observability owns the Prometheus instrumentation for
// pool-reconciler. The CLI is one-shot for most commands; metrics are
// only useful in the long-running watch-rounds mode, so all collectors
// here are designed to be ticked by that command and scraped from a
// dedicated listener (NewMetricsHandler).
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	roundCloseTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_reconciler_round_close_total",
		Help: "Total round-close attempts handled by the reconciler, by outcome.",
	}, []string{"outcome"})

	roundCloseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_pool_reconciler_round_close_duration_seconds",
		Help:    "Wall-clock time spent in attemptRoundClose by outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"outcome"})

	pendingRoundsRetried = promauto.NewCounter(prometheus.CounterOpts{
		Name: "livepeer_pool_reconciler_pending_rounds_retried_total",
		Help: "Total number of pending-round retry attempts processed by the ticker.",
	})
)

// RoundCloseOutcome is the canonical label value for a single
// attemptRoundClose call.
type RoundCloseOutcome string

const (
	OutcomeClosed RoundCloseOutcome = "closed"
	OutcomeFailed RoundCloseOutcome = "failed"
)

// RecordRoundClose increments the round-close counter and records the
// observed duration. Outcome must be one of the constants above so
// dashboards remain stable.
func RecordRoundClose(outcome RoundCloseOutcome, seconds float64) {
	label := string(outcome)
	roundCloseTotal.WithLabelValues(label).Inc()
	roundCloseDuration.WithLabelValues(label).Observe(seconds)
}

// RecordPendingRoundRetry increments the retry-tick counter once per
// pending round the ticker touches (irrespective of outcome — the
// outcome itself is recorded by RecordRoundClose).
func RecordPendingRoundRetry() {
	pendingRoundsRetried.Inc()
}

// NewMetricsHandler returns the standard /metrics HTTP handler.
func NewMetricsHandler() http.Handler {
	return promhttp.Handler()
}
