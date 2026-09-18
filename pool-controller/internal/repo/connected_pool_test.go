package repo

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestStateRepoConnectedPoolEntitiesPersist(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	member := types.PoolMember{
		EthAddress: "0xABC",
		PayoutMode: "eth",
	}
	if err := repo.PutPoolMember(member); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}
	gotMember, err := repo.GetPoolMember("0xabc")
	if err != nil {
		t.Fatalf("GetPoolMember() error = %v", err)
	}
	if gotMember.Status != types.MemberStatusActive {
		t.Fatalf("member status = %q, want %q", gotMember.Status, types.MemberStatusActive)
	}

	nonce := types.MemberNonce{
		ID:         "nonce-1",
		EthAddress: "0xABC",
		Nonce:      "abc123",
		Message:    "Sign this",
		ExpiresAt:  time.Now().UTC().Add(time.Minute),
	}
	if err := repo.PutMemberNonce(nonce); err != nil {
		t.Fatalf("PutMemberNonce() error = %v", err)
	}
	if err := repo.MarkMemberNonceUsed("nonce-1", time.Now().UTC()); err != nil {
		t.Fatalf("MarkMemberNonceUsed() error = %v", err)
	}
	gotNonce, err := repo.GetMemberNonce("nonce-1")
	if err != nil {
		t.Fatalf("GetMemberNonce() error = %v", err)
	}
	if gotNonce.UsedAt.IsZero() {
		t.Fatalf("nonce was not marked used: %#v", gotNonce)
	}

	enrollment := types.HostEnrollment{
		ID:               "host-1",
		MemberEthAddress: "0xABC",
		HostLabel:        "rig-a",
	}
	if err := repo.PutHostEnrollment(enrollment); err != nil {
		t.Fatalf("PutHostEnrollment() error = %v", err)
	}
	gotEnrollment, err := repo.GetHostEnrollment("host-1")
	if err != nil {
		t.Fatalf("GetHostEnrollment() error = %v", err)
	}
	if gotEnrollment.Status != types.HostEnrollmentPending {
		t.Fatalf("enrollment status = %q, want %q", gotEnrollment.Status, types.HostEnrollmentPending)
	}

	unit := types.HardwareUnit{
		ID:               "gpu-1",
		EnrollmentID:     "host-1",
		MemberEthAddress: "0xABC",
		GPUUUID:          "GPU-abc",
		GPUModel:         "RTX 4090",
		VRAMBytes:        24 * 1024 * 1024 * 1024,
	}
	if err := repo.PutHardwareUnit(unit); err != nil {
		t.Fatalf("PutHardwareUnit() error = %v", err)
	}
	units, err := repo.ListHardwareUnitsByEnrollment("host-1")
	if err != nil {
		t.Fatalf("ListHardwareUnitsByEnrollment() error = %v", err)
	}
	if len(units) != 1 || units[0].State != types.HardwareUnitRegistered {
		t.Fatalf("hardware units = %#v", units)
	}

	// The catalog itself is files on disk; the only template state the
	// database carries is the pool's own decision about one.
	override := types.TemplateOverride{
		TemplateID: "image-realvisxl",
		Enabled:    true,
		Price:      &config.Price{AmountWei: "12", PerUnits: 1},
		Extra:      map[string]any{"provider": "comfy"},
		UpdatedBy:  "operator-a",
	}
	if err := repo.PutTemplateOverride(override); err != nil {
		t.Fatalf("PutTemplateOverride() error = %v", err)
	}
	gotOverride, err := repo.GetTemplateOverride("image-realvisxl")
	if err != nil {
		t.Fatalf("GetTemplateOverride() error = %v", err)
	}
	if !gotOverride.Enabled || gotOverride.Price == nil || gotOverride.Price.AmountWei != "12" {
		t.Fatalf("override = %#v", gotOverride)
	}
	if gotOverride.Extra["provider"] != "comfy" || gotOverride.UpdatedBy != "operator-a" {
		t.Fatalf("override lost its operator metadata: %#v", gotOverride)
	}
	// The store stamps the write time, so a console can show when the
	// pool last changed its mind without the caller supplying it.
	if gotOverride.UpdatedAt.IsZero() {
		t.Fatalf("override was stored without an updated_at: %#v", gotOverride)
	}
	overrides, err := repo.ListTemplateOverrides()
	if err != nil {
		t.Fatalf("ListTemplateOverrides() error = %v", err)
	}
	if len(overrides) != 1 || overrides[0].TemplateID != "image-realvisxl" {
		t.Fatalf("overrides = %#v", overrides)
	}
	// Deleting reverts to the catalog's default, which is the absence of
	// a record rather than a disabled one.
	if err := repo.DeleteTemplateOverride("image-realvisxl"); err != nil {
		t.Fatalf("DeleteTemplateOverride() error = %v", err)
	}
	if overrides, err := repo.ListTemplateOverrides(); err != nil || len(overrides) != 0 {
		t.Fatalf("after delete overrides = %#v err = %v", overrides, err)
	}

	assignment := types.TemplateAssignment{
		ID:               "assign-1",
		HardwareUnitID:   "gpu-1",
		HostEnrollmentID: "host-1",
		MemberEthAddress: "0xABC",
		TemplateID:       "image-realvisxl",
	}
	if err := repo.PutTemplateAssignment(assignment); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	assignments, err := repo.ListTemplateAssignmentsByHardwareUnit("gpu-1")
	if err != nil {
		t.Fatalf("ListTemplateAssignmentsByHardwareUnit() error = %v", err)
	}
	if len(assignments) != 1 || assignments[0].Role != types.TemplateAssignmentPrimary {
		t.Fatalf("assignments = %#v", assignments)
	}

	run := types.CertificationRun{
		ID:               "cert-1",
		AssignmentID:     "assign-1",
		HardwareUnitID:   "gpu-1",
		HostEnrollmentID: "host-1",
		TemplateID:       "image-realvisxl",
	}
	if err := repo.PutCertificationRun(run); err != nil {
		t.Fatalf("PutCertificationRun() error = %v", err)
	}
	runs, err := repo.ListCertificationRuns()
	if err != nil {
		t.Fatalf("ListCertificationRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != types.CertificationPending {
		t.Fatalf("runs = %#v", runs)
	}

	window := types.SettlementWindow{
		ID:           "window-1",
		StartRoundID: "140",
		EndRoundID:   "153",
		LengthRounds: 14,
	}
	if err := repo.PutSettlementWindow(window); err != nil {
		t.Fatalf("PutSettlementWindow() error = %v", err)
	}
	gotWindow, err := repo.GetSettlementWindow("window-1")
	if err != nil {
		t.Fatalf("GetSettlementWindow() error = %v", err)
	}
	if gotWindow.Status != types.SettlementWindowOpen {
		t.Fatalf("window status = %q, want %q", gotWindow.Status, types.SettlementWindowOpen)
	}

	batch := types.PayoutBatch{
		ID:                 "batch-1",
		SettlementWindowID: "window-1",
		TotalAmountWei:     "100",
	}
	if err := repo.PutPayoutBatch(batch); err != nil {
		t.Fatalf("PutPayoutBatch() error = %v", err)
	}
	gotBatch, err := repo.GetPayoutBatch("batch-1")
	if err != nil {
		t.Fatalf("GetPayoutBatch() error = %v", err)
	}
	if gotBatch.Status != types.PayoutBatchPendingApproval {
		t.Fatalf("batch status = %q, want %q", gotBatch.Status, types.PayoutBatchPendingApproval)
	}
}

func TestStateRepoHardwareUnitRejectsDuplicateGPUAcrossMembers(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	first := types.HardwareUnit{
		ID:               "gpu-1",
		EnrollmentID:     "host-1",
		MemberEthAddress: "0xaaa",
		GPUUUID:          "GPU-same",
	}
	if err := repo.PutHardwareUnit(first); err != nil {
		t.Fatalf("PutHardwareUnit(first) error = %v", err)
	}

	secondSameMember := types.HardwareUnit{
		ID:               "gpu-1-refresh",
		EnrollmentID:     "host-1",
		MemberEthAddress: "0xAAA",
		GPUUUID:          "GPU-same",
	}
	if err := repo.PutHardwareUnit(secondSameMember); err != nil {
		t.Fatalf("PutHardwareUnit(same member) error = %v", err)
	}

	secondOtherMember := types.HardwareUnit{
		ID:               "gpu-2",
		EnrollmentID:     "host-2",
		MemberEthAddress: "0xbbb",
		GPUUUID:          "GPU-same",
	}
	err = repo.PutHardwareUnit(secondOtherMember)
	if err == nil {
		t.Fatal("PutHardwareUnit(other member) succeeded; want duplicate error")
	}
	if !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("duplicate error = %v", err)
	}
}

// Contested GPUs. A contested card is a REFUSED claim, not a duplicate
// row: the uniqueness guard above is what makes it so, deliberately,
// because otherwise anyone could take a member's card contested — and
// stop it earning — just by declaring its uuid. So the tests below are
// about the record of the refusal, and mostly about it staying ONE
// record however hard the challenger's agent tries.

// openConflictRepo is a repo plus one recorded dispute over GPU-same:
// 0xbbb claiming a card 0xaaa holds.
func openConflictRepo(t *testing.T, at time.Time) (*StateRepo, types.HardwareClaimConflict) {
	t.Helper()
	stateRepo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	conflict, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-same",
		ChallengerEthAddress: "0xbbb",
		ChallengerHostID:     "host-2",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           at,
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict() error = %v", err)
	}
	return stateRepo, conflict
}

// A host whose agent re-attaches every thirty seconds is one dispute
// seen repeatedly, not a dispute a second. If each attempt appended a
// row, a single misconfigured host would bury every other exception an
// operator has to look at within an hour.
func TestRecordHardwareClaimConflictUpsertsOnGPUAndChallenger(t *testing.T) {
	first := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	stateRepo, conflict := openConflictRepo(t, first)
	if conflict.Attempts != 1 {
		t.Fatalf("attempts = %d on the first sighting, want 1", conflict.Attempts)
	}
	if !conflict.FirstSeenAt.Equal(first) || !conflict.LastSeenAt.Equal(first) {
		t.Fatalf("first_seen = %s last_seen = %s, want both %s",
			conflict.FirstSeenAt, conflict.LastSeenAt, first)
	}

	later := first.Add(30 * time.Second)
	again, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-same",
		ChallengerEthAddress: "0xbbb",
		ChallengerHostID:     "host-2",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           later,
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict(again) error = %v", err)
	}
	if again.ID != conflict.ID {
		t.Fatalf("id = %q on the second sighting, want the same record %q", again.ID, conflict.ID)
	}
	if again.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2: the count is how an operator tells a single "+
			"mistaken enrolment from a host retrying for a week", again.Attempts)
	}
	// FirstSeenAt is the age of the dispute. Overwriting it would make
	// a week-old dispute look thirty seconds old at every retry.
	if !again.FirstSeenAt.Equal(first) {
		t.Fatalf("first_seen = %s, want it preserved at %s", again.FirstSeenAt, first)
	}
	if !again.LastSeenAt.Equal(later) {
		t.Fatalf("last_seen = %s, want it advanced to %s", again.LastSeenAt, later)
	}

	all, err := stateRepo.ListHardwareClaimConflicts()
	if err != nil {
		t.Fatalf("ListHardwareClaimConflicts() error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one record for one dispute", all)
	}
}

// Case and whitespace are not a distinction between claimants: the
// agent's spelling of an address or a uuid must not be able to split
// one dispute into two queue entries, which would be a way to hide a
// dispute in plain sight.
func TestRecordHardwareClaimConflictNormalisesAddressesAndUUID(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	stateRepo, first := openConflictRepo(t, at)

	messy, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "  GPU-same ",
		ChallengerEthAddress: " 0xBBB ",
		IncumbentEthAddress:  " 0xAAA ",
		LastSeenAt:           at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict(messy) error = %v", err)
	}
	if messy.ID != first.ID {
		t.Fatalf("id = %q, want the same dispute %q", messy.ID, first.ID)
	}
	if messy.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2 — the messy spelling is the same dispute", messy.Attempts)
	}
	if messy.GPUUUID != "GPU-same" {
		t.Fatalf("gpu_uuid = %q, want it trimmed", messy.GPUUUID)
	}
	if messy.ChallengerEthAddress != "0xbbb" || messy.IncumbentEthAddress != "0xaaa" {
		t.Fatalf("challenger = %q incumbent = %q, want both lowercased",
			messy.ChallengerEthAddress, messy.IncumbentEthAddress)
	}
	if messy.ID != HardwareClaimConflictID(" GPU-same ", "0xBBB") {
		t.Fatalf("id = %q, want HardwareClaimConflictID to agree on the same normalisation", messy.ID)
	}
	all, _ := stateRepo.ListHardwareClaimConflicts()
	if len(all) != 1 {
		t.Fatalf("conflicts = %+v, want one", all)
	}
}

// A different challenger on the same card is a genuinely different
// dispute: the operator has to decide about each claimant separately.
func TestRecordHardwareClaimConflictSeparatesDifferentChallengers(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	stateRepo, _ := openConflictRepo(t, at)
	if _, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-same",
		ChallengerEthAddress: "0xccc",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           at,
	}); err != nil {
		t.Fatalf("RecordHardwareClaimConflict(other challenger) error = %v", err)
	}
	all, _ := stateRepo.ListHardwareClaimConflicts()
	if len(all) != 2 {
		t.Fatalf("conflicts = %+v, want one per challenger", all)
	}
}

// A rejection was a decision about the claim the operator saw. A host
// still trying afterwards is new information — possibly a member who
// really did buy the card and is now stuck — so the dispute comes back
// to the queue rather than being silently swallowed forever.
func TestRejectedConflictReopensWhenTheChallengerKeepsTrying(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	stateRepo, conflict := openConflictRepo(t, at)

	conflict.Resolution = types.ConflictRejected
	conflict.ResolvedBy = "ops@pool"
	conflict.ResolvedAt = at.Add(time.Hour)
	conflict.Reason = "uuid looks cloned"
	if err := stateRepo.PutHardwareClaimConflict(conflict); err != nil {
		t.Fatalf("PutHardwareClaimConflict() error = %v", err)
	}

	reopened, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-same",
		ChallengerEthAddress: "0xbbb",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           at.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict(after rejection) error = %v", err)
	}
	if !reopened.Open() {
		t.Fatalf("resolution = %q after a re-claim, want it back on the queue: a host still "+
			"trying after a rejection is something a person should see", reopened.Resolution)
	}
	if reopened.Reason == "" || !strings.Contains(reopened.Reason, "rejection") {
		t.Fatalf("reason = %q, want it to say the dispute came back after a rejection", reopened.Reason)
	}
	if reopened.Attempts != 2 {
		t.Fatalf("attempts = %d, want the count carried across the reopen", reopened.Attempts)
	}
	if !reopened.FirstSeenAt.Equal(at) {
		t.Fatalf("first_seen = %s, want the original %s: reopening is not a new dispute",
			reopened.FirstSeenAt, at)
	}
}

// A transfer is settled. The incumbent's unit was retired, so the
// challenger's next attach is expected — reopening on it would hand the
// operator back a decision they already made, every thirty seconds.
func TestTransferredConflictDoesNotReopen(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	stateRepo, conflict := openConflictRepo(t, at)

	conflict.Resolution = types.ConflictTransferred
	conflict.ResolvedBy = "ops@pool"
	conflict.ResolvedAt = at.Add(time.Hour)
	conflict.Reason = "member sold the card"
	if err := stateRepo.PutHardwareClaimConflict(conflict); err != nil {
		t.Fatalf("PutHardwareClaimConflict() error = %v", err)
	}

	after, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-same",
		ChallengerEthAddress: "0xbbb",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           at.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict(after transfer) error = %v", err)
	}
	if after.Open() {
		t.Fatalf("resolution = %q, want it to stay transferred: the operator already decided "+
			"this one and the challenger attaching again is the expected outcome", after.Resolution)
	}
	if after.Resolution != types.ConflictTransferred {
		t.Fatalf("resolution = %q, want transferred", after.Resolution)
	}
	// The decision itself survives: who made it and why is what makes a
	// later dispute over the same card reviewable.
	if after.ResolvedBy != "ops@pool" || after.Reason != "member sold the card" {
		t.Fatalf("resolved_by = %q reason = %q, want the original decision preserved",
			after.ResolvedBy, after.Reason)
	}
}

// A record with no gpu or no challenger names no dispute, so it cannot
// be one — and would collide with every other malformed write on the
// same derived id.
func TestRecordHardwareClaimConflictRequiresBothSides(t *testing.T) {
	stateRepo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	for _, conflict := range []types.HardwareClaimConflict{
		{ChallengerEthAddress: "0xbbb", IncumbentEthAddress: "0xaaa"},
		{GPUUUID: "GPU-same", IncumbentEthAddress: "0xaaa"},
		{GPUUUID: "   ", ChallengerEthAddress: "   "},
	} {
		if _, err := stateRepo.RecordHardwareClaimConflict(conflict); err == nil {
			t.Fatalf("RecordHardwareClaimConflict(%+v) succeeded, want an error", conflict)
		}
	}
	if all, _ := stateRepo.ListHardwareClaimConflicts(); len(all) != 0 {
		t.Fatalf("conflicts = %+v, want nothing written by a refused record", all)
	}
}
