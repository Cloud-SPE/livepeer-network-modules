package runtimeservice

import (
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type MarkRequest struct {
	Revision string
	Actor    string
	Error    string
}

type View struct {
	DesiredRevision     string    `json:"desired_revision,omitempty"`
	AppliedRevision     string    `json:"applied_revision,omitempty"`
	Dirty               bool      `json:"dirty"`
	LastApplyStartedAt  time.Time `json:"last_apply_started_at,omitempty"`
	LastApplyFinishedAt time.Time `json:"last_apply_finished_at,omitempty"`
	LastApplyStatus     string    `json:"last_apply_status,omitempty"`
	LastApplyError      string    `json:"last_apply_error,omitempty"`
	OfferCount          int       `json:"offer_count,omitempty"`
	MemberCount         int       `json:"member_count,omitempty"`
	BackendCount        int       `json:"backend_count,omitempty"`
	AssignmentCount     int       `json:"assignment_count,omitempty"`
}

func BuildView(desired *types.DesiredBrokerRuntime, applied types.AppliedBrokerRuntime) View {
	return View{
		DesiredRevision:     revisionOf(desired),
		AppliedRevision:     applied.AppliedRevision,
		Dirty:               revisionOf(desired) != applied.AppliedRevision,
		LastApplyStartedAt:  applied.LastApplyStartedAt,
		LastApplyFinishedAt: applied.LastApplyFinishedAt,
		LastApplyStatus:     applied.LastApplyStatus,
		LastApplyError:      applied.LastApplyError,
		OfferCount:          countFromDesired(desired, "offer"),
		MemberCount:         countFromDesired(desired, "member"),
		BackendCount:        countFromDesired(desired, "backend"),
		AssignmentCount:     countFromDesired(desired, "assignment"),
	}
}

func BuildDiff(desired *types.DesiredBrokerRuntime, applied types.AppliedBrokerRuntime) map[string]any {
	return map[string]any{
		"desired_revision": revisionOf(desired),
		"applied_revision": applied.AppliedRevision,
		"dirty":            revisionOf(desired) != applied.AppliedRevision,
	}
}

func MarkApplied(stateRepo *repo.StateRepo, desired *types.DesiredBrokerRuntime, req MarkRequest, now time.Time) (types.AppliedBrokerRuntime, error) {
	if desired == nil || desired.Revision == "" {
		return types.AppliedBrokerRuntime{}, fmt.Errorf("desired broker runtime is not available")
	}
	revision := strings.TrimSpace(req.Revision)
	if revision == "" {
		revision = desired.Revision
	}
	if revision != desired.Revision {
		return types.AppliedBrokerRuntime{}, fmt.Errorf("revision must match current desired revision")
	}
	applied := types.AppliedBrokerRuntime{
		DesiredRevision:     desired.Revision,
		AppliedRevision:     revision,
		LastApplyStartedAt:  now,
		LastApplyFinishedAt: now,
		LastApplyStatus:     "applied",
	}
	if err := stateRepo.PutAppliedBrokerRuntime(applied); err != nil {
		return types.AppliedBrokerRuntime{}, err
	}
	return applied, nil
}

func MarkStarted(stateRepo *repo.StateRepo, desired *types.DesiredBrokerRuntime, now time.Time) (types.AppliedBrokerRuntime, error) {
	if desired == nil || desired.Revision == "" {
		return types.AppliedBrokerRuntime{}, fmt.Errorf("desired broker runtime is not available")
	}
	applied, _ := stateRepo.GetAppliedBrokerRuntime()
	applied.DesiredRevision = desired.Revision
	applied.LastApplyStartedAt = now
	applied.LastApplyStatus = "started"
	applied.LastApplyError = ""
	if err := stateRepo.PutAppliedBrokerRuntime(applied); err != nil {
		return types.AppliedBrokerRuntime{}, err
	}
	return applied, nil
}

func MarkFailed(stateRepo *repo.StateRepo, desired *types.DesiredBrokerRuntime, req MarkRequest, now time.Time) (types.AppliedBrokerRuntime, error) {
	if desired == nil || desired.Revision == "" {
		return types.AppliedBrokerRuntime{}, fmt.Errorf("desired broker runtime is not available")
	}
	applied, _ := stateRepo.GetAppliedBrokerRuntime()
	applied.DesiredRevision = desired.Revision
	applied.LastApplyFinishedAt = now
	applied.LastApplyStatus = "failed"
	applied.LastApplyError = strings.TrimSpace(req.Error)
	if err := stateRepo.PutAppliedBrokerRuntime(applied); err != nil {
		return types.AppliedBrokerRuntime{}, err
	}
	return applied, nil
}

func Apply(stateRepo *repo.StateRepo, desired *types.DesiredBrokerRuntime, req MarkRequest, now time.Time, applyFn func(*types.DesiredBrokerRuntime) error) (types.AppliedBrokerRuntime, string, error) {
	if _, err := MarkStarted(stateRepo, desired, now); err != nil {
		return types.AppliedBrokerRuntime{}, "started", err
	}
	if applyFn != nil {
		if err := applyFn(desired); err != nil {
			failed, failedErr := MarkFailed(stateRepo, desired, MarkRequest{Actor: req.Actor, Error: err.Error()}, now)
			if failedErr != nil {
				return failed, "failed", failedErr
			}
			return failed, "failed", fmt.Errorf("apply failed: %w", err)
		}
	}
	applied, err := MarkApplied(stateRepo, desired, req, now)
	if err != nil {
		return types.AppliedBrokerRuntime{}, "applied", err
	}
	return applied, "applied", nil
}

func revisionOf(desired *types.DesiredBrokerRuntime) string {
	if desired == nil {
		return ""
	}
	return desired.Revision
}

func countFromDesired(desired *types.DesiredBrokerRuntime, kind string) int {
	if desired == nil {
		return 0
	}
	switch kind {
	case "offer":
		return desired.OfferCount
	case "member":
		return desired.MemberCount
	case "backend":
		return desired.BackendCount
	case "assignment":
		return desired.AssignmentCount
	default:
		return 0
	}
}
