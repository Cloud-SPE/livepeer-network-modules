package registry

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

type MetadataStatusSource interface {
	StatusFor(capabilityID, offeringID string) (MetadataStatus, bool)
}

type MetadataStatus struct {
	Provider            string
	Applicable          bool
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	LastError           string
	LastResult          string
	ConsecutiveFailures int
}

type healthResponse struct {
	BrokerStatus string                   `json:"broker_status"`
	GeneratedAt  time.Time                `json:"generated_at"`
	Capabilities []healthCapabilityStatus `json:"capabilities"`
}

type healthCapabilityStatus struct {
	ID                   string          `json:"id"`
	OfferingID           string          `json:"offering_id"`
	Status               health.Status   `json:"status"`
	Reason               string          `json:"reason,omitempty"`
	ProbeType            string          `json:"probe_type,omitempty"`
	ProbedAt             time.Time       `json:"probed_at,omitempty"`
	StaleAfter           time.Time       `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int             `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int             `json:"consecutive_failures,omitempty"`
	Backends             []backendStatus `json:"backends,omitempty"`
	Metadata             *metadataStatus `json:"metadata,omitempty"`
}

type backendStatus struct {
	BackendID            string        `json:"backend_id,omitempty"`
	Status               health.Status `json:"status"`
	Reason               string        `json:"reason,omitempty"`
	ProbeType            string        `json:"probe_type,omitempty"`
	ProbedAt             time.Time     `json:"probed_at,omitempty"`
	StaleAfter           time.Time     `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int           `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int           `json:"consecutive_failures,omitempty"`
	SelectionEligible    bool          `json:"selection_eligible"`
	SelectionWeight      int           `json:"selection_weight,omitempty"`
	SelectionReason      string        `json:"selection_reason,omitempty"`
}

type metadataStatus struct {
	Provider              string    `json:"provider,omitempty"`
	Applicable            bool      `json:"applicable"`
	LastAttemptAt         time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt         time.Time `json:"last_success_at,omitempty"`
	LastSuccessAgeSeconds float64   `json:"last_success_age_seconds,omitempty"`
	LastError             string    `json:"last_error,omitempty"`
	LastResult            string    `json:"last_result,omitempty"`
	ConsecutiveFailures   int       `json:"consecutive_failures,omitempty"`
}

// HealthHandler returns the broker's normalized live-health snapshot.
func HealthHandler(mgr *health.Manager, metadata MetadataStatusSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := mgr.Snapshot()
		statuses := make(map[string]string, len(snap.Capabilities))
		grouped := make(map[string]*healthCapabilityStatus, len(snap.Capabilities))
		out := healthResponse{
			BrokerStatus: snap.BrokerStatus,
			GeneratedAt:  snap.GeneratedAt,
			Capabilities: make([]healthCapabilityStatus, 0, len(snap.Capabilities)),
		}
		for _, cap := range snap.Capabilities {
			key := cap.ID + "|" + cap.OfferingID
			entry, ok := grouped[key]
			if !ok {
				grouped[key] = &healthCapabilityStatus{
					ID:         cap.ID,
					OfferingID: cap.OfferingID,
					Status:     cap.Status,
					Reason:     cap.Reason,
					ProbeType:  cap.ProbeType,
					ProbedAt:   cap.ProbedAt,
					StaleAfter: cap.StaleAfter,
					Backends:   make([]backendStatus, 0, 1),
				}
				entry = grouped[key]
				if st, ok := metadata.StatusFor(cap.ID, cap.OfferingID); ok {
					lastSuccessAgeSeconds := 0.0
					if st.LastSuccessAt.IsZero() {
						lastSuccessAgeSeconds = -1
					} else {
						lastSuccessAgeSeconds = out.GeneratedAt.Sub(st.LastSuccessAt).Seconds()
					}
					entry.Metadata = &metadataStatus{
						Provider:              st.Provider,
						Applicable:            st.Applicable,
						LastAttemptAt:         st.LastAttemptAt,
						LastSuccessAt:         st.LastSuccessAt,
						LastSuccessAgeSeconds: lastSuccessAgeSeconds,
						LastError:             st.LastError,
						LastResult:            st.LastResult,
						ConsecutiveFailures:   st.ConsecutiveFailures,
					}
				}
			}
			entry.Backends = append(entry.Backends, backendStatus{
				BackendID:            cap.BackendID,
				Status:               cap.Status,
				Reason:               cap.Reason,
				ProbeType:            cap.ProbeType,
				ProbedAt:             cap.ProbedAt,
				StaleAfter:           cap.StaleAfter,
				ConsecutiveSuccesses: cap.ConsecutiveSuccesses,
				ConsecutiveFailures:  cap.ConsecutiveFailures,
				SelectionEligible:    selectionEligible(cap),
				SelectionWeight:      selectionWeight(cap),
				SelectionReason:      selectionReason(cap),
			})
			entry.Status = aggregateStatus(entry.Backends)
			entry.Reason = aggregateReason(entry.Backends)
			statuses[key] = string(entry.Status)
		}
		for _, entry := range grouped {
			out.Capabilities = append(out.Capabilities, *entry)
		}
		sort.Slice(out.Capabilities, func(i, j int) bool {
			if out.Capabilities[i].ID != out.Capabilities[j].ID {
				return out.Capabilities[i].ID < out.Capabilities[j].ID
			}
			return out.Capabilities[i].OfferingID < out.Capabilities[j].OfferingID
		})
		statusesJSON, _ := json.Marshal(statuses)
		w.Header().Set(livepeerheader.HealthStatus, string(statusesJSON))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}
}

func selectionWeight(snap health.Snapshot) int {
	base := 0
	switch snap.Status {
	case health.StatusReady:
		base = 100
	case health.StatusDegraded:
		base = 25
	default:
		return 0
	}
	if !snap.StaleAfter.IsZero() && snap.StaleAfter.Before(time.Now().UTC()) {
		return 0
	}
	successBonus := minInt(snap.ConsecutiveSuccesses, 5) * 10
	failurePenalty := minInt(snap.ConsecutiveFailures, 5) * 15
	weight := base + successBonus - failurePenalty
	if weight <= 0 {
		return 0
	}
	if isNearStale(snap) {
		weight /= 2
	}
	if weight <= 0 {
		return 1
	}
	return weight
}

func selectionEligible(snap health.Snapshot) bool {
	return selectionWeight(snap) > 0
}

func selectionReason(snap health.Snapshot) string {
	switch snap.Status {
	case health.StatusReady, health.StatusDegraded:
	default:
		return "status_not_selectable"
	}
	if !snap.StaleAfter.IsZero() && snap.StaleAfter.Before(time.Now().UTC()) {
		return "stale"
	}
	if selectionWeight(snap) == 0 {
		return "failure_penalized"
	}
	if isNearStale(snap) {
		return "near_stale_discounted"
	}
	return "eligible"
}

func isNearStale(snap health.Snapshot) bool {
	if snap.ProbedAt.IsZero() || snap.StaleAfter.IsZero() {
		return false
	}
	ttl := snap.StaleAfter.Sub(snap.ProbedAt)
	if ttl <= 0 {
		return false
	}
	remaining := snap.StaleAfter.Sub(time.Now().UTC())
	return remaining > 0 && remaining*4 < ttl
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func aggregateStatus(backends []backendStatus) health.Status {
	if len(backends) == 0 {
		return health.StatusStale
	}
	best := health.StatusUnreachable
	for _, backend := range backends {
		switch backend.Status {
		case health.StatusReady:
			return health.StatusReady
		case health.StatusDegraded:
			best = health.StatusDegraded
		case health.StatusDraining:
			if best != health.StatusDegraded {
				best = health.StatusDraining
			}
		case health.StatusStale:
			if best != health.StatusDegraded && best != health.StatusDraining {
				best = health.StatusStale
			}
		}
	}
	return best
}

func aggregateReason(backends []backendStatus) string {
	for _, backend := range backends {
		if backend.Status == health.StatusReady || backend.Status == health.StatusDegraded {
			return backend.Reason
		}
	}
	if len(backends) == 0 {
		return ""
	}
	return backends[0].Reason
}
