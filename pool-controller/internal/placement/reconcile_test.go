package placement

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

var reconcileNow = time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

func existingAssignment(hw, templateID string, role types.TemplateAssignmentRole, state types.TemplateAssignmentState) types.TemplateAssignment {
	return types.TemplateAssignment{
		ID:               hw + "|" + templateID,
		HardwareUnitID:   hw,
		HostEnrollmentID: "host-1",
		MemberEthAddress: testMember,
		TemplateID:       templateID,
		Role:             role,
		State:            state,
		CreatedAt:        reconcileNow.Add(-time.Hour),
		UpdatedAt:        reconcileNow.Add(-time.Hour),
	}
}

func wantedOn(hw string, placements ...Placement) Decision {
	return Decision{
		HardwareUnitID:   hw,
		MemberEthAddress: testMember,
		GPUClass:         ClassRTX4090,
		Placements:       placements,
	}
}

func primaryOf(templateID string) Placement {
	return Placement{TemplateID: templateID, Role: types.TemplateAssignmentPrimary, Reason: ReasonPlacedPrimary}
}

func secondaryOf(templateID string) Placement {
	return Placement{TemplateID: templateID, Role: types.TemplateAssignmentSecondary, Reason: ReasonPlacedSecondary}
}

func changeStrings(changes []Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, fmt.Sprintf("%s/%s/%s/%s", c.Kind, c.HardwareID, c.TemplateID, c.Role))
	}
	return out
}

func TestReconcileCreatesWhatIsWantedAndAbsent(t *testing.T) {
	changes := Reconcile(nil, []Decision{wantedOn("gpu-1", primaryOf("t-chat"))}, reconcileNow)
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want one create", changeStrings(changes))
	}
	change := changes[0]
	if change.Kind != ChangeCreate || change.Role != types.TemplateAssignmentPrimary {
		t.Fatalf("change = %+v, want a primary create", change)
	}
	// The assignment carried on the change is what the apply path
	// writes, so its shape is the contract, not an implementation
	// detail.
	got := change.Assignment
	if got.ID != "gpu-1|t-chat" {
		t.Errorf("assignment id = %q, want the hardware|template pair", got.ID)
	}
	if got.HardwareUnitID != "gpu-1" || got.TemplateID != "t-chat" || got.MemberEthAddress != testMember {
		t.Errorf("assignment = %+v, want it bound to gpu-1/t-chat/%s", got, testMember)
	}
	// Pending, not active: a new assignment has to be certified before
	// it may take real traffic.
	if got.State != types.TemplateAssignmentPending {
		t.Errorf("assignment state = %q, want pending", got.State)
	}
	if !got.CreatedAt.Equal(reconcileNow) || !got.UpdatedAt.Equal(reconcileNow) {
		t.Errorf("assignment timestamps = %v/%v, want the caller's now %v", got.CreatedAt, got.UpdatedAt, reconcileNow)
	}
	if change.Reason != ReasonPlacedPrimary {
		t.Errorf("change reason = %q, want the placement's reason", change.Reason)
	}
}

// Withdrawing an assignment drains it. Deleting the row would strand
// in-flight work: the member's container is still serving and the
// broker is still dispatching to it, and neither of them learns
// anything from a record that simply vanished.
func TestReconcileDrainsRatherThanDeletes(t *testing.T) {
	existing := []types.TemplateAssignment{
		existingAssignment("gpu-1", "t-images", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
	}
	changes := Reconcile(existing, []Decision{wantedOn("gpu-1")}, reconcileNow)
	if got := changeStrings(changes); !reflect.DeepEqual(got, []string{"drain/gpu-1/t-images/"}) {
		t.Fatalf("changes = %v, want a single drain", got)
	}
	if changes[0].Assignment.State != types.TemplateAssignmentActive {
		t.Errorf("drain change carries state %q, want the assignment untouched — the apply step sets draining",
			changes[0].Assignment.State)
	}
	if changes[0].Reason == "" {
		t.Errorf("drain change carries no reason; an operator reading the change list gets no explanation")
	}
}

func TestReconcileNoChangeWhenPlanMatchesReality(t *testing.T) {
	existing := []types.TemplateAssignment{
		existingAssignment("gpu-1", "t-chat", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
		existingAssignment("gpu-1", "t-audio", types.TemplateAssignmentSecondary, types.TemplateAssignmentProbationary),
	}
	decisions := []Decision{wantedOn("gpu-1", primaryOf("t-chat"), secondaryOf("t-audio"))}
	if changes := Reconcile(existing, decisions, reconcileNow); len(changes) != 0 {
		t.Fatalf("changes = %v, want none — a steady state must not churn the member's containers", changeStrings(changes))
	}
}

// A demotion or promotion changes what the card may spend itself on but
// not what it was certified to run, so it is a role change rather than
// a drain and a fresh create.
func TestReconcileRoleChangeBothDirections(t *testing.T) {
	demote := Reconcile(
		[]types.TemplateAssignment{
			existingAssignment("gpu-1", "t-audio", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
		},
		[]Decision{wantedOn("gpu-1", secondaryOf("t-audio"))},
		reconcileNow,
	)
	if got := changeStrings(demote); !reflect.DeepEqual(got, []string{"role_change/gpu-1/t-audio/secondary"}) {
		t.Fatalf("demotion changes = %v, want a role_change to secondary", got)
	}
	if demote[0].Assignment.ID != "gpu-1|t-audio" {
		t.Errorf("role change carries assignment %+v, want the existing row", demote[0].Assignment)
	}

	promote := Reconcile(
		[]types.TemplateAssignment{
			existingAssignment("gpu-1", "t-audio", types.TemplateAssignmentSecondary, types.TemplateAssignmentActive),
		},
		[]Decision{wantedOn("gpu-1", primaryOf("t-audio"))},
		reconcileNow,
	)
	if got := changeStrings(promote); !reflect.DeepEqual(got, []string{"role_change/gpu-1/t-audio/primary"}) {
		t.Fatalf("promotion changes = %v, want a role_change to primary", got)
	}
}

// An assignment already on its way out is left alone in both
// directions. Re-creating one the plan wants back would undo a drain
// mid-flight, and draining one that is already draining would reset the
// clock the retirement waits on.
func TestReconcileLeavesDepartingAssignmentsAlone(t *testing.T) {
	for _, state := range []types.TemplateAssignmentState{
		types.TemplateAssignmentDraining,
		types.TemplateAssignmentSuspended,
		types.TemplateAssignmentRetired,
	} {
		t.Run(string(state), func(t *testing.T) {
			existing := []types.TemplateAssignment{
				existingAssignment("gpu-1", "t-chat", types.TemplateAssignmentPrimary, state),
			}
			// Still wanted, and wanted in a different role: neither is
			// enough to interrupt the departure.
			if changes := Reconcile(existing, []Decision{wantedOn("gpu-1", secondaryOf("t-chat"))}, reconcileNow); len(changes) != 0 {
				t.Errorf("wanted-again changes = %v, want none", changeStrings(changes))
			}
			if changes := Reconcile(existing, []Decision{wantedOn("gpu-1")}, reconcileNow); len(changes) != 0 {
				t.Errorf("no-longer-wanted changes = %v, want none", changeStrings(changes))
			}
		})
	}
}

// The change list is logged, shown to operators and compared between
// runs; the existing assignments arrive from a map, so the ordering has
// to be imposed here rather than inherited.
func TestReconcileOrderingIsDeterministic(t *testing.T) {
	existing := []types.TemplateAssignment{
		existingAssignment("gpu-2", "t-embed", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
		existingAssignment("gpu-1", "t-images", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
		existingAssignment("gpu-1", "t-audio", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
		existingAssignment("gpu-3", "t-transcode", types.TemplateAssignmentPrimary, types.TemplateAssignmentActive),
	}
	decisions := []Decision{
		wantedOn("gpu-1", primaryOf("t-chat"), secondaryOf("t-audio")),
		wantedOn("gpu-2", primaryOf("t-embed")),
		wantedOn("gpu-3"),
	}
	// Sorted by hardware, then template id, then kind — so the two
	// gpu-1 changes are ordered t-audio, t-chat, t-images regardless of
	// which kind each one is.
	want := []string{
		"role_change/gpu-1/t-audio/secondary",
		"create/gpu-1/t-chat/primary",
		"drain/gpu-1/t-images/",
		"drain/gpu-3/t-transcode/",
	}
	first := Reconcile(existing, decisions, reconcileNow)
	if got := changeStrings(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	// Map iteration order varies per run, so repeat enough times that a
	// dependence on it would show.
	baseline, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal changes: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := json.Marshal(Reconcile(existing, decisions, reconcileNow))
		if err != nil {
			t.Fatalf("marshal changes: %v", err)
		}
		if string(again) != string(baseline) {
			t.Fatalf("run %d differs:\nbaseline=%s\n     got=%s", i, baseline, again)
		}
	}
}
