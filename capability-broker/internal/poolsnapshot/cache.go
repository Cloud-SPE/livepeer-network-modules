package poolsnapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
)

const snapshotPath = "/admin/v1/backend-selection-snapshot"

type Entry struct {
	BackendID                    string    `json:"backend_id"`
	CapabilityID                 string    `json:"capability_id"`
	OfferingID                   string    `json:"offering_id"`
	State                        string    `json:"state,omitempty"`
	ExclusionReason              string    `json:"exclusion_reason,omitempty"`
	RoutingReason                string    `json:"routing_reason,omitempty"`
	SyntheticConfidence          float64   `json:"synthetic_confidence,omitempty"`
	RealSuccessScore             float64   `json:"real_success_score,omitempty"`
	RealLatencyScore             float64   `json:"real_latency_score,omitempty"`
	EffectiveSelectionScore      float64   `json:"effective_selection_score"`
	ConsecutiveSyntheticFailures uint64    `json:"consecutive_synthetic_failures,omitempty"`
	CooldownUntil                time.Time `json:"cooldown_until,omitempty"`
	AutomaticWarmup              bool      `json:"automatic_warmup,omitempty"`
	WarmupOverride               *float64  `json:"warmup_override,omitempty"`
	WarmupSource                 string    `json:"warmup_source,omitempty"`
	WarmupModifier               float64   `json:"warmup_modifier,omitempty"`
	MaxShareCap                  float64   `json:"max_share_cap,omitempty"`
	RecentOutcomeCount           int       `json:"recent_outcome_count,omitempty"`
	RecentRoutableOutcomeCount   int       `json:"recent_routable_outcome_count,omitempty"`
	RecentBackendFailureCount    int       `json:"recent_backend_failure_count,omitempty"`
	RecentWindowStartedAt        time.Time `json:"recent_window_started_at,omitempty"`
	RecentWindowEndedAt          time.Time `json:"recent_window_ended_at,omitempty"`
	RecentWindowAgeSeconds       float64   `json:"recent_window_age_seconds,omitempty"`
	LastSyntheticResult          string    `json:"last_synthetic_result,omitempty"`
	LastSyntheticAt              time.Time `json:"last_synthetic_at,omitempty"`
	LastRealOutcomeAt            time.Time `json:"last_real_outcome_at,omitempty"`
	UpdatedAt                    time.Time `json:"updated_at,omitempty"`
}

type document struct {
	GeneratedAt                   time.Time `json:"generated_at"`
	Version                       int       `json:"version,omitempty"`
	CooldownDurationSeconds       float64   `json:"cooldown_duration_seconds,omitempty"`
	CooldownFailureTrigger        int       `json:"cooldown_failure_trigger,omitempty"`
	EMAHalfLifeSeconds            float64   `json:"ema_half_life_seconds,omitempty"`
	LatencyTargetMS               float64   `json:"latency_target_ms,omitempty"`
	RecentWindowStaleAfterSeconds float64   `json:"recent_window_stale_after_seconds,omitempty"`
	WindowScoreWeight             float64   `json:"window_score_weight,omitempty"`
	EMAScoreWeight                float64   `json:"ema_score_weight,omitempty"`
	WarmupModifier                float64   `json:"warmup_modifier,omitempty"`
	WarmupExitSamples             int       `json:"warmup_exit_samples,omitempty"`
	Entries                       []Entry   `json:"entries"`
}

type Status struct {
	Configured                            bool
	SnapshotStatus                        string
	SnapshotGeneratedAt                   time.Time
	SnapshotFetchedAt                     time.Time
	SnapshotAgeSeconds                    float64
	SnapshotTimeoutSeconds                float64
	SnapshotPollIntervalSeconds           float64
	SnapshotStaleAfterSeconds             float64
	SnapshotExpireAfterSeconds            float64
	SnapshotCooldownDurationSeconds       float64
	SnapshotCooldownFailureTrigger        int
	SnapshotEMAHalfLifeSeconds            float64
	SnapshotLatencyTargetMS               float64
	SnapshotRecentWindowStaleAfterSeconds float64
	SnapshotWindowScoreWeight             float64
	SnapshotEMAScoreWeight                float64
	SnapshotWarmupModifier                float64
	SnapshotWarmupExitSamples             int
	EntryFound                            bool
	EntryState                            string
	EntryExclusionReason                  string
	EntryRoutingReason                    string
	EntrySyntheticConfidence              float64
	EntryRealSuccessScore                 float64
	EntryRealLatencyScore                 float64
	EntryEffectiveSelectionScore          float64
	EntryConsecutiveSyntheticFailures     uint64
	EntryCooldownUntil                    time.Time
	EntryAutomaticWarmup                  bool
	EntryWarmupOverride                   *float64
	EntryWarmupSource                     string
	EntryWarmupModifier                   float64
	EntryMaxShareCap                      float64
	EntryRecentOutcomeCount               int
	EntryRecentRoutableOutcomeCount       int
	EntryRecentBackendFailureCount        int
	EntryRecentWindowStartedAt            time.Time
	EntryRecentWindowEndedAt              time.Time
	EntryRecentWindowAgeSeconds           float64
	EntryLastSyntheticResult              string
	EntryLastSyntheticAt                  time.Time
	EntryLastRealOutcomeAt                time.Time
	LastError                             string
}

type Cache struct {
	configured   bool
	endpoint     string
	client       *http.Client
	auth         *backend.AuthApplier
	authCfg      config.AuthConfig
	pollInterval time.Duration
	staleAfter   time.Duration
	expireAfter  time.Duration
	now          func() time.Time

	mu                            sync.RWMutex
	entries                       map[string]Entry
	generatedAt                   time.Time
	fetchedAt                     time.Time
	timeoutSeconds                float64
	pollIntervalSeconds           float64
	staleAfterSeconds             float64
	expireAfterSeconds            float64
	cooldownDurationSeconds       float64
	cooldownFailureTrigger        int
	emaHalfLifeSeconds            float64
	latencyTargetMS               float64
	recentWindowStaleAfterSeconds float64
	windowScoreWeight             float64
	emaScoreWeight                float64
	warmupModifier                float64
	warmupExitSamples             int
	lastError                     string
}

func New(cfg config.PoolSnapshot, auth *backend.AuthApplier) (*Cache, error) {
	cache := &Cache{
		configured: false,
		entries:    map[string]Entry{},
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	if cfg.URL == "" {
		return cache, nil
	}

	baseURL := strings.TrimRight(cfg.URL, "/")
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	endpoint := u.ResolveReference(&url.URL{Path: snapshotPath}).String()
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	pollInterval := time.Duration(cfg.PollIntervalMS) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	staleAfter := time.Duration(cfg.StaleAfterMS) * time.Millisecond
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	expireAfter := time.Duration(cfg.ExpireAfterMS) * time.Millisecond
	if expireAfter <= 0 {
		expireAfter = 60 * time.Second
	}

	cache.configured = true
	cache.endpoint = endpoint
	cache.client = &http.Client{Timeout: timeout}
	cache.auth = auth
	cache.authCfg = cfg.Auth
	cache.pollInterval = pollInterval
	cache.staleAfter = staleAfter
	cache.expireAfter = expireAfter
	cache.timeoutSeconds = timeout.Seconds()
	cache.pollIntervalSeconds = pollInterval.Seconds()
	cache.staleAfterSeconds = staleAfter.Seconds()
	cache.expireAfterSeconds = expireAfter.Seconds()
	return cache, nil
}

func (c *Cache) Run(ctx context.Context) {
	if c == nil || !c.configured {
		return
	}
	c.poll(ctx)
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.poll(ctx)
		}
	}
}

func (c *Cache) StatusFor(backendID, capabilityID, offeringID string) Status {
	if c == nil || !c.configured {
		return Status{}
	}

	now := c.now()

	c.mu.RLock()
	generatedAt := c.generatedAt
	fetchedAt := c.fetchedAt
	snapshotTimeoutSeconds := c.timeoutSeconds
	snapshotPollIntervalSeconds := c.pollIntervalSeconds
	snapshotStaleAfterSeconds := c.staleAfterSeconds
	snapshotExpireAfterSeconds := c.expireAfterSeconds
	snapshotCooldownDurationSeconds := c.cooldownDurationSeconds
	snapshotCooldownFailureTrigger := c.cooldownFailureTrigger
	snapshotEMAHalfLifeSeconds := c.emaHalfLifeSeconds
	snapshotLatencyTargetMS := c.latencyTargetMS
	snapshotRecentWindowStaleAfterSeconds := c.recentWindowStaleAfterSeconds
	snapshotWindowScoreWeight := c.windowScoreWeight
	snapshotEMAScoreWeight := c.emaScoreWeight
	snapshotWarmupModifier := c.warmupModifier
	snapshotWarmupExitSamples := c.warmupExitSamples
	lastError := c.lastError
	entry, entryFound := c.entries[keyFor(backendID, capabilityID, offeringID)]
	c.mu.RUnlock()

	status := Status{
		Configured:                            true,
		SnapshotGeneratedAt:                   generatedAt,
		SnapshotFetchedAt:                     fetchedAt,
		SnapshotTimeoutSeconds:                snapshotTimeoutSeconds,
		SnapshotPollIntervalSeconds:           snapshotPollIntervalSeconds,
		SnapshotStaleAfterSeconds:             snapshotStaleAfterSeconds,
		SnapshotExpireAfterSeconds:            snapshotExpireAfterSeconds,
		SnapshotCooldownDurationSeconds:       snapshotCooldownDurationSeconds,
		SnapshotCooldownFailureTrigger:        snapshotCooldownFailureTrigger,
		SnapshotEMAHalfLifeSeconds:            snapshotEMAHalfLifeSeconds,
		SnapshotLatencyTargetMS:               snapshotLatencyTargetMS,
		SnapshotRecentWindowStaleAfterSeconds: snapshotRecentWindowStaleAfterSeconds,
		SnapshotWindowScoreWeight:             snapshotWindowScoreWeight,
		SnapshotEMAScoreWeight:                snapshotEMAScoreWeight,
		SnapshotWarmupModifier:                snapshotWarmupModifier,
		SnapshotWarmupExitSamples:             snapshotWarmupExitSamples,
		EntryFound:                            entryFound,
		LastError:                             lastError,
	}
	if entryFound {
		status.EntryState = entry.State
		status.EntryExclusionReason = entry.ExclusionReason
		status.EntryRoutingReason = entry.RoutingReason
		status.EntrySyntheticConfidence = entry.SyntheticConfidence
		status.EntryRealSuccessScore = entry.RealSuccessScore
		status.EntryRealLatencyScore = entry.RealLatencyScore
		status.EntryEffectiveSelectionScore = entry.EffectiveSelectionScore
		status.EntryConsecutiveSyntheticFailures = entry.ConsecutiveSyntheticFailures
		status.EntryCooldownUntil = entry.CooldownUntil
		status.EntryAutomaticWarmup = entry.AutomaticWarmup
		status.EntryWarmupOverride = entry.WarmupOverride
		status.EntryWarmupSource = entry.WarmupSource
		status.EntryWarmupModifier = entry.WarmupModifier
		status.EntryMaxShareCap = entry.MaxShareCap
		status.EntryRecentOutcomeCount = entry.RecentOutcomeCount
		status.EntryRecentRoutableOutcomeCount = entry.RecentRoutableOutcomeCount
		status.EntryRecentBackendFailureCount = entry.RecentBackendFailureCount
		status.EntryRecentWindowStartedAt = entry.RecentWindowStartedAt
		status.EntryRecentWindowEndedAt = entry.RecentWindowEndedAt
		status.EntryRecentWindowAgeSeconds = entry.RecentWindowAgeSeconds
		status.EntryLastSyntheticResult = entry.LastSyntheticResult
		status.EntryLastSyntheticAt = entry.LastSyntheticAt
		status.EntryLastRealOutcomeAt = entry.LastRealOutcomeAt
	}

	switch {
	case fetchedAt.IsZero() && lastError != "":
		status.SnapshotStatus = "fetch_error"
		return status
	case fetchedAt.IsZero():
		status.SnapshotStatus = "bootstrap_pending"
		return status
	}

	ageBase := generatedAt
	if ageBase.IsZero() {
		ageBase = fetchedAt
	}
	age := now.Sub(ageBase).Seconds()
	if age < 0 {
		age = 0
	}
	status.SnapshotAgeSeconds = age

	switch {
	case now.Sub(ageBase) >= c.expireAfter:
		status.SnapshotStatus = "expired"
	case now.Sub(ageBase) >= c.staleAfter:
		status.SnapshotStatus = "stale"
	default:
		status.SnapshotStatus = "fresh"
	}
	return status
}

func (c *Cache) poll(ctx context.Context) {
	if c == nil || !c.configured {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		c.recordError(fmt.Errorf("build request: %w", err))
		return
	}
	if c.auth != nil {
		if err := c.auth.Apply(req.Header, c.authCfg); err != nil {
			c.recordError(fmt.Errorf("apply auth: %w", err))
			return
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.recordError(fmt.Errorf("fetch snapshot: %w", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.recordError(fmt.Errorf("snapshot endpoint returned status %d", resp.StatusCode))
		return
	}
	var doc document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		c.recordError(fmt.Errorf("decode snapshot: %w", err))
		return
	}
	entries := make(map[string]Entry, len(doc.Entries))
	for _, entry := range doc.Entries {
		entries[keyFor(entry.BackendID, entry.CapabilityID, entry.OfferingID)] = entry
	}
	now := c.now()

	c.mu.Lock()
	c.entries = entries
	c.generatedAt = doc.GeneratedAt
	c.fetchedAt = now
	c.cooldownDurationSeconds = doc.CooldownDurationSeconds
	c.cooldownFailureTrigger = doc.CooldownFailureTrigger
	c.emaHalfLifeSeconds = doc.EMAHalfLifeSeconds
	c.latencyTargetMS = doc.LatencyTargetMS
	c.recentWindowStaleAfterSeconds = doc.RecentWindowStaleAfterSeconds
	c.windowScoreWeight = doc.WindowScoreWeight
	c.emaScoreWeight = doc.EMAScoreWeight
	c.warmupModifier = doc.WarmupModifier
	c.warmupExitSamples = doc.WarmupExitSamples
	c.lastError = ""
	c.mu.Unlock()

	metricEntries := make([]observability.PoolSnapshotEntryMetric, 0, len(doc.Entries))
	for _, entry := range doc.Entries {
		metricEntries = append(metricEntries, observability.PoolSnapshotEntryMetric{
			CapabilityID:           entry.CapabilityID,
			OfferingID:             entry.OfferingID,
			State:                  entry.State,
			RoutingReason:          entry.RoutingReason,
			AutomaticWarmup:        entry.AutomaticWarmup,
			CooldownUntil:          entry.CooldownUntil,
			RecentWindowAgeSeconds: entry.RecentWindowAgeSeconds,
		})
	}
	observability.UpdatePoolSnapshotMetrics(
		"fresh",
		doc.GeneratedAt,
		now,
		c.client.Timeout,
		c.pollInterval,
		c.staleAfter,
		c.expireAfter,
		metricEntries,
		now,
	)
}

func (c *Cache) recordError(err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.lastError = err.Error()
	generatedAt := c.generatedAt
	fetchedAt := c.fetchedAt
	c.mu.Unlock()
	observability.UpdatePoolSnapshotMetrics(
		"fetch_error",
		generatedAt,
		fetchedAt,
		c.client.Timeout,
		c.pollInterval,
		c.staleAfter,
		c.expireAfter,
		nil,
		c.now(),
	)
}

func keyFor(backendID, capabilityID, offeringID string) string {
	return backendID + "|" + capabilityID + "|" + offeringID
}
