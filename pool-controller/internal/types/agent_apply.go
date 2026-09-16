package types

import "time"

type AgentServiceResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}
type AgentApplyReport struct {
	PoolID       string               `json:"pool_id"`
	EnrollmentID string               `json:"enrollment_id"`
	Revision     string               `json:"revision"`
	ReportedAt   time.Time            `json:"reported_at"`
	Services     []AgentServiceResult `json:"services"`
}
