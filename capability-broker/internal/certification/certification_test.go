package certification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/openaiusage"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/responsejsonpath"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// handlerConn adapts an http.Handler into a runners.Conn: forwarded
// requests are served in-process, with every request captured.
type handlerConn struct {
	h    http.Handler
	mu   sync.Mutex
	seen []*http.Request
}

func (c *handlerConn) Close() error { return nil }
func (c *handlerConn) Forward(_ context.Context, req backend.ForwardRequest) (*http.Response, error) {
	httpReq := httptest.NewRequest(req.Method, req.URL, req.Body)
	for k, vs := range req.Headers {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	c.mu.Lock()
	c.seen = append(c.seen, httpReq)
	c.mu.Unlock()
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, httpReq)
	return rec.Result(), nil
}

func testRegistryWith(t *testing.T, conn runners.Conn, cap *runnerattach.Capability) *runners.Registry {
	t.Helper()
	reg := runners.New(0)
	doc := &runnerattach.Document{HostID: "h1", AgentVersion: "t/1", Capabilities: []runnerattach.Capability{*cap}}
	res := &runnerattach.Result{Document: "accepted", HostID: "h1",
		Capabilities: []runnerattach.CapabilityResult{{Index: 0, LocalID: cap.LocalID, CapabilityID: cap.CapabilityID, Status: "accepted"}}}
	reg.Attach("conn-1", conn, runners.Enrollment{}, doc, res)
	return reg
}

func extractorRegistry() *extractors.Registry {
	r := extractors.NewRegistry()
	r.Register(openaiusage.Name, openaiusage.New)
	r.Register(responsejsonpath.Name, responsejsonpath.New)
	return r
}

func jobCapability() *runnerattach.Capability {
	return &runnerattach.Capability{
		CapabilityID: "openai:chat-completions", Protocol: "paid-job/v1", LocalID: "chat",
		Transports: []string{"unary", "stream"},
		WorkUnit:   runnerattach.WorkUnit{Name: "tokens", Extractor: map[string]any{"type": "openai-usage"}},
		Paths:      map[string]string{"invoke": "/v1/chat/completions"},
		Readiness:  runnerattach.Readiness{Type: "http-status", Path: "/ready"},
		Identity:   map[string]string{"openai.model": "llama"},
	}
}

func chatOffer(steps ...config.CertificationStep) config.Offer {
	return config.Offer{
		OfferingID: "shared", Capability: "openai:chat-completions", Protocol: "paid-job/v1",
		Extra:         map[string]any{"region": "us-west-2"},
		Certification: steps,
	}
}

// runAndWait triggers Certify and waits for the reported outcome.
func runAndWait(t *testing.T, conn runners.Conn, cap *runnerattach.Capability, offer config.Offer) (offers.CertOutcome, Result, *Engine) {
	t.Helper()
	reg := testRegistryWith(t, conn, cap)
	e := New(reg, Options{Extractors: extractorRegistry()})
	t.Cleanup(e.Close)
	ch := make(chan offers.CertOutcome, 1)
	e.Report = func(_ offers.PairKey, out offers.CertOutcome) { ch <- out }
	sn, _ := reg.Get("h1")
	first := e.Certify(sn, cap, offer)
	if len(offer.Certification) == 0 {
		return first, Result{}, e
	}
	if !first.Pending {
		t.Fatalf("expected pending, got %+v", first)
	}
	select {
	case out := <-ch:
		res := e.PairResults("h1", offer.OfferingID)
		if len(res) == 0 {
			t.Fatal("no result recorded")
		}
		return out, res[0], e
	case <-time.After(10 * time.Second):
		t.Fatal("run did not report")
		return offers.CertOutcome{}, Result{}, e
	}
}

func TestEmptyStepsCertifyOnMatch(t *testing.T) {
	out, _, _ := runAndWait(t, &handlerConn{h: http.NotFoundHandler()}, jobCapability(), chatOffer())
	if !out.Passed || out.State != RunPassed {
		t.Fatalf("empty steps: %+v", out)
	}
}

func TestFullJobRunPassesWithEvidence(t *testing.T) {
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			w.WriteHeader(200)
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "llama" {
				http.Error(w, "wrong model", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}],"usage":{"total_tokens":17}}`))
		default:
			http.NotFound(w, r)
		}
	})}
	offer := chatOffer(
		config.CertificationStep{Name: "ready", Type: "readiness"},
		config.CertificationStep{Name: "smoke", Type: "request", Config: map[string]any{
			"transport": "unary",
			"body":      map[string]any{"model": "{{identity.openai.model}}", "region": "{{offer.extra.region}}"},
			"assert":    []any{"$.choices[0].message.content", map[string]any{"path": "$.usage.total_tokens", "min": 1}},
		}},
		config.CertificationStep{Name: "usage", Type: "usage", Config: map[string]any{"min_units": 10}},
		config.CertificationStep{Name: "latency", Type: "latency", Required: boolPtr(false),
			Config: map[string]any{"samples": 2, "p50_max_ms": 60000}},
	)
	out, res, _ := runAndWait(t, conn, jobCapability(), offer)
	if !out.Passed || res.State != RunPassed || len(res.Steps) != 4 {
		t.Fatalf("run: %+v %+v", out, res)
	}
	if res.Steps[2].Evidence["extractor"] != "openai-usage" || res.Steps[2].Evidence["units"] != uint64(17) {
		t.Fatalf("usage evidence: %+v", res.Steps[2].Evidence)
	}
	if res.Steps[3].Evidence["p50_ms"] == nil {
		t.Fatalf("latency evidence: %+v", res.Steps[3].Evidence)
	}
	// Routing and hygiene: every request carried the local-id header and
	// no payment headers; substitution reached the runner.
	conn.mu.Lock()
	defer conn.mu.Unlock()
	for _, r := range conn.seen {
		if r.Header.Get(LocalIDHeader) != "chat" {
			t.Fatalf("missing %s on %s", LocalIDHeader, r.URL.Path)
		}
		if r.Header.Get("Livepeer-Payment") != "" {
			t.Fatal("payment header on certification traffic")
		}
	}
}

func TestRequiredFailureSkipsRestAndReports(t *testing.T) {
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(200)
			return
		}
		http.Error(w, "boom", 500)
	})}
	offer := chatOffer(
		config.CertificationStep{Name: "ready", Type: "readiness"},
		config.CertificationStep{Name: "smoke", Type: "request", Config: map[string]any{"body": map[string]any{}}},
		config.CertificationStep{Name: "usage", Type: "usage"},
	)
	out, res, _ := runAndWait(t, conn, jobCapability(), offer)
	if out.Passed || res.State != RunFailed {
		t.Fatalf("outcome: %+v %+v", out, res)
	}
	if res.Steps[1].Status != StepFailed || res.Steps[2].Status != StepSkipped {
		t.Fatalf("steps: %+v", res.Steps)
	}
	if out.Reason == nil || out.Reason.Code != "certification_failed" || out.Reason.Field != "/smoke" {
		t.Fatalf("reason: %+v", out.Reason)
	}
}

func TestNonRequiredFailurePasses(t *testing.T) {
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"total_tokens":1}}`))
	})}
	offer := chatOffer(
		config.CertificationStep{Name: "smoke", Type: "request", Config: map[string]any{"body": map[string]any{}}},
		config.CertificationStep{Name: "latency", Type: "latency", Required: boolPtr(false),
			Config: map[string]any{"samples": 1, "p50_max_ms": 1}},
	)
	out, res, _ := runAndWait(t, conn, jobCapability(), offer)
	if !out.Passed || res.Steps[1].Status != StepFailed {
		t.Fatalf("non-required failure: %+v %+v", out, res.Steps)
	}
}

func TestSubstitutionMissingIsErrorNothingSent(t *testing.T) {
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
	offer := chatOffer(config.CertificationStep{Name: "smoke", Type: "request",
		Config: map[string]any{"body": map[string]any{"x": "{{offer.extra.nope}}"}}})
	out, res, _ := runAndWait(t, conn, jobCapability(), offer)
	if out.Passed || res.Steps[0].Status != StepError || !strings.Contains(res.Steps[0].Message, "substitution_missing") {
		t.Fatalf("substitution: %+v %+v", out, res.Steps)
	}
	if len(conn.seen) != 0 {
		t.Fatal("a request was sent despite the template bug")
	}
}

func TestUndeclaredTransportIsError(t *testing.T) {
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
	offer := chatOffer(config.CertificationStep{Name: "smoke", Type: "request",
		Config: map[string]any{"transport": "multipart", "parts": []any{map[string]any{"name": "f", "value": "v"}}}})
	out, res, _ := runAndWait(t, conn, jobCapability(), offer)
	if out.Passed || res.Steps[0].Status != StepError || !strings.Contains(res.Steps[0].Message, "transport_not_declared") {
		t.Fatalf("transport: %+v %+v", out, res.Steps)
	}
}

func TestMultipartFixtureInlineReachesRunner(t *testing.T) {
	var gotFile []byte
	var gotName, gotCT string
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer f.Close()
		buf := make([]byte, 16)
		n, _ := f.Read(buf)
		gotFile, gotName, gotCT = buf[:n], hdr.Filename, hdr.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"ok","usage":{"total_tokens":3}}`))
	})}
	cap := jobCapability()
	cap.Transports = []string{"multipart"}
	offer := chatOffer(config.CertificationStep{Name: "smoke", Type: "request", Config: map[string]any{
		"transport": "multipart",
		"parts": []any{
			map[string]any{"name": "model", "value": "{{identity.openai.model}}"},
			map[string]any{"name": "file", "filename": "probe.wav", "content_type": "audio/wav",
				"fixture": map[string]any{"inline_base64": "cHJvYmU=", "content_type": "audio/wav"}},
		},
		"assert": []any{"$.text"},
	}})
	out, _, _ := runAndWait(t, conn, cap, offer)
	if !out.Passed {
		t.Fatalf("multipart run failed: %+v", out)
	}
	if string(gotFile) != "probe" || gotName != "probe.wav" || gotCT != "audio/wav" {
		t.Fatalf("file part: %q %q %q", gotFile, gotName, gotCT)
	}
}

func TestSessionRequestOpensChecksDescriptorTerminates(t *testing.T) {
	var terminated bool
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sessions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			params, _ := body["session_params"].(map[string]any)
			if params["room_name"] == nil {
				http.Error(w, "no room", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"runner_session_id":"rs-1","runtime":{"schema":"sfu-room/v1","public":{"join_url":"https://x/join"}}}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sessions/"):
			terminated = true
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})}
	cap := &runnerattach.Capability{
		CapabilityID: "livepeer:meet/sfu-room", Protocol: "paid-session/v1", LocalID: "sfu",
		DescriptorSchemas: []string{"sfu-room/v1"}, Metering: "runner-reported",
		WorkUnit:  runnerattach.WorkUnit{Name: "participant_seconds"},
		Paths:     map[string]string{"create": "/sessions", "status": "/sessions/{id}", "terminate": "/sessions/{id}"},
		Readiness: runnerattach.Readiness{Type: "http-status", Path: "/ready"},
		Identity:  map[string]string{"provider": "livekit"},
	}
	offer := config.Offer{OfferingID: "meet", Capability: cap.CapabilityID, Protocol: "paid-session/v1",
		Certification: []config.CertificationStep{{Name: "open", Type: "request", Config: map[string]any{
			"session_params":           map[string]any{"room_name": "cert-{{run.id}}"},
			"expect_descriptor_schema": "sfu-room/v1",
			"assert":                   []any{"$.join_url"},
		}}}}
	out, res, _ := runAndWait(t, conn, cap, offer)
	if !out.Passed {
		t.Fatalf("session run: %+v %+v", out, res.Steps)
	}
	if !terminated {
		t.Fatal("session was not terminated")
	}
	if res.Steps[0].Evidence["descriptor_schema"] != "sfu-room/v1" || res.Steps[0].Evidence["terminated"] != true {
		t.Fatalf("evidence: %+v", res.Steps[0].Evidence)
	}
}

func TestOperatorRunAbortsInFlight(t *testing.T) {
	release := make(chan struct{})
	conn := &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(200)
	})}
	cap := jobCapability()
	offer := chatOffer(config.CertificationStep{Name: "smoke", Type: "request",
		TimeoutMS: 60000, Config: map[string]any{"body": map[string]any{}}})
	reg := testRegistryWith(t, conn, cap)
	e := New(reg, Options{Extractors: extractorRegistry()})
	t.Cleanup(e.Close)
	t.Cleanup(func() { close(release) })
	var mu sync.Mutex
	var outs []offers.CertOutcome
	e.Report = func(_ offers.PairKey, out offers.CertOutcome) { mu.Lock(); outs = append(outs, out); mu.Unlock() }
	key := offers.PairKey{HostID: "h1", LocalID: "chat", OfferingID: "shared"}
	first := e.Start(key, "match", offer, cap)
	second := e.Start(key, "operator", offer, cap)
	if first == second {
		t.Fatal("same run id")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rs := e.PairResults("h1", "shared")
		if len(rs) == 2 && rs[1].State == RunAborted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("first run not aborted: %+v", e.PairResults("h1", "shared"))
}

func boolPtr(b bool) *bool { return &b }
