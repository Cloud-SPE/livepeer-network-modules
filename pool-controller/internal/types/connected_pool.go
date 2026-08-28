package types

import "time"

type HostEnrollmentStatus string
type HardwareUnitState string
type TemplateAssignmentRole string
type TemplateAssignmentState string
type CertificationStatus string
type SettlementWindowStatus string
type PayoutBatchStatus string

const (
	HostEnrollmentPending        HostEnrollmentStatus = "pending"
	HostEnrollmentActive         HostEnrollmentStatus = "active"
	HostEnrollmentUpdateRequired HostEnrollmentStatus = "update_required"
	HostEnrollmentRevoked        HostEnrollmentStatus = "revoked"
	HostEnrollmentRetired        HostEnrollmentStatus = "retired"
)

const (
	HardwareUnitRegistered   HardwareUnitState = "registered"
	HardwareUnitOnline       HardwareUnitState = "online"
	HardwareUnitTesting      HardwareUnitState = "certification_testing"
	HardwareUnitProbationary HardwareUnitState = "probationary_real_traffic"
	HardwareUnitActive       HardwareUnitState = "active"
	HardwareUnitThrottled    HardwareUnitState = "throttled"
	HardwareUnitSuspended    HardwareUnitState = "suspended"
	HardwareUnitRetired      HardwareUnitState = "retired"
)

const ()

const (
	TemplateAssignmentPrimary   TemplateAssignmentRole = "primary"
	TemplateAssignmentSecondary TemplateAssignmentRole = "secondary"
)

const (
	TemplateAssignmentPending        TemplateAssignmentState = "pending"
	TemplateAssignmentDraining       TemplateAssignmentState = "draining"
	TemplateAssignmentUpdateRequired TemplateAssignmentState = "update_required"
	TemplateAssignmentTesting        TemplateAssignmentState = "certification_testing"
	TemplateAssignmentProbationary   TemplateAssignmentState = "probationary_real_traffic"
	TemplateAssignmentActive         TemplateAssignmentState = "active"
	TemplateAssignmentThrottled      TemplateAssignmentState = "throttled"
	TemplateAssignmentSuspended      TemplateAssignmentState = "suspended"
	TemplateAssignmentRetired        TemplateAssignmentState = "retired"
)

const (
	CertificationPending CertificationStatus = "pending"
	CertificationRunning CertificationStatus = "running"
	CertificationPassed  CertificationStatus = "passed"
	CertificationFailed  CertificationStatus = "failed"
)

const (
	SettlementWindowOpen            SettlementWindowStatus = "open"
	SettlementWindowClosing         SettlementWindowStatus = "closing"
	SettlementWindowPendingApproval SettlementWindowStatus = "pending_approval"
	SettlementWindowApproved        SettlementWindowStatus = "approved"
	SettlementWindowPaid            SettlementWindowStatus = "paid"
	SettlementWindowFailed          SettlementWindowStatus = "failed"
)

const (
	PayoutBatchPendingApproval PayoutBatchStatus = "pending_approval"
	PayoutBatchApproved        PayoutBatchStatus = "approved"
	PayoutBatchSubmitted       PayoutBatchStatus = "submitted"
	PayoutBatchPaid            PayoutBatchStatus = "paid"
	PayoutBatchFailed          PayoutBatchStatus = "failed"
)

type PoolMember struct {
	ID          string       `json:"id"`
	EthAddress  string       `json:"eth_address"`
	DisplayName string       `json:"display_name,omitempty"`
	Contact     string       `json:"contact,omitempty"`
	PayoutMode  string       `json:"payout_mode"`
	Status      MemberStatus `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type MemberNonce struct {
	ID         string    `json:"id"`
	EthAddress string    `json:"eth_address"`
	Nonce      string    `json:"nonce"`
	Message    string    `json:"message"`
	ExpiresAt  time.Time `json:"expires_at"`
	UsedAt     time.Time `json:"used_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type HostEnrollment struct {
	ID                      string               `json:"id"`
	MemberEthAddress        string               `json:"member_eth_address"`
	HostLabel               string               `json:"host_label,omitempty"`
	EnrollmentTokenHash     string               `json:"enrollment_token_hash,omitempty"`
	BrokerSessionCredential string               `json:"broker_session_credential,omitempty"`
	Status                  HostEnrollmentStatus `json:"status"`
	LastSeenAt              time.Time            `json:"last_seen_at,omitempty"`
	RevokedAt               time.Time            `json:"revoked_at,omitempty"`
	RevocationReason        string               `json:"revocation_reason,omitempty"`
	CreatedAt               time.Time            `json:"created_at"`
	UpdatedAt               time.Time            `json:"updated_at"`
}

type HardwareUnit struct {
	ID               string            `json:"id"`
	EnrollmentID     string            `json:"enrollment_id"`
	MemberEthAddress string            `json:"member_eth_address"`
	GPUUUID          string            `json:"gpu_uuid"`
	GPUModel         string            `json:"gpu_model"`
	VRAMBytes        uint64            `json:"vram_bytes,omitempty"`
	DriverVersion    string            `json:"driver_version,omitempty"`
	CUDAVersion      string            `json:"cuda_version,omitempty"`
	RuntimeFacts     map[string]string `json:"runtime_facts,omitempty"`
	State            HardwareUnitState `json:"state"`
	LastSeenAt       time.Time         `json:"last_seen_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// CertificationStep is one step of a template's certification recipe.
//
// The yaml tags are load-bearing: templates are authored as YAML files,
// and without them yaml.v3 would lowercase the Go field names, so
// `timeout_ms` would be a parse error and the only accepted spelling
// would be `timeoutms`. The loader sets KnownFields(true), so that
// mismatch fails the boot rather than silently dropping a timeout.
type CertificationStep struct {
	Name        string         `yaml:"name" json:"name"`
	Type        string         `yaml:"type" json:"type"`
	Config      map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
	TimeoutMS   int            `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	Required    bool           `yaml:"required" json:"required"`
	Description string         `yaml:"description,omitempty" json:"description,omitempty"`
}

type TemplateAssignment struct {
	ID                  string                  `json:"id"`
	HardwareUnitID      string                  `json:"hardware_unit_id"`
	HostEnrollmentID    string                  `json:"host_enrollment_id"`
	MemberEthAddress    string                  `json:"member_eth_address"`
	TemplateID          string                  `json:"template_id"`
	Role                TemplateAssignmentRole  `json:"role"`
	State               TemplateAssignmentState `json:"state"`
	MaxInFlight         int                     `json:"max_in_flight,omitempty"`
	QueueLimit          int                     `json:"queue_limit,omitempty"`
	ShareCapPPM         uint64                  `json:"share_cap_ppm,omitempty"`
	ProbationStartedAt  time.Time               `json:"probation_started_at,omitempty"`
	ProbationRoundStart string                  `json:"probation_round_start,omitempty"`
	ActivatedAt         time.Time               `json:"activated_at,omitempty"`
	DrainingSince       time.Time               `json:"draining_since,omitempty"`
	UpdateRequiredAt    time.Time               `json:"update_required_at,omitempty"`
	LastCertifiedAt     time.Time               `json:"last_certified_at,omitempty"`
	// SuspensionReason is the ladder's reason code for the CURRENT
	// suspension, and SuspendedAt when it happened.
	//
	// Recorded but not yet acted on. The two causes want opposite
	// remedies: repeated certification failure is fixed by re-running
	// certification, whereas invalid output is not — the smoke step
	// checks that a response came back with the right shape, and a
	// runner returning fluent, confident, wrong answers passes it every
	// time. Reinstating currently re-certifies in both cases, which is
	// right for the first and thin for the second.
	//
	// Nothing produces an invalid-output signal today, so the gap is
	// latent. It is recorded now so the audit trail is honest
	// immediately, and so whoever builds that detector finds the cause
	// already here rather than inheriting a default that quietly does
	// the wrong thing.
	SuspensionReason string    `json:"suspension_reason,omitempty"`
	SuspendedAt      time.Time `json:"suspended_at,omitempty"`

	// ReinstatedAt is when an operator last lifted a suspension.
	//
	// The ladder counts certification failures only after it. Without
	// that boundary a placement suspended for two failed runs would be
	// re-suspended by the same historical count on the very next tick,
	// and the operator's decision would visibly do nothing, once a
	// minute, forever. Keeping the boundary rather than resetting the
	// counters leaves the history intact for whoever reviews whether
	// the reinstate was wise.
	ReinstatedAt time.Time `json:"reinstated_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CertificationRun struct {
	ID               string                `json:"id"`
	AssignmentID     string                `json:"assignment_id"`
	HardwareUnitID   string                `json:"hardware_unit_id"`
	HostEnrollmentID string                `json:"host_enrollment_id"`
	TemplateID       string                `json:"template_id"`
	ExecutionPath    string                `json:"execution_path,omitempty"`
	Status           CertificationStatus   `json:"status"`
	StartedAt        time.Time             `json:"started_at,omitempty"`
	CompletedAt      time.Time             `json:"completed_at,omitempty"`
	Results          []CertificationResult `json:"results,omitempty"`
	FailureReason    string                `json:"failure_reason,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

type CertificationResult struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	LatencyMS float64   `json:"latency_ms,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type SettlementWindow struct {
	ID                         string                 `json:"id"`
	StartRoundID               string                 `json:"start_round_id"`
	EndRoundID                 string                 `json:"end_round_id"`
	LengthRounds               int                    `json:"length_rounds"`
	Status                     SettlementWindowStatus `json:"status"`
	AttributedRevenueWei       string                 `json:"attributed_revenue_wei,omitempty"`
	ConfirmedRevenueWei        string                 `json:"confirmed_revenue_wei,omitempty"`
	SettlementScalePPM         uint64                 `json:"settlement_scale_ppm,omitempty"`
	Anomaly                    string                 `json:"anomaly,omitempty"`
	IncludedRoundReceiptIDs    []string               `json:"included_round_receipt_ids,omitempty"`
	OfferingSettlementLineItem []OfferingSettlement   `json:"offering_settlement_line_items,omitempty"`
	CreatedAt                  time.Time              `json:"created_at"`
	UpdatedAt                  time.Time              `json:"updated_at"`
	ClosedAt                   time.Time              `json:"closed_at,omitempty"`
	ApprovedAt                 time.Time              `json:"approved_at,omitempty"`
}

type OfferingSettlement struct {
	CapabilityID            string `json:"capability_id"`
	OfferingID              string `json:"offering_id"`
	AttributedRevenueWei    string `json:"attributed_revenue_wei"`
	SettlementRevenueWei    string `json:"settlement_revenue_wei"`
	CommissionWei           string `json:"commission_wei"`
	DistributableRevenueWei string `json:"distributable_revenue_wei"`
}

type PayoutBatch struct {
	ID                 string            `json:"id"`
	SettlementWindowID string            `json:"settlement_window_id"`
	Status             PayoutBatchStatus `json:"status"`
	TotalAmountWei     string            `json:"total_amount_wei"`
	LineItems          []PayoutLineItem  `json:"line_items,omitempty"`
	ApprovedBy         string            `json:"approved_by,omitempty"`
	ApprovedAt         time.Time         `json:"approved_at,omitempty"`
	SubmittedAt        time.Time         `json:"submitted_at,omitempty"`
	PaidAt             time.Time         `json:"paid_at,omitempty"`
	FailureReason      string            `json:"failure_reason,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type PayoutLineItem struct {
	MemberEthAddress     string `json:"member_eth_address"`
	DestinationAddress   string `json:"destination_address"`
	CapabilityID         string `json:"capability_id,omitempty"`
	OfferingID           string `json:"offering_id,omitempty"`
	AttributedRevenueWei string `json:"attributed_revenue_wei,omitempty"`
	AmountWei            string `json:"amount_wei"`
	AdjustmentReason     string `json:"adjustment_reason,omitempty"`
}
