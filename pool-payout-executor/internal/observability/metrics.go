// Package observability owns the Prometheus instrumentation for
// pool-payout-executor. The CLI is one-shot for most commands; metrics
// are only useful in long-running reconcile-loop mode, so all
// collectors here are designed to be ticked from that loop and
// scraped from a dedicated listener (NewMetricsHandler).
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	transactionSubmittedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_payout_executor_transaction_submitted_total",
		Help: "Total payout-intent dispatch attempts by outcome (succeeded, failed, skipped, dry_run, backoff_skipped).",
	}, []string{"outcome"})

	transactionConfirmedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_payout_executor_transaction_confirmed_total",
		Help: "Total payout-intent confirmation outcomes (succeeded, failed, pending, skipped, backoff_skipped).",
	}, []string{"outcome"})

	reconcileIterationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_payout_executor_reconcile_iteration_total",
		Help: "Total reconcile-loop iterations, labelled by outcome (success, error).",
	}, []string{"outcome"})

	reconcileIterationDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "livepeer_pool_payout_executor_reconcile_iteration_duration_seconds",
		Help:    "Wall-clock time for a single reconcileOnce invocation (confirm + auto-requeue + dispatch).",
		Buckets: prometheus.DefBuckets,
	})
)

// RecordTransactionSubmitted increments the per-outcome counter for
// each dispatch action observed in a sendNativeBatch result.
func RecordTransactionSubmitted(outcome string) {
	if outcome == "" {
		outcome = "unknown"
	}
	transactionSubmittedTotal.WithLabelValues(outcome).Inc()
}

// RecordTransactionConfirmed increments the per-outcome counter for
// each confirm action observed in a confirmSubmitted result.
func RecordTransactionConfirmed(outcome string) {
	if outcome == "" {
		outcome = "unknown"
	}
	transactionConfirmedTotal.WithLabelValues(outcome).Inc()
}

// RecordReconcileIteration counts a single completed reconcileOnce
// invocation and observes its duration.
func RecordReconcileIteration(outcome string, seconds float64) {
	if outcome == "" {
		outcome = "unknown"
	}
	reconcileIterationTotal.WithLabelValues(outcome).Inc()
	reconcileIterationDuration.Observe(seconds)
}

// NewMetricsHandler returns the standard /metrics HTTP handler.
func NewMetricsHandler() http.Handler {
	return promhttp.Handler()
}
