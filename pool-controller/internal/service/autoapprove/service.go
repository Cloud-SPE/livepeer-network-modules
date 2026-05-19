// Package autoapprove implements the auto-approval policy for pending
// member join requests. When the policy worker invokes RunOnce, the
// service applies the same admission checks an operator would (via
// admissionreview.BuildJoinRequestPreview) and approves any pending
// request that is already Approvable. This removes the manual click
// without weakening any precondition: backend verification, capability
// claims, and offer matching all still gate approval.
package autoapprove

import (
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/admissionreview"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// AutoApproveReason is the review_reason recorded against every
// auto-approved join request and the audit-event Kind written to the
// audit log. Operators can grep on either to attribute decisions.
const AutoApproveReason = "auto-approved by policy"

// AuditEventKind is the audit event kind emitted when this service
// approves a join request.
const AuditEventKind = "join_request_auto_approved"

// Summary captures what a single RunOnce invocation did.
type Summary struct {
	Scanned        int      `json:"scanned"`
	Approved       int      `json:"approved"`
	NotApprovable  int      `json:"not_approvable"`
	ApprovedIDs    []string `json:"approved_ids,omitempty"`
	ErroredIDs     []string `json:"errored_ids,omitempty"`
	NotApprovedIDs []string `json:"not_approved_ids,omitempty"`
}

// RunOnce evaluates every pending join request once and approves the
// ones admissionreview already considers Approvable. Errors against a
// single request do not stop the loop; they are recorded in the
// Summary so the caller can log per-batch.
func RunOnce(stateRepo *repo.StateRepo, now time.Time) (Summary, error) {
	var summary Summary
	if stateRepo == nil {
		return summary, fmt.Errorf("state repo is required")
	}
	joinRequests, err := stateRepo.ListJoinRequests()
	if err != nil {
		return summary, fmt.Errorf("list join requests: %w", err)
	}
	offers, err := stateRepo.ListOffers()
	if err != nil {
		return summary, fmt.Errorf("list offers: %w", err)
	}
	for _, jr := range joinRequests {
		if jr.Status != types.JoinRequestPending {
			continue
		}
		summary.Scanned++
		preview := admissionreview.BuildJoinRequestPreview(jr, offers)
		if !preview.Approavable {
			summary.NotApprovable++
			summary.NotApprovedIDs = append(summary.NotApprovedIDs, jr.ID)
			continue
		}
		if _, err := admissionreview.ApproveJoinRequest(stateRepo, jr, AutoApproveReason, now); err != nil {
			summary.ErroredIDs = append(summary.ErroredIDs, jr.ID)
			continue
		}
		summary.Approved++
		summary.ApprovedIDs = append(summary.ApprovedIDs, jr.ID)
		_ = stateRepo.AppendAuditEvent(types.AuditEvent{
			Kind:         AuditEventKind,
			OccurredAt:   now,
			ResourceID:   jr.ID,
			ResourceType: "join_request",
			Details: map[string]any{
				"reason": AutoApproveReason,
			},
		})
	}
	return summary, nil
}
