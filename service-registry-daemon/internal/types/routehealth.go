package types

import "time"

// RouteHealthCapability is one capability/offering health entry from a
// broker /registry/health response.
type RouteHealthCapability struct {
	ID         string    `json:"id"`
	OfferingID string    `json:"offering_id"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	StaleAfter time.Time `json:"stale_after,omitempty"`
}

// RouteHealthSnapshot is the full normalized broker health payload.
type RouteHealthSnapshot struct {
	BrokerStatus string                  `json:"broker_status"`
	GeneratedAt  time.Time               `json:"generated_at"`
	Capabilities []RouteHealthCapability `json:"capabilities"`
}
