package repo

import (
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) SaveAgentApply(report types.AgentApplyReport) error {
	enrollment, err := r.GetHostEnrollment(report.EnrollmentID)
	if err != nil {
		return err
	}
	if report.PoolID != r.PoolID() || enrollment.PoolID != "" && enrollment.PoolID != report.PoolID || report.ReportedAt.IsZero() {
		return fmt.Errorf("invalid agent report identity")
	}
	return putJSON(r, "member_agent_apply", report.EnrollmentID, report)
}
func (r *StateRepo) AgentApply(id string) (types.AgentApplyReport, error) {
	var report types.AgentApplyReport
	err := getJSON(r, "member_agent_apply", id, &report)
	return report, err
}
