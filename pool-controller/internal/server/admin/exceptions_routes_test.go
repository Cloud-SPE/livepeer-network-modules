package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ladder"
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
	// A contested GPU is a REFUSED CLAIM, not a duplicate row. The
	// uniqueness guard means the second write never lands — that is the
	// point, or anyone could take a member's card contested by
	// declaring its uuid — so the queue reads the record of the refusal
	// instead.
	if _, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-AAA",
		ChallengerEthAddress: "0xZZZ",
		ChallengerHostID:     "host-3",
		IncumbentEthAddress:  "0xaaa",
		LastSeenAt:           now,
	}); err != nil {
		t.Fatalf("RecordHardwareClaimConflict() error = %v", err)
	}

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
	// Which side is which is the whole question an operator answers, so
	// the queue has to say — "two addresses claim this" is not enough
	// to act on. Addresses are lowercased because case is not a
	// distinction between claimants.
	if dup.IncumbentEthAddress != "0xaaa" || dup.ChallengerEthAddress != "0xzzz" {
		t.Fatalf("incumbent = %q challenger = %q, want 0xaaa and 0xzzz",
			dup.IncumbentEthAddress, dup.ChallengerEthAddress)
	}
	if dup.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", dup.Attempts)
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

// ---------------------------------------------------------------------
// Contested GPUs, from the operator's side of the wire.
//
// The queue reads refused CLAIMS. Resolving one either takes a card off
// the incumbent (transfer) or tells the challenger no (reject), and
// both are decisions about someone's ability to earn — so both leave a
// reason and an audit event behind, and neither may be guessed at from
// the shape of the data.

const (
	conflictIncumbent  = "0xAAA"
	conflictChallenger = "0xbbb"
)

// resolveConflict posts an operator decision. The conflict id is
// derived from the gpu uuid and the challenger and so contains a "|",
// which has to survive the round trip as a single path segment.
func resolveConflict(t *testing.T, server *httptest.Server, id, action, body string) (int, []byte) {
	t.Helper()
	target := server.URL + "/admin/v1/gpu-conflicts/" + url.PathEscape(id) + "/" + action
	resp, err := http.Post(target, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s error = %v", target, err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, out
}

// contestedFixture is one card 0xAAA holds and 0xbbb claims, plus a
// second card of the incumbent's that no dispute touches.
func contestedFixture(t *testing.T) (*repo.StateRepo, *httptest.Server, types.HardwareClaimConflict) {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	for _, unit := range []types.HardwareUnit{
		// The contested card. Enrolled with the checksummed spelling of
		// the address, which must not stop the retire finding it.
		{ID: "unit-contested", EnrollmentID: "host-1", MemberEthAddress: conflictIncumbent,
			GPUUUID: "GPU-CONTESTED", State: types.HardwareUnitActive},
		// A second card of the incumbent's: a decision about one GPU
		// must not touch the rest of their fleet.
		{ID: "unit-untouched", EnrollmentID: "host-1", MemberEthAddress: conflictIncumbent,
			GPUUUID: "GPU-OTHER", State: types.HardwareUnitActive},
	} {
		if err := stateRepo.PutHardwareUnit(unit); err != nil {
			t.Fatalf("PutHardwareUnit(%s) error = %v", unit.ID, err)
		}
	}
	conflict, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
		GPUUUID:              "GPU-CONTESTED",
		ChallengerEthAddress: conflictChallenger,
		ChallengerHostID:     "host-2",
		IncumbentEthAddress:  conflictIncumbent,
	})
	if err != nil {
		t.Fatalf("RecordHardwareClaimConflict() error = %v", err)
	}
	return stateRepo, exceptionsServer(t, stateRepo), conflict
}

// The queue is a work list: a dispute a person has already decided is
// not work, and leaving it there would drown the ones that are.
func TestExceptionsListsOnlyOpenConflicts(t *testing.T) {
	stateRepo, server, open := contestedFixture(t)
	for _, settled := range []struct {
		gpu        string
		challenger string
		resolution types.ConflictResolution
	}{
		{"GPU-SETTLED-1", "0xccc", types.ConflictRejected},
		{"GPU-SETTLED-2", "0xddd", types.ConflictTransferred},
	} {
		conflict, err := stateRepo.RecordHardwareClaimConflict(types.HardwareClaimConflict{
			GPUUUID: settled.gpu, ChallengerEthAddress: settled.challenger,
			IncumbentEthAddress: conflictIncumbent,
		})
		if err != nil {
			t.Fatalf("RecordHardwareClaimConflict(%s) error = %v", settled.gpu, err)
		}
		conflict.Resolution = settled.resolution
		conflict.Reason = "already decided"
		conflict.ResolvedAt = time.Now().UTC()
		if err := stateRepo.PutHardwareClaimConflict(conflict); err != nil {
			t.Fatalf("PutHardwareClaimConflict(%s) error = %v", settled.gpu, err)
		}
	}

	view := getExceptions(t, server)
	if len(view.DuplicateGPUs) != 1 {
		t.Fatalf("DuplicateGPUs = %+v, want only the undecided dispute", view.DuplicateGPUs)
	}
	got := view.DuplicateGPUs[0]
	if got.ConflictID != open.ID {
		t.Fatalf("conflict_id = %q, want %q — the operator has to be able to address "+
			"the decision back at the record", got.ConflictID, open.ID)
	}
	// An operator cannot act on "two addresses claim this": which side
	// holds the card and which is asking for it is the entire question.
	if got.IncumbentEthAddress != "0xaaa" || got.ChallengerEthAddress != "0xbbb" {
		t.Fatalf("incumbent = %q challenger = %q, want 0xaaa and 0xbbb",
			got.IncumbentEthAddress, got.ChallengerEthAddress)
	}
	if got.ChallengerHostID != "host-2" {
		t.Fatalf("challenger_host_id = %q, want host-2", got.ChallengerHostID)
	}
	if got.FirstSeenAt.IsZero() || got.LastSeenAt.IsZero() {
		t.Fatalf("first_seen = %s last_seen = %s, want both carried: how long a dispute has "+
			"been running is what says whether it is a mistake or a campaign",
			got.FirstSeenAt, got.LastSeenAt)
	}
}

func TestTransferRetiresTheIncumbentsUnitAndRecordsWhy(t *testing.T) {
	stateRepo, server, conflict := contestedFixture(t)

	status, body := resolveConflict(t, server, conflict.ID, "transfer",
		`{"reason":"member sold the card and never retired the enrolment","actor":"ops@pool"}`)
	if status != http.StatusOK {
		t.Fatalf("POST transfer status = %d body = %s", status, body)
	}
	var result struct {
		Conflict     types.HardwareClaimConflict `json:"conflict"`
		RetiredUnits int                         `json:"retired_units"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Unmarshal error = %v body = %s", err, string(body))
	}
	if result.RetiredUnits != 1 {
		t.Fatalf("retired_units = %d, want 1", result.RetiredUnits)
	}
	if result.Conflict.Resolution != types.ConflictTransferred {
		t.Fatalf("resolution = %q, want transferred", result.Conflict.Resolution)
	}

	// Retired, not deleted. The card's history under the incumbent is
	// what makes a later dispute over the same uuid reviewable, and a
	// deleted row would also let the uniqueness guard forget the card
	// was ever contested.
	retired, err := stateRepo.GetHardwareUnit("unit-contested")
	if err != nil {
		t.Fatalf("GetHardwareUnit(unit-contested) error = %v — a transfer deleted the row "+
			"instead of retiring it, taking the evidence with it", err)
	}
	if retired.State != types.HardwareUnitRetired {
		t.Fatalf("contested unit state = %q, want retired", retired.State)
	}
	untouched, err := stateRepo.GetHardwareUnit("unit-untouched")
	if err != nil || untouched.State != types.HardwareUnitActive {
		t.Fatalf("the incumbent's other card = %+v err = %v, want it untouched", untouched, err)
	}

	stored, err := stateRepo.GetHardwareClaimConflict(conflict.ID)
	if err != nil {
		t.Fatalf("GetHardwareClaimConflict() error = %v", err)
	}
	if stored.Open() {
		t.Fatalf("resolution = %q after a transfer, want it off the queue", stored.Resolution)
	}
	if stored.ResolvedBy != "ops@pool" || stored.ResolvedAt.IsZero() {
		t.Fatalf("resolved_by = %q at = %s, want the decision attributed", stored.ResolvedBy, stored.ResolvedAt)
	}
	if view := getExceptions(t, server); len(view.DuplicateGPUs) != 0 {
		t.Fatalf("DuplicateGPUs = %+v after a transfer, want the queue cleared", view.DuplicateGPUs)
	}

	events, err := stateRepo.ListAuditEventsFiltered(
		"gpu_conflict_resolved", "hardware_claim_conflict", conflict.ID, 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %+v, want one", events)
	}
	event := events[0]
	if event.Actor != "ops@pool" {
		t.Fatalf("audit actor = %q", event.Actor)
	}
	if event.Details["resolution"] != string(types.ConflictTransferred) {
		t.Fatalf("audit resolution = %v", event.Details["resolution"])
	}
	if event.Details["gpu_uuid"] != "GPU-CONTESTED" {
		t.Fatalf("audit gpu_uuid = %v", event.Details["gpu_uuid"])
	}
	// The reason is the whole point of demanding one: it has to reach
	// the record, not merely gate the request.
	if event.Details["reason"] != "member sold the card and never retired the enrolment" {
		t.Fatalf("audit reason = %v, want the submitted reason", event.Details["reason"])
	}
	if event.Details["incumbent"] != "0xaaa" || event.Details["challenger"] != "0xbbb" {
		t.Fatalf("audit sides = %v / %v, want both recorded",
			event.Details["incumbent"], event.Details["challenger"])
	}
	if retiredCount, ok := event.Details["retired_units"].(float64); !ok || int(retiredCount) != 1 {
		t.Fatalf("audit retired_units = %#v, want 1", event.Details["retired_units"])
	}
}

// Rejecting is the operator saying the incumbent keeps the card, so the
// incumbent's hardware must come through it completely untouched.
func TestRejectRetiresNothing(t *testing.T) {
	stateRepo, server, conflict := contestedFixture(t)

	status, body := resolveConflict(t, server, conflict.ID, "reject",
		`{"reason":"uuid appears cloned; incumbent has held it for months","actor":"ops@pool"}`)
	if status != http.StatusOK {
		t.Fatalf("POST reject status = %d body = %s", status, body)
	}
	var result struct {
		Conflict     types.HardwareClaimConflict `json:"conflict"`
		RetiredUnits int                         `json:"retired_units"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Unmarshal error = %v body = %s", err, string(body))
	}
	if result.RetiredUnits != 0 {
		t.Fatalf("retired_units = %d on a rejection, want 0", result.RetiredUnits)
	}
	if result.Conflict.Resolution != types.ConflictRejected {
		t.Fatalf("resolution = %q, want rejected", result.Conflict.Resolution)
	}
	for _, id := range []string{"unit-contested", "unit-untouched"} {
		unit, err := stateRepo.GetHardwareUnit(id)
		if err != nil {
			t.Fatalf("GetHardwareUnit(%s) error = %v", id, err)
		}
		if unit.State != types.HardwareUnitActive {
			t.Fatalf("%s state = %q after a rejection, want it still active: rejecting the "+
				"challenger is a decision in the incumbent's favour", id, unit.State)
		}
	}
	if view := getExceptions(t, server); len(view.DuplicateGPUs) != 0 {
		t.Fatalf("DuplicateGPUs = %+v after a rejection, want the queue cleared", view.DuplicateGPUs)
	}
	events, _ := stateRepo.ListAuditEventsFiltered(
		"gpu_conflict_resolved", "hardware_claim_conflict", conflict.ID, 0)
	if len(events) != 1 || events[0].Details["resolution"] != string(types.ConflictRejected) {
		t.Fatalf("audit events = %+v, want one rejection record", events)
	}
}

// Either outcome takes a card from someone. A decision with no recorded
// cause cannot be reviewed later, including by the operator who made
// it, so both actions refuse rather than resolve.
func TestResolvingAGPUConflictWithoutAReasonIsRefused(t *testing.T) {
	stateRepo, server, conflict := contestedFixture(t)

	for _, action := range []string{"transfer", "reject"} {
		for _, body := range []string{``, `{}`, `{"reason":"   ","actor":"ops@pool"}`} {
			status, out := resolveConflict(t, server, conflict.ID, action, body)
			if status != http.StatusBadRequest {
				t.Fatalf("POST %s with body %q status = %d, want 400: %s", action, body, status, out)
			}
		}
	}

	// And nothing moved on the way to refusing: no half-applied
	// decision, no retired card, no audit event asserting one happened.
	stored, err := stateRepo.GetHardwareClaimConflict(conflict.ID)
	if err != nil {
		t.Fatalf("GetHardwareClaimConflict() error = %v", err)
	}
	if !stored.Open() {
		t.Fatalf("resolution = %q after refused requests, want it still open", stored.Resolution)
	}
	unit, _ := stateRepo.GetHardwareUnit("unit-contested")
	if unit.State != types.HardwareUnitActive {
		t.Fatalf("contested unit state = %q, want it untouched by a refused transfer", unit.State)
	}
	events, _ := stateRepo.ListAuditEventsFiltered(
		"gpu_conflict_resolved", "hardware_claim_conflict", conflict.ID, 0)
	if len(events) != 0 {
		t.Fatalf("audit events = %+v, want none for refused decisions", events)
	}
}

func TestUnknownGPUConflictActionOrIDIsRefused(t *testing.T) {
	stateRepo, server, conflict := contestedFixture(t)

	// A typo must not become a third, undefined outcome — and in
	// particular must not fall through to the transfer arm, which takes
	// someone's card away.
	for _, action := range []string{"ban", "TRANSFER", "resolve"} {
		status, body := resolveConflict(t, server, conflict.ID, action, `{"reason":"x"}`)
		if status != http.StatusBadRequest {
			t.Fatalf("POST %s status = %d, want 400: %s", action, status, body)
		}
	}
	stored, _ := stateRepo.GetHardwareClaimConflict(conflict.ID)
	if !stored.Open() {
		t.Fatalf("resolution = %q, want an unrecognised action to change nothing", stored.Resolution)
	}
	unit, _ := stateRepo.GetHardwareUnit("unit-contested")
	if unit.State != types.HardwareUnitActive {
		t.Fatalf("contested unit state = %q after an unknown action", unit.State)
	}

	// A dispute that does not exist is a 404 rather than a silent
	// create: resolving nothing must not look like resolving something.
	if status, body := resolveConflict(t, server,
		"GPU-NOPE|0xnope", "transfer", `{"reason":"x"}`); status != http.StatusNotFound {
		t.Fatalf("POST on an unknown conflict = %d, want 404: %s", status, body)
	}
}

// ---------------------------------------------------------------------
// Reinstating a suspended placement.

const reinstateMember = "0x2222222222222222222222222222222222222222"

const reinstateTemplateYAML = `id: chat-4090
capability: openai:chat-completions
offering_id: default
protocol: paid-job/v1
price_default:
  amount_wei: "5"
  per_units: 1
stacking:
  primary: true
`

const (
	reinstateCapability = "openai:chat-completions"
	reinstateOffering   = "default"
)

// reinstateFixture is one placement in the given state, its member
// active, and the selection row the ladder would have left behind:
// excluded, with the suspension as its reason.
func reinstateFixture(t *testing.T, state types.TemplateAssignmentState) (
	*repo.StateRepo, *httptest.Server, types.TemplateAssignment) {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	if err := stateRepo.PutPoolMember(types.PoolMember{
		EthAddress: reinstateMember, Status: types.MemberStatusActive,
	}); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}
	assignment := types.TemplateAssignment{
		ID: "assign-1", HardwareUnitID: "gpu-1", HostEnrollmentID: "host-1",
		MemberEthAddress: reinstateMember, TemplateID: "chat-4090", State: state,
	}
	if err := stateRepo.PutTemplateAssignment(assignment); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	seeded, err := stateRepo.SeedBackendSelectionState(
		assignment.MemberEthAddress, ladder.BackendID(assignment), reinstateCapability, reinstateOffering)
	if err != nil {
		t.Fatalf("SeedBackendSelectionState() error = %v", err)
	}
	seeded.State = types.BackendSelectionStateExcluded
	seeded.ExclusionReason = ladder.ReasonSuspendedCertFails
	seeded.MaxShareCap = 0
	if err := stateRepo.SaveBackendSelectionState(seeded); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}

	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		Catalog:  loadAdminCatalog(t, reinstateTemplateYAML),
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return stateRepo, server, assignment
}

func postReinstate(t *testing.T, server *httptest.Server, id, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(server.URL+"/admin/v1/template-assignments/"+url.PathEscape(id)+"/reinstate",
		"application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST reinstate error = %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, out
}

func reinstateSelectionState(t *testing.T, stateRepo *repo.StateRepo,
	assignment types.TemplateAssignment) types.BackendSelectionState {
	t.Helper()
	state, err := stateRepo.GetBackendSelectionState(
		assignment.MemberEthAddress, ladder.BackendID(assignment), reinstateCapability, reinstateOffering)
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	return state
}

// Reinstating is lifting a suspension. Applied to anything else it
// would be an undocumented way to move a placement between rungs
// without the evidence the ladder demands.
func TestReinstateOnlyLiftsASuspension(t *testing.T) {
	for _, state := range []types.TemplateAssignmentState{
		types.TemplateAssignmentActive,
		types.TemplateAssignmentProbationary,
		types.TemplateAssignmentTesting,
		types.TemplateAssignmentThrottled,
		types.TemplateAssignmentDraining,
		types.TemplateAssignmentRetired,
		types.TemplateAssignmentPending,
	} {
		stateRepo, server, assignment := reinstateFixture(t, state)
		status, body := postReinstate(t, server, assignment.ID, `{"reason":"looks fine to me"}`)
		if status != http.StatusBadRequest {
			t.Fatalf("reinstating a %s placement = %d, want 400: %s", state, status, body)
		}
		stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
		if stored.State != state {
			t.Fatalf("state = %q, want it left at %q", stored.State, state)
		}
		if !stored.ReinstatedAt.IsZero() {
			t.Fatalf("ReinstatedAt was stamped on a refused reinstate of a %s placement", state)
		}
	}

	// A placement that does not exist is a 404, not a silent create.
	_, server, _ := reinstateFixture(t, types.TemplateAssignmentSuspended)
	if status, body := postReinstate(t, server, "nope", `{"reason":"x"}`); status != http.StatusNotFound {
		t.Fatalf("reinstating an unknown placement = %d, want 404: %s", status, body)
	}
}

// Lifting a suspension is a decision about someone's ability to earn.
// Without a recorded cause nobody can review it afterwards — including
// the operator who made it.
func TestReinstateRequiresAReason(t *testing.T) {
	stateRepo, server, assignment := reinstateFixture(t, types.TemplateAssignmentSuspended)

	for _, body := range []string{``, `{}`, `{"reason":"  "}`, `{"to":"probationary_real_traffic"}`} {
		status, out := postReinstate(t, server, assignment.ID, body)
		if status != http.StatusBadRequest {
			t.Fatalf("reinstate with body %q = %d, want 400: %s", body, status, out)
		}
	}
	stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
	if stored.State != types.TemplateAssignmentSuspended || !stored.ReinstatedAt.IsZero() {
		t.Fatalf("assignment = %+v, want it untouched by a refused reinstate", stored)
	}
	// The runner must not have been let back into the draw either.
	if state := reinstateSelectionState(t, stateRepo, assignment); state.State != types.BackendSelectionStateExcluded {
		t.Fatalf("selection state = %q after a refused reinstate, want it still excluded", state.State)
	}
	events, _ := stateRepo.ListAuditEventsFiltered(
		"placement_reinstated", "template_assignment", assignment.ID, 0)
	if len(events) != 0 {
		t.Fatalf("audit events = %+v, want none for a refused reinstate", events)
	}
}

// Only the two rungs the route documents. Anything else would be an
// operator typing a placement straight to active, which is the one
// thing the ladder exists to stop.
func TestReinstateRejectsAnUnknownTarget(t *testing.T) {
	stateRepo, server, assignment := reinstateFixture(t, types.TemplateAssignmentSuspended)

	for _, target := range []string{"active", "suspended", "throttled", "retired", "probationary", "PROBATIONARY_REAL_TRAFFIC"} {
		status, body := postReinstate(t, server, assignment.ID,
			`{"to":"`+target+`","reason":"operator reviewed the logs"}`)
		if status != http.StatusBadRequest {
			t.Fatalf("reinstate to %q = %d, want 400: %s", target, status, body)
		}
		stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
		if stored.State != types.TemplateAssignmentSuspended {
			t.Fatalf("state = %q, want it left suspended after a refused target", stored.State)
		}
	}
}

// The default is certification_testing, not probationary: the failures
// that cause a suspension are exactly what certification tests, so a
// reinstated runner re-proves itself on one cheap probe before it is
// handed paid work again.
func TestReinstateDefaultsToCertificationTestingAndStaysExcluded(t *testing.T) {
	stateRepo, server, assignment := reinstateFixture(t, types.TemplateAssignmentSuspended)

	for _, body := range []string{
		`{"reason":"host replaced the failing driver","actor":"ops@pool"}`,
		`{"to":"","reason":"host replaced the failing driver","actor":"ops@pool"}`,
		`{"to":"certification_testing","reason":"host replaced the failing driver","actor":"ops@pool"}`,
	} {
		// Reset between spellings: each has to reach the same place on
		// its own, not inherit the previous one's result.
		reset := assignment
		reset.State = types.TemplateAssignmentSuspended
		if err := stateRepo.PutTemplateAssignment(reset); err != nil {
			t.Fatalf("PutTemplateAssignment() error = %v", err)
		}
		status, out := postReinstate(t, server, assignment.ID, body)
		if status != http.StatusOK {
			t.Fatalf("reinstate %q = %d: %s", body, status, out)
		}
		var got types.TemplateAssignment
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("Unmarshal error = %v body = %s", err, string(out))
		}
		if got.State != types.TemplateAssignmentTesting {
			t.Fatalf("state = %q for body %q, want certification_testing", got.State, body)
		}
	}

	stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
	// The boundary the ladder counts certification failures from.
	// Without it the historical failures that caused the suspension
	// would re-suspend the placement on the very next tick, and the
	// operator would watch their decision undone once a minute.
	if stored.ReinstatedAt.IsZero() {
		t.Fatal("ReinstatedAt is zero: without the boundary the ladder re-suspends on the old failures")
	}
	if stored.ShareCapPPM != 0 {
		t.Fatalf("share_cap_ppm = %d, want 0 while re-certifying", stored.ShareCapPPM)
	}

	// It has proved nothing yet, and certification needs no real
	// traffic. Eligible-with-a-zero-cap would be the worst of both: the
	// broker reads max_share_cap == 0 as NO CAP, so it would hand a
	// just-unsuspended runner unlimited traffic.
	state := reinstateSelectionState(t, stateRepo, assignment)
	if state.State != types.BackendSelectionStateExcluded {
		t.Fatalf("selection state = %q, want %q — a placement re-certifying earns nothing",
			state.State, types.BackendSelectionStateExcluded)
	}
	if state.ExclusionReason != "awaiting_recertification" {
		t.Fatalf("exclusion_reason = %q, want awaiting_recertification", state.ExclusionReason)
	}
	if state.State == types.BackendSelectionStateEligible && state.MaxShareCap == 0 {
		t.Fatalf("eligible with max_share_cap = 0, which the broker reads as UNCAPPED: %+v", state)
	}

	events, err := stateRepo.ListAuditEventsFiltered(
		"placement_reinstated", "template_assignment", assignment.ID, 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want one per reinstate", len(events))
	}
	event := events[0]
	if event.Actor != "ops@pool" {
		t.Fatalf("audit actor = %q", event.Actor)
	}
	if event.Details["to"] != string(types.TemplateAssignmentTesting) {
		t.Fatalf("audit to = %v, want certification_testing", event.Details["to"])
	}
	if event.Details["reason"] != "host replaced the failing driver" {
		t.Fatalf("audit reason = %v, want the submitted reason", event.Details["reason"])
	}
}

// An operator who knows certification was never the issue can send the
// placement straight back to paid traffic — but only onto the probation
// share, and only with a cap the broker will actually honour.
func TestReinstateToProbationaryIsEligibleWithARealCap(t *testing.T) {
	stateRepo, server, assignment := reinstateFixture(t, types.TemplateAssignmentSuspended)

	status, body := postReinstate(t, server, assignment.ID,
		`{"to":"probationary_real_traffic","reason":"the suspension was a monitoring fault","actor":"ops@pool"}`)
	if status != http.StatusOK {
		t.Fatalf("reinstate to probationary = %d: %s", status, body)
	}

	stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
	if stored.State != types.TemplateAssignmentProbationary {
		t.Fatalf("state = %q, want probationary_real_traffic", stored.State)
	}
	if stored.ReinstatedAt.IsZero() {
		t.Fatal("ReinstatedAt is zero on a probationary reinstate too: the boundary is what stops " +
			"the old certification failures re-suspending it on the next tick")
	}
	wantShare := ladder.DefaultPolicy.ProbationSharePPM
	if stored.ShareCapPPM != wantShare {
		t.Fatalf("share_cap_ppm = %d, want the pool's probation share %d", stored.ShareCapPPM, wantShare)
	}

	state := reinstateSelectionState(t, stateRepo, assignment)
	if state.State != types.BackendSelectionStateEligible {
		t.Fatalf("selection state = %q, want eligible — a placement on probation that is still "+
			"excluded gets no traffic, so it can never earn its way back", state.State)
	}
	if state.ExclusionReason != "" {
		t.Fatalf("exclusion_reason = %q, want it cleared", state.ExclusionReason)
	}
	// The other half, and the one that actually costs money if it is
	// missed: the broker reads max_share_cap == 0 as "no cap
	// configured" and leaves the runner UNCAPPED, so eligible-with-zero
	// would hand a just-unsuspended runner the whole offering.
	if state.MaxShareCap == 0 {
		t.Fatalf("eligible with max_share_cap = 0, which the broker reads as UNCAPPED: %+v", state)
	}
	if want := float64(wantShare) / 1_000_000; state.MaxShareCap != want {
		t.Fatalf("max_share_cap = %v, want %v (the probation share)", state.MaxShareCap, want)
	}

	events, _ := stateRepo.ListAuditEventsFiltered(
		"placement_reinstated", "template_assignment", assignment.ID, 0)
	if len(events) != 1 {
		t.Fatalf("audit events = %+v, want one", events)
	}
	if events[0].Details["to"] != string(types.TemplateAssignmentProbationary) {
		t.Fatalf("audit to = %v, want the target actually applied", events[0].Details["to"])
	}
}

// Reinstating a placement whose MEMBER is suspended would route work to
// someone the pool has stopped dealing with — the member suspension is
// the broader decision and has to win.
func TestReinstateIsRefusedWhileTheMemberIsSuspended(t *testing.T) {
	stateRepo, server, assignment := reinstateFixture(t, types.TemplateAssignmentSuspended)
	if err := stateRepo.PutPoolMember(types.PoolMember{
		EthAddress: reinstateMember, Status: types.MemberStatusSuspended,
	}); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}

	status, body := postReinstate(t, server, assignment.ID,
		`{"to":"probationary_real_traffic","reason":"host says it is fixed"}`)
	if status != http.StatusConflict {
		t.Fatalf("reinstate under a suspended member = %d, want 409: %s", status, body)
	}
	stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
	if stored.State != types.TemplateAssignmentSuspended || !stored.ReinstatedAt.IsZero() {
		t.Fatalf("assignment = %+v, want it untouched", stored)
	}
	if state := reinstateSelectionState(t, stateRepo, assignment); state.State != types.BackendSelectionStateExcluded {
		t.Fatalf("selection state = %q, want the runner still out of the draw", state.State)
	}
	events, _ := stateRepo.ListAuditEventsFiltered(
		"placement_reinstated", "template_assignment", assignment.ID, 0)
	if len(events) != 0 {
		t.Fatalf("audit events = %+v, want none", events)
	}
}

// The reinstate has to stick.
//
// This is the end-to-end version of the ladder's unit test, and the
// only one that exercises the boundary the way an operator meets it:
// the ladder suspends a placement on two failed certification runs, the
// operator lifts it, and the very next ladder tick must NOT put it
// straight back. Without ReinstatedAt the same historical failures are
// counted again and the operator watches their decision undone once a
// minute, forever.
func TestReinstatedPlacementSurvivesTheNextLadderTick(t *testing.T) {
	stateRepo, server, assignment := reinstateFixture(t, types.TemplateAssignmentTesting)
	past := time.Now().UTC().Add(-time.Hour)
	for i, at := range []time.Time{past, past.Add(time.Minute)} {
		if err := stateRepo.PutCertificationRun(types.CertificationRun{
			ID:           "cert-" + string(rune('a'+i)),
			AssignmentID: assignment.ID,
			TemplateID:   "chat-4090",
			Status:       types.CertificationFailed,
			StartedAt:    at.Add(-time.Minute),
			CompletedAt:  at,
		}); err != nil {
			t.Fatalf("PutCertificationRun() error = %v", err)
		}
	}

	ladderService := ladder.New(stateRepo, loadAdminCatalog(t, reinstateTemplateYAML), ladder.Policy{})
	summary, err := ladderService.RunOnce()
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(summary.Transitions) != 1 || summary.Transitions[0].To != types.TemplateAssignmentSuspended {
		t.Fatalf("transitions = %+v, want the placement suspended on two failed runs", summary.Transitions)
	}
	suspended, _ := stateRepo.GetTemplateAssignment(assignment.ID)
	if suspended.State != types.TemplateAssignmentSuspended {
		t.Fatalf("state = %q, want suspended before the operator acts", suspended.State)
	}

	status, body := postReinstate(t, server, assignment.ID,
		`{"reason":"driver reinstalled on the host","actor":"ops@pool"}`)
	if status != http.StatusOK {
		t.Fatalf("reinstate = %d: %s", status, body)
	}

	// The tick that would have undone it.
	after, err := ladderService.RunOnce()
	if err != nil {
		t.Fatalf("RunOnce() after reinstate error = %v", err)
	}
	for _, transition := range after.Transitions {
		if transition.AssignmentID == assignment.ID {
			t.Fatalf("the ladder moved the reinstated placement to %q on the next tick, "+
				"reason %q — the old failures are why it was suspended and must not be counted twice",
				transition.To, transition.ReasonCode)
		}
	}
	stored, _ := stateRepo.GetTemplateAssignment(assignment.ID)
	if stored.State != types.TemplateAssignmentTesting {
		t.Fatalf("state = %q after the next ladder tick, want it still certification_testing: "+
			"the operator's decision has to survive longer than one minute", stored.State)
	}
	// And it is still out of the draw while it re-certifies, rather
	// than eligible with a zero cap the broker would read as uncapped.
	state := reinstateSelectionState(t, stateRepo, assignment)
	if state.State != types.BackendSelectionStateExcluded {
		t.Fatalf("selection state = %q, want it excluded until certification passes", state.State)
	}
	if !strings.EqualFold(state.ExclusionReason, "awaiting_recertification") {
		t.Fatalf("exclusion_reason = %q, want awaiting_recertification", state.ExclusionReason)
	}
}

// Reinstating a member does not reinstate their placements, and nothing
// else tells the operator that second step is outstanding — so a member
// who is back but earning nothing looks, from every other screen, like
// a member who is fine.
func TestStalledDrainsNameTheReinstateThatIsStillOwed(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	now := time.Now().UTC()

	if err := stateRepo.PutPoolMember(types.PoolMember{
		ID: "0xaaa", EthAddress: "0xaaa", Status: types.MemberStatusActive,
	}); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}
	// Long enough to be stalled, and a second that has only just begun.
	if err := stateRepo.PutTemplateAssignment(types.TemplateAssignment{
		ID: "stalled", MemberEthAddress: "0xaaa", TemplateID: "t", HardwareUnitID: "gpu-1",
		State: types.TemplateAssignmentDraining, DrainingSince: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	if err := stateRepo.PutTemplateAssignment(types.TemplateAssignment{
		ID: "fresh", MemberEthAddress: "0xaaa", TemplateID: "t", HardwareUnitID: "gpu-2",
		State: types.TemplateAssignmentDraining, DrainingSince: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}

	view := getExceptions(t, exceptionsServer(t, stateRepo))
	if len(view.StalledDrains) != 1 {
		t.Fatalf("StalledDrains = %+v, want only the long-running one — a drain in normal "+
			"progress must not appear or the queue becomes noise", view.StalledDrains)
	}
	stalled := view.StalledDrains[0]
	if stalled.AssignmentID != "stalled" {
		t.Fatalf("stalled assignment = %q", stalled.AssignmentID)
	}
	// The member being active is what makes this actionable rather than
	// merely broken, and the detail has to say which it is.
	if stalled.MemberStatus != string(types.MemberStatusActive) {
		t.Fatalf("member status = %q, want active", stalled.MemberStatus)
	}
	if !strings.Contains(stalled.Detail, "reinstate") {
		t.Fatalf("detail = %q, want it to name the outstanding reinstate", stalled.Detail)
	}
}
