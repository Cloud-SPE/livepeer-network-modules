package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// The exception queue is what onboarding deliberately refuses to
// decide. Suspending a member, releasing a GPU two addresses claim,
// holding a settlement window — each is a judgement about someone's
// participation or someone's money, and none of them should happen
// because a score crossed a line.

func exceptionsServer(t *testing.T, stateRepo *repo.StateRepo) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func getExceptions(t *testing.T, server *httptest.Server) exceptionsView {
	t.Helper()
	resp, err := http.Get(server.URL + "/admin/v1/exceptions")
	if err != nil {
		t.Fatalf("GET /admin/v1/exceptions error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/v1/exceptions status=%d body=%s", resp.StatusCode, string(body))
	}
	var view exceptionsView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("Unmarshal(exceptions) error = %v body=%s", err, string(body))
	}
	return view
}

func TestExceptionsListsEverythingAPersonStillHasToDecide(t *testing.T) {
	dir := t.TempDir()
	stateRepo, err := repo.Open(dir)
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	for _, member := range []types.PoolMember{
		{ID: "0xaaa", EthAddress: "0xaaa", Status: types.MemberStatusSuspended},
		{ID: "0xbbb", EthAddress: "0xbbb", Status: types.MemberStatusActive},
	} {
		if err := stateRepo.PutPoolMember(member); err != nil {
			t.Fatalf("PutPoolMember() error = %v", err)
		}
	}
	for _, unit := range []types.HardwareUnit{
		{ID: "gpu-1", EnrollmentID: "host-1", MemberEthAddress: "0xaaa", GPUUUID: "GPU-AAA", State: types.HardwareUnitSuspended},
		{ID: "gpu-2", EnrollmentID: "host-2", MemberEthAddress: "0xbbb", GPUUUID: "GPU-BBB", State: types.HardwareUnitActive},
	} {
		if err := stateRepo.PutHardwareUnit(unit); err != nil {
			t.Fatalf("PutHardwareUnit() error = %v", err)
		}
	}
	for _, window := range []types.SettlementWindow{
		// Anomalous: a person has to look regardless of its status.
		{ID: "w-anomaly", Status: types.SettlementWindowClosing, Anomaly: "confirmed_revenue_below_attributed_revenue", SettlementScalePPM: 500_000},
		// Clean, but waiting on a human approval.
		{ID: "w-pending", Status: types.SettlementWindowPendingApproval, SettlementScalePPM: 1_000_000},
		// Neither: already paid and clean, nothing to decide.
		{ID: "w-paid", Status: types.SettlementWindowPaid, SettlementScalePPM: 1_000_000},
		{ID: "w-open", Status: types.SettlementWindowOpen, SettlementScalePPM: 1_000_000},
	} {
		if err := stateRepo.PutSettlementWindow(window); err != nil {
			t.Fatalf("PutSettlementWindow() error = %v", err)
		}
	}
	// PutHardwareUnit refuses to bind one gpu_uuid to a second member,
	// so the only way this list is ever non-empty is a row that
	// predates that guard. Writing it straight into the store is how a
	// test reaches the condition the queue exists to report.
	if err := stateRepo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	injectLegacyHardwareUnit(t, dir, types.HardwareUnit{
		ID: "gpu-3", EnrollmentID: "host-3", MemberEthAddress: "0xZZZ",
		GPUUUID: "GPU-AAA", State: types.HardwareUnitOnline, CreatedAt: now, UpdatedAt: now,
	})
	stateRepo, err = repo.Open(dir)
	if err != nil {
		t.Fatalf("repo.Open() (reopen) error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })

	view := getExceptions(t, exceptionsServer(t, stateRepo))

	if len(view.SuspendedMembers) != 1 || view.SuspendedMembers[0].EthAddress != "0xaaa" {
		t.Fatalf("SuspendedMembers = %+v, want only 0xaaa", view.SuspendedMembers)
	}
	if len(view.SuspendedGPUs) != 1 || view.SuspendedGPUs[0].ID != "gpu-1" {
		t.Fatalf("SuspendedGPUs = %+v, want only gpu-1", view.SuspendedGPUs)
	}

	held := map[string]heldWindowView{}
	for _, window := range view.HeldWindows {
		held[window.WindowID] = window
	}
	if len(held) != 2 {
		t.Fatalf("HeldWindows = %+v, want the anomalous and the pending_approval one", view.HeldWindows)
	}
	if held["w-anomaly"].Anomaly == "" {
		t.Fatalf("the anomalous window lost its anomaly: %+v", held["w-anomaly"])
	}
	if held["w-anomaly"].ScalePPM != 500_000 {
		t.Fatalf("scale = %d, want it carried so the operator can judge without a second call",
			held["w-anomaly"].ScalePPM)
	}
	if _, listed := held["w-pending"]; !listed {
		t.Fatal("a pending_approval window is not listed: a window nobody is told about is a window nobody approves")
	}
	if _, listed := held["w-paid"]; listed {
		t.Fatal("a paid, clean window is in the queue")
	}
	if _, listed := held["w-open"]; listed {
		t.Fatal("an open, clean window is in the queue")
	}

	if len(view.DuplicateGPUs) != 1 {
		t.Fatalf("DuplicateGPUs = %+v, want the one contested card", view.DuplicateGPUs)
	}
	dup := view.DuplicateGPUs[0]
	if dup.GPUUUID != "GPU-AAA" {
		t.Fatalf("duplicate gpu_uuid = %q", dup.GPUUUID)
	}
	// Sorted and lowercased: an operator comparing today's queue with
	// yesterday's must not see a diff that is only iteration order, and
	// address case is not a distinction between claimants.
	want := []string{"0xaaa", "0xzzz"}
	if len(dup.Members) != len(want) {
		t.Fatalf("duplicate members = %v, want %v", dup.Members, want)
	}
	for i, addr := range want {
		if dup.Members[i] != addr {
			t.Fatalf("duplicate members = %v, want %v (sorted, lowercased)", dup.Members, want)
		}
	}
	if !view.GeneratedAt.IsZero() && view.GeneratedAt.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("GeneratedAt = %s, want roughly now", view.GeneratedAt)
	}
}

// TestExceptionsAreEmptyListsNotNull matters because the console
// renders this directly: a null would be a crash or a blank panel where
// "nothing needs you" should read as an answer.
func TestExceptionsAreEmptyListsNotNull(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	server := exceptionsServer(t, stateRepo)

	resp, err := http.Get(server.URL + "/admin/v1/exceptions")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, field := range []string{
		`"suspended_members":[]`, `"suspended_hardware":[]`,
		`"held_windows":[]`, `"duplicate_gpus":[]`,
	} {
		if !bytes.Contains(body, []byte(field)) {
			t.Fatalf("body = %s, want %s", string(body), field)
		}
	}
}

func TestSuspendingAMemberWithoutAReasonIsRefused(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	if err := stateRepo.PutPoolMember(types.PoolMember{
		EthAddress: "0xAAA", Status: types.MemberStatusActive,
	}); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}
	server := exceptionsServer(t, stateRepo)

	for _, body := range []string{`{"status":"suspended"}`, `{"status":"suspended","reason":"   "}`} {
		status, _ := patchMember(t, server, "0xaaa", body)
		if status != http.StatusBadRequest {
			t.Fatalf("PATCH %s status = %d, want 400: a suspension nobody can review "+
				"later is not a decision", body, status)
		}
	}
	// And nothing was written on the way to refusing.
	member, _ := stateRepo.GetPoolMember("0xaaa")
	if member.Status != types.MemberStatusActive {
		t.Fatalf("member status = %q after a refused suspension", member.Status)
	}

	// An unrecognised status is refused too, so a typo cannot become a
	// third, undefined member state.
	if status, _ := patchMember(t, server, "0xaaa", `{"status":"banned","reason":"x"}`); status != http.StatusBadRequest {
		t.Fatalf("PATCH with an unknown status = %d, want 400", status)
	}
	// A member who does not exist is a 404 rather than a silent create.
	if status, _ := patchMember(t, server, "0xnope", `{"status":"suspended","reason":"x"}`); status != http.StatusNotFound {
		t.Fatalf("PATCH on an unknown member = %d, want 404", status)
	}
}

func TestSuspendingAMemberDrainsTheirPlacementsAndRecordsWhy(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	if err := stateRepo.PutPoolMember(types.PoolMember{
		EthAddress: "0xAAA", Status: types.MemberStatusActive,
	}); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}
	for _, assignment := range []types.TemplateAssignment{
		{ID: "a-active", MemberEthAddress: "0xAAA", State: types.TemplateAssignmentActive},
		{ID: "a-probation", MemberEthAddress: "0xaaa", State: types.TemplateAssignmentProbationary},
		// Already on its way out: draining it again would reset the
		// clock that says how long it has been draining.
		{ID: "a-draining", MemberEthAddress: "0xAAA", State: types.TemplateAssignmentDraining},
		// Finished: nothing to drain.
		{ID: "a-retired", MemberEthAddress: "0xAAA", State: types.TemplateAssignmentRetired},
		// Somebody else's runner, which a suspension must not touch.
		{ID: "a-other", MemberEthAddress: "0xBBB", State: types.TemplateAssignmentActive},
	} {
		if err := stateRepo.PutTemplateAssignment(assignment); err != nil {
			t.Fatalf("PutTemplateAssignment() error = %v", err)
		}
	}
	before, _ := stateRepo.GetTemplateAssignment("a-draining")
	server := exceptionsServer(t, stateRepo)

	status, body := patchMember(t, server, "0xaaa",
		`{"status":"suspended","reason":"repeated invalid output","actor":"ops@pool"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH status = %d body = %s", status, body)
	}
	var result struct {
		Member  types.PoolMember `json:"member"`
		Drained int              `json:"drained_placements"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Unmarshal error = %v body = %s", err, string(body))
	}
	if result.Member.Status != types.MemberStatusSuspended {
		t.Fatalf("member status = %q, want suspended", result.Member.Status)
	}
	// Two: the active and the probationary one. Suspension has to reach
	// the work, not just the record — a member suspended on paper whose
	// runners keep serving is not suspended.
	if result.Drained != 2 {
		t.Fatalf("drained = %d, want 2", result.Drained)
	}

	for _, id := range []string{"a-active", "a-probation"} {
		assignment, err := stateRepo.GetTemplateAssignment(id)
		if err != nil {
			t.Fatalf("GetTemplateAssignment(%s) error = %v", id, err)
		}
		if assignment.State != types.TemplateAssignmentDraining {
			t.Fatalf("%s state = %q, want draining", id, assignment.State)
		}
		// Draining rather than stopping, so in-flight requests finish
		// where they are — and the timestamp is what tells anyone how
		// long that has been going on.
		if assignment.DrainingSince.IsZero() {
			t.Fatalf("%s has no DrainingSince", id)
		}
	}
	after, _ := stateRepo.GetTemplateAssignment("a-draining")
	if !after.DrainingSince.Equal(before.DrainingSince) {
		t.Fatalf("an already-draining placement had its DrainingSince reset: %s -> %s",
			before.DrainingSince, after.DrainingSince)
	}
	retired, _ := stateRepo.GetTemplateAssignment("a-retired")
	if retired.State != types.TemplateAssignmentRetired {
		t.Fatalf("a retired placement was moved to %q", retired.State)
	}
	other, _ := stateRepo.GetTemplateAssignment("a-other")
	if other.State != types.TemplateAssignmentActive {
		t.Fatalf("another member's placement was drained: state = %q", other.State)
	}

	// Filtered on the CANONICAL lowercase id, which is the key every
	// lookup in this system uses. Recording the enrolled spelling —
	// which may be checksummed — would hide a member's status changes
	// from anyone querying the trail by that id.
	events, err := stateRepo.ListAuditEventsFiltered("member_status_changed", "pool_member", "0xaaa", 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %+v, want one", events)
	}
	event := events[0]
	if event.Actor != "ops@pool" {
		t.Fatalf("actor = %q", event.Actor)
	}
	if event.Details["status"] != string(types.MemberStatusSuspended) {
		t.Fatalf("audit status = %v", event.Details["status"])
	}
	// The reason is the whole point of requiring one: it has to survive
	// into the record, not just gate the request.
	if event.Details["reason"] != "repeated invalid output" {
		t.Fatalf("audit reason = %v, want the submitted reason", event.Details["reason"])
	}
	if drained, ok := event.Details["drained_placements"].(float64); !ok || int(drained) != 2 {
		t.Fatalf("audit drained_placements = %#v, want 2", event.Details["drained_placements"])
	}

	// The member now shows up in the queue a person reads.
	view := getExceptions(t, server)
	if len(view.SuspendedMembers) != 1 || view.SuspendedMembers[0].EthAddress != "0xAAA" {
		t.Fatalf("SuspendedMembers = %+v", view.SuspendedMembers)
	}
}

func TestReinstatingAMemberNeedsNoReasonAndDrainsNothing(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	if err := stateRepo.PutPoolMember(types.PoolMember{
		EthAddress: "0xAAA", Status: types.MemberStatusSuspended,
	}); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}
	if err := stateRepo.PutTemplateAssignment(types.TemplateAssignment{
		ID: "a-active", MemberEthAddress: "0xAAA", State: types.TemplateAssignmentActive,
	}); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	server := exceptionsServer(t, stateRepo)

	// Letting someone back in is not the decision that needs a written
	// justification; shutting them out is.
	status, body := patchMember(t, server, "0xaaa", `{"status":"active"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH status = %d body = %s", status, body)
	}
	var result struct {
		Member  types.PoolMember `json:"member"`
		Drained int              `json:"drained_placements"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if result.Member.Status != types.MemberStatusActive {
		t.Fatalf("status = %q, want active", result.Member.Status)
	}
	if result.Drained != 0 {
		t.Fatalf("drained = %d on a reinstatement, want 0", result.Drained)
	}
	assignment, _ := stateRepo.GetTemplateAssignment("a-active")
	if assignment.State != types.TemplateAssignmentActive {
		t.Fatalf("a reinstatement drained a placement: state = %q", assignment.State)
	}
	if view := getExceptions(t, server); len(view.SuspendedMembers) != 0 {
		t.Fatalf("SuspendedMembers = %+v after reinstatement", view.SuspendedMembers)
	}
}

func patchMember(t *testing.T, server *httptest.Server, address, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch,
		server.URL+"/admin/v1/pool-members/"+address, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH error = %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, out
}

// injectLegacyHardwareUnit writes a hardware-unit row straight into the
// store, bypassing PutHardwareUnit's one-member-per-gpu_uuid guard.
//
// That guard means the duplicate-GPU list can no longer be produced
// through the API at all: the only rows that can populate it are ones
// written before the guard existed. The repo must be closed first —
// bolt is single-writer.
func injectLegacyHardwareUnit(t *testing.T, dir string, unit types.HardwareUnit) {
	t.Helper()
	db, err := bolt.Open(filepath.Join(dir, "pool-controller.db"), 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bolt.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	raw, err := json.Marshal(unit)
	if err != nil {
		t.Fatalf("Marshal(unit) error = %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("hardware_units"))
		if err != nil {
			return err
		}
		return bucket.Put([]byte(unit.ID), raw)
	}); err != nil {
		t.Fatalf("inject legacy hardware unit: %v", err)
	}
}
