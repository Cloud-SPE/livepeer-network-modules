package types

import "time"

const (
	BackendOutcomeSuccess            = "success"
	BackendOutcomeBackendFailure     = "backend_failure"
	BackendOutcomeCallerFailure      = "caller_failure"
	BackendOutcomePolicyTermination  = "policy_termination"
	BackendOutcomePaymentTermination = "payment_termination"
)

type BackendOutcome struct {
	MemberEthAddress string     `json:"member_eth_address"`
	BackendID        string     `json:"backend_id"`
	CapabilityID     string     `json:"capability_id"`
	OfferingID       string     `json:"offering_id"`
	Outcome          string     `json:"outcome"`
	LatencyMetricMS  uint64     `json:"latency_metric_ms,omitempty"`
	OccurredAt       *time.Time `json:"occurred_at,omitempty"`
}
