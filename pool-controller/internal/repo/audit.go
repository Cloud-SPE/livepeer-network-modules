package repo

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) AppendAuditEvent(event types.AuditEvent) error {
	now := time.Now().UTC()
	if event.ID == "" {
		event.ID = now.Format(time.RFC3339Nano)
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now
	}
	return putJSON(r, auditEventsBucket, event.ID, event)
}

func (r *StateRepo) ListAuditEvents() ([]types.AuditEvent, error) {
	return listJSON(r, auditEventsBucket, func(left, right types.AuditEvent) bool {
		if !left.OccurredAt.Equal(right.OccurredAt) {
			return left.OccurredAt.Before(right.OccurredAt)
		}
		return left.ID < right.ID
	})
}
