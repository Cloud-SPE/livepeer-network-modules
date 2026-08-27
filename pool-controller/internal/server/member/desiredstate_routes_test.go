package member

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/desiredstate"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/memberenrollment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type desiredStateFixture struct {
	repo    *repo.StateRepo
	server  *httptest.Server
	host    types.HostEnrollment
	token   string
	other   types.HostEnrollment
	otherTk string
}

const desiredStateTemplate = `id: chat-a
capability: openai:chat-completions
offering_id: a
protocol: paid-job/v1
price_default:
  amount_wei: "1"
  per_units: 1
stacking:
  primary: true
runner_compose:
  image: ghcr.io/example/a:1
`

// newDesiredStateFixture stands up two enrolled hosts, one GPU each, so
// every test can check both what a host is told and what it is refused.
func newDesiredStateFixture(t *testing.T) *desiredStateFixture {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })

	catalogDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(catalogDir, "chat-a.yaml"), []byte(desiredStateTemplate), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	catalog, err := templates.Load(catalogDir)
	if err != nil {
		t.Fatalf("templates.Load() error = %v", err)
	}

	enrollment := memberenrollment.New(stateRepo)
	newHost := func(addr, label, unitID, uuid string) (types.HostEnrollment, string) {
		t.Helper()
		if err := stateRepo.PutPoolMember(types.PoolMember{EthAddress: addr}); err != nil {
			t.Fatalf("PutPoolMember() error = %v", err)
		}
		result, err := enrollment.CreateEnrollment(memberenrollment.CreateEnrollmentRequest{
			MemberEthAddress: addr, HostLabel: label,
		})
		if err != nil {
			t.Fatalf("CreateEnrollment() error = %v", err)
		}
		if err := stateRepo.PutHardwareUnit(types.HardwareUnit{
			ID: unitID, EnrollmentID: result.Enrollment.ID, MemberEthAddress: addr,
			GPUUUID: uuid, GPUModel: "NVIDIA GeForce RTX 4090",
		}); err != nil {
			t.Fatalf("PutHardwareUnit() error = %v", err)
		}
		return result.Enrollment, result.Token
	}
	host, token := newHost("0x1111111111111111111111111111111111111111", "rig-1", "unit-a", "GPU-aaa")
	other, otherToken := newHost("0x2222222222222222222222222222222222222222", "rig-2", "unit-b", "GPU-bbb")

	mux := http.NewServeMux()
	Register(mux, Deps{Repo: stateRepo, Enrollment: enrollment, Sessions: NewSessionAuth(), Catalog: catalog})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &desiredStateFixture{
		repo: stateRepo, server: server,
		host: host, token: token, other: other, otherTk: otherToken,
	}
}

func (f *desiredStateFixture) putAssignment(t *testing.T, unitID, host, templateID string, state types.TemplateAssignmentState) types.TemplateAssignment {
	t.Helper()
	assignment := types.TemplateAssignment{
		ID:               unitID + "|" + templateID,
		HardwareUnitID:   unitID,
		HostEnrollmentID: host,
		TemplateID:       templateID,
		Role:             types.TemplateAssignmentPrimary,
		State:            state,
	}
	if err := f.repo.PutTemplateAssignment(assignment); err != nil {
		t.Fatalf("PutTemplateAssignment() error = %v", err)
	}
	return assignment
}

func (f *desiredStateFixture) get(t *testing.T, enrollmentID, token, ifNoneMatch string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.server.URL+"/member/v1/enrollments/"+enrollmentID+"/desired-state", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET desired-state error = %v", err)
	}
	return resp
}

func (f *desiredStateFixture) postStatus(t *testing.T, enrollmentID, token string, report statusReport) *http.Response {
	t.Helper()
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost,
		f.server.URL+"/member/v1/enrollments/"+enrollmentID+"/status", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST status error = %v", err)
	}
	return resp
}

func (f *desiredStateFixture) stateOf(t *testing.T, assignmentID string) types.TemplateAssignmentState {
	t.Helper()
	assignment, err := f.repo.GetTemplateAssignment(assignmentID)
	if err != nil {
		t.Fatalf("GetTemplateAssignment(%s) error = %v", assignmentID, err)
	}
	return assignment.State
}

// The desired state is a host credential's view of itself, and an agent
// that polls often should mostly be answered with 304 and no body.
func TestDesiredStateRequiresTheTokenAndAnswers304OnItsOwnRevision(t *testing.T) {
	fixture := newDesiredStateFixture(t)
	fixture.putAssignment(t, "unit-a", fixture.host.ID, "chat-a", types.TemplateAssignmentPending)

	resp := fixture.get(t, fixture.host.ID, "", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d, want 401", resp.StatusCode)
	}

	bad := fixture.get(t, fixture.host.ID, "not-the-token", "")
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET with a wrong token = %d, want 401", bad.StatusCode)
	}

	first := fixture.get(t, fixture.host.ID, fixture.token, "")
	defer func() { _ = first.Body.Close() }()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d, want 200", first.StatusCode)
	}
	var doc desiredstate.Document
	if err := json.NewDecoder(first.Body).Decode(&doc); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if doc.EnrollmentID != fixture.host.ID || len(doc.Services) != 1 {
		t.Fatalf("document = %+v, want this host's single service", doc)
	}
	etag := first.Header.Get("ETag")
	if etag != `"`+doc.Revision+`"` {
		t.Fatalf("ETag = %q, want the revision %q", etag, doc.Revision)
	}

	second := fixture.get(t, fixture.host.ID, fixture.token, etag)
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", second.StatusCode)
	}
	body := readBody(t, second)
	if len(body) != 0 {
		t.Fatalf("304 carried a body: %q", body)
	}

	// A placement change has to break the condition, or the agent would
	// never learn about it.
	fixture.putAssignment(t, "unit-a", fixture.host.ID, "chat-a", types.TemplateAssignmentDraining)
	third := fixture.get(t, fixture.host.ID, fixture.token, etag)
	defer func() { _ = third.Body.Close() }()
	if third.StatusCode != http.StatusOK {
		t.Fatalf("conditional GET after a drain = %d, want 200 with the new document", third.StatusCode)
	}
}

// An enrollment token authenticates one host. Reading another's desired
// state would leak what the pool placed on someone else's hardware.
func TestDesiredStateRefusesAnotherHostsEnrollment(t *testing.T) {
	fixture := newDesiredStateFixture(t)
	fixture.putAssignment(t, "unit-a", fixture.host.ID, "chat-a", types.TemplateAssignmentPending)
	fixture.putAssignment(t, "unit-b", fixture.other.ID, "chat-a", types.TemplateAssignmentPending)

	resp := fixture.get(t, fixture.host.ID, fixture.otherTk, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET of another host's desired state = %d, want 401", resp.StatusCode)
	}

	// Its own enrollment still works, and shows only its own service.
	own := fixture.get(t, fixture.other.ID, fixture.otherTk, "")
	defer func() { _ = own.Body.Close() }()
	if own.StatusCode != http.StatusOK {
		t.Fatalf("GET of its own desired state = %d, want 200", own.StatusCode)
	}
	var doc desiredstate.Document
	if err := json.NewDecoder(own.Body).Decode(&doc); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(doc.Services) != 1 || doc.Services[0].AssignmentID != "unit-b|chat-a" {
		t.Fatalf("document = %+v, want only the other host's own placement", doc)
	}
}

// The status report is the only thing that advances an assignment's
// lifecycle from the host side, so each verdict has to land exactly
// where the pool expects it — and nowhere else.
func TestStatusReportAppliesEachVerdictToTheAssignment(t *testing.T) {
	fixture := newDesiredStateFixture(t)
	fixture.putAssignment(t, "unit-a", fixture.host.ID, "chat-a", types.TemplateAssignmentPending)

	unauthorized := fixture.postStatus(t, fixture.host.ID, "", statusReport{Revision: "rev-1"})
	defer func() { _ = unauthorized.Body.Close() }()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST = %d, want 401", unauthorized.StatusCode)
	}
	foreign := fixture.postStatus(t, fixture.host.ID, fixture.otherTk, statusReport{Revision: "rev-1"})
	defer func() { _ = foreign.Body.Close() }()
	if foreign.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST with another host's token = %d, want 401", foreign.StatusCode)
	}

	service := desiredstate.ServiceName("unit-a|chat-a")

	// A host that got the container running is what certification waits
	// on, so pending advances.
	running := fixture.postStatus(t, fixture.host.ID, fixture.token, statusReport{
		Revision: "rev-1",
		Services: []serviceStatus{
			{Name: service, Status: "running"},
			// A service the pool never asked for is stale local state,
			// not a reason to throw away the rest of the report.
			{Name: "runner-ghost", Status: "running"},
		},
	})
	defer func() { _ = running.Body.Close() }()
	if running.StatusCode != http.StatusOK {
		t.Fatalf("running report = %d, want 200", running.StatusCode)
	}
	var recorded struct {
		Status   string `json:"status"`
		Recorded int    `json:"recorded"`
	}
	if err := json.NewDecoder(running.Body).Decode(&recorded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if recorded.Status != "recorded" || recorded.Recorded != 1 {
		t.Fatalf("response = %+v, want the unknown service ignored and the known one applied", recorded)
	}
	if got := fixture.stateOf(t, "unit-a|chat-a"); got != types.TemplateAssignmentTesting {
		t.Fatalf("state after running = %s, want %s", got, types.TemplateAssignmentTesting)
	}

	// A failure is recorded, not acted on: the ladder decides what a
	// failing runner means, not the report.
	failed := fixture.postStatus(t, fixture.host.ID, fixture.token, statusReport{
		Revision: "rev-1",
		Services: []serviceStatus{{Name: service, Status: "failed", Detail: "pull: no such image"}},
	})
	defer func() { _ = failed.Body.Close() }()
	if failed.StatusCode != http.StatusOK {
		t.Fatalf("failed report = %d, want 200", failed.StatusCode)
	}
	if got := fixture.stateOf(t, "unit-a|chat-a"); got != types.TemplateAssignmentTesting {
		t.Fatalf("state after a failure = %s, want it left at %s", got, types.TemplateAssignmentTesting)
	}

	// A stopped report on a service that is not draining must not
	// retire it: an assignment the pool still wants has to survive a
	// container that merely fell over.
	stoppedWhileTesting := fixture.postStatus(t, fixture.host.ID, fixture.token, statusReport{
		Revision: "rev-1",
		Services: []serviceStatus{{Name: service, Status: "stopped"}},
	})
	defer func() { _ = stoppedWhileTesting.Body.Close() }()
	if got := fixture.stateOf(t, "unit-a|chat-a"); got != types.TemplateAssignmentTesting {
		t.Fatalf("state after a stop that was not a drain = %s, want it left at %s", got, types.TemplateAssignmentTesting)
	}

	// Draining plus stopped is the one path that removes a placement,
	// and it happens only after the host says the work has stopped.
	fixture.putAssignment(t, "unit-a", fixture.host.ID, "chat-a", types.TemplateAssignmentDraining)
	stopped := fixture.postStatus(t, fixture.host.ID, fixture.token, statusReport{
		Revision: "rev-2",
		Services: []serviceStatus{{Name: service, Status: "stopped", Detail: "draining"}},
	})
	defer func() { _ = stopped.Body.Close() }()
	if stopped.StatusCode != http.StatusOK {
		t.Fatalf("stopped report = %d, want 200", stopped.StatusCode)
	}
	if got := fixture.stateOf(t, "unit-a|chat-a"); got != types.TemplateAssignmentRetired {
		t.Fatalf("state after a drained stop = %s, want %s", got, types.TemplateAssignmentRetired)
	}

	// The retired placement disappears from the next desired state, so
	// the agent's compose file loses the service.
	resp := fixture.get(t, fixture.host.ID, fixture.token, "")
	defer func() { _ = resp.Body.Close() }()
	var doc desiredstate.Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(doc.Services) != 0 {
		t.Fatalf("retired placement still in the desired state: %+v", doc.Services)
	}
}

// Every report is an operator-visible fact about a host: what revision
// it claimed and how much it reported on.
func TestStatusReportWritesAnAuditEvent(t *testing.T) {
	fixture := newDesiredStateFixture(t)
	fixture.putAssignment(t, "unit-a", fixture.host.ID, "chat-a", types.TemplateAssignmentPending)

	resp := fixture.postStatus(t, fixture.host.ID, fixture.token, statusReport{
		Revision: "rev-audit",
		Services: []serviceStatus{{Name: desiredstate.ServiceName("unit-a|chat-a"), Status: "running"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status report = %d, want 200", resp.StatusCode)
	}

	events, err := fixture.repo.ListAuditEventsFiltered("member_status_report", "host_enrollment", fixture.host.ID, 10)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want exactly one status report", len(events))
	}
	event := events[0]
	if event.Actor != fixture.host.MemberEthAddress {
		t.Fatalf("audit actor = %q, want the member %q", event.Actor, fixture.host.MemberEthAddress)
	}
	if event.Details["revision"] != "rev-audit" {
		t.Fatalf("audit details = %+v, want the reported revision", event.Details)
	}
	if count, ok := event.Details["services"].(float64); !ok || int(count) != 1 {
		t.Fatalf("audit details = %+v, want the number of reported services", event.Details)
	}
}
