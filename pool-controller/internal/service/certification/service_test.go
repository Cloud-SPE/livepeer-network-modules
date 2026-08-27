package certification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestServiceStartAndCompleteCertificationPass(t *testing.T) {
	stateRepo, catalog := seedCertificationRepo(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	svc := NewWithClock(stateRepo, catalog, func() time.Time { return now })

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
	stateRepo, catalog := seedCertificationRepo(t)
	svc := New(stateRepo, catalog)
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

func seedCertificationRepo(t *testing.T) (*repo.StateRepo, *templates.Catalog) {
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
	return stateRepo, loadCatalog(t, "chat-4090")
}

// loadCatalog builds a catalog the way production does — from files —
// so a test cannot certify against a template shape the loader would
// have rejected.
func loadCatalog(t *testing.T, ids ...string) *templates.Catalog {
	t.Helper()
	dir := t.TempDir()
	for i, id := range ids {
		body := "id: " + id + "\n" +
			"capability: openai:chat-completions\n" +
			"offering_id: offering-" + id + "\n" +
			"protocol: paid-job/v1\n" +
			"price_default:\n  amount_wei: \"1\"\n  per_units: 1\n" +
			"stacking:\n  primary: true\n"
		if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write template %d: %v", i, err)
		}
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("templates.Load() error = %v", err)
	}
	return catalog
}

// A run certifies against a recipe. An assignment naming a template
// this build does not ship has no recipe, so it must not start one.
func TestServiceRejectsTemplateOutsideCatalog(t *testing.T) {
	stateRepo, _ := seedCertificationRepo(t)
	svc := New(stateRepo, loadCatalog(t, "something-else"))
	_, err := svc.StartAssignmentCertification("assign-1")
	if err == nil || !strings.Contains(err.Error(), "not in the catalog") {
		t.Fatalf("StartAssignmentCertification() error = %v, want a catalog rejection", err)
	}
	runs, err := stateRepo.ListCertificationRuns()
	if err != nil || len(runs) != 0 {
		t.Fatalf("a rejected start left runs behind: %#v err=%v", runs, err)
	}
}
