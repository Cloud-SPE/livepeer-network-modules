package repo

import (
	"strings"
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

func (r *StateRepo) ListAuditEventsFiltered(kind string, resourceType string, resourceID string, limit int) ([]types.AuditEvent, error) {
	items, err := r.ListAuditEvents()
	if err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	out := make([]types.AuditEvent, 0, len(items))
	for _, item := range items {
		if kind != "" && item.Kind != kind {
			continue
		}
		if resourceType != "" && item.ResourceType != resourceType {
			continue
		}
		if resourceID != "" && item.ResourceID != resourceID {
			continue
		}
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		return out[len(out)-limit:], nil
	}
	return out, nil
}
