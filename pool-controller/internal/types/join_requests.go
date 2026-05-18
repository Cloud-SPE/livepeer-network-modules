package types

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

type JoinRequestStatus string

const (
	JoinRequestPending   JoinRequestStatus = "pending"
	JoinRequestApproved  JoinRequestStatus = "approved"
	JoinRequestRejected  JoinRequestStatus = "rejected"
	JoinRequestWithdrawn JoinRequestStatus = "withdrawn"
)

type JoinRequest struct {
	ID                string             `json:"id"`
	MemberEthAddress  string             `json:"member_eth_address"`
	DisplayName       string             `json:"display_name,omitempty"`
	PayoutMode        string             `json:"payout_mode"`
	RequestedBackends []RequestedBackend `json:"requested_backends"`
	Status            JoinRequestStatus  `json:"status"`
	ReviewReason      string             `json:"review_reason,omitempty"`
	SubmittedAt       time.Time          `json:"submitted_at"`
	ReviewedAt        *time.Time         `json:"reviewed_at,omitempty"`
}

type RequestedBackend struct {
	ID                  string             `json:"id"`
	Transport           string             `json:"transport"`
	URL                 string             `json:"url"`
	Auth                config.AuthConfig  `json:"auth,omitempty"`
	HealthProbe         config.HealthProbe `json:"health_probe,omitempty"`
	ClaimedCapabilities []ClaimedOffer     `json:"claimed_capabilities,omitempty"`
	VerificationStatus  VerificationStatus `json:"verification_status,omitempty"`
	VerificationError   string             `json:"verification_error,omitempty"`
	LastVerifiedAt      *time.Time         `json:"last_verified_at,omitempty"`
}

type ClaimedOffer struct {
	CapabilityID    string         `json:"capability_id"`
	OfferingID      string         `json:"offering_id,omitempty"`
	InteractionMode string         `json:"interaction_mode,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
	Constraints     map[string]any `json:"constraints,omitempty"`
}
