package ownership

import "time"

type DeviceDrainProof struct {
	Revoked              bool      `json:"revoked"`
	PoolID               string    `json:"pool_id"`
	SourceID             string    `json:"source_id"`
	BrokerID             string    `json:"broker_id"`
	EnrollmentID         string    `json:"enrollment_id"`
	DeviceID             string    `json:"device_id"`
	Generation           uint64    `json:"generation"`
	Reason               string    `json:"reason"`
	StartedAt            time.Time `json:"started_at"`
	PendingOperations    uint64    `json:"pending_operations"`
	ActiveAuthorizations uint64    `json:"active_authorizations"`
	UndeliveredReceipts  uint64    `json:"undelivered_receipts"`
	UnqualifiedWork      uint64    `json:"unqualified_work"`
	ObservedAt           time.Time `json:"observed_at"`
}

type DeviceDrainRequest struct {
	Action       string `json:"action,omitempty"`
	PoolID       string `json:"pool_id"`
	EnrollmentID string `json:"enrollment_id"`
	DeviceID     string `json:"device_id"`
	Generation   uint64 `json:"generation"`
	Reason       string `json:"reason"`
}
