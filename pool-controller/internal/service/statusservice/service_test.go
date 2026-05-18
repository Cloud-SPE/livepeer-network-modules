package statusservice

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestSetStatuses(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	_ = stateRepo.PutMember(types.MemberRecord{ID: "member-1", EthAddress: "0xabc", Status: types.MemberStatusActive, PayoutMode: "onchain"})
	_ = stateRepo.PutMemberBackend(types.MemberBackend{ID: "backend-1", MemberID: "member-1", Status: types.BackendStatusActive})
	_ = stateRepo.PutAssignment(types.Assignment{ID: "assignment-1", OfferID: "offer-1", MemberBackendID: "backend-1", Status: types.AssignmentStatusActive})

	member, err := SetMemberStatus(stateRepo, "member-1", "suspended")
	if err != nil || member.Status != types.MemberStatusSuspended {
		t.Fatalf("SetMemberStatus() member=%#v err=%v", member, err)
	}
	backend, err := SetBackendStatus(stateRepo, "backend-1", "draining")
	if err != nil || backend.Status != types.BackendStatusDraining {
		t.Fatalf("SetBackendStatus() backend=%#v err=%v", backend, err)
	}
	assignment, err := SetAssignmentStatus(stateRepo, "assignment-1", "disabled")
	if err != nil || assignment.Status != types.AssignmentStatusDisabled {
		t.Fatalf("SetAssignmentStatus() assignment=%#v err=%v", assignment, err)
	}
}
