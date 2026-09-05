package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/placement"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const placementMember = "0x1111111111111111111111111111111111111111"

// seedPlacementServer builds a pool whose plan exercises every change
// kind at once: a card that should gain a primary and demote its
// current template, an assignment the plan no longer wants, and a
// second card with nothing on it yet.
func seedPlacementServer(t *testing.T) (*repo.StateRepo, *httptest.Server) {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })

	now := time.Now().UTC()
	units := []types.HardwareUnit{
		{
			ID: "gpu-1", EnrollmentID: "host-1", MemberEthAddress: placementMember,
			GPUUUID: "GPU-1", GPUModel: "NVIDIA GeForce RTX 4090",
			VRAMBytes: 24 << 30, State: types.HardwareUnitActive,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "gpu-2", EnrollmentID: "host-1", MemberEthAddress: placementMember,
			GPUUUID: "GPU-2", GPUModel: "NVIDIA GeForce GTX 1080",
			VRAMBytes: 8 << 30, State: types.HardwareUnitActive,
			CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, unit := range units {
		if err := stateRepo.PutHardwareUnit(unit); err != nil {
			t.Fatalf("PutHardwareUnit(%s) error = %v", unit.ID, err)
		}
	}
	// Already running: audio as gpu-1's primary (the plan demotes it
	// once chat qualifies) and a template the pool has since dropped.
	existing := []types.TemplateAssignment{
		{
			ID: "gpu-1|t-audio", HardwareUnitID: "gpu-1", HostEnrollmentID: "host-1",
			MemberEthAddress: placementMember, TemplateID: "t-audio",
			Role: types.TemplateAssignmentPrimary, State: types.TemplateAssignmentActive,
		},
		{
			ID: "gpu-1|t-retired-family", HardwareUnitID: "gpu-1", HostEnrollmentID: "host-1",
			MemberEthAddress: placementMember, TemplateID: "t-retired-family",
			Role: types.TemplateAssignmentPrimary, State: types.TemplateAssignmentActive,
		},
	}
	for _, assignment := range existing {
		if err := stateRepo.PutTemplateAssignment(assignment); err != nil {
			t.Fatalf("PutTemplateAssignment(%s) error = %v", assignment.ID, err)
		}
	}
	catalog := loadAdminCatalog(t,
		`id: t-chat
capability: openai:chat-completions
offering_id: chat-default
protocol: paid-job/v1
price_default: { amount_wei: "5" }
priority: 30
requirements:
  gpu_classes: [rtx-4090]
stacking: { primary: true }
`,
		`id: t-audio
capability: openai:audio-transcriptions
offering_id: audio-default
protocol: paid-job/v1
price_default: { amount_wei: "3" }
priority: 12
requirements:
  gpu_classes: [rtx-4090]
stacking:
  primary: true
  secondary_on: [rtx-4090]
`,
		`id: t-transcode
capability: video:transcode.abr
offering_id: transcode-default
protocol: paid-job/v1
price_default: { amount_wei: "1" }
priority: 10
requirements:
  gpu_classes: [gtx-1080, rtx-4090]
stacking: { primary: true }
`)
	for _, id := range []string{"t-chat", "t-audio", "t-transcode"} {
		if err := stateRepo.PutTemplateOverride(types.TemplateOverride{TemplateID: id, Enabled: true}); err != nil {
			t.Fatalf("PutTemplateOverride(%s) error = %v", id, err)
		}
	}

	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		Catalog:  catalog,
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return stateRepo, server
}

func decodePlan(t *testing.T, body []byte) struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Decisions   []placement.Decision `json:"decisions"`
	Changes     []placement.Change   `json:"changes"`
} {
	t.Helper()
	var out struct {
		GeneratedAt time.Time            `json:"generated_at"`
		Decisions   []placement.Decision `json:"decisions"`
		Changes     []placement.Change   `json:"changes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode plan: %v (body=%s)", err, body)
	}
	return out
}

// The read-only plan is what an operator consults before applying, and
// what a member's "why is my card idle" question is answered from, so
// it has to carry both halves: the per-GPU decisions with their reason
// codes, and the changes applying would make.
func TestPlacementPlanRouteReportsDecisionsAndChanges(t *testing.T) {
	_, server := seedPlacementServer(t)

	resp, err := http.Get(server.URL + "/admin/v1/placement-plan")
	if err != nil {
		t.Fatalf("GET placement-plan error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET placement-plan status=%d body=%s", resp.StatusCode, body)
	}
	plan := decodePlan(t, body)
	if len(plan.Decisions) != 2 {
		t.Fatalf("decisions = %d, want one per GPU", len(plan.Decisions))
	}
	if plan.GeneratedAt.IsZero() {
		t.Errorf("generated_at is zero; the plan is a snapshot and has to say when")
	}

	gpu1 := plan.Decisions[0]
	if gpu1.HardwareUnitID != "gpu-1" || gpu1.GPUClass != placement.ClassRTX4090 {
		t.Fatalf("decision[0] = %+v, want gpu-1 classed rtx-4090", gpu1)
	}
	if len(gpu1.Placements) != 2 ||
		gpu1.Placements[0].TemplateID != "t-chat" || gpu1.Placements[0].Role != types.TemplateAssignmentPrimary ||
		gpu1.Placements[1].TemplateID != "t-audio" || gpu1.Placements[1].Role != types.TemplateAssignmentSecondary {
		t.Errorf("gpu-1 placements = %+v, want chat primary and audio secondary", gpu1.Placements)
	}
	if len(gpu1.Rejections) != 1 || gpu1.Rejections[0].TemplateID != "t-transcode" ||
		gpu1.Rejections[0].Reason != placement.ReasonStackingFull {
		t.Errorf("gpu-1 rejections = %+v, want transcode held off by the stacking limit", gpu1.Rejections)
	}

	gpu2 := plan.Decisions[1]
	if len(gpu2.Placements) != 1 || gpu2.Placements[0].TemplateID != "t-transcode" {
		t.Errorf("gpu-2 placements = %+v, want transcode alone on the 1080", gpu2.Placements)
	}
	if len(gpu2.Rejections) != 2 {
		t.Errorf("gpu-2 rejections = %+v, want the two 4090-only templates explained", gpu2.Rejections)
	}
	for _, rejection := range gpu2.Rejections {
		if rejection.Reason != placement.ReasonClassNotAllowed {
			t.Errorf("gpu-2 rejection %+v, want gpu_class_not_allowed", rejection)
		}
	}

	wantChanges := []struct{ kind, hardware, template string }{
		{placement.ChangeRoleChange, "gpu-1", "t-audio"},
		{placement.ChangeCreate, "gpu-1", "t-chat"},
		{placement.ChangeDrain, "gpu-1", "t-retired-family"},
		{placement.ChangeCreate, "gpu-2", "t-transcode"},
	}
	if len(plan.Changes) != len(wantChanges) {
		t.Fatalf("changes = %+v, want %d", plan.Changes, len(wantChanges))
	}
	for i, want := range wantChanges {
		got := plan.Changes[i]
		if got.Kind != want.kind || got.HardwareID != want.hardware || got.TemplateID != want.template {
			t.Errorf("change[%d] = %s/%s/%s, want %s/%s/%s",
				i, got.Kind, got.HardwareID, got.TemplateID, want.kind, want.hardware, want.template)
		}
	}

	// Reading the plan must not change anything: an operator can look
	// before deciding.
	assignments, err := loadAssignments(t, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 2 {
		t.Errorf("GET placement-plan wrote %d assignments; it is read-only", len(assignments))
	}
}

func TestPlacementPlanApplyCreatesDrainsAndAudits(t *testing.T) {
	stateRepo, server := seedPlacementServer(t)

	resp, err := http.Post(server.URL+"/admin/v1/placement-plan/apply", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST apply error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST apply status=%d body=%s", resp.StatusCode, body)
	}
	var applied struct {
		Status  string             `json:"status"`
		Applied []placement.Change `json:"applied"`
	}
	if err := json.Unmarshal(body, &applied); err != nil {
		t.Fatalf("decode apply response: %v (body=%s)", err, body)
	}
	if applied.Status != "applied" || len(applied.Applied) != 4 {
		t.Fatalf("apply response = %+v, want four applied changes", applied)
	}

	// A created assignment starts pending: it has to be certified
	// before the broker may send it real work.
	created, err := stateRepo.GetTemplateAssignment("gpu-1|t-chat")
	if err != nil {
		t.Fatalf("GetTemplateAssignment(gpu-1|t-chat) error = %v", err)
	}
	if created.State != types.TemplateAssignmentPending || created.Role != types.TemplateAssignmentPrimary {
		t.Errorf("created assignment = %+v, want pending/primary", created)
	}
	if created.MemberEthAddress != placementMember {
		t.Errorf("created assignment member = %q, want %q", created.MemberEthAddress, placementMember)
	}
	if _, err := stateRepo.GetTemplateAssignment("gpu-2|t-transcode"); err != nil {
		t.Errorf("GetTemplateAssignment(gpu-2|t-transcode) error = %v", err)
	}

	demoted, err := stateRepo.GetTemplateAssignment("gpu-1|t-audio")
	if err != nil {
		t.Fatalf("GetTemplateAssignment(gpu-1|t-audio) error = %v", err)
	}
	if demoted.Role != types.TemplateAssignmentSecondary {
		t.Errorf("demoted assignment role = %q, want secondary", demoted.Role)
	}
	// A demotion is not a re-certification: the runner keeps the state
	// it earned.
	if demoted.State != types.TemplateAssignmentActive {
		t.Errorf("demoted assignment state = %q, want it left active", demoted.State)
	}

	// The withdrawal is the one that matters. Deleting the row would
	// strand whatever the runner is still serving, and the broker would
	// keep dispatching to a runner nothing knows about.
	drained, err := stateRepo.GetTemplateAssignment("gpu-1|t-retired-family")
	if err != nil {
		t.Fatalf("withdrawn assignment was removed rather than drained: %v", err)
	}
	if drained.State != types.TemplateAssignmentDraining {
		t.Errorf("withdrawn assignment state = %q, want draining", drained.State)
	}
	if drained.DrainingSince.IsZero() {
		t.Errorf("withdrawn assignment has no draining_since; retirement has no clock to wait on")
	}

	events, err := stateRepo.ListAuditEventsFiltered("placement_plan_applied", "", "", 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("placement_plan_applied events = %d, want 1 — applying a plan moves a member's hardware and has to be on the record", len(events))
	}
	if events[0].ResourceType != "template_assignment" || events[0].OccurredAt.IsZero() {
		t.Errorf("audit event = %+v, want a timestamped template_assignment event", events[0])
	}

	// Applying twice is a no-op: the plan now matches reality, and the
	// drained assignment is left where it is.
	resp, err = http.Post(server.URL+"/admin/v1/placement-plan/apply", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("second POST apply error = %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	applied.Applied = nil
	if err := json.Unmarshal(body, &applied); err != nil {
		t.Fatalf("decode second apply response: %v (body=%s)", err, body)
	}
	if len(applied.Applied) != 0 {
		t.Errorf("second apply changed %+v, want a settled plan to change nothing", applied.Applied)
	}
}

func loadAssignments(t *testing.T, server *httptest.Server) ([]types.TemplateAssignment, error) {
	t.Helper()
	resp, err := http.Get(server.URL + "/admin/v1/template-assignments")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Assignments []types.TemplateAssignment `json:"assignments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Assignments, nil
}
