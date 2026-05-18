package httpreqresp

import (
	"bytes"
	"context"
	"errors"
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
	err  error
}

func (s stubForwarder) Forward(context.Context, backend.ForwardRequest) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
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

func TestServeReportsSuccessOutcome(t *testing.T) {
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
		Backend:          stubForwarder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"ok":true}`))}},
		PoolReporter:     outcomeSink{ch: reported},
		MemberEthAddress: "0xabc",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	select {
	case got := <-reported:
		if got.Outcome != poolreport.OutcomeSuccess {
			t.Fatalf("Outcome = %q, want success", got.Outcome)
		}
		if got.BackendID != "backend-a" || got.MemberEthAddress != "0xabc" {
			t.Fatalf("reported outcome = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outcome report")
	}
}

func TestServeReportsBackendFailureOnForwardError(t *testing.T) {
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
		Backend:          stubForwarder{err: errors.New("boom")},
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
