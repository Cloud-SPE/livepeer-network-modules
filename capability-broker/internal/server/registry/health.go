package registry

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

type MetadataStatusSource interface {
	StatusFor(capabilityID, offeringID string) (MetadataStatus, bool)
}

type MetadataStatus struct {
	Provider      string
	Applicable    bool
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastError     string
	LastResult    string
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
	Metadata             *metadataStatus `json:"metadata,omitempty"`
}

type metadataStatus struct {
	Provider      string    `json:"provider,omitempty"`
	Applicable    bool      `json:"applicable"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastResult    string    `json:"last_result,omitempty"`
}

// HealthHandler returns the broker's normalized live-health snapshot.
func HealthHandler(mgr *health.Manager, metadata MetadataStatusSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := mgr.Snapshot()
		statuses := make(map[string]string, len(snap.Capabilities))
		out := healthResponse{
			BrokerStatus: snap.BrokerStatus,
			GeneratedAt:  snap.GeneratedAt,
			Capabilities: make([]healthCapabilityStatus, 0, len(snap.Capabilities)),
		}
		for _, cap := range snap.Capabilities {
			statuses[cap.ID+"|"+cap.OfferingID] = string(cap.Status)
			entry := healthCapabilityStatus{
				ID:                   cap.ID,
				OfferingID:           cap.OfferingID,
				Status:               cap.Status,
				Reason:               cap.Reason,
				ProbeType:            cap.ProbeType,
				ProbedAt:             cap.ProbedAt,
				StaleAfter:           cap.StaleAfter,
				ConsecutiveSuccesses: cap.ConsecutiveSuccesses,
				ConsecutiveFailures:  cap.ConsecutiveFailures,
			}
			if st, ok := metadata.StatusFor(cap.ID, cap.OfferingID); ok {
				entry.Metadata = &metadataStatus{
					Provider:      st.Provider,
					Applicable:    st.Applicable,
					LastAttemptAt: st.LastAttemptAt,
					LastSuccessAt: st.LastSuccessAt,
					LastError:     st.LastError,
					LastResult:    st.LastResult,
				}
			}
			out.Capabilities = append(out.Capabilities, entry)
		}
		statusesJSON, _ := json.Marshal(statuses)
		w.Header().Set(livepeerheader.HealthStatus, string(statusesJSON))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}
}
