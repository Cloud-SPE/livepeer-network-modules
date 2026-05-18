package types

import "time"

type AssignmentStatus string

const (
	AssignmentStatusActive   AssignmentStatus = "active"
	AssignmentStatusDraining AssignmentStatus = "draining"
	AssignmentStatusDisabled AssignmentStatus = "disabled"
)

type Assignment struct {
	ID              string           `json:"id"`
	OfferID         string           `json:"offer_id"`
	MemberBackendID string           `json:"member_backend_id"`
	Status          AssignmentStatus `json:"status"`
	Notes           string           `json:"notes,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}
