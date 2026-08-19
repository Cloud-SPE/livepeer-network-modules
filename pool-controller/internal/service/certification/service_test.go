package certification

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestServiceStartAndCompleteCertificationPass(t *testing.T) {
	stateRepo := seedCertificationRepo(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	svc := NewWithClock(stateRepo, func() time.Time { return now })

	run, err := svc.StartAssignmentCertification("assign-1")
	if err != nil {
		t.Fatalf("StartAssignmentCertification() error = %v", err)
	}
	if run.ExecutionPath != ExecutionPathBrokerVirtualBackend || run.Status != types.CertificationRunning {
		t.Fatalf("run = %+v", run)
	}
	assignment, _ := stateRepo.GetTemplateAssignment("assign-1")
	hardware, _ := stateRepo.GetHardwareUnit("gpu-1")
	if assignment.State != types.TemplateAssignmentTesting || hardware.State != types.HardwareUnitTesting {
		t.Fatalf("testing states assignment=%s hardware=%s", assignment.State, hardware.State)
	}

	run, err = svc.CompleteRun(CompleteRequest{
		RunID:  run.ID,
		Passed: true,
		Results: []types.CertificationResult{{
			Name:      "health",
			Status:    "passed",
			CheckedAt: now,
		}},
	})
	if err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	assignment, _ = stateRepo.GetTemplateAssignment("assign-1")
	hardware, _ = stateRepo.GetHardwareUnit("gpu-1")
	if run.Status != types.CertificationPassed || assignment.State != types.TemplateAssignmentProbationary || hardware.State != types.HardwareUnitProbationary {
		t.Fatalf("pass states run=%s assignment=%s hardware=%s", run.Status, assignment.State, hardware.State)
	}
}

func TestServiceCompleteCertificationFailureThrottles(t *testing.T) {
	stateRepo := seedCertificationRepo(t)
	svc := New(stateRepo)
	run, err := svc.StartAssignmentCertification("assign-1")
	if err != nil {
		t.Fatalf("StartAssignmentCertification() error = %v", err)
	}
	run, err = svc.CompleteRun(CompleteRequest{RunID: run.ID, Passed: false, FailureReason: "smoke failed"})
	if err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	assignment, _ := stateRepo.GetTemplateAssignment("assign-1")
	hardware, _ := stateRepo.GetHardwareUnit("gpu-1")
	if run.Status != types.CertificationFailed || assignment.State != types.TemplateAssignmentThrottled || hardware.State != types.HardwareUnitThrottled {
		t.Fatalf("failure states run=%s assignment=%s hardware=%s", run.Status, assignment.State, hardware.State)
	}
}

func seedCertificationRepo(t *testing.T) *repo.StateRepo {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	now := time.Now().UTC()
	if err := stateRepo.PutHardwareUnit(types.HardwareUnit{
		ID:               "gpu-1",
		EnrollmentID:     "host-1",
		MemberEthAddress: "0x1111111111111111111111111111111111111111",
		GPUUUID:          "GPU-1",
		GPUModel:         "NVIDIA GeForce RTX 4090",
		State:            types.HardwareUnitRegistered,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("PutHardwareUnit() error = %v", err)
	}
	if err := stateRepo.PutTemplateCatalogEntry(types.TemplateCatalogEntry{
		ID:           "chat-4090",
		CapabilityID: "openai:chat-completions",
		OfferingID:   "default",
		Protocol:     "paid-job/v1",
		Status:       types.TemplateStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("PutTemplateCatalogEntry() error = %v", err)
	}
	if err := stateRepo.PutTemplateAssignment(types.TemplateAssignment{
		ID:               "assign-1",
		HardwareUnitID:   "gpu-1",
		HostEnrollmentID: "host-1",
		MemberEthAddress: "0x1111111111111111111111111111111111111111",
		TemplateID:       "chat-4090",
		Role:             types.TemplateAssignmentPrimary,
		State:            types.TemplateAssignmentPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	return stateRepo
}
