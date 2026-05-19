package types

import "time"

// RouteHealthCapability is one capability/offering health entry from a
// broker /registry/health response.
type RouteHealthCapability struct {
	ID         string               `json:"id"`
	OfferingID string               `json:"offering_id"`
	Status     string               `json:"status"`
	Reason     string               `json:"reason,omitempty"`
	ProbeType  string               `json:"probe_type,omitempty"`
	ProbedAt   time.Time            `json:"probed_at,omitempty"`
	StaleAfter time.Time            `json:"stale_after,omitempty"`
	Backends   []RouteHealthBackend `json:"backends,omitempty"`
	Metadata   map[string]any       `json:"metadata,omitempty"`
}

// RouteHealthSnapshot is the full normalized broker health payload.
type RouteHealthSnapshot struct {
	BrokerStatus string                  `json:"broker_status"`
	GeneratedAt  time.Time               `json:"generated_at"`
	Capabilities []RouteHealthCapability `json:"capabilities"`
}

// RouteHealthBackend is one backend-level health entry nested under a
// capability/offering broker health response.
type RouteHealthBackend struct {
	BackendID            string    `json:"backend_id"`
	Status               string    `json:"status"`
	Reason               string    `json:"reason,omitempty"`
	ProbeType            string    `json:"probe_type,omitempty"`
	ProbedAt             time.Time `json:"probed_at,omitempty"`
	StaleAfter           time.Time `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int       `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int       `json:"consecutive_failures,omitempty"`
	SelectionEligible    bool      `json:"selection_eligible,omitempty"`
	SelectionWeight      int       `json:"selection_weight,omitempty"`
	SelectionReason      string    `json:"selection_reason,omitempty"`
}
