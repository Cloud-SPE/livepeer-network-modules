package repo

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const backendSelectionStatesBucket = "backend_selection_states"
const backendSelectionRuntimeBucket = "backend_selection_runtime"

const (
	realOutcomeWindow = 5 * time.Minute
	neutralScore      = 0.5
)

var defaultBackendSelectionSettings = backendSelectionSettings{
	CooldownDuration:       5 * time.Minute,
	CooldownFailureTrigger: 5,
	EMAHalfLife:            24 * time.Hour,
	LatencyTargetMS:        1200.0,
	RecentWindowStaleAfter: 5 * time.Minute,
	WindowScoreWeight:      0.7,
	EMAScoreWeight:         0.3,
	WarmupModifier:         0.25,
	WarmupExitSamples:      20,
}

var backendSelectionSettingsState = struct {
	sync.RWMutex
	value backendSelectionSettings
}{
	value: defaultBackendSelectionSettings,
}

type backendSelectionSettings struct {
	CooldownDuration       time.Duration
	CooldownFailureTrigger int
	EMAHalfLife            time.Duration
	LatencyTargetMS        float64
	RecentWindowStaleAfter time.Duration
	WindowScoreWeight      float64
	EMAScoreWeight         float64
	WarmupModifier         float64
	WarmupExitSamples      int
}

type realOutcomeSample struct {
	Outcome         string    `json:"outcome"`
	OccurredAt      time.Time `json:"occurred_at"`
	LatencyMetricMS uint64    `json:"latency_metric_ms,omitempty"`
}

type backendSelectionRuntime struct {
	Outcomes            []realOutcomeSample `json:"outcomes,omitempty"`
	EMASuccessScore     float64             `json:"ema_success_score,omitempty"`
	EMALatencyScore     float64             `json:"ema_latency_score,omitempty"`
	EMALastUpdatedAt    *time.Time          `json:"ema_last_updated_at,omitempty"`
	BackendFailureTimes []time.Time         `json:"backend_failure_times,omitempty"`
}

func backendSelectionStateKey(memberEthAddress, backendID, capabilityID, offeringID string) string {
	return fmt.Sprintf("%s|%s|%s|%s", memberEthAddress, backendID, capabilityID, offeringID)
}

func ApplyBackendSelectionSettings(scoring config.Scoring) {
	backendSelectionSettingsState.Lock()
	defer backendSelectionSettingsState.Unlock()
	backendSelectionSettingsState.value = backendSelectionSettings{
		CooldownDuration:       time.Duration(scoring.CooldownDurationMS) * time.Millisecond,
		CooldownFailureTrigger: scoring.CooldownFailureTrigger,
		EMAHalfLife:            time.Duration(scoring.EMAHalfLifeMS) * time.Millisecond,
		LatencyTargetMS:        scoring.LatencyTargetMS,
		RecentWindowStaleAfter: time.Duration(scoring.RecentWindowStaleAfterMS) * time.Millisecond,
		WindowScoreWeight:      scoring.WindowScoreWeight,
		EMAScoreWeight:         scoring.EMAScoreWeight,
		WarmupModifier:         scoring.WarmupModifier,
		WarmupExitSamples:      scoring.WarmupExitSamples,
	}
}

func currentBackendSelectionSettings() backendSelectionSettings {
	backendSelectionSettingsState.RLock()
	defer backendSelectionSettingsState.RUnlock()
	return backendSelectionSettingsState.value
}

func defaultBackendSelectionState(memberEthAddress string, backend config.Backend, offering config.Offering, now time.Time) types.BackendSelectionState {
	return defaultBackendSelectionStateValues(memberEthAddress, backend.ID, offering.CapabilityID, offering.OfferingID, now)
}

func defaultBackendSelectionStateValues(memberEthAddress, backendID, capabilityID, offeringID string, now time.Time) types.BackendSelectionState {
	return types.BackendSelectionState{
		Key:                     backendSelectionStateKey(memberEthAddress, backendID, capabilityID, offeringID),
		MemberEthAddress:        memberEthAddress,
		BackendID:               backendID,
		CapabilityID:            capabilityID,
		OfferingID:              offeringID,
		State:                   types.BackendSelectionStateEligible,
		SyntheticConfidence:     0.5,
		RealSuccessScore:        0.5,
		RealLatencyScore:        0.5,
		AutomaticWarmup:         false,
		WarmupSource:            "none",
		WarmupModifier:          1.0,
		EffectiveSelectionScore: 0.5,
		RoutingReason:           "pool_eligible",
		MaxShareCap:             0.0,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
}

func (r *StateRepo) initBackendSelectionBuckets(tx *bolt.Tx) error {
	_, err := tx.CreateBucketIfNotExists([]byte(backendSelectionStatesBucket))
	if err != nil {
		return err
	}
	_, err = tx.CreateBucketIfNotExists([]byte(backendSelectionRuntimeBucket))
	return err
}

func (r *StateRepo) SaveBackendSelectionState(state types.BackendSelectionState) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if state.Key == "" {
		state.Key = backendSelectionStateKey(state.MemberEthAddress, state.BackendID, state.CapabilityID, state.OfferingID)
	}
	if state.Key == "" {
		return fmt.Errorf("backend selection state key is required")
	}
	now := time.Now().UTC()
	if state.CreatedAt.IsZero() {
		state.CreatedAt = now
	}
	state = normalizeBackendSelectionState(state, now)
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = now
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal backend selection state: %w", err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(backendSelectionStatesBucket)).Put([]byte(state.Key), raw)
	})
}

func (r *StateRepo) ListBackendSelectionStates() ([]types.BackendSelectionState, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]types.BackendSelectionState, 0)
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(backendSelectionStatesBucket)).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var state types.BackendSelectionState
			if err := json.Unmarshal(v, &state); err != nil {
				return err
			}
			out = append(out, state)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list backend selection states: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MemberEthAddress != out[j].MemberEthAddress {
			return out[i].MemberEthAddress < out[j].MemberEthAddress
		}
		if out[i].BackendID != out[j].BackendID {
			return out[i].BackendID < out[j].BackendID
		}
		if out[i].CapabilityID != out[j].CapabilityID {
			return out[i].CapabilityID < out[j].CapabilityID
		}
		return out[i].OfferingID < out[j].OfferingID
	})
	return out, nil
}

func (r *StateRepo) GetBackendSelectionState(memberEthAddress, backendID, capabilityID, offeringID string) (types.BackendSelectionState, error) {
	if r == nil || r.db == nil {
		return types.BackendSelectionState{}, fmt.Errorf("repo is not open")
	}
	key := backendSelectionStateKey(memberEthAddress, backendID, capabilityID, offeringID)
	var out types.BackendSelectionState
	err := r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(backendSelectionStatesBucket)).Get([]byte(key))
		if raw == nil {
			return fmt.Errorf("backend selection state %q: not found", key)
		}
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return types.BackendSelectionState{}, err
	}
	return out, nil
}

func (r *StateRepo) SyncBackendSelectionStatesFromEntities(offers []types.Offer, members []types.MemberRecord, backends []types.MemberBackend, assignments []types.Assignment) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	now := time.Now().UTC()
	offersByID := make(map[string]types.Offer, len(offers))
	for _, offer := range offers {
		offersByID[offer.ID] = offer
	}
	membersByID := make(map[string]types.MemberRecord, len(members))
	for _, member := range members {
		membersByID[member.ID] = member
	}
	backendsByID := make(map[string]types.MemberBackend, len(backends))
	for _, backend := range backends {
		backendsByID[backend.ID] = backend
	}

	type selectionIdentity struct {
		MemberEthAddress string
		BackendID        string
		CapabilityID     string
		OfferingID       string
	}
	items := make([]selectionIdentity, 0)
	for _, assignment := range assignments {
		if assignment.Status != types.AssignmentStatusActive {
			continue
		}
		offer, ok := offersByID[assignment.OfferID]
		if !ok || offer.Status != types.OfferStatusActive {
			continue
		}
		backend, ok := backendsByID[assignment.MemberBackendID]
		if !ok || backend.Status != types.BackendStatusActive {
			continue
		}
		member, ok := membersByID[backend.MemberID]
		if !ok || member.Status != types.MemberStatusActive {
			continue
		}
		items = append(items, selectionIdentity{
			MemberEthAddress: member.EthAddress,
			BackendID:        backend.ID,
			CapabilityID:     offer.CapabilityID,
			OfferingID:       offer.OfferingID,
		})
	}

	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(backendSelectionStatesBucket))
		for _, item := range items {
			key := backendSelectionStateKey(item.MemberEthAddress, item.BackendID, item.CapabilityID, item.OfferingID)
			if raw := b.Get([]byte(key)); raw != nil {
				var existing types.BackendSelectionState
				if err := json.Unmarshal(raw, &existing); err != nil {
					return fmt.Errorf("decode backend selection state %q: %w", key, err)
				}
				changed := false
				if existing.Key == "" {
					existing.Key = key
					changed = true
				}
				if existing.MemberEthAddress == "" {
					existing.MemberEthAddress = item.MemberEthAddress
					changed = true
				}
				if existing.BackendID == "" {
					existing.BackendID = item.BackendID
					changed = true
				}
				if existing.CapabilityID == "" {
					existing.CapabilityID = item.CapabilityID
					changed = true
				}
				if existing.OfferingID == "" {
					existing.OfferingID = item.OfferingID
					changed = true
				}
				if existing.State == "" {
					existing.State = types.BackendSelectionStateEligible
					changed = true
				}
				if existing.CreatedAt.IsZero() {
					existing.CreatedAt = now
					changed = true
				}
				if changed {
					existing.UpdatedAt = now
					next, err := json.Marshal(existing)
					if err != nil {
						return fmt.Errorf("marshal backend selection state %q: %w", key, err)
					}
					if err := b.Put([]byte(key), next); err != nil {
						return err
					}
				}
				continue
			}
			state := defaultBackendSelectionStateValues(item.MemberEthAddress, item.BackendID, item.CapabilityID, item.OfferingID, now)
			next, err := json.Marshal(state)
			if err != nil {
				return fmt.Errorf("marshal backend selection state %q: %w", key, err)
			}
			if err := b.Put([]byte(key), next); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *StateRepo) ApplyBackendOutcome(outcome types.BackendOutcome) (types.BackendSelectionState, error) {
	if r == nil || r.db == nil {
		return types.BackendSelectionState{}, fmt.Errorf("repo is not open")
	}
	outcome.MemberEthAddress = strings.TrimSpace(outcome.MemberEthAddress)
	outcome.BackendID = strings.TrimSpace(outcome.BackendID)
	outcome.CapabilityID = strings.TrimSpace(outcome.CapabilityID)
	outcome.OfferingID = strings.TrimSpace(outcome.OfferingID)
	outcome.Outcome = strings.TrimSpace(outcome.Outcome)
	if outcome.MemberEthAddress == "" || outcome.BackendID == "" || outcome.CapabilityID == "" || outcome.OfferingID == "" {
		return types.BackendSelectionState{}, fmt.Errorf("member_eth_address, backend_id, capability_id, and offering_id are required")
	}
	if !isSupportedBackendOutcome(outcome.Outcome) {
		return types.BackendSelectionState{}, fmt.Errorf("unsupported outcome %q", outcome.Outcome)
	}

	key := backendSelectionStateKey(outcome.MemberEthAddress, outcome.BackendID, outcome.CapabilityID, outcome.OfferingID)
	occurredAt := time.Now().UTC()
	settings := currentBackendSelectionSettings()
	if outcome.OccurredAt != nil && !outcome.OccurredAt.IsZero() {
		occurredAt = outcome.OccurredAt.UTC()
	}

	var updated types.BackendSelectionState
	err := r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(backendSelectionStatesBucket))
		runtimeBucket := tx.Bucket([]byte(backendSelectionRuntimeBucket))
		raw := b.Get([]byte(key))
		if raw == nil {
			return fmt.Errorf("backend selection state %q: not found", key)
		}
		if err := json.Unmarshal(raw, &updated); err != nil {
			return fmt.Errorf("decode backend selection state %q: %w", key, err)
		}
		updated.LastRealOutcomeAt = &occurredAt
		runtimeState, err := loadBackendSelectionRuntime(runtimeBucket, key)
		if err != nil {
			return err
		}
		runtimeState = decayRuntime(runtimeState, occurredAt)
		runtimeState.Outcomes = pruneOutcomeSamples(runtimeState.Outcomes, occurredAt.Add(-realOutcomeWindow))
		runtimeState.Outcomes = append(runtimeState.Outcomes, realOutcomeSample{
			Outcome:         outcome.Outcome,
			OccurredAt:      occurredAt,
			LatencyMetricMS: outcome.LatencyMetricMS,
		})
		runtimeState.BackendFailureTimes = pruneTimes(runtimeState.BackendFailureTimes, occurredAt.Add(-realOutcomeWindow))
		if outcome.Outcome == types.BackendOutcomeBackendFailure {
			runtimeState.BackendFailureTimes = append(runtimeState.BackendFailureTimes, occurredAt)
		}
		windowSuccess := computeWindowSuccessScore(runtimeState.Outcomes)
		windowLatency := computeWindowLatencyScore(runtimeState.Outcomes)
		runtimeState.EMASuccessScore = advanceEMA(runtimeState.EMASuccessScore, windowSuccess, occurredAt, runtimeState.EMALastUpdatedAt)
		runtimeState.EMALatencyScore = advanceEMA(runtimeState.EMALatencyScore, windowLatency, occurredAt, runtimeState.EMALastUpdatedAt)
		runtimeState.EMALastUpdatedAt = &occurredAt
		updated.RealSuccessScore = combineWindowAndEMA(windowSuccess, runtimeState.EMASuccessScore)
		updated.RealLatencyScore = combineWindowAndEMA(windowLatency, runtimeState.EMALatencyScore)
		applyRecentOutcomeCounts(&updated, runtimeState, occurredAt)
		if settings.CooldownFailureTrigger > 0 && len(runtimeState.BackendFailureTimes) >= settings.CooldownFailureTrigger {
			cooldownUntil := occurredAt.Add(settings.CooldownDuration)
			updated.CooldownUntil = &cooldownUntil
			if updated.ExclusionReason == "" || updated.ExclusionReason == "pool_cooldown" {
				updated.ExclusionReason = "pool_cooldown"
			}
		}
		updated = normalizeBackendSelectionState(updated, occurredAt)
		if shouldExitWarmup(updated, runtimeState.Outcomes, settings) {
			updated.AutomaticWarmup = false
			updated = normalizeBackendSelectionState(updated, occurredAt)
		}
		updated.UpdatedAt = time.Now().UTC()
		next, err := json.Marshal(updated)
		if err != nil {
			return fmt.Errorf("marshal backend selection state %q: %w", key, err)
		}
		if err := b.Put([]byte(key), next); err != nil {
			return err
		}
		return saveBackendSelectionRuntime(runtimeBucket, key, runtimeState)
	})
	if err != nil {
		return types.BackendSelectionState{}, err
	}
	return updated, nil
}

func (r *StateRepo) ApplySyntheticProbeObservation(observation types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
	if r == nil || r.db == nil {
		return types.BackendSelectionState{}, fmt.Errorf("repo is not open")
	}
	observation.MemberEthAddress = strings.TrimSpace(observation.MemberEthAddress)
	observation.BackendID = strings.TrimSpace(observation.BackendID)
	observation.CapabilityID = strings.TrimSpace(observation.CapabilityID)
	observation.OfferingID = strings.TrimSpace(observation.OfferingID)
	observation.Result = strings.TrimSpace(observation.Result)
	if observation.MemberEthAddress == "" || observation.BackendID == "" || observation.CapabilityID == "" || observation.OfferingID == "" {
		return types.BackendSelectionState{}, fmt.Errorf("member_eth_address, backend_id, capability_id, and offering_id are required")
	}
	observedAt := observation.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	key := backendSelectionStateKey(observation.MemberEthAddress, observation.BackendID, observation.CapabilityID, observation.OfferingID)

	var updated types.BackendSelectionState
	err := r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(backendSelectionStatesBucket))
		raw := b.Get([]byte(key))
		if raw == nil {
			return fmt.Errorf("backend selection state %q: not found", key)
		}
		if err := json.Unmarshal(raw, &updated); err != nil {
			return fmt.Errorf("decode backend selection state %q: %w", key, err)
		}
		hadSyntheticObservation := updated.LastSyntheticAt != nil && !updated.LastSyntheticAt.IsZero()
		updated.LastSyntheticAt = &observedAt
		if observation.Success {
			updated.LastSyntheticResult = defaultSyntheticResult(observation.Result, "success")
			updated.ConsecutiveSyntheticFailures = 0
			updated.SyntheticConfidence = clampUnit(updated.SyntheticConfidence + 0.10)
			if !hadSyntheticObservation {
				updated.AutomaticWarmup = true
			}
			if updated.State == types.BackendSelectionStateExcluded && updated.ExclusionReason == "synthetic_probe_failure_threshold" {
				updated.State = types.BackendSelectionStateEligible
				updated.ExclusionReason = ""
				updated.AutomaticWarmup = true
			}
		} else {
			updated.LastSyntheticResult = defaultSyntheticResult(observation.Result, "failure")
			updated.ConsecutiveSyntheticFailures++
			updated.SyntheticConfidence = clampUnit(updated.SyntheticConfidence - 0.15)
			if updated.ConsecutiveSyntheticFailures >= 3 {
				updated.State = types.BackendSelectionStateExcluded
				updated.ExclusionReason = "synthetic_probe_failure_threshold"
			}
		}
		updated = normalizeBackendSelectionState(updated, observedAt)
		updated.UpdatedAt = time.Now().UTC()
		next, err := json.Marshal(updated)
		if err != nil {
			return fmt.Errorf("marshal backend selection state %q: %w", key, err)
		}
		return b.Put([]byte(key), next)
	})
	if err != nil {
		return types.BackendSelectionState{}, err
	}
	return updated, nil
}

func isSupportedBackendOutcome(value string) bool {
	switch value {
	case types.BackendOutcomeSuccess,
		types.BackendOutcomeBackendFailure,
		types.BackendOutcomeCallerFailure,
		types.BackendOutcomePolicyTermination,
		types.BackendOutcomePaymentTermination:
		return true
	default:
		return false
	}
}

func recomputeEffectiveSelectionScore(state types.BackendSelectionState) float64 {
	composite := (clampUnit(state.SyntheticConfidence) + clampUnit(state.RealSuccessScore) + clampUnit(state.RealLatencyScore)) / 3.0
	return clampUnit(composite * effectiveWarmupModifier(state))
}

func normalizeBackendSelectionState(state types.BackendSelectionState, now time.Time) types.BackendSelectionState {
	state.SyntheticConfidence = clampUnit(state.SyntheticConfidence)
	state.RealSuccessScore = clampUnit(state.RealSuccessScore)
	state.RealLatencyScore = clampUnit(state.RealLatencyScore)
	if state.WarmupOverride != nil {
		override := maxFloat(*state.WarmupOverride, 0)
		state.WarmupOverride = &override
	}
	state.WarmupModifier = effectiveWarmupModifier(state)
	state.WarmupSource = warmupSource(state)
	state.EffectiveSelectionScore = recomputeEffectiveSelectionScore(state)

	switch {
	case state.State == types.BackendSelectionStateQuarantined:
		state.RoutingReason = backendSelectionRoutingReason(state)
		return state
	case state.State == types.BackendSelectionStateExcluded &&
		state.ExclusionReason != "" &&
		state.ExclusionReason != "score_below_floor" &&
		state.ExclusionReason != "pool_cooldown":
		state.State = types.BackendSelectionStateExcluded
		state.RoutingReason = backendSelectionRoutingReason(state)
		return state
	}

	if state.CooldownUntil != nil {
		cooldownUntil := state.CooldownUntil.UTC()
		if cooldownUntil.After(now) {
			state.CooldownUntil = &cooldownUntil
			state.State = types.BackendSelectionStateExcluded
			if state.ExclusionReason == "" || state.ExclusionReason == "pool_cooldown" {
				state.ExclusionReason = "pool_cooldown"
			}
			state.RoutingReason = backendSelectionRoutingReason(state)
			return state
		}
		state.CooldownUntil = nil
		if state.ExclusionReason == "pool_cooldown" {
			state.ExclusionReason = ""
			state.AutomaticWarmup = true
		}
	}

	switch {
	case state.EffectiveSelectionScore < 0.10:
		state.State = types.BackendSelectionStateExcluded
		if state.ExclusionReason == "" {
			state.ExclusionReason = "score_below_floor"
		}
	case state.EffectiveSelectionScore < 0.30:
		state.State = types.BackendSelectionStateDegraded
		if state.ExclusionReason == "score_below_floor" {
			state.ExclusionReason = ""
		}
	default:
		state.State = types.BackendSelectionStateEligible
		if state.ExclusionReason == "score_below_floor" {
			state.ExclusionReason = ""
		}
	}
	state.RoutingReason = backendSelectionRoutingReason(state)

	return state
}

func backendSelectionRoutingReason(state types.BackendSelectionState) string {
	settings := currentBackendSelectionSettings()
	switch state.State {
	case types.BackendSelectionStateQuarantined:
		return firstNonEmpty(state.ExclusionReason, "pool_quarantined")
	case types.BackendSelectionStateExcluded:
		return firstNonEmpty(state.ExclusionReason, "pool_excluded")
	case types.BackendSelectionStateDegraded:
		switch {
		case state.WarmupSource == "manual_override":
			return "pool_warmup_manual_override"
		case state.AutomaticWarmup:
			return "pool_warmup"
		case settings.RecentWindowStaleAfter > 0 && state.RecentWindowAgeSeconds >= settings.RecentWindowStaleAfter.Seconds():
			return "pool_degraded_stale_sample_window"
		case state.EffectiveSelectionScore < 0.30:
			return "pool_degraded_low_score"
		default:
			return "pool_degraded"
		}
	case types.BackendSelectionStateEligible:
		switch {
		case state.WarmupSource == "manual_override":
			return "pool_warmup_manual_override"
		case state.AutomaticWarmup:
			return "pool_warmup"
		case settings.RecentWindowStaleAfter > 0 && state.RecentWindowAgeSeconds >= settings.RecentWindowStaleAfter.Seconds():
			return "pool_eligible_stale_sample_window"
		default:
			return "pool_eligible"
		}
	default:
		return ""
	}
}

func firstNonEmpty(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func clampUnit(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func defaultSyntheticResult(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func loadBackendSelectionRuntime(bucket *bolt.Bucket, key string) (backendSelectionRuntime, error) {
	var runtimeState backendSelectionRuntime
	if bucket == nil {
		return runtimeState, fmt.Errorf("backend selection runtime bucket is not initialized")
	}
	raw := bucket.Get([]byte(key))
	if raw == nil {
		return runtimeState, nil
	}
	if err := json.Unmarshal(raw, &runtimeState); err != nil {
		return backendSelectionRuntime{}, fmt.Errorf("decode backend selection runtime %q: %w", key, err)
	}
	return runtimeState, nil
}

func saveBackendSelectionRuntime(bucket *bolt.Bucket, key string, runtimeState backendSelectionRuntime) error {
	if bucket == nil {
		return fmt.Errorf("backend selection runtime bucket is not initialized")
	}
	raw, err := json.Marshal(runtimeState)
	if err != nil {
		return fmt.Errorf("marshal backend selection runtime %q: %w", key, err)
	}
	return bucket.Put([]byte(key), raw)
}

func pruneTimes(values []time.Time, cutoff time.Time) []time.Time {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if value.UTC().Before(cutoff) {
			continue
		}
		out = append(out, value.UTC())
	}
	return out
}

func pruneOutcomeSamples(values []realOutcomeSample, cutoff time.Time) []realOutcomeSample {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if value.OccurredAt.UTC().Before(cutoff) {
			continue
		}
		value.OccurredAt = value.OccurredAt.UTC()
		out = append(out, value)
	}
	return out
}

func computeWindowSuccessScore(values []realOutcomeSample) float64 {
	var successes, failures int
	for _, value := range values {
		switch value.Outcome {
		case types.BackendOutcomeSuccess:
			successes++
		case types.BackendOutcomeBackendFailure:
			failures++
		}
	}
	total := successes + failures
	if total == 0 {
		return neutralScore
	}
	return clampUnit(float64(successes) / float64(total))
}

func computeWindowLatencyScore(values []realOutcomeSample) float64 {
	settings := currentBackendSelectionSettings()
	latencies := make([]uint64, 0, len(values))
	for _, value := range values {
		if value.LatencyMetricMS == 0 {
			continue
		}
		switch value.Outcome {
		case types.BackendOutcomeSuccess, types.BackendOutcomeCallerFailure:
			latencies = append(latencies, value.LatencyMetricMS)
		}
	}
	if len(latencies) == 0 {
		return neutralScore
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	idx := int(math.Ceil(0.95*float64(len(latencies)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}
	p95 := float64(latencies[idx])
	if p95 <= 0 {
		return neutralScore
	}
	return clampUnit(math.Min(settings.LatencyTargetMS/p95, 1.0))
}

func advanceEMA(current, observed float64, now time.Time, lastUpdatedAt *time.Time) float64 {
	settings := currentBackendSelectionSettings()
	current = clampUnit(current)
	observed = clampUnit(observed)
	if lastUpdatedAt == nil || lastUpdatedAt.IsZero() {
		return observed
	}
	elapsed := now.Sub(lastUpdatedAt.UTC())
	if elapsed <= 0 {
		return observed
	}
	alpha := 1 - math.Exp(-math.Ln2*elapsed.Seconds()/settings.EMAHalfLife.Seconds())
	decayed := driftTowardNeutral(current, elapsed)
	return clampUnit(((1 - alpha) * decayed) + (alpha * observed))
}

func driftTowardNeutral(current float64, elapsed time.Duration) float64 {
	settings := currentBackendSelectionSettings()
	current = clampUnit(current)
	if elapsed <= 0 {
		return current
	}
	retention := math.Exp(-math.Ln2 * elapsed.Seconds() / settings.EMAHalfLife.Seconds())
	return clampUnit((neutralScore * (1 - retention)) + (current * retention))
}

func combineWindowAndEMA(windowScore, emaScore float64) float64 {
	settings := currentBackendSelectionSettings()
	return clampUnit((settings.WindowScoreWeight * clampUnit(windowScore)) + (settings.EMAScoreWeight * clampUnit(emaScore)))
}

func applyRecentOutcomeCounts(state *types.BackendSelectionState, runtimeState backendSelectionRuntime, observedAt time.Time) {
	if state == nil {
		return
	}
	state.RecentOutcomeCount = len(runtimeState.Outcomes)
	state.RecentRoutableOutcomeCount = routableSampleCount(runtimeState.Outcomes)
	state.RecentBackendFailureCount = len(runtimeState.BackendFailureTimes)
	state.RecentWindowStartedAt = nil
	state.RecentWindowEndedAt = nil
	state.RecentWindowAgeSeconds = 0
	if len(runtimeState.Outcomes) == 0 {
		return
	}
	startedAt := runtimeState.Outcomes[0].OccurredAt.UTC()
	endedAt := runtimeState.Outcomes[0].OccurredAt.UTC()
	for _, sample := range runtimeState.Outcomes[1:] {
		at := sample.OccurredAt.UTC()
		if at.Before(startedAt) {
			startedAt = at
		}
		if at.After(endedAt) {
			endedAt = at
		}
	}
	state.RecentWindowStartedAt = &startedAt
	state.RecentWindowEndedAt = &endedAt
	if observedAt.After(endedAt) {
		state.RecentWindowAgeSeconds = observedAt.Sub(endedAt).Seconds()
	}
}

func shouldExitWarmup(state types.BackendSelectionState, outcomes []realOutcomeSample, settings backendSelectionSettings) bool {
	if !state.AutomaticWarmup {
		return false
	}
	return settings.WarmupExitSamples > 0 && routableSampleCount(outcomes) >= settings.WarmupExitSamples
}

func decayRuntime(runtimeState backendSelectionRuntime, now time.Time) backendSelectionRuntime {
	if runtimeState.EMALastUpdatedAt == nil || runtimeState.EMALastUpdatedAt.IsZero() {
		if runtimeState.EMASuccessScore == 0 {
			runtimeState.EMASuccessScore = neutralScore
		}
		if runtimeState.EMALatencyScore == 0 {
			runtimeState.EMALatencyScore = neutralScore
		}
		return runtimeState
	}
	elapsed := now.Sub(runtimeState.EMALastUpdatedAt.UTC())
	if elapsed <= 0 {
		return runtimeState
	}
	runtimeState.EMASuccessScore = driftTowardNeutral(runtimeState.EMASuccessScore, elapsed)
	runtimeState.EMALatencyScore = driftTowardNeutral(runtimeState.EMALatencyScore, elapsed)
	return runtimeState
}

func routableSampleCount(values []realOutcomeSample) int {
	count := 0
	for _, value := range values {
		switch value.Outcome {
		case types.BackendOutcomeSuccess, types.BackendOutcomeBackendFailure, types.BackendOutcomeCallerFailure:
			count++
		}
	}
	return count
}

func effectiveWarmupModifier(state types.BackendSelectionState) float64 {
	settings := currentBackendSelectionSettings()
	if state.WarmupOverride != nil {
		return maxFloat(*state.WarmupOverride, 0)
	}
	if state.AutomaticWarmup {
		return settings.WarmupModifier
	}
	return 1.0
}

func warmupSource(state types.BackendSelectionState) string {
	if state.WarmupOverride != nil {
		return "manual_override"
	}
	if state.AutomaticWarmup {
		return "automatic"
	}
	return "none"
}
