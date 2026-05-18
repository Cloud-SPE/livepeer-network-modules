package types

import "time"

type DesiredBrokerRuntime struct {
	Revision        string    `json:"revision"`
	RenderedYAML    string    `json:"rendered_yaml"`
	RenderedAt      time.Time `json:"rendered_at"`
	OfferCount      int       `json:"offer_count"`
	MemberCount     int       `json:"member_count"`
	BackendCount    int       `json:"backend_count"`
	AssignmentCount int       `json:"assignment_count"`
}

type AppliedBrokerRuntime struct {
	DesiredRevision     string    `json:"desired_revision"`
	AppliedRevision     string    `json:"applied_revision"`
	LastApplyStartedAt  time.Time `json:"last_apply_started_at,omitempty"`
	LastApplyFinishedAt time.Time `json:"last_apply_finished_at,omitempty"`
	LastApplyStatus     string    `json:"last_apply_status,omitempty"`
	LastApplyError      string    `json:"last_apply_error,omitempty"`
}
