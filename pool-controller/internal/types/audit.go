package types

import "time"

type AuditEvent struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	OccurredAt   time.Time      `json:"occurred_at"`
	Actor        string         `json:"actor,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	ResourceType string         `json:"resource_type,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
}
