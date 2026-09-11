package claims

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func openRepo(t *testing.T) *repo.StateRepo {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	return stateRepo
}

// The point of the whole mechanism: an unproven claim must actually
// RELEASE the uuid, so the real owner's next attach succeeds without an
// operator. Retiring the row while the uniqueness guard still counted it
// would be a state change that changed nothing.
func TestExpiringAClaimReleasesTheUUIDForSomeoneElse(t *testing.T) {
	stateRepo := openRepo(t)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	if err := stateRepo.PutHardwareUnit(types.HardwareUnit{
		ID: "squat", EnrollmentID: "host-s", MemberEthAddress: "0xsquatter",
		GPUUUID: "GPU-DISPUTED", State: types.HardwareUnitOnline,
		CreatedAt: now.Add(-30 * 24 * time.Hour), UpdatedAt: now.Add(-30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("PutHardwareUnit() error = %v", err)
	}
	// The real owner is refused while the squat stands.
	realOwner := types.HardwareUnit{
		ID: "real", EnrollmentID: "host-r", MemberEthAddress: "0xowner",
		GPUUUID: "GPU-DISPUTED", State: types.HardwareUnitOnline, CreatedAt: now, UpdatedAt: now,
	}
	if err := stateRepo.PutHardwareUnit(realOwner); err == nil {
		t.Fatal("the real owner was admitted while another member held the uuid; " +
			"uniqueness is what stops one card earning twice")
	}

	summary, err := Sweep(stateRepo, DefaultGrace, now)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(summary.Expired) != 1 || summary.Expired[0].HardwareUnitID != "squat" {
		t.Fatalf("Expired = %+v, want the unproven claim released", summary.Expired)
	}

	// And now it lands. This is the assertion the feature exists for.
	if err := stateRepo.PutHardwareUnit(realOwner); err != nil {
		t.Fatalf("the real owner is STILL blocked after the claim expired: %v", err)
	}
}

// A card that has done real work keeps its uuid for as long as it holds
// it — that is the anti-fraud rule and expiry must not weaken it.
func TestAProvenClaimIsNeverExpired(t *testing.T) {
	stateRepo := openRepo(t)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-365 * 24 * time.Hour)

	for _, tc := range []struct {
		name  string
		setup func(unitID string)
	}{
		{"a placement that reached probation", func(unitID string) {
			_ = stateRepo.PutTemplateAssignment(types.TemplateAssignment{
				ID: unitID + "|t", HardwareUnitID: unitID, TemplateID: "t",
				State: types.TemplateAssignmentProbationary,
			})
		}},
		{"a placement carrying a certification date", func(unitID string) {
			_ = stateRepo.PutTemplateAssignment(types.TemplateAssignment{
				ID: unitID + "|t", HardwareUnitID: unitID, TemplateID: "t",
				State: types.TemplateAssignmentPending, LastCertifiedAt: ancient,
			})
		}},
		{"a passed certification run", func(unitID string) {
			_ = stateRepo.PutTemplateAssignment(types.TemplateAssignment{
				ID: unitID + "|t", HardwareUnitID: unitID, TemplateID: "t",
				State: types.TemplateAssignmentPending,
			})
			_ = stateRepo.PutCertificationRun(types.CertificationRun{
				ID: "cert-" + unitID, AssignmentID: unitID + "|t",
				Status: types.CertificationPassed, CompletedAt: ancient,
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unitID := "unit-" + tc.name
			if err := stateRepo.PutHardwareUnit(types.HardwareUnit{
				ID: unitID, MemberEthAddress: "0xowner", GPUUUID: "GPU-" + unitID,
				State: types.HardwareUnitOnline, CreatedAt: ancient, UpdatedAt: ancient,
			}); err != nil {
				t.Fatalf("PutHardwareUnit() error = %v", err)
			}
			tc.setup(unitID)

			summary, err := Sweep(stateRepo, DefaultGrace, now)
			if err != nil {
				t.Fatalf("Sweep() error = %v", err)
			}
			for _, expired := range summary.Expired {
				if expired.HardwareUnitID == unitID {
					t.Fatalf("a card that has done real work was released: %+v", expired)
				}
			}
		})
	}
}

// Inside the grace a claim stands, however idle. A member who enrols on
// a Friday and installs on a Monday must never lose their card to this.
func TestAClaimInsideTheGraceStands(t *testing.T) {
	stateRepo := openRepo(t)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if err := stateRepo.PutHardwareUnit(types.HardwareUnit{
		ID: "fresh", MemberEthAddress: "0xowner", GPUUUID: "GPU-FRESH",
		State:     types.HardwareUnitOnline,
		CreatedAt: now.Add(-DefaultGrace + time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("PutHardwareUnit() error = %v", err)
	}
	summary, err := Sweep(stateRepo, DefaultGrace, now)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(summary.Expired) != 0 {
		t.Fatalf("Expired = %+v, want none inside the grace", summary.Expired)
	}
}

// A missing claim time must never release a card. Treating "unknown" as
// "ancient" would take hardware away from members on the strength of a
// field nobody set.
func TestAClaimWithNoRecordedTimeIsLeftAlone(t *testing.T) {
	stateRepo := openRepo(t)
	unit := types.HardwareUnit{
		ID: "no-time", MemberEthAddress: "0xowner", GPUUUID: "GPU-NOTIME",
		State: types.HardwareUnitOnline,
	}
	if err := stateRepo.PutHardwareUnit(unit); err != nil {
		t.Fatalf("PutHardwareUnit() error = %v", err)
	}
	stored, err := stateRepo.GetHardwareUnit("no-time")
	if err != nil {
		t.Fatalf("GetHardwareUnit() error = %v", err)
	}
	if !stored.CreatedAt.IsZero() {
		t.Skip("the store stamps CreatedAt itself, so this case cannot arise")
	}
	summary, err := Sweep(stateRepo, DefaultGrace, time.Now().UTC().Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(summary.Expired) != 0 {
		t.Fatalf("a claim with no recorded time was released: %+v", summary.Expired)
	}
}
