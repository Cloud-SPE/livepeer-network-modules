package repo

import (
	"strings"
	"testing"
	"time"

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

	template := types.TemplateCatalogEntry{
		ID:               "image-realvisxl",
		CapabilityID:     "image-generation",
		OfferingID:       "realvisxl",
		InteractionMode:  "http-reqresp@v0",
		PrimaryAllowed:   true,
		AllowedGPUModels: []string{"RTX 4090"},
	}
	if err := repo.PutTemplateCatalogEntry(template); err != nil {
		t.Fatalf("PutTemplateCatalogEntry() error = %v", err)
	}
	gotTemplate, err := repo.GetTemplateCatalogEntry("image-realvisxl")
	if err != nil {
		t.Fatalf("GetTemplateCatalogEntry() error = %v", err)
	}
	if gotTemplate.Status != types.TemplateStatusActive {
		t.Fatalf("template status = %q, want %q", gotTemplate.Status, types.TemplateStatusActive)
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
