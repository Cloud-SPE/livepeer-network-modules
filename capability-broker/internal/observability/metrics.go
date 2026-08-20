// Package observability provides Prometheus metrics + structured logging
// helpers shared across the broker.
//
// Per the spec's observability section, the broker exposes the following
// counters/histograms (labels: capability, offering, outcome):
//
//	livepeer_paid_requests_total{capability,offering,outcome}
//	livepeer_paid_request_duration_seconds{capability,offering}
//	livepeer_paid_work_units_total{capability,offering}
//
// In addition, the standard Go runtime + process collectors are exposed via
// promauto's default registry.
package observability

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	poolMetricsMu sync.Mutex

	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_paid_requests_total",
		Help: "Total paid requests received by the broker, labeled by capability, offering, and outcome.",
	}, []string{"capability", "offering", "outcome"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_paid_request_duration_seconds",
		Help:    "Wall-clock duration of paid requests, headers-to-response.",
		Buckets: prometheus.DefBuckets,
	}, []string{"capability", "offering"})

	workUnitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_paid_work_units_total",
		Help: "Sum of actualUnits reported by the extractor across all paid requests.",
	}, []string{"capability", "offering"})

	backendSelectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_backend_selected_total",
		Help: "Total backend selections for published offerings with one or more runtime backend candidates.",
	}, []string{"capability", "offering", "backend_id"})

	backendSelectionFinalTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_backend_selection_final_total",
		Help: "Total final backend selections after broker-local health and Pool state are combined, labeled by winner reason.",
	}, []string{"capability", "offering", "backend_id", "reason"})

	backendSelectionDeniedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_backend_selection_denied_total",
		Help: "Total backend candidate denials after broker-local health and Pool state are combined.",
	}, []string{"capability", "offering", "backend_id", "reason"})

	backendSelectionExhaustedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_backend_selection_exhausted_total",
		Help: "Total request-time selection failures where no backend candidate remained eligible.",
	}, []string{"capability", "offering", "reason"})

	backendOutcomeEmitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_backend_outcome_emit_total",
		Help: "Total best-effort backend outcome reports emitted toward pool-controller, labeled by outcome and result.",
	}, []string{"outcome", "result"})

	workReceiptEmitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_work_receipt_emit_total",
		Help: "Total best-effort work receipt emits toward pool-controller, labeled by receipt status and result.",
	}, []string{"status", "result"})

	paymentClientRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_payment_client_requests_total",
		Help: "Total payment-daemon client RPCs issued by the broker, labeled by method and result (\"ok\" or the gRPC status code).",
	}, []string{"method", "result"})

	paymentClientRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_payment_client_request_duration_seconds",
		Help:    "Wall-clock duration of payment-daemon client RPCs issued by the broker, labeled by method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method"})

	paymentClientInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_payment_client_in_flight",
		Help: "In-flight payment-daemon client RPCs issued by the broker, labeled by method.",
	}, []string{"method"})

	brokerRegistryScrapeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_broker_registry_scrape_total",
		Help: "Total scrapes of the broker's unpaid registry endpoints, labeled by endpoint and HTTP status code.",
	}, []string{"endpoint", "code"})

	brokerRegistryScrapeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_broker_registry_scrape_duration_seconds",
		Help:    "Wall-clock duration of broker registry endpoint scrapes, labeled by endpoint.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint"})

	brokerRegistryPayloadBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_broker_registry_payload_bytes",
		Help:    "Response payload size in bytes for broker registry endpoint scrapes, labeled by endpoint.",
		Buckets: prometheus.ExponentialBuckets(256, 2, 12),
	}, []string{"endpoint"})

	brokerRegistryPublishedOfferings = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_broker_registry_published_offerings",
		Help: "Number of distinct (capability, offering) pairs the broker currently publishes at /registry/offerings.",
	})

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

	metadataRefreshLastSuccessAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_last_success_age_seconds",
		Help: "Age in seconds since the most recent healthy metadata discovery refresh for a published offering. -1 means no healthy refresh has completed yet.",
	}, []string{"family", "capability", "offering", "provider"})

	metadataRefreshCurrentResult = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_current_result",
		Help: "Current metadata discovery result for a published offering. The active result label is 1 and previous results are reset to 0 on transition.",
	}, []string{"family", "capability", "offering", "provider", "result"})

	metadataRefreshConsecutiveFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_metadata_refresh_consecutive_failures",
		Help: "Number of consecutive unhealthy metadata discovery results for a published offering.",
	}, []string{"family", "capability", "offering", "provider"})

	poolSnapshotCacheStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_cache_status",
		Help: "Current overall broker-side Pool snapshot cache status. The active status label is 1 and others are 0.",
	}, []string{"status"})

	poolSnapshotGeneratedTimestamp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_generated_timestamp_seconds",
		Help: "Unix timestamp of the latest Pool snapshot generation time seen by the broker.",
	})

	poolSnapshotFetchedTimestamp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_fetched_timestamp_seconds",
		Help: "Unix timestamp of the latest successful Pool snapshot fetch time at the broker.",
	})

	poolSnapshotSettingSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_setting_seconds",
		Help: "Broker-side Pool snapshot timing settings in seconds.",
	}, []string{"setting"})

	poolSnapshotEntryStateTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_entry_state_total",
		Help: "Count of Pool snapshot entries by capability, offering, and entry state.",
	}, []string{"capability", "offering", "state"})

	poolSnapshotRoutingReasonTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_routing_reason_total",
		Help: "Count of Pool snapshot entries by capability, offering, and routing reason.",
	}, []string{"capability", "offering", "routing_reason"})

	poolSnapshotAutomaticWarmupTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_automatic_warmup_total",
		Help: "Count of Pool snapshot entries in automatic warm-up by capability and offering.",
	}, []string{"capability", "offering"})

	poolSnapshotCooldownTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_cooldown_total",
		Help: "Count of Pool snapshot entries currently cooling down by capability and offering.",
	}, []string{"capability", "offering"})

	poolSnapshotAverageRecentWindowAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_snapshot_average_recent_window_age_seconds",
		Help: "Average recent window age across Pool snapshot entries by capability and offering.",
	}, []string{"capability", "offering"})
)

type PoolSnapshotEntryMetric struct {
	CapabilityID           string
	OfferingID             string
	State                  string
	RoutingReason          string
	AutomaticWarmup        bool
	CooldownUntil          time.Time
	RecentWindowAgeSeconds float64
}

type poolSnapshotAggregate struct {
	recentWindowAgeSum float64
	count              int
	automaticWarmup    int
	cooldown           int
}

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

func RecordBackendSelection(capID, offID, backendID string) {
	if capID == "" || offID == "" || backendID == "" {
		return
	}
	backendSelectionsTotal.WithLabelValues(capID, offID, backendID).Inc()
}

func RecordBackendSelectionFinal(capID, offID, backendID, reason string) {
	if capID == "" || offID == "" || backendID == "" {
		return
	}
	backendSelectionFinalTotal.WithLabelValues(capID, offID, backendID, metricLabelValue(reason)).Inc()
}

func RecordBackendSelectionDenied(capID, offID, backendID, reason string) {
	if capID == "" || offID == "" || backendID == "" {
		return
	}
	backendSelectionDeniedTotal.WithLabelValues(capID, offID, backendID, metricLabelValue(reason)).Inc()
}

func RecordBackendSelectionExhausted(capID, offID, reason string) {
	if capID == "" || offID == "" {
		return
	}
	backendSelectionExhaustedTotal.WithLabelValues(capID, offID, metricLabelValue(reason)).Inc()
}

func RecordBackendOutcomeEmit(outcome, result string) {
	backendOutcomeEmitTotal.WithLabelValues(metricLabelValue(outcome), metricLabelValue(result)).Inc()
}

func RecordWorkReceiptEmit(status, result string) {
	workReceiptEmitTotal.WithLabelValues(metricLabelValue(status), metricLabelValue(result)).Inc()
}

// StartPaymentClientCall marks one payment-daemon client RPC as in-flight and
// returns a completion closure. The caller invokes the closure exactly once
// with the result label ("ok" or the gRPC status code), which records the
// total, observes the duration, and clears the in-flight gauge.
func StartPaymentClientCall(method string) func(result string) {
	method = metricLabelValue(method)
	paymentClientInFlight.WithLabelValues(method).Inc()
	start := time.Now()
	return func(result string) {
		paymentClientInFlight.WithLabelValues(method).Dec()
		paymentClientRequestsTotal.WithLabelValues(method, metricLabelValue(result)).Inc()
		paymentClientRequestDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
	}
}

func TestPaymentClientRequestsCounter(method, result string) prometheus.Counter {
	return paymentClientRequestsTotal.WithLabelValues(metricLabelValue(method), metricLabelValue(result))
}

// RecordRegistryScrape emits one scrape of an unpaid registry endpoint
// ("offerings" or "health"). payloadBytes < 0 means the size was not captured.
func RecordRegistryScrape(endpoint string, statusCode int, durationSeconds float64, payloadBytes int) {
	endpoint = metricLabelValue(endpoint)
	brokerRegistryScrapeTotal.WithLabelValues(endpoint, strconv.Itoa(statusCode)).Inc()
	brokerRegistryScrapeDuration.WithLabelValues(endpoint).Observe(durationSeconds)
	if payloadBytes >= 0 {
		brokerRegistryPayloadBytes.WithLabelValues(endpoint).Observe(float64(payloadBytes))
	}
}

// SetPublishedOfferings updates the gauge of currently published
// (capability, offering) pairs.
func SetPublishedOfferings(n int) {
	brokerRegistryPublishedOfferings.Set(float64(n))
}

func TestRegistryScrapeCounter(endpoint string, statusCode int) prometheus.Counter {
	return brokerRegistryScrapeTotal.WithLabelValues(metricLabelValue(endpoint), strconv.Itoa(statusCode))
}

func TestBackendOutcomeEmitCounter(outcome, result string) prometheus.Counter {
	return backendOutcomeEmitTotal.WithLabelValues(metricLabelValue(outcome), metricLabelValue(result))
}

func TestWorkReceiptEmitCounter(status, result string) prometheus.Counter {
	return workReceiptEmitTotal.WithLabelValues(metricLabelValue(status), metricLabelValue(result))
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
		metadataRefreshLastSuccessAge.WithLabelValues(family, capability, offering, provider).Set(attemptedAt.Sub(successAt).Seconds())
	} else {
		metadataRefreshLastSuccessAge.WithLabelValues(family, capability, offering, provider).Set(-1)
	}
	if previousResult != result {
		metadataRefreshCurrentResult.WithLabelValues(family, capability, offering, provider, previousResult).Set(0)
	}
	metadataRefreshCurrentResult.WithLabelValues(family, capability, offering, provider, result).Set(1)
	metadataRefreshConsecutiveFailures.WithLabelValues(family, capability, offering, provider).Set(float64(consecutiveFailures))
}

func UpdatePoolSnapshotMetrics(
	status string,
	generatedAt time.Time,
	fetchedAt time.Time,
	timeout time.Duration,
	pollInterval time.Duration,
	staleAfter time.Duration,
	expireAfter time.Duration,
	entries []PoolSnapshotEntryMetric,
	now time.Time,
) {
	poolMetricsMu.Lock()
	defer poolMetricsMu.Unlock()

	for _, candidate := range []string{"fresh", "stale", "expired", "bootstrap_pending", "fetch_error"} {
		value := 0.0
		if candidate == status {
			value = 1.0
		}
		poolSnapshotCacheStatus.WithLabelValues(candidate).Set(value)
	}

	if generatedAt.IsZero() {
		poolSnapshotGeneratedTimestamp.Set(0)
	} else {
		poolSnapshotGeneratedTimestamp.Set(float64(generatedAt.UTC().Unix()))
	}
	if fetchedAt.IsZero() {
		poolSnapshotFetchedTimestamp.Set(0)
	} else {
		poolSnapshotFetchedTimestamp.Set(float64(fetchedAt.UTC().Unix()))
	}

	poolSnapshotSettingSeconds.WithLabelValues("timeout").Set(timeout.Seconds())
	poolSnapshotSettingSeconds.WithLabelValues("poll_interval").Set(pollInterval.Seconds())
	poolSnapshotSettingSeconds.WithLabelValues("stale_after").Set(staleAfter.Seconds())
	poolSnapshotSettingSeconds.WithLabelValues("expire_after").Set(expireAfter.Seconds())

	poolSnapshotEntryStateTotal.Reset()
	poolSnapshotRoutingReasonTotal.Reset()
	poolSnapshotAutomaticWarmupTotal.Reset()
	poolSnapshotCooldownTotal.Reset()
	poolSnapshotAverageRecentWindowAge.Reset()

	aggregates := map[string]*poolSnapshotAggregate{}
	for _, entry := range entries {
		poolSnapshotEntryStateTotal.WithLabelValues(entry.CapabilityID, entry.OfferingID, metricLabelValue(entry.State)).Inc()
		if entry.RoutingReason != "" {
			poolSnapshotRoutingReasonTotal.WithLabelValues(entry.CapabilityID, entry.OfferingID, entry.RoutingReason).Inc()
		}
		key := entry.CapabilityID + "|" + entry.OfferingID
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &poolSnapshotAggregate{}
			aggregates[key] = aggregate
		}
		aggregate.count++
		aggregate.recentWindowAgeSum += entry.RecentWindowAgeSeconds
		if entry.AutomaticWarmup {
			aggregate.automaticWarmup++
		}
		if !entry.CooldownUntil.IsZero() && entry.CooldownUntil.UTC().After(now.UTC()) {
			aggregate.cooldown++
		}
	}
	for key, aggregate := range aggregates {
		capability, offering, _ := splitMetricKey(key)
		poolSnapshotAutomaticWarmupTotal.WithLabelValues(capability, offering).Set(float64(aggregate.automaticWarmup))
		poolSnapshotCooldownTotal.WithLabelValues(capability, offering).Set(float64(aggregate.cooldown))
		if aggregate.count > 0 {
			poolSnapshotAverageRecentWindowAge.WithLabelValues(capability, offering).Set(aggregate.recentWindowAgeSum / float64(aggregate.count))
		}
	}
}

func metricLabelValue(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func splitMetricKey(value string) (string, string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] == '|' {
			return value[:i], value[i+1:], true
		}
	}
	return "", "", false
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
