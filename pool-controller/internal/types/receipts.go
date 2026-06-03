package types

import "time"

type WorkReceipt struct {
	ID                   string    `json:"id"`
	CreatedAt            time.Time `json:"created_at"`
	RoundID              string    `json:"round_id,omitempty"`
	RequestID            string    `json:"request_id"`
	CapabilityID         string    `json:"capability_id"`
	OfferingID           string    `json:"offering_id"`
	MemberEthAddress     string    `json:"member_eth_address"`
	BackendID            string    `json:"backend_id"`
	HostEnrollmentID     string    `json:"host_enrollment_id,omitempty"`
	HardwareUnitID       string    `json:"hardware_unit_id,omitempty"`
	GPUUUID              string    `json:"gpu_uuid,omitempty"`
	TemplateID           string    `json:"template_id,omitempty"`
	ExpectedMaxUnits     uint64    `json:"expected_max_units,omitempty"`
	ActualUnits          uint64    `json:"actual_units,omitempty"`
	AcceptedWorkUnits    uint64    `json:"accepted_work_units,omitempty"`
	GatewayRevenueWei    string    `json:"gateway_revenue_wei,omitempty"`
	AttributedRevenueWei string    `json:"attributed_revenue_wei,omitempty"`
	Status               string    `json:"status"`
}

type RoundReceipt struct {
	ID                     string              `json:"id"`
	CreatedAt              time.Time           `json:"created_at"`
	RoundID                string              `json:"round_id"`
	PoolRevenueWei         string              `json:"pool_revenue_wei"`
	PoolCutWei             string              `json:"pool_cut_wei"`
	DistributableWei       string              `json:"distributable_wei"`
	IncludedWorkReceiptIDs []string            `json:"included_work_receipt_ids,omitempty"`
	MemberPayouts          []MemberRoundPayout `json:"member_payouts,omitempty"`
}

type MemberRoundPayout struct {
	MemberEthAddress string `json:"member_eth_address"`
	ContributionWei  string `json:"contribution_wei"`
	SharePPM         uint64 `json:"share_ppm"`
	PayoutWei        string `json:"payout_wei"`
}

type PayoutIntent struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	RoundReceiptID     string    `json:"round_receipt_id"`
	RoundID            string    `json:"round_id"`
	MemberEthAddress   string    `json:"member_eth_address"`
	DestinationAddress string    `json:"destination_address"`
	ChainID            uint64    `json:"chain_id"`
	Asset              string    `json:"asset"`
	AmountWei          string    `json:"amount_wei"`
	Status             string    `json:"status"`
	ExportedAt         time.Time `json:"exported_at,omitempty"`
	LeasedAt           time.Time `json:"leased_at,omitempty"`
	LeaseID            string    `json:"lease_id,omitempty"`
	LeaseOwner         string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt     time.Time `json:"lease_expires_at,omitempty"`
	SubmittedAt        time.Time `json:"submitted_at,omitempty"`
	PaidAt             time.Time `json:"paid_at,omitempty"`
	FailedAt           time.Time `json:"failed_at,omitempty"`
	RetryCount         uint64    `json:"retry_count,omitempty"`
	LastRequeuedAt     time.Time `json:"last_requeued_at,omitempty"`
	ExternalRef        string    `json:"external_ref,omitempty"`
	TxHash             string    `json:"tx_hash,omitempty"`
	FailureReason      string    `json:"failure_reason,omitempty"`
}
