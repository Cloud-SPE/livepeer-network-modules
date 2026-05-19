package httpmultipart

import (
	"bytes"
	"context"
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
	resp *http.Response
}

func (s stubForwarder) Forward(context.Context, backend.ForwardRequest) (*http.Response, error) {
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

// TestServePreservesUpstreamTrailerDeclarations confirms the multipart
// driver does not clobber any Trailer header declared by upstream
// middleware. The payment middleware declares Trailer:
// X-Livepeer-Settlement before this driver runs; that declaration must
// survive so the settlement trailer is emitted after the body.
func TestServePreservesUpstreamTrailerDeclarations(t *testing.T) {
	d := New()
	w := httptest.NewRecorder()
	const upstreamTrailer = "X-Livepeer-Settlement"
	w.Header().Add("Trailer", upstreamTrailer)
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", bytes.NewBufferString(`body`))
	err := d.Serve(context.Background(), modes.Params{
		Writer:  w,
		Request: req,
		Capability: &config.Capability{
			ID:         "openai:audio-transcription",
			OfferingID: "shared",
			Backend:    config.Backend{ID: "backend-a", URL: "http://backend-a"},
		},
		Extractor: stubExtractor{units: 3},
		Backend: stubForwarder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
		}},
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	declared := w.Header().Values("Trailer")
	found := false
	for _, v := range declared {
		if v == upstreamTrailer {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("upstream-declared trailer %q lost; got Trailer=%v", upstreamTrailer, declared)
	}
}

func TestServeReportsCallerFailureForFourHundred(t *testing.T) {
	d := New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", bytes.NewBufferString(`body`))
	reported := make(chan poolreport.BackendOutcome, 1)
	err := d.Serve(context.Background(), modes.Params{
		Writer:  w,
		Request: req,
		Capability: &config.Capability{
			ID:         "openai:audio-transcriptions",
			OfferingID: "shared",
			Backend:    config.Backend{ID: "backend-a", URL: "http://backend-a"},
		},
		Extractor:        stubExtractor{units: 12},
		Backend:          stubForwarder{resp: &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`bad request`))}},
		PoolReporter:     outcomeSink{ch: reported},
		MemberEthAddress: "0xabc",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	select {
	case got := <-reported:
		if got.Outcome != poolreport.OutcomeCallerFailure {
			t.Fatalf("Outcome = %q, want caller_failure", got.Outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outcome report")
	}
}
