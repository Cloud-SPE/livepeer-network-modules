package types

import "time"

// RegionalTerms is an immutable policy snapshot. EffectiveRound starts a new
// accounting schedule; transitions are permitted only at the old window boundary.
type RegionalTerms struct {
	PoolID             string `json:"pool_id"`
	Version            string `json:"version"`
	EffectiveRound     uint64 `json:"effective_round"`
	WindowRounds       uint64 `json:"window_rounds"`
	CommissionBPS      uint64 `json:"commission_bps"`
	ParticipationRules string `json:"participation_rules"`
	// These disclosures are mandatory in Model B, not optional economic switches.
	ZeroWorkToOperator bool      `json:"zero_work_to_operator"`
	RoundingToOperator bool      `json:"rounding_to_operator"`
	CreatedAt          time.Time `json:"created_at"`
}

type TermsAcceptance struct {
	PoolID           string    `json:"pool_id"`
	MemberEthAddress string    `json:"member_eth_address"`
	TermsVersion     string    `json:"terms_version"`
	AcceptedAt       time.Time `json:"accepted_at"`
}

type RegionalAllocation struct {
	RevenueWei          string           `json:"revenue_wei"`
	BilledWeightWei     string           `json:"billed_weight_wei"`
	CommissionWei       string           `json:"commission_wei"`
	MemberPotWei        string           `json:"member_pot_wei"`
	MemberPayoutWei     string           `json:"member_payout_wei"`
	RoundingResidualWei string           `json:"rounding_residual_wei"`
	ZeroWorkOperatorWei string           `json:"zero_work_operator_wei"`
	Members             []PayoutLineItem `json:"members"`
}

// Monetary totals and fraction operands remain exact in reports. Prometheus
// exposes approximate operational summaries of this authoritative ledger view.
type RegionalAccountingSummary struct {
	PoolID                 string `json:"pool_id"`
	CompletedWindows       uint64 `json:"completed_windows"`
	ZeroWorkRevenueWindows uint64 `json:"zero_work_revenue_windows"`
	ConfirmedRevenueWei    string `json:"confirmed_revenue_wei"`
	ZeroWorkOperatorWei    string `json:"zero_work_operator_wei"`
	CommissionWei          string `json:"commission_wei"`
	RoundingResidualWei    string `json:"rounding_residual_wei"`
	MemberPayoutWei        string `json:"member_payout_wei"`
}
