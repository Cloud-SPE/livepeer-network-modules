package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/desiredstate"
)

// recordingRunner logs the order of the compose work so a test can say
// what happened before what.
type recordingRunner struct {
	events *[]string
}

func (r recordingRunner) WriteCompose(path, content string) error {
	*r.events = append(*r.events, "write")
	return nil
}

func (r recordingRunner) Pull(ctx context.Context, path string) error {
	*r.events = append(*r.events, "pull")
	return nil
}

func (r recordingRunner) Up(ctx context.Context, path string) error {
	*r.events = append(*r.events, "up")
	return nil
}

func desiredDoc() desiredstate.Document {
	return desiredstate.Document{
		EnrollmentID: "host-1",
		Revision:     "rev-9",
		Services: []desiredstate.Service{
			{
				Name: "runner-unit-a-chat-a", ComposeFragment: "  runner-unit-a-chat-a:\n    image: a\n",
				DeviceIDs: []string{"GPU-aaa"}, TemplateID: "chat-a", AssignmentID: "unit-a|chat-a",
				Capability: "openai:chat-completions", Protocol: "paid-job/v1",
				Identity: map[string]string{"openai.model": "gpt-oss-20b"},
			},
			{
				Name: "runner-unit-b-chat-b", ComposeFragment: "  runner-unit-b-chat-b:\n    image: b\n",
				DeviceIDs: []string{"GPU-bbb"}, TemplateID: "chat-b", AssignmentID: "unit-b|chat-b",
				Capability: "openai:chat-completions", Protocol: "paid-job/v1",
				Identity: map[string]string{"openai.model": "gpt-oss-20b"},
				Draining: true,
			},
		},
	}
}

// controllerStub serves one desired state and captures the report.
func controllerStub(t *testing.T, doc desiredstate.Document, reports *[]desiredstate.StatusReport) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/desired-state"):
			if r.Header.Get("If-None-Match") != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		case strings.HasSuffix(r.URL.Path, "/status"):
			var report desiredstate.StatusReport
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				t.Errorf("decode report: %v", err)
			}
			*reports = append(*reports, report)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// The ordering rule of the drain (runner-attach §7.1): the broker has to
// know a runner is draining BEFORE the container is touched. Stopping
// first drops the requests the broker had already dispatched.
func TestReconcileOnceTellsTheBrokerBeforeItTouchesCompose(t *testing.T) {
	doc := desiredDoc()
	var reports []desiredstate.StatusReport
	server := controllerStub(t, doc, &reports)

	var events []string
	state := &runnerState{}
	var atReattach []attach.Runner
	var revisionAtReattach string
	reattach := func() {
		events = append(events, "reattach")
		// Re-registering is only useful if the shared state already
		// holds the new set: the tunnel builds its document from here.
		atReattach, revisionAtReattach = state.get()
	}

	cfg := config{ComposeFile: filepath.Join(t.TempDir(), "runners.compose.yaml")}
	client := desiredstate.New(server.URL, "host-1", "token", time.Second)
	if err := reconcileOnce(context.Background(), client, recordingRunner{events: &events}, cfg, state, reattach); err != nil {
		t.Fatalf("reconcileOnce() error = %v", err)
	}

	if got := strings.Join(events, ","); got != "reattach,write,pull,up" {
		t.Fatalf("order = %s; the broker must be told before the containers are touched", got)
	}
	if revisionAtReattach != doc.Revision {
		t.Fatalf("revision at reattach = %q, want %q: the tunnel would re-register the old set",
			revisionAtReattach, doc.Revision)
	}
	if len(atReattach) != 2 {
		t.Fatalf("runners at reattach = %d, want 2", len(atReattach))
	}
	if !atReattach[1].Draining {
		t.Fatalf("the withdrawn runner was not marked draining before compose ran: %+v", atReattach[1])
	}

	// What the state holds afterwards is what the next tunnel session
	// declares.
	runners, revision := state.get()
	if revision != doc.Revision || len(runners) != 2 {
		t.Fatalf("state after reconcile = %d runner(s) at %q", len(runners), revision)
	}
	if len(reports) != 1 || reports[0].Revision != doc.Revision {
		t.Fatalf("reports = %+v, want one for this revision", reports)
	}
	status := map[string]string{}
	for _, service := range reports[0].Services {
		status[service.Name] = service.Status
	}
	if status["runner-unit-a-chat-a"] != desiredstate.StatusRunning {
		t.Fatalf("report = %+v, want the live service reported running", reports[0].Services)
	}
	if status["runner-unit-b-chat-b"] != desiredstate.StatusStopped {
		t.Fatalf("report = %+v, want the draining service reported stopped", reports[0].Services)
	}
}

// An unchanged pool must cost nothing: no compose run, no re-register,
// no report. This loop runs on every enrolled host forever.
func TestReconcileOnceDoesNothingWhenTheDesiredStateIsUnchanged(t *testing.T) {
	doc := desiredDoc()
	var reports []desiredstate.StatusReport
	server := controllerStub(t, doc, &reports)

	var events []string
	state := &runnerState{}
	reattach := func() { events = append(events, "reattach") }
	cfg := config{ComposeFile: filepath.Join(t.TempDir(), "runners.compose.yaml")}
	client := desiredstate.New(server.URL, "host-1", "token", time.Second)
	runner := recordingRunner{events: &events}

	if err := reconcileOnce(context.Background(), client, runner, cfg, state, reattach); err != nil {
		t.Fatalf("first reconcileOnce() error = %v", err)
	}
	before := len(events)
	if err := reconcileOnce(context.Background(), client, runner, cfg, state, reattach); err != nil {
		t.Fatalf("second reconcileOnce() error = %v", err)
	}
	if len(events) != before {
		t.Fatalf("an unchanged poll did work: %v", events[before:])
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want no second report for an unchanged revision", len(reports))
	}
}

// The attach entry is where a drain becomes visible to the broker, and
// the devices are what keep two runners off one card.
func TestRunnersForCarriesDrainingAndDevices(t *testing.T) {
	runners := runnersFor(desiredDoc())
	if len(runners) != 2 {
		t.Fatalf("runners = %d, want one per service", len(runners))
	}
	if runners[0].LocalID != "runner-unit-a-chat-a" {
		t.Fatalf("local_id = %q, want the compose service name the agent routes on", runners[0].LocalID)
	}
	if !strings.Contains(runners[0].URL, "runner-unit-a-chat-a") {
		t.Fatalf("url = %q, want the container's own address on the compose network", runners[0].URL)
	}
	if len(runners[0].Devices) != 1 || runners[0].Devices[0] != "GPU-aaa" {
		t.Fatalf("devices = %v, want the card the pool placed it on", runners[0].Devices)
	}
	if runners[0].Draining {
		t.Fatal("a service the pool still wants was marked draining")
	}
	if !runners[1].Draining {
		t.Fatal("the withdrawn service is not marked draining; the broker would keep dispatching to it")
	}
	if len(runners[1].Devices) != 1 || runners[1].Devices[0] != "GPU-bbb" {
		t.Fatalf("devices = %v, want the second card", runners[1].Devices)
	}
	if runners[0].Profile != attach.ProfileOpenAICompatible {
		t.Fatalf("profile = %q", runners[0].Profile)
	}
	// The profile follows the CAPABILITY the controller named, not the
	// template id: a pool that renames a template must not silently
	// change what its runners declare on the wire.
	if got := runnersFor(desiredstate.Document{Services: []desiredstate.Service{
		{Name: "runner-x", TemplateID: "anything", Capability: "video:transcode.abr"},
	}}); got[0].Profile != attach.ProfileTranscode {
		t.Fatalf("transcode capability got profile %q", got[0].Profile)
	}
}

// The runner set the desired-state loop produces is the set the tunnel
// declares, so it has to be a set the agent can actually attach with.
func TestRunnersForProducesAnAttachableDocument(t *testing.T) {
	runners := runnersFor(desiredDoc())
	_, err := attach.Build(attach.Host{
		HostID:     "host-1",
		Credential: attach.Credential{Kind: "bearer", Token: "cred"},
		Hardware: []attach.Hardware{
			{GPUUUID: "GPU-aaa", GPUModel: "NVIDIA GeForce RTX 4090", VRAMBytes: 24 << 30},
			{GPUUUID: "GPU-bbb", GPUModel: "NVIDIA GeForce RTX 4090", VRAMBytes: 24 << 30},
		},
	}, runners)
	if err != nil {
		t.Fatalf("attach.Build() rejected the pool's own runner set: %v\n"+
			"a pool-managed host cannot attach at all, so it never serves work", err)
	}
}

// Pool management is all three facts or none: a half-configured host
// must poll nobody rather than poll anonymously.
func TestPoolManagedNeedsControllerEnrollmentAndToken(t *testing.T) {
	full := config{ControllerURL: "http://controller", EnrollmentID: "host-1", EnrollmentToken: "token"}
	if !full.PoolManaged() {
		t.Fatal("a fully configured host is not pool-managed")
	}
	for _, tc := range []struct {
		name string
		cfg  config
	}{
		{"no controller", config{EnrollmentID: "host-1", EnrollmentToken: "token"}},
		{"no enrollment", config{ControllerURL: "http://controller", EnrollmentToken: "token"}},
		{"no token", config{ControllerURL: "http://controller", EnrollmentID: "host-1"}},
		{"nothing", config{}},
	} {
		if tc.cfg.PoolManaged() {
			t.Fatalf("%s: PoolManaged() = true", tc.name)
		}
	}
}

// The token is a credential: an environment variable is readable by
// every process on the host, so the file the bundle ships wins.
func TestEnrollmentTokenPrefersTheFileOverTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment-token")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("POOL_ENROLLMENT_TOKEN_FILE", path)
	t.Setenv("POOL_ENROLLMENT_TOKEN", "from-env")
	if got := enrollmentToken(); got != "from-file" {
		t.Fatalf("enrollmentToken() = %q, want the file's token", got)
	}

	// With no file the variable is still honoured — a throwaway run has
	// nothing else to offer.
	t.Setenv("POOL_ENROLLMENT_TOKEN_FILE", "")
	if got := enrollmentToken(); got != "from-env" {
		t.Fatalf("enrollmentToken() = %q, want the environment token", got)
	}

	// A path that names no file leaves the variable as the only token
	// on offer, which is what a host mid-bundle-install has.
	t.Setenv("POOL_ENROLLMENT_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
	if got := enrollmentToken(); got != "from-env" {
		t.Fatalf("enrollmentToken() = %q", got)
	}
}
