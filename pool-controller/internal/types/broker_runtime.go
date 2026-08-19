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
	// RenderWarnings surfaces choices the renderer made on incomplete
	// offer data (e.g. a defaulted job.transports set).
	RenderWarnings []string `json:"render_warnings,omitempty"`
}

type AppliedBrokerRuntime struct {
	DesiredRevision       string    `json:"desired_revision"`
	AppliedRevision       string    `json:"applied_revision"`
	BrokerReloadAttemptID string    `json:"broker_reload_attempt_id,omitempty"`
	BrokerLoadedRevision  string    `json:"broker_loaded_revision,omitempty"`
	BrokerLoadedAt        time.Time `json:"broker_loaded_at,omitempty"`
	BrokerReloadStatus    string    `json:"broker_reload_status,omitempty"`
	BrokerReloadError     string    `json:"broker_reload_error,omitempty"`
	LastApplyStartedAt    time.Time `json:"last_apply_started_at,omitempty"`
	LastApplyFinishedAt   time.Time `json:"last_apply_finished_at,omitempty"`
	LastApplyStatus       string    `json:"last_apply_status,omitempty"`
	LastApplyError        string    `json:"last_apply_error,omitempty"`
}
