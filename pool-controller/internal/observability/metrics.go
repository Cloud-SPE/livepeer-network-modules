package observability

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	metricsMu sync.Mutex

	backendSelectionStateTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_state_total",
		Help: "Count of backend selection entries by capability, offering, and state.",
	}, []string{"capability", "offering", "state"})

	backendSelectionRoutingReasonTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_routing_reason_total",
		Help: "Count of backend selection entries by capability, offering, and routing reason.",
	}, []string{"capability", "offering", "routing_reason"})

	backendSelectionExclusionReasonTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_exclusion_reason_total",
		Help: "Count of backend selection entries by capability, offering, and exclusion reason.",
	}, []string{"capability", "offering", "exclusion_reason"})

	backendSelectionAutomaticWarmupTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_automatic_warmup_total",
		Help: "Count of backend selection entries currently in automatic warm-up by capability and offering.",
	}, []string{"capability", "offering"})

	backendSelectionCooldownTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_cooldown_total",
		Help: "Count of backend selection entries currently cooling down by capability and offering.",
	}, []string{"capability", "offering"})

	backendSelectionAverageEffectiveScore = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_average_effective_score",
		Help: "Average effective selection score by capability and offering.",
	}, []string{"capability", "offering"})

	backendSelectionAverageRecentWindowAgeSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_backend_selection_average_recent_window_age_seconds",
		Help: "Average recent outcome window age in seconds by capability and offering.",
	}, []string{"capability", "offering"})

	scoringSetting = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_scoring_setting",
		Help: "Active Pool scoring settings after defaults and reload have been applied.",
	}, []string{"setting"})

	backendOutcomeIngestTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_backend_outcome_ingest_total",
		Help: "Total backend outcome ingests by capability, offering, and outcome class.",
	}, []string{"capability", "offering", "outcome"})

	syntheticProbeRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_synthetic_probe_runs_total",
		Help: "Total synthetic probe runs by overall result.",
	}, []string{"result"})

	syntheticProbeRunDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "livepeer_pool_synthetic_probe_run_duration_seconds",
		Help:    "Wall-clock duration of synthetic probe runs by overall result.",
		Buckets: prometheus.DefBuckets,
	}, []string{"result"})

	syntheticProbeResultsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_synthetic_probe_results_total",
		Help: "Total synthetic probe results by capability, offering, status, and reason.",
	}, []string{"capability", "offering", "status", "reason"})

	workReceiptStatusTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_work_receipt_status_total",
		Help: "Current count of persisted work receipts by status.",
	}, []string{"status"})

	roundReceiptTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_pool_round_receipt_total",
		Help: "Current count of persisted round receipts.",
	})

	payoutIntentStatusTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "livepeer_pool_payout_intent_status_total",
		Help: "Current count of persisted payout intents by status.",
	}, []string{"status"})

	payoutIntentRetryCountMax = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_pool_payout_intent_retry_count_max",
		Help: "Maximum retry_count observed across persisted payout intents. Indicates retry-storm pressure when sustained > 1.",
	})

	payoutIntentWithRetriesTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_pool_payout_intent_with_retries_total",
		Help: "Count of persisted payout intents with retry_count > 0.",
	})

	payoutIntentFailedAgeSecondsMax = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "livepeer_pool_payout_intent_failed_age_seconds_max",
		Help: "Age in seconds of the oldest payout intent currently in a failure status. Zero when no intent is failed.",
	})

	receiptWriteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_receipt_write_total",
		Help: "Total receipt-write actions handled by the controller admin plane.",
	}, []string{"kind", "status"})

	payoutIntentActionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_pool_payout_intent_action_total",
		Help: "Total payout-intent admin actions by action and resulting status.",
	}, []string{"action", "status"})
)

type selectionAggregate struct {
	effectiveScoreSum  float64
	recentWindowAgeSum float64
	count              int
	automaticWarmup    int
	cooldown           int
}

func MetricsHandler() http.Handler { return promhttp.Handler() }

func UpdateScoringSettings(scoring config.Scoring) {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	scoringSetting.WithLabelValues("cooldown_duration_seconds").Set(float64(scoring.CooldownDurationMS) / 1000.0)
	scoringSetting.WithLabelValues("cooldown_failure_trigger").Set(float64(scoring.CooldownFailureTrigger))
	scoringSetting.WithLabelValues("ema_half_life_seconds").Set(float64(scoring.EMAHalfLifeMS) / 1000.0)
	scoringSetting.WithLabelValues("latency_target_ms").Set(scoring.LatencyTargetMS)
	scoringSetting.WithLabelValues("recent_window_stale_after_seconds").Set(float64(scoring.RecentWindowStaleAfterMS) / 1000.0)
	scoringSetting.WithLabelValues("window_score_weight").Set(scoring.WindowScoreWeight)
	scoringSetting.WithLabelValues("ema_score_weight").Set(scoring.EMAScoreWeight)
	scoringSetting.WithLabelValues("warmup_modifier").Set(scoring.WarmupModifier)
	scoringSetting.WithLabelValues("warmup_exit_samples").Set(float64(scoring.WarmupExitSamples))
	scoringSetting.WithLabelValues("top_degraded_limit").Set(float64(scoring.TopDegradedLimit))
	scoringSetting.WithLabelValues("top_excluded_limit").Set(float64(scoring.TopExcludedLimit))
	scoringSetting.WithLabelValues("worst_offerings_limit").Set(float64(scoring.WorstOfferingsLimit))
	scoringSetting.WithLabelValues("public_worst_offerings_limit").Set(float64(scoring.PublicWorstOfferingsLimit))
}

func UpdateBackendSelectionSnapshot(items []types.BackendSelectionState, now time.Time) {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	backendSelectionStateTotal.Reset()
	backendSelectionRoutingReasonTotal.Reset()
	backendSelectionExclusionReasonTotal.Reset()
	backendSelectionAutomaticWarmupTotal.Reset()
	backendSelectionCooldownTotal.Reset()
	backendSelectionAverageEffectiveScore.Reset()
	backendSelectionAverageRecentWindowAgeSeconds.Reset()

	aggregates := map[string]*selectionAggregate{}

	for _, item := range items {
		labels := []string{item.CapabilityID, item.OfferingID}
		backendSelectionStateTotal.WithLabelValues(item.CapabilityID, item.OfferingID, item.State).Inc()
		if item.RoutingReason != "" {
			backendSelectionRoutingReasonTotal.WithLabelValues(item.CapabilityID, item.OfferingID, item.RoutingReason).Inc()
		}
		if item.ExclusionReason != "" {
			backendSelectionExclusionReasonTotal.WithLabelValues(item.CapabilityID, item.OfferingID, item.ExclusionReason).Inc()
		}
		key := labels[0] + "|" + labels[1]
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &selectionAggregate{}
			aggregates[key] = aggregate
		}
		aggregate.count++
		aggregate.effectiveScoreSum += item.EffectiveSelectionScore
		aggregate.recentWindowAgeSum += item.RecentWindowAgeSeconds
		if item.AutomaticWarmup {
			aggregate.automaticWarmup++
		}
		if item.CooldownUntil != nil && item.CooldownUntil.UTC().After(now.UTC()) {
			aggregate.cooldown++
		}
	}

	for key, aggregate := range aggregates {
		capability, offering, _ := splitKey(key)
		if aggregate.count > 0 {
			backendSelectionAverageEffectiveScore.WithLabelValues(capability, offering).Set(aggregate.effectiveScoreSum / float64(aggregate.count))
			backendSelectionAverageRecentWindowAgeSeconds.WithLabelValues(capability, offering).Set(aggregate.recentWindowAgeSum / float64(aggregate.count))
		}
		backendSelectionAutomaticWarmupTotal.WithLabelValues(capability, offering).Set(float64(aggregate.automaticWarmup))
		backendSelectionCooldownTotal.WithLabelValues(capability, offering).Set(float64(aggregate.cooldown))
	}
}

func RecordBackendOutcomeIngest(outcome types.BackendOutcome) {
	backendOutcomeIngestTotal.WithLabelValues(
		outcome.CapabilityID,
		outcome.OfferingID,
		metricLabel(outcome.Outcome),
	).Inc()
}

func RecordSyntheticProbeRunSummary(summaryResults []ProbeResultMetric, duration time.Duration, result string) {
	syntheticProbeRunsTotal.WithLabelValues(metricLabel(result)).Inc()
	syntheticProbeRunDuration.WithLabelValues(metricLabel(result)).Observe(duration.Seconds())
	for _, item := range summaryResults {
		syntheticProbeResultsTotal.WithLabelValues(
			item.CapabilityID,
			item.OfferingID,
			metricLabel(item.Status),
			metricLabel(item.Reason),
		).Inc()
	}
}

func UpdateAccountingSnapshot(workReceipts []types.WorkReceipt, roundReceipts []types.RoundReceipt, payoutIntents []types.PayoutIntent) {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	workReceiptStatusTotal.Reset()
	roundReceiptTotal.Set(float64(len(roundReceipts)))
	payoutIntentStatusTotal.Reset()

	for _, receipt := range workReceipts {
		workReceiptStatusTotal.WithLabelValues(metricLabel(receipt.Status)).Inc()
	}

	var (
		maxRetryCount   uint64
		withRetries     int
		oldestFailedAge float64
		now             = time.Now()
	)
	for _, intent := range payoutIntents {
		payoutIntentStatusTotal.WithLabelValues(metricLabel(intent.Status)).Inc()
		if intent.RetryCount > maxRetryCount {
			maxRetryCount = intent.RetryCount
		}
		if intent.RetryCount > 0 {
			withRetries++
		}
		if !intent.FailedAt.IsZero() && isUnresolvedFailureStatus(intent.Status) {
			if age := now.Sub(intent.FailedAt).Seconds(); age > oldestFailedAge {
				oldestFailedAge = age
			}
		}
	}
	payoutIntentRetryCountMax.Set(float64(maxRetryCount))
	payoutIntentWithRetriesTotal.Set(float64(withRetries))
	payoutIntentFailedAgeSecondsMax.Set(oldestFailedAge)
}

// isUnresolvedFailureStatus identifies payout-intent statuses where a
// FailedAt timestamp still represents an open operator concern. A
// payment that was failed once but later requeued and paid is no
// longer "unresolved"; only intents whose current status is itself a
// failure-bucket should contribute to the failed-age gauge.
func isUnresolvedFailureStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "stale_failed", "requeue_failed", "lease_expired":
		return true
	}
	return false
}

func RecordReceiptWrite(kind, status string, count int) {
	if count <= 0 {
		return
	}
	receiptWriteTotal.WithLabelValues(metricLabel(kind), metricLabel(status)).Add(float64(count))
}

func RecordPayoutIntentAction(action, status string, count int) {
	if count <= 0 {
		return
	}
	payoutIntentActionTotal.WithLabelValues(metricLabel(action), metricLabel(status)).Add(float64(count))
}

func TestWorkReceiptStatusGauge(status string) prometheus.Gauge {
	return workReceiptStatusTotal.WithLabelValues(metricLabel(status))
}

func TestRoundReceiptGauge() prometheus.Gauge {
	return roundReceiptTotal
}

func TestPayoutIntentStatusGauge(status string) prometheus.Gauge {
	return payoutIntentStatusTotal.WithLabelValues(metricLabel(status))
}

type ProbeResultMetric struct {
	CapabilityID string
	OfferingID   string
	Status       string
	Reason       string
}

func NewProbeResultMetric(capabilityID, offeringID, status, reason string) ProbeResultMetric {
	return ProbeResultMetric{
		CapabilityID: capabilityID,
		OfferingID:   offeringID,
		Status:       status,
		Reason:       reason,
	}
}

func splitKey(value string) (string, string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] == '|' {
			return value[:i], value[i+1:], true
		}
	}
	return "", "", false
}

func metricLabel(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
