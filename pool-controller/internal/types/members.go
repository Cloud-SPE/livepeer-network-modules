package types

import "time"

type MemberStatus string

const (
	MemberStatusActive    MemberStatus = "active"
	MemberStatusSuspended MemberStatus = "suspended"
)

type MemberRecord struct {
	ID                  string       `json:"id"`
	EthAddress          string       `json:"eth_address"`
	DisplayName         string       `json:"display_name,omitempty"`
	PayoutMode          string       `json:"payout_mode"`
	Status              MemberStatus `json:"status"`
	SourceJoinRequestID string       `json:"source_join_request_id,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}
