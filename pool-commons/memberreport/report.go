// Package memberreport defines qualified presentation records, never payment instructions.
package memberreport

import "time"

type RoundSpan struct {
	Start uint64 `json:"start_round"`
	End   uint64 `json:"end_round"`
}
type SourceEvidence struct {
	PoolID          string    `json:"pool_id"`
	SourceID        string    `json:"source_id"`
	BrokerID        string    `json:"broker_id"`
	Round           uint64    `json:"round"`
	InclusionDigest string    `json:"inclusion_digest"`
	WorkDigest      string    `json:"work_digest"`
	ObservedAt      time.Time `json:"observed_at"`
}
type Payment struct {
	PoolID    string    `json:"pool_id"`
	IntentID  string    `json:"intent_id"`
	AmountWei string    `json:"amount_wei"`
	Status    string    `json:"status"`
	TxHash    string    `json:"tx_hash,omitempty"`
	PaidAt    time.Time `json:"paid_at,omitempty"`
}
type Window struct {
	PoolID              string           `json:"pool_id"`
	WindowID            string           `json:"window_id"`
	BatchID             string           `json:"batch_id"`
	ChainID             uint64           `json:"chain_id"`
	Asset               string           `json:"asset"`
	StartRound          uint64           `json:"start_round"`
	EndRound            uint64           `json:"end_round"`
	TermsVersion        string           `json:"terms_version"`
	ClosedAt            time.Time        `json:"closed_at"`
	BatchStatus         string           `json:"batch_status"`
	BilledWeightWei     string           `json:"billed_weight_wei"`
	EarnedWei           string           `json:"earned_wei"`
	AwaitingApprovalWei string           `json:"awaiting_approval_wei"`
	PendingWei          string           `json:"pending_wei"`
	PaidWei             string           `json:"paid_wei"`
	CorrectionHeld      bool             `json:"correction_held"`
	Sources             []SourceEvidence `json:"sources"`
	Payments            []Payment        `json:"payments"`
}
type Report struct {
	Telemetry          Telemetry   `json:"telemetry"`
	PoolID             string      `json:"pool_id"`
	Wallet             string      `json:"wallet"`
	ObservedAt         time.Time   `json:"observed_at"`
	LedgerObservedAt   time.Time   `json:"ledger_observed_at"`
	CompleteRoundSpans []RoundSpan `json:"complete_round_spans"`
	HeldWindowCount    uint64      `json:"held_window_count"`
	Windows            []Window    `json:"windows"`
}

// Telemetry exposes exact pool-level operands. It contains no other member allocations.
type Telemetry struct {
	CompletedWindows       uint64 `json:"completed_windows"`
	ZeroWorkRevenueWindows uint64 `json:"zero_work_revenue_windows"`
	ConfirmedRevenueWei    string `json:"confirmed_revenue_wei"`
	ZeroWorkOperatorWei    string `json:"zero_work_operator_wei"`
	CommissionWei          string `json:"commission_wei"`
	RoundingResidualWei    string `json:"rounding_residual_wei"`
}
