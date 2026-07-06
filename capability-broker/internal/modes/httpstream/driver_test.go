package httpstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
)

type stubForwarder struct {
	resp     *http.Response
	gotBody  []byte
	gotHdrs  http.Header
}

func (s *stubForwarder) Forward(_ context.Context, req backend.ForwardRequest) (*http.Response, error) {
	if req.Body != nil {
		s.gotBody, _ = io.ReadAll(req.Body)
	}
	s.gotHdrs = req.Headers.Clone()
	return s.resp, nil
}

type stubExtractor struct {
	units uint64
}

func (s stubExtractor) Name() string { return "stub" }

func (s stubExtractor) Extract(context.Context, *extractors.Request, *extractors.Response) (uint64, error) {
	return s.units, nil
}

type outcomeSink struct {
	ch chan poolreport.BackendOutcome
}

func (s outcomeSink) ReportBackendOutcome(_ context.Context, outcome poolreport.BackendOutcome) error {
	s.ch <- outcome
	return nil
}

// TestServePreservesUpstreamTrailerDeclarations confirms the driver
// appends its Livepeer-Work-Units trailer to whatever Trailer header
// upstream middleware already declared (e.g. the payment middleware's
// X-Livepeer-Settlement trailer). A previous version used Header().Set
// which clobbered the upstream declaration and silently dropped the
// settlement trailer.
func TestServePreservesUpstreamTrailerDeclarations(t *testing.T) {
	d := New()
	w := httptest.NewRecorder()
	// Simulate the payment middleware declaring its trailer before the
	// driver runs.
	const upstreamTrailer = "X-Livepeer-Settlement"
	w.Header().Add("Trailer", upstreamTrailer)
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", bytes.NewBufferString(`{"prompt":"hi"}`))
	err := d.Serve(context.Background(), modes.Params{
		Writer:  w,
		Request: req,
		Capability: &config.Capability{
			ID:         "openai:chat-completions",
			OfferingID: "shared",
			Backend:    config.Backend{ID: "backend-a", URL: "http://backend-a"},
		},
		Extractor: stubExtractor{units: 7},
		Backend: &stubForwarder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
		}},
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	declared := w.Header().Values("Trailer")
	hasUpstream := false
	hasWorkUnits := false
	for _, v := range declared {
		if v == upstreamTrailer {
			hasUpstream = true
		}
		if v == "Livepeer-Work-Units" {
			hasWorkUnits = true
		}
	}
	if !hasUpstream {
		t.Fatalf("upstream-declared trailer %q lost; got Trailer=%v", upstreamTrailer, declared)
	}
	if !hasWorkUnits {
		t.Fatalf("driver-declared Livepeer-Work-Units trailer missing; got Trailer=%v", declared)
	}
}

func TestServeReportsBackendFailureForFiveHundred(t *testing.T) {
	d := New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", bytes.NewBufferString(`{"prompt":"hi"}`))
	reported := make(chan poolreport.BackendOutcome, 1)
	err := d.Serve(context.Background(), modes.Params{
		Writer:  w,
		Request: req,
		Capability: &config.Capability{
			ID:         "openai:chat-completions",
			OfferingID: "shared",
			Backend:    config.Backend{ID: "backend-a", URL: "http://backend-a"},
		},
		Extractor:        stubExtractor{units: 42},
		Backend:          &stubForwarder{resp: &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`oops`))}},
		PoolReporter:     outcomeSink{ch: reported},
		MemberEthAddress: "0xabc",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	select {
	case got := <-reported:
		if got.Outcome != poolreport.OutcomeBackendFailure {
			t.Fatalf("Outcome = %q, want backend_failure", got.Outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outcome report")
	}
}

func TestServeInjectsOpenAIStreamUsage(t *testing.T) {
	d := New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", bytes.NewBufferString(`{"model":"qwen","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	fwd := &stubForwarder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(`data: {"usage":{"total_tokens":7}}`)),
	}}
	err := d.Serve(context.Background(), modes.Params{
		Writer:  w,
		Request: req,
		Capability: &config.Capability{
			ID:              "openai:chat-completions",
			OfferingID:      "shared",
			InteractionMode: Mode,
			Backend:         config.Backend{ID: "backend-a", URL: "http://backend-a"},
		},
		Extractor: openAIUsageExtractor{},
		Backend:   fwd,
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(fwd.gotBody, &payload); err != nil {
		t.Fatalf("forwarded body is not JSON: %v", err)
	}
	streamOptions, _ := payload["stream_options"].(map[string]any)
	if streamOptions == nil || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options.include_usage not injected; got %#v", payload["stream_options"])
	}
}

// TestServeInjectsOpenAIStreamUsageRegardlessOfContentType guards the fix for
// unmetered streaming chat requests: when the gateway forwards a job with a
// Content-Type other than application/json (or none at all), the broker must
// still inject stream_options.include_usage AND normalize the outbound
// Content-Type to application/json, because the backend only emits a usage
// block for application/json requests. Without both, work_units silently
// meters 0.
func TestServeInjectsOpenAIStreamUsageRegardlessOfContentType(t *testing.T) {
	for _, ct := range []string{"", "text/plain", "application/octet-stream"} {
		t.Run("ct="+ct, func(t *testing.T) {
			d := New()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/cap",
				bytes.NewBufferString(`{"model":"qwen","stream":true,"stream_options":{"include_usage":false},"messages":[{"role":"user","content":"hi"}]}`))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			} else {
				req.Header.Del("Content-Type")
			}
			fwd := &stubForwarder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`data: {"usage":{"total_tokens":7}}`)),
			}}
			err := d.Serve(context.Background(), modes.Params{
				Writer:  w,
				Request: req,
				Capability: &config.Capability{
					ID:              "openai:chat-completions",
					OfferingID:      "shared",
					InteractionMode: Mode,
					Backend:         config.Backend{ID: "backend-a", URL: "http://backend-a"},
				},
				Extractor: openAIUsageExtractor{},
				Backend:   fwd,
			})
			if err != nil {
				t.Fatalf("Serve() error = %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(fwd.gotBody, &payload); err != nil {
				t.Fatalf("forwarded body is not JSON: %v", err)
			}
			streamOptions, _ := payload["stream_options"].(map[string]any)
			if streamOptions == nil || streamOptions["include_usage"] != true {
				t.Fatalf("include_usage not forced to true; got %#v", payload["stream_options"])
			}
			if got := fwd.gotHdrs.Get("Content-Type"); got != "application/json" {
				t.Fatalf("outbound Content-Type = %q, want application/json", got)
			}
		})
	}
}

type openAIUsageExtractor struct{}

func (openAIUsageExtractor) Name() string { return "openai-usage" }

func (openAIUsageExtractor) Extract(context.Context, *extractors.Request, *extractors.Response) (uint64, error) {
	return 7, nil
}
