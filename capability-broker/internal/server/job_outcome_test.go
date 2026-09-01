package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
)

// capturingReporter is the pool controller's end, as the broker sees it.
type capturingReporter struct {
	got chan poolreport.BackendOutcome
}

func (c *capturingReporter) ReportBackendOutcome(_ context.Context, o poolreport.BackendOutcome) error {
	c.got <- o
	return nil
}

// next returns the next report, or fails if none arrives.
func (c *capturingReporter) next(t *testing.T) poolreport.BackendOutcome {
	t.Helper()
	select {
	case o := <-c.got:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("no backend outcome was reported")
		return poolreport.BackendOutcome{}
	}
}

// none asserts nothing is reported within a window long enough for a
// goroutine to have run.
func (c *capturingReporter) none(t *testing.T) {
	t.Helper()
	select {
	case o := <-c.got:
		t.Fatalf("an outcome was reported that must not have been: %+v", o)
	case <-time.After(300 * time.Millisecond):
	}
}

// attachOutcomeRunner attaches a runner whose response status the test
// controls, enrolled to a member so there is someone to attribute to.
func attachOutcomeRunner(t *testing.T, s *Server, ts *httptest.Server, member string, status *atomic.Int32) {
	t.Helper()
	body := `{"host_id":"h1"`
	if member != "" {
		body += `,"member_eth_address":"` + member + `"`
	}
	body += `}`
	code, enr, _ := adminReq(t, s, http.MethodPost, "/admin/v1/enroll", body, nil)
	if code != http.StatusCreated {
		t.Fatalf("enroll %s: %d %v", body, code, enr)
	}
	token := enr["credential"].(map[string]any)["token"].(string)
	c := dialAttach(t, ts)
	results := runnerSideFull(t, c,
		func(_, _ string, _ map[string][]string, _ []byte) (int, http.Header, []byte) {
			st := int(status.Load())
			out := []byte(`{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
			if st >= 400 {
				out = []byte(`{"error":"nope"}`)
			}
			return st, http.Header{
				"Content-Type":   {"application/json"},
				"Content-Length": {strconv.Itoa(len(out))},
			}, out
		})
	res := registerVia(t, c, results, attachDoc(token, "h1", func(m map[string]any) {
		cap0 := m["capabilities"].([]any)[0].(map[string]any)
		cap0["identity"] = map[string]any{"openai.model": "test-model"}
	}))
	if res["document"] != "accepted" {
		t.Fatalf("attach: %v", res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.offersEngine.EligiblePairs("default")) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the runner never became eligible")
}

// The loop the pool's scoring depends on: a dispatched exchange is
// reported, classified into the controller's three buckets, with the
// runner's first-byte latency.
func TestJobOutcomeIsReportedForDispatchedExchanges(t *testing.T) {
	ts, s := newJobOfferBrokerBare(t, nil, "")
	reporter := &capturingReporter{got: make(chan poolreport.BackendOutcome, 8)}
	s.poolReporter = reporter
	var status atomic.Int32
	status.Store(http.StatusOK)
	const member = "0x1111111111111111111111111111111111111111"
	attachOutcomeRunner(t, s, ts, member, &status)

	resp := jobReq(t, ts, "outcome-ok", "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: job status %d", resp.StatusCode)
	}
	got := reporter.next(t)
	if got.Outcome != poolreport.OutcomeSuccess {
		t.Fatalf("outcome = %q, want success", got.Outcome)
	}
	// host|local is the broker's own backend id; the controller keys its
	// selection state on it and the ladder derives it the same way.
	if got.BackendID != "h1|chat" || got.CapabilityID != "openai:chat-completions" || got.OfferingID != "default" {
		t.Fatalf("identity = %s / %s / %s", got.BackendID, got.CapabilityID, got.OfferingID)
	}
	if got.MemberEthAddress != member {
		t.Fatalf("member = %q, want the enrolment's", got.MemberEthAddress)
	}
	// Time to first byte, not zero: the runner answered, so the pool is
	// told how quickly.
	if got.LatencyMetricMS < 0 {
		t.Fatalf("latency = %d", got.LatencyMetricMS)
	}
	if got.OccurredAt.IsZero() {
		t.Fatal("occurred_at is zero")
	}

	// A runner that answers 5xx is a backend failure — the controller
	// counts it against the member and ignores its latency.
	status.Store(http.StatusBadGateway)
	resp = jobReq(t, ts, "outcome-5xx", "")
	_ = resp.Body.Close()
	if got := reporter.next(t); got.Outcome != poolreport.OutcomeBackendFailure {
		t.Fatalf("outcome = %q, want backend_failure", got.Outcome)
	}

	// A runner that answers 4xx says the CALLER was wrong. Reported as
	// caller_failure, which the controller excludes from the success
	// ratio — a bad gateway must not degrade an innocent member — while
	// still measuring how fast the runner said no.
	status.Store(http.StatusUnprocessableEntity)
	resp = jobReq(t, ts, "outcome-4xx", "")
	_ = resp.Body.Close()
	if got := reporter.next(t); got.Outcome != poolreport.OutcomeCallerFailure {
		t.Fatalf("outcome = %q, want caller_failure", got.Outcome)
	}
}

// The broker's own refusals never reach a runner, and must not be
// attributed to one: a refusal on capability, protocol or payment is
// the broker's decision, and reporting it would let the broker's
// decisions move a member's score.
func TestJobOutcomeIsNotReportedForBrokerRefusals(t *testing.T) {
	ts, s := newJobOfferBrokerBare(t, nil, "")
	reporter := &capturingReporter{got: make(chan poolreport.BackendOutcome, 8)}
	s.poolReporter = reporter
	var status atomic.Int32
	status.Store(http.StatusOK)
	attachOutcomeRunner(t, s, ts, "0x1111111111111111111111111111111111111111", &status)

	// Unknown capability: refused before selection.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/job", nil)
	req.Header.Set(livepeerheader.Capability, "nonexistent:cap")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "refused-cap")
	req.Header.Set(livepeerheader.Payment, "c3R1Yg==")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("setup: expected 404, got %d", resp.StatusCode)
	}
	reporter.none(t)

	// Missing request id: refused by the idempotency layer, before
	// selection.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/job", nil)
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.Payment, "c3R1Yg==")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("setup: expected 400, got %d", resp.StatusCode)
	}
	reporter.none(t)
}

// A runner with no member is the orch's own hardware — a pool of one.
// There is nobody to attribute the work to and no controller that
// would accept the report, so nothing is sent.
func TestJobOutcomeIsNotReportedWithoutAMember(t *testing.T) {
	ts, s := newJobOfferBrokerBare(t, nil, "")
	reporter := &capturingReporter{got: make(chan poolreport.BackendOutcome, 8)}
	s.poolReporter = reporter
	var status atomic.Int32
	status.Store(http.StatusOK)
	attachOutcomeRunner(t, s, ts, "", &status)

	resp := jobReq(t, ts, "no-member", "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: job status %d", resp.StatusCode)
	}
	reporter.none(t)
}
