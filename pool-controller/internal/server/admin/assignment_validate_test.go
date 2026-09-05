package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// postAssignment writes an assignment by hand, as an operator would.
func postAssignment(t *testing.T, server *httptest.Server, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(server.URL+"/admin/v1/template-assignments",
		"application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST template-assignments error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// The escape hatch still works: an operator can place a template the
// planner would have placed.
func TestManualAssignmentAcceptsAPairingThePlannerWouldMake(t *testing.T) {
	stateRepo, server := seedPlacementServer(t)

	status, body := postAssignment(t,
		server, `{"hardware_unit_id":"gpu-1","template_id":"t-chat"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var got types.TemplateAssignment
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	// The id is the planner's, so a hand-written assignment and a
	// planned one for the same pair are one record rather than two that
	// quietly double the card's work.
	if got.ID != "gpu-1|t-chat" {
		t.Errorf("id = %q, want the planner's gpu-1|t-chat", got.ID)
	}
	// Host and member come from the card, not the request body.
	if got.HostEnrollmentID != "host-1" || got.MemberEthAddress != placementMember {
		t.Errorf("assignment = %+v, want host and member derived from the hardware unit", got)
	}
	if _, err := stateRepo.GetTemplateAssignment("gpu-1|t-chat"); err != nil {
		t.Fatalf("the accepted assignment was not stored: %v", err)
	}
	// Applying a plan is audited; a hand-written assignment moves the
	// same member's hardware and has to be on the record too, or the
	// trail reads as though placement were entirely automatic.
	events, err := stateRepo.ListAuditEventsFiltered("template_assignment_created", "", "", 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want the manual assignment recorded", len(events))
	}
	if events[0].ResourceID != "gpu-1|t-chat" || events[0].OccurredAt.IsZero() {
		t.Errorf("audit event = %+v, want a timestamped event naming the assignment", events[0])
	}
}

// An assignment that names a card the workload cannot run used to be
// accepted. It produced a container pinned to a GPU that cannot serve
// it, and the operator learned that from a failed certification hours
// later rather than from the request that caused it.
func TestManualAssignmentRejectsAnIncompatibleCard(t *testing.T) {
	stateRepo, server := seedPlacementServer(t)

	// t-chat requires rtx-4090; gpu-2 is a GTX 1080.
	status, body := postAssignment(t,
		server, `{"hardware_unit_id":"gpu-2","template_id":"t-chat"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — a 1080 cannot run a 4090-only template. body = %s", status, body)
	}
	// The message has to name both halves, or an operator cannot tell
	// which of the two ids they got wrong.
	if !strings.Contains(body, "t-chat") || !strings.Contains(body, "1080") {
		t.Errorf("message %q names neither the template nor the card", strings.TrimSpace(body))
	}
	if _, err := stateRepo.GetTemplateAssignment("gpu-2|t-chat"); err == nil {
		t.Fatal("the rejected assignment was stored anyway")
	}
}

// A template that is not in the catalog renders no service at all: the
// agent gets a desired state with nothing in it and the card sits idle
// with no error anywhere.
func TestManualAssignmentRejectsAnUnknownTemplate(t *testing.T) {
	_, server := seedPlacementServer(t)

	status, body := postAssignment(t,
		server, `{"hardware_unit_id":"gpu-1","template_id":"t-does-not-exist"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body = %s", status, body)
	}
	if !strings.Contains(body, "t-does-not-exist") {
		t.Errorf("message %q does not name the template", strings.TrimSpace(body))
	}
}

// A hardware unit that does not exist has no host and no member, so the
// assignment could never reach a machine or pay anyone.
func TestManualAssignmentRejectsAnUnknownHardwareUnit(t *testing.T) {
	_, server := seedPlacementServer(t)

	status, body := postAssignment(t,
		server, `{"hardware_unit_id":"gpu-nope","template_id":"t-chat"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body = %s", status, body)
	}
	if !strings.Contains(body, "gpu-nope") {
		t.Errorf("message %q does not name the missing unit", strings.TrimSpace(body))
	}
}

// Both ids are required. Without them the record names nothing.
func TestManualAssignmentRequiresBothIDs(t *testing.T) {
	_, server := seedPlacementServer(t)

	for _, body := range []string{
		`{"template_id":"t-chat"}`,
		`{"hardware_unit_id":"gpu-1"}`,
		`{}`,
	} {
		if status, got := postAssignment(t, server, body); status != http.StatusBadRequest {
			t.Errorf("POST %s status = %d, want 400 (%s)", body, status, strings.TrimSpace(got))
		}
	}
}

// Roles are a stacking claim, not a label. A template that does not
// declare itself stackable on a class cannot ride alongside another
// workload there, and one that does not declare stacking.primary cannot
// hold a card's primary slot.
func TestManualAssignmentEnforcesTheTemplatesStackingStance(t *testing.T) {
	_, server := seedPlacementServer(t)

	// t-transcode declares primary only, with no secondary_on.
	status, body := postAssignment(t, server,
		`{"hardware_unit_id":"gpu-1","template_id":"t-transcode","role":"secondary"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — t-transcode declares no secondary_on. body = %s", status, body)
	}
	if !strings.Contains(body, "secondary_on") {
		t.Errorf("message %q does not point at the stance it violates", strings.TrimSpace(body))
	}

	// t-audio does declare rtx-4090 in secondary_on, so the same shape
	// with a stackable template is accepted.
	if status, body := postAssignment(t, server,
		`{"hardware_unit_id":"gpu-1","template_id":"t-audio","role":"secondary"}`); status != http.StatusOK {
		t.Fatalf("a template that declares secondary_on was refused a secondary role: %d %s", status, body)
	}
}

// An unrecognised role is a typo, and a typo that stored would put a
// card in a state neither the planner nor the ladder reasons about.
func TestManualAssignmentRejectsAnUnknownRole(t *testing.T) {
	_, server := seedPlacementServer(t)

	status, body := postAssignment(t, server,
		`{"hardware_unit_id":"gpu-1","template_id":"t-chat","role":"tertiary"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body = %s", status, body)
	}
	if !strings.Contains(body, "tertiary") {
		t.Errorf("message %q does not quote the bad role", strings.TrimSpace(body))
	}
}

// A retired or suspended card takes no new work, however compatible it
// is: assigning to one would render a service for hardware the pool has
// already stopped trusting.
func TestManualAssignmentRejectsACardThatTakesNoWork(t *testing.T) {
	stateRepo, server := seedPlacementServer(t)

	for _, state := range []types.HardwareUnitState{
		types.HardwareUnitSuspended, types.HardwareUnitRetired,
	} {
		unit, err := stateRepo.GetHardwareUnit("gpu-1")
		if err != nil {
			t.Fatalf("GetHardwareUnit() error = %v", err)
		}
		unit.State = state
		if err := stateRepo.PutHardwareUnit(unit); err != nil {
			t.Fatalf("PutHardwareUnit() error = %v", err)
		}
		status, body := postAssignment(t, server,
			`{"hardware_unit_id":"gpu-1","template_id":"t-chat"}`)
		if status != http.StatusBadRequest {
			t.Errorf("a %s card accepted an assignment: %d %s", state, status, body)
		}
	}
}
