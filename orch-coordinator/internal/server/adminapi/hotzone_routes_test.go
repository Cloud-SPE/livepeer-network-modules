package adminapi

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/providers/brokeradmin"
)

// fakeAdmin records writes and serves canned reads, so the tests assert
// what an operator sees and what a gesture actually sends.
type fakeAdmin struct {
	mu         sync.Mutex
	runners    map[string][]brokeradmin.Runner
	offers     map[string][]brokeradmin.Offer
	runs       map[string][]brokeradmin.CertificationRun
	creds      map[string][]brokeradmin.Credential
	failFor    map[string]error
	accepted   []string
	certified  []string
	enrolled   []string
	revoked    []string
	disconnect []string
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{
		runners: map[string][]brokeradmin.Runner{},
		offers:  map[string][]brokeradmin.Offer{},
		runs:    map[string][]brokeradmin.CertificationRun{},
		creds:   map[string][]brokeradmin.Credential{},
		failFor: map[string]error{},
	}
}

func (f *fakeAdmin) Runners(_ context.Context, b string) ([]brokeradmin.Runner, error) {
	if err := f.failFor[b]; err != nil {
		return nil, err
	}
	return f.runners[b], nil
}

func (f *fakeAdmin) Offers(_ context.Context, b string) ([]brokeradmin.Offer, error) {
	if err := f.failFor[b]; err != nil {
		return nil, err
	}
	return f.offers[b], nil
}

func (f *fakeAdmin) Certification(_ context.Context, b string) ([]brokeradmin.CertificationRun, error) {
	if err := f.failFor[b]; err != nil {
		return nil, err
	}
	return f.runs[b], nil
}

func (f *fakeAdmin) Credentials(_ context.Context, b string) ([]brokeradmin.Credential, error) {
	if err := f.failFor[b]; err != nil {
		return nil, err
	}
	return f.creds[b], nil
}

func (f *fakeAdmin) AcceptShape(_ context.Context, b, offering, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepted = append(f.accepted, fmt.Sprintf("%s/%s/%s", b, offering, hash))
	return nil
}

func (f *fakeAdmin) RunCertification(_ context.Context, b, host, offering, local string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.certified = append(f.certified, fmt.Sprintf("%s/%s/%s/%s", b, host, offering, local))
	return "run_test", nil
}

func (f *fakeAdmin) Enroll(_ context.Context, b, host, label string) (*brokeradmin.Enrollment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enrolled = append(f.enrolled, fmt.Sprintf("%s/%s/%s", b, host, label))
	out := &brokeradmin.Enrollment{CredentialID: "cred_1", HostID: "host-new", ExpiresAt: time.Now().Add(time.Hour)}
	out.Credential.Kind = "bearer"
	out.Credential.Token = "lpc_secret_shown_once"
	out.Bundle.BrokerURLs = map[string]string{"ws": "wss://broker/internal/v1/worker/session"}
	out.Bundle.ContractVersion = "1.0"
	return out, nil
}

func (f *fakeAdmin) Revoke(_ context.Context, b, cred, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, fmt.Sprintf("%s/%s/%s", b, cred, reason))
	return nil
}

func (f *fakeAdmin) Disconnect(_ context.Context, b, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnect = append(f.disconnect, b+"/"+host)
	return nil
}

func setupHotzone(t *testing.T, admin *fakeAdmin, brokers []HotzoneBroker) *Server {
	t.Helper()
	srv, deps := setupServer(t)
	// Re-register with the hot zone attached: WebRoutes owns the mux.
	srv2 := New("127.0.0.1:0", srv.logger, nil)
	deps.Hotzone = &HotzoneDeps{Admin: admin, Brokers: brokers, Timeout: 2 * time.Second}
	if err := srv2.WebRoutes(deps); err != nil {
		t.Fatal(err)
	}
	if _, err := srv2.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv2.Serve(ctx) }()
	return srv2
}

func get(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get("http://" + srv.Addr() + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func post(t *testing.T, srv *Server, path string, form url.Values) (int, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm("http://"+srv.Addr()+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if loc := resp.Header.Get("Location"); loc != "" {
		return resp.StatusCode, loc
	}
	return resp.StatusCode, string(body)
}

func oneBroker() []HotzoneBroker {
	return []HotzoneBroker{{Name: "b1", BaseURL: "http://x:1", Administrable: true}}
}

// The Runners page exists to answer "why is this host not earning?", so
// the disagreeing field has to be on it.
func TestRunnersPageShowsWhyARunnerIsNotServing(t *testing.T) {
	admin := newFakeAdmin()
	run := brokeradmin.Runner{HostID: "host-3f9a", State: "connected", AgentVersion: "agent/1", ContractVersion: "1.0"}
	run.Enrollment.Label = "rig in rack 3"
	run.Hardware = []brokeradmin.Hardware{{GPUUUID: "GPU-1", GPUModel: "NVIDIA H100 80GB HBM3", VRAMBytes: 85899345920}}
	run.Capabilities = []brokeradmin.RunnerCapability{
		{
			LocalID: "chat", CapabilityID: "openai:chat-completions", Protocol: "paid-job/v1",
			Attach:   brokeradmin.AttachVerdict{Status: "accepted"},
			Declared: map[string]any{"identity": map[string]any{"openai.model": "llama-3-70b"}},
			Offers: []brokeradmin.OfferPair{{
				OfferingID: "llama-shared", State: "ineligible",
				Reason: &brokeradmin.Reason{Code: "shape_mismatch", Field: "/promoted/x-quantization",
					Declared: `"int8"`, Frozen: `"fp8"`},
			}},
		},
		{
			LocalID: "whisper", CapabilityID: "openai:audio-transcriptions",
			Attach: brokeradmin.AttachVerdict{Status: "rejected", Reasons: []brokeradmin.Reason{{
				Code: "extractor_unknown", Field: "/capabilities/1/work_unit/extractor/type",
				Declared: "whisper-seconds", Expected: "one of: openai-usage, …",
			}}},
		},
	}
	admin.runners["b1"] = []brokeradmin.Runner{run}
	srv := setupHotzone(t, admin, oneBroker())

	code, body := get(t, srv, "/runners")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		"host-3f9a", "rig in rack 3", "NVIDIA H100 80GB HBM3", "80 GiB",
		// The ineligible pair names the field and both sides.
		"shape_mismatch", "/promoted/x-quantization", "int8", "fp8",
		// The rejected capability is visible with its reason.
		"extractor_unknown", "whisper-seconds",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("runners page missing %q", want)
		}
	}
}

// The accept-shape gesture is the one that changes what is advertised;
// it must send exactly the hash the operator clicked.
func TestOffersPageAndAcceptShape(t *testing.T) {
	admin := newFakeAdmin()
	offer := brokeradmin.Offer{
		OfferingID: "llama-shared", CapabilityID: "openai:chat-completions",
		Protocol: "paid-job/v1", State: "frozen", Advertised: true, Source: "file",
		Frozen: &brokeradmin.FrozenShape{ShapeHash: "sha256:aaaa1111", FrozenAt: time.Now()},
		Candidates: []brokeradmin.Candidate{{
			ShapeHash: "sha256:bbbb2222",
			Diff:      []brokeradmin.Reason{{Field: "/promoted/x-quantization", Expected: `"fp8"`, Declared: `"int8"`}},
		}},
	}
	offer.Operator.Price.AmountWei = "210000000"
	offer.Operator.Price.PerUnits = 1
	offer.Runners.Eligible = 3
	offer.Runners.Ineligible = 1
	admin.offers["b1"] = []brokeradmin.Offer{offer}
	srv := setupHotzone(t, admin, oneBroker())

	code, body := get(t, srv, "/offers")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"llama-shared", "sha256:aaaa1111", "sha256:bbbb2222", "210000000", "Accept this shape"} {
		if !strings.Contains(body, want) {
			t.Fatalf("offers page missing %q", want)
		}
	}

	code, loc := post(t, srv, "/offers/accept-shape", url.Values{
		"broker": {"b1"}, "offering_id": {"llama-shared"}, "shape_hash": {"sha256:bbbb2222"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("accept-shape status %d (%s)", code, loc)
	}
	if len(admin.accepted) != 1 || admin.accepted[0] != "b1/llama-shared/sha256:bbbb2222" {
		t.Fatalf("accept-shape sent %v", admin.accepted)
	}
	// The operator is told the signature is still required.
	if !strings.Contains(loc, "sign") {
		t.Fatalf("flash does not mention signing: %s", loc)
	}
}

// The credential is shown once, inline — never redirected into a URL.
func TestEnrollShowsCredentialInlineOnce(t *testing.T) {
	admin := newFakeAdmin()
	admin.creds["b1"] = []brokeradmin.Credential{{
		CredentialID: "cred_old", HostID: "host-old", Kind: "bearer", State: "active", Source: "enroll",
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	srv := setupHotzone(t, admin, oneBroker())

	code, body := post(t, srv, "/enroll", url.Values{"broker": {"b1"}, "label": {"rack 3"}})
	if code != http.StatusOK {
		t.Fatalf("enroll status %d: %s", code, body)
	}
	if !strings.Contains(body, "lpc_secret_shown_once") {
		t.Fatalf("credential not rendered: %s", body)
	}
	if !strings.Contains(body, "Shown once") || !strings.Contains(body, "LIVEPEER_ATTACH_CREDENTIAL_FILE") {
		t.Fatalf("enroll page missing the handling guidance / bundle: %s", body)
	}
	if len(admin.enrolled) != 1 || !strings.HasSuffix(admin.enrolled[0], "/rack 3") {
		t.Fatalf("enroll sent %v", admin.enrolled)
	}

	// Revoke names a reason for the audit trail.
	code, loc := post(t, srv, "/enroll/revoke", url.Values{
		"broker": {"b1"}, "credential_id": {"cred_old"}, "reason": {"laptop lost"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("revoke status %d", code)
	}
	if len(admin.revoked) != 1 || admin.revoked[0] != "b1/cred_old/laptop lost" {
		t.Fatalf("revoke sent %v", admin.revoked)
	}
	_ = loc
}

func TestCertificationPageAndRerun(t *testing.T) {
	admin := newFakeAdmin()
	run := brokeradmin.CertificationRun{
		HostID: "host-3f9a", LocalID: "chat", OfferingID: "llama-shared",
		RunID: "run_1", Trigger: "match", State: "failed", StartedAt: time.Now(),
	}
	run.Steps = []struct {
		Name       string         `json:"name"`
		Type       string         `json:"type"`
		Required   bool           `json:"required"`
		Status     string         `json:"status"`
		DurationMS int64          `json:"duration_ms"`
		Evidence   map[string]any `json:"evidence,omitempty"`
		Message    string         `json:"message,omitempty"`
	}{
		{Name: "ready", Type: "readiness", Required: true, Status: "passed", DurationMS: 41},
		{Name: "smoke", Type: "request", Required: true, Status: "failed", DurationMS: 1830,
			Evidence: map[string]any{"status": 500}, Message: "status 500 not in expect_status"},
		{Name: "usage", Type: "usage", Required: true, Status: "skipped"},
	}
	admin.runs["b1"] = []brokeradmin.CertificationRun{run}
	srv := setupHotzone(t, admin, oneBroker())

	code, body := get(t, srv, "/certification")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"run_1", "readiness", "status 500 not in expect_status", "skipped"} {
		if !strings.Contains(body, want) {
			t.Fatalf("certification page missing %q", want)
		}
	}

	code, _ = post(t, srv, "/certification/run", url.Values{
		"broker": {"b1"}, "host_id": {"host-3f9a"}, "offering_id": {"llama-shared"}, "local_id": {"chat"},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("rerun status %d", code)
	}
	if len(admin.certified) != 1 || admin.certified[0] != "b1/host-3f9a/llama-shared/chat" {
		t.Fatalf("rerun sent %v", admin.certified)
	}
}

// A broker that is unreachable, or has no token, must not hide the ones
// that work — and must say which is missing.
func TestPartialBrokerFailureIsNamed(t *testing.T) {
	admin := newFakeAdmin()
	admin.runners["ok"] = []brokeradmin.Runner{{HostID: "host-visible", State: "connected"}}
	admin.failFor["broken"] = brokeradmin.ErrUnavailable
	srv := setupHotzone(t, admin, []HotzoneBroker{
		{Name: "ok", BaseURL: "http://a", Administrable: true},
		{Name: "broken", BaseURL: "http://b", Administrable: true},
		{Name: "untokened", BaseURL: "http://c", Administrable: false},
	})
	code, body := get(t, srv, "/runners")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !strings.Contains(body, "host-visible") {
		t.Fatal("a failing broker hid a working one")
	}
	if !strings.Contains(body, "broken") || !strings.Contains(body, "unreachable") {
		t.Fatalf("unreachable broker not named: %s", body)
	}
	if !strings.Contains(body, "untokened") || !strings.Contains(body, "admin_token_ref") {
		t.Fatalf("untokened broker not explained: %s", body)
	}
}

// Without a configured hot zone the pages do not exist at all, rather
// than rendering an empty console.
func TestHotzonePagesAbsentWhenUnconfigured(t *testing.T) {
	srv, _ := setupServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	for _, path := range []string{"/runners", "/offers", "/enroll", "/certification"} {
		if code, _ := get(t, srv, path); code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404 when the hot zone is unconfigured", path, code)
		}
	}
}

// A template fault must not ship a truncated page with 200 OK.
func TestRenderPageFailsCleanly(t *testing.T) {
	tmpl := template.Must(template.New("x").Parse(`{{define "layout"}}before {{.Boom}} after{{end}}`))
	rec := httptest.NewRecorder()
	renderPage(rec, tmpl, struct{}{})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "before") {
		t.Fatalf("partial page was written: %q", rec.Body.String())
	}
}
