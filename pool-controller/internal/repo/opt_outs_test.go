package repo

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Opting out is the only lever a member has over placement, so it has
// to survive a restart and has to be found again by the address the
// member signed with — which is not necessarily the case the address is
// stored in.
func TestMemberTemplateOptOutRoundTrip(t *testing.T) {
	stateRepo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	created := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	optOuts := []types.MemberTemplateOptOut{
		{
			ID:               "opt-1",
			MemberEthAddress: "0xAbCd000000000000000000000000000000000001",
			TemplateID:       "openai-images-flux-1-dev",
			Reason:           "power budget",
			CreatedAt:        created,
		},
		{
			ID:               "opt-2",
			MemberEthAddress: "0xAbCd000000000000000000000000000000000001",
			TemplateID:       "openai-chat-gpt-oss-20b",
			HardwareUnitID:   "gpu-2",
		},
		{
			ID:               "opt-3",
			MemberEthAddress: "0x9999000000000000000000000000000000000009",
			TemplateID:       "openai-images-flux-1-dev",
		},
	}
	for _, optOut := range optOuts {
		if err := stateRepo.PutMemberTemplateOptOut(optOut); err != nil {
			t.Fatalf("PutMemberTemplateOptOut(%s) error = %v", optOut.ID, err)
		}
	}

	all, err := stateRepo.ListMemberTemplateOptOuts()
	if err != nil {
		t.Fatalf("ListMemberTemplateOptOuts() error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListMemberTemplateOptOuts() = %d records, want 3", len(all))
	}
	if all[0].ID != "opt-1" || all[1].ID != "opt-2" || all[2].ID != "opt-3" {
		t.Errorf("list order = %s/%s/%s, want opt-1/opt-2/opt-3", all[0].ID, all[1].ID, all[2].ID)
	}
	// Every field the placement engine reads has to survive the trip.
	if all[0].TemplateID != "openai-images-flux-1-dev" || all[0].Reason != "power budget" {
		t.Errorf("opt-1 = %+v, want template and reason preserved", all[0])
	}
	if !all[0].CreatedAt.Equal(created) {
		t.Errorf("opt-1 created_at = %v, want the supplied %v", all[0].CreatedAt, created)
	}
	if all[1].HardwareUnitID != "gpu-2" {
		t.Errorf("opt-2 hardware unit = %q, want gpu-2 — the per-card scope is the record's whole point", all[1].HardwareUnitID)
	}
	// A record stored without a timestamp is still a real decision; it
	// gets one rather than sorting as the epoch forever.
	if all[1].CreatedAt.IsZero() {
		t.Errorf("opt-2 created_at is zero, want the write time filled in")
	}

	// The member signs with a checksummed address and the pool stores
	// members lower-cased, so the lookup has to fold case or a member
	// would appear to have no opt-outs at all.
	for _, address := range []string{
		"0xAbCd000000000000000000000000000000000001",
		"0xabcd000000000000000000000000000000000001",
		"0xABCD000000000000000000000000000000000001",
		"  0xAbCd000000000000000000000000000000000001  ",
	} {
		mine, err := stateRepo.ListMemberTemplateOptOutsFor(address)
		if err != nil {
			t.Fatalf("ListMemberTemplateOptOutsFor(%q) error = %v", address, err)
		}
		if len(mine) != 2 {
			t.Errorf("ListMemberTemplateOptOutsFor(%q) = %d records, want 2", address, len(mine))
		}
	}
	other, err := stateRepo.ListMemberTemplateOptOutsFor("0x9999000000000000000000000000000000000009")
	if err != nil {
		t.Fatalf("ListMemberTemplateOptOutsFor(other) error = %v", err)
	}
	if len(other) != 1 || other[0].ID != "opt-3" {
		t.Errorf("other member's opt-outs = %+v, want only opt-3", other)
	}
	none, err := stateRepo.ListMemberTemplateOptOutsFor("0x0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("ListMemberTemplateOptOutsFor(unknown) error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown member's opt-outs = %+v, want none", none)
	}

	// Deleting is how a member changes their mind: the record goes and
	// the template becomes placeable again.
	if err := stateRepo.DeleteMemberTemplateOptOut("opt-1"); err != nil {
		t.Fatalf("DeleteMemberTemplateOptOut() error = %v", err)
	}
	remaining, err := stateRepo.ListMemberTemplateOptOutsFor("0xAbCd000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("ListMemberTemplateOptOutsFor() error = %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "opt-2" {
		t.Errorf("after delete = %+v, want only opt-2", remaining)
	}
}

// Covers is the scope rule: an opt-out with no unit is the member
// declining a template everywhere, one naming a unit declines it on
// that card alone. Reading this backwards would either idle a member's
// whole rig or ignore their refusal on the card they meant.
func TestMemberTemplateOptOutCovers(t *testing.T) {
	poolWide := types.MemberTemplateOptOut{TemplateID: "t-images"}
	if !poolWide.Covers("gpu-1") || !poolWide.Covers("gpu-2") || !poolWide.Covers("") {
		t.Errorf("an opt-out with no hardware unit must cover every card the member has")
	}
	oneCard := types.MemberTemplateOptOut{TemplateID: "t-images", HardwareUnitID: "gpu-2"}
	if !oneCard.Covers("gpu-2") {
		t.Errorf("Covers(gpu-2) = false for an opt-out naming gpu-2")
	}
	if oneCard.Covers("gpu-1") {
		t.Errorf("Covers(gpu-1) = true for an opt-out naming gpu-2 — the member's other card keeps earning")
	}
}
