package member

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// A member whose GPU is claimed by someone else must be told.
//
// The uniqueness guard refuses their claim, so without this the card
// simply never appears: they bought second-hand hardware, set it up
// correctly, and the pool says nothing at all. That is the common case,
// not the fraud one.
func TestMemberSeesTheirOwnContestedGPUWithoutLearningWhoHoldsIt(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	now := time.Now().UTC()

	enrollment := types.HostEnrollment{
		ID: "host-b", MemberEthAddress: "0xbbb", Status: types.HostEnrollmentActive,
	}
	if err := stateRepo.PutHostEnrollment(enrollment); err != nil {
		t.Fatalf("PutHostEnrollment() error = %v", err)
	}
	if _, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-CONTESTED",
		ChallengerEthAddress: "0xBBB",
		ChallengerHostID:     "host-b",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           now,
	}); err != nil {
		t.Fatalf("RecordHardwareClaimConflict() error = %v", err)
	}
	// Another member's dispute, which this member must not see at all.
	if _, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-SOMEONE-ELSE",
		ChallengerEthAddress: "0xccc",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           now,
	}); err != nil {
		t.Fatalf("RecordHardwareClaimConflict() error = %v", err)
	}

	deps := Deps{Repo: stateRepo}
	view, err := deps.hostStatus(enrollment)
	if err != nil {
		t.Fatalf("hostStatus() error = %v", err)
	}
	if len(view.ContestedGPUs) != 1 {
		t.Fatalf("ContestedGPUs = %+v, want only this member's own dispute", view.ContestedGPUs)
	}
	contested := view.ContestedGPUs[0]
	if contested.GPUUUID != "GPU-CONTESTED" {
		t.Fatalf("gpu = %q", contested.GPUUUID)
	}
	if contested.Status != "under_review" {
		t.Fatalf("status = %q, want under_review", contested.Status)
	}
	// The detail has to be actionable — the likely cause is a previous
	// owner who never retired their host, and saying so is the
	// difference between a member waiting and a member fixing it.
	if !strings.Contains(contested.Detail, "second-hand") {
		t.Fatalf("detail = %q, want it to name the likely cause", contested.Detail)
	}
	// Naming the holder would let anyone learn which address owns a
	// given GPU just by claiming it.
	if strings.Contains(contested.Detail, "0xaaa") || strings.Contains(contested.GPUUUID, "0xaaa") {
		t.Fatalf("the incumbent's address leaked to the challenger: %+v", contested)
	}
}

// A dispute resolved in the member's favour stops being reported: their
// next attach succeeds and there is nothing left to tell them.
func TestTransferredConflictDisappearsFromTheMemberView(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })

	enrollment := types.HostEnrollment{ID: "host-b", MemberEthAddress: "0xbbb"}
	if err := stateRepo.PutHostEnrollment(enrollment); err != nil {
		t.Fatalf("PutHostEnrollment() error = %v", err)
	}
	conflict, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID: "GPU-X", ChallengerEthAddress: "0xbbb", ChallengerHostID: "host-b",
		IncumbentEthAddress: "0xaaa", LastSeenAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict() error = %v", err)
	}
	conflict.Resolution = types.ConflictTransferred
	if err := stateRepo.PutHardwareClaimConflict(conflict); err != nil {
		t.Fatalf("PutHardwareClaimConflict() error = %v", err)
	}

	view, err := Deps{Repo: stateRepo}.hostStatus(enrollment)
	if err != nil {
		t.Fatalf("hostStatus() error = %v", err)
	}
	if len(view.ContestedGPUs) != 0 {
		t.Fatalf("ContestedGPUs = %+v, want none once it is resolved in their favour", view.ContestedGPUs)
	}
}
