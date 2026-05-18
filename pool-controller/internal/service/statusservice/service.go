package statusservice

import (
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func SetMemberStatus(stateRepo *repo.StateRepo, id string, rawStatus string) (types.MemberRecord, error) {
	status := types.MemberStatus(strings.TrimSpace(rawStatus))
	switch status {
	case types.MemberStatusActive, types.MemberStatusSuspended:
	default:
		return types.MemberRecord{}, fmt.Errorf("status must be active or suspended")
	}
	if err := stateRepo.SetMemberStatus(id, status); err != nil {
		return types.MemberRecord{}, err
	}
	return stateRepo.GetMember(id)
}

func SetBackendStatus(stateRepo *repo.StateRepo, id string, rawStatus string) (types.MemberBackend, error) {
	status := types.BackendStatus(strings.TrimSpace(rawStatus))
	switch status {
	case types.BackendStatusActive, types.BackendStatusDraining, types.BackendStatusDisabled:
	default:
		return types.MemberBackend{}, fmt.Errorf("status must be active, draining, or disabled")
	}
	if err := stateRepo.SetMemberBackendStatus(id, status); err != nil {
		return types.MemberBackend{}, err
	}
	return stateRepo.GetMemberBackend(id)
}

func SetAssignmentStatus(stateRepo *repo.StateRepo, id string, rawStatus string) (types.Assignment, error) {
	status := types.AssignmentStatus(strings.TrimSpace(rawStatus))
	switch status {
	case types.AssignmentStatusActive, types.AssignmentStatusDraining, types.AssignmentStatusDisabled:
	default:
		return types.Assignment{}, fmt.Errorf("status must be active, draining, or disabled")
	}
	if err := stateRepo.SetAssignmentStatus(id, status); err != nil {
		return types.Assignment{}, err
	}
	return stateRepo.GetAssignment(id)
}
