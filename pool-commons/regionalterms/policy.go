// Package regionalterms contains the controller-to-broker admission policy.
package regionalterms

// Policy is a durable broker admission fence. Pausing never cancels admitted
// work; a successor can activate only after all admitted work is settled.
type Policy struct {
	SourceID       string `json:"source_id"`
	BrokerID       string `json:"broker_id"`
	PoolID         string `json:"pool_id"`
	Version        string `json:"version"`
	EffectiveRound uint64 `json:"effective_round"`
	Revision       uint64 `json:"revision"`
	Paused         bool   `json:"paused"`
	Reason         string `json:"reason,omitempty"`
}
