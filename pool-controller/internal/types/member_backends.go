package types

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

type VerificationStatus string
type BackendStatus string

const (
	VerificationUnknown VerificationStatus = "unknown"
	VerificationPassing VerificationStatus = "passing"
	VerificationFailing VerificationStatus = "failing"
)

const (
	BackendStatusActive   BackendStatus = "active"
	BackendStatusDraining BackendStatus = "draining"
	BackendStatusDisabled BackendStatus = "disabled"
)

type MemberBackend struct {
	ID                  string             `json:"id"`
	MemberID            string             `json:"member_id"`
	Transport           string             `json:"transport"`
	URL                 string             `json:"url"`
	Auth                config.AuthConfig  `json:"auth,omitempty"`
	HealthProbe         config.HealthProbe `json:"health_probe,omitempty"`
	ClaimedCapabilities []ClaimedOffer     `json:"claimed_capabilities,omitempty"`
	VerificationStatus  VerificationStatus `json:"verification_status"`
	VerificationError   string             `json:"verification_error,omitempty"`
	LastVerifiedAt      *time.Time         `json:"last_verified_at,omitempty"`
	Status              BackendStatus      `json:"status"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}
