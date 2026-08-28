package certification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// sessionCapability is a paid-session runner whose usage arrives on a
// callback rather than in a response body.
func sessionCapability() *runnerattach.Capability {
	return &runnerattach.Capability{
		CapabilityID: "livepeer:meet/sfu-room", Protocol: "paid-session/v1", LocalID: "sfu",
		DescriptorSchemas: []string{"sfu-room/v1"}, Metering: "runner-reported",
		WorkUnit:  runnerattach.WorkUnit{Name: "participant_seconds"},
		Paths:     map[string]string{"create": "/sessions", "status": "/sessions/{id}", "terminate": "/sessions/{id}"},
		Readiness: runnerattach.Readiness{Type: "http-status", Path: "/ready"},
	}
}

// sessionOffer opens a session, holds it, then checks usage.
func sessionOffer() config.Offer {
	return config.Offer{OfferingID: "meet", Capability: "livepeer:meet/sfu-room", Protocol: "paid-session/v1",
		Certification: []config.CertificationStep{
			{Name: "open", Type: "request", Config: map[string]any{
				"expect_descriptor_schema": "sfu-room/v1", "hold_ms": 50,
			}},
			// An explicit short window keeps the failure-path tests
			// fast. §3.3's real default is 10s and is asserted on its
			// own, in TestSessionUsageDefaultWindowIsTenSeconds.
			{Name: "usage", Type: "usage", Config: map[string]any{"min_units": 1, "window_ms": 400}},
		}}
}

// reportingRunner answers create/terminate and posts usage to whatever
// callback it was handed — which is exactly what it does in production,
// where the URL is a paid session's.
func reportingRunner(t *testing.T, report func(callbackURL, token string)) runners.Conn {
	t.Helper()
	return &handlerConn{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sessions":
			var body struct {
				CallbackURL   string `json:"callback_url"`
				CallbackToken string `json:"callback_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if report != nil {
				go report(body.CallbackURL, body.CallbackToken)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"runner_session_id":"rs-1","runtime":{"schema":"sfu-room/v1","public":{}}}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sessions/"):
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})}
}

// runSessionCert runs a session recipe against a broker whose callback
// surface is a real HTTP server, so the runner reports over the wire.
func runSessionCert(t *testing.T, offer config.Offer, report func(base string) func(string, string)) (offers.CertOutcome, Result) {
	t.Helper()
	var e *Engine
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tapID := strings.TrimPrefix(r.URL.Path, TapPathPrefix)
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var body struct {
			Sequence uint64 `json:"sequence"`
			Usage    *struct {
				Unit  string `json:"unit"`
				Total uint64 `json:"total"`
			} `json:"usage"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ev := UsageEvent{Sequence: body.Sequence, EventType: "usage", At: time.Now().UTC()}
		if body.Usage != nil {
			ev.Unit, ev.Total = body.Usage.Unit, body.Usage.Total
		}
		if !e.RecordUsageEvent(tapID, token, ev) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cap := sessionCapability()
	var fn func(string, string)
	if report != nil {
		fn = report(srv.URL)
	}
	reg := testRegistryWith(t, reportingRunner(t, fn), cap)
	e = New(reg, Options{Extractors: extractorRegistry(), CallbackBaseURL: srv.URL})
	t.Cleanup(e.Close)
	ch := make(chan offers.CertOutcome, 1)
	e.Report = func(_ offers.PairKey, out offers.CertOutcome) { ch <- out }
	sn, _ := reg.Get("h1")
	if first := e.Certify(sn, cap, offer); !first.Pending {
		t.Fatalf("expected pending, got %+v", first)
	}
	select {
	case out := <-ch:
		res := e.PairResults("h1", offer.OfferingID)
		if len(res) == 0 {
			t.Fatal("no result recorded")
		}
		return out, res[0]
	case <-time.After(45 * time.Second):
		t.Fatal("run did not report")
		return offers.CertOutcome{}, Result{}
	}
}

// postUsage is a runner reporting usage the way paid-session/v1 §7.2
// says to: a cumulative total, in the unit it declared.
func postUsage(url, token, unit string, total uint64, seq uint64) error {
	body, _ := json.Marshal(map[string]any{
		"event_id": fmt.Sprintf("ev-%d", seq), "sequence": seq, "event_type": "usage",
		"usage": map[string]any{"unit": unit, "total": total},
	})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("callback returned %d", resp.StatusCode)
	}
	return nil
}

// The point of the whole tap: a session runner that reports usage
// certifies, and the evidence says how much it reported.
func TestSessionUsageCertifiesFromTheRunnersCallback(t *testing.T) {
	out, res := runSessionCert(t, sessionOffer(), func(string) func(string, string) {
		return func(url, token string) {
			_ = postUsage(url, token, "participant_seconds", 12, 1)
			_ = postUsage(url, token, "participant_seconds", 30, 2)
		}
	})
	if !out.Passed {
		t.Fatalf("session run failed: %+v", res.Steps)
	}
	usage := res.Steps[1]
	if usage.Status != StepPassed {
		t.Fatalf("usage step = %s: %s", usage.Status, usage.Message)
	}
	// The highest cumulative total, not the sum and not the first.
	if usage.Evidence["units"] != uint64(30) {
		t.Fatalf("units = %v, want the highest cumulative total 30", usage.Evidence["units"])
	}
	// certification-steps §3.3 names the evidence keys.
	if usage.Evidence["work_unit"] != "participant_seconds" {
		t.Fatalf("work_unit = %v", usage.Evidence["work_unit"])
	}
	if usage.Evidence["event_at"] == nil {
		t.Fatalf("evidence carries no event_at: %+v", usage.Evidence)
	}
}

// The failure this was built to make possible. Before the tap a session
// runner that could not be billed still certified, because the step
// recorded "not implemented" rather than a verdict.
func TestSessionThatReportsNoUsageFailsCertification(t *testing.T) {
	out, res := runSessionCert(t, sessionOffer(), nil)
	if out.Passed {
		t.Fatal("a session runner that never reported usage was certified; the offer it " +
			"unlocks cannot be billed")
	}
	usage := res.Steps[1]
	if usage.Status != StepFailed {
		t.Fatalf("usage step = %s: %s", usage.Status, usage.Message)
	}
	if !strings.Contains(usage.Message, "no usage") {
		t.Fatalf("message does not say what was missing: %q", usage.Message)
	}
}

// Reporting in a unit the offer is not priced in is unbillable too.
func TestSessionReportingTheWrongUnitFails(t *testing.T) {
	_, res := runSessionCert(t, sessionOffer(), func(string) func(string, string) {
		return func(url, token string) { _ = postUsage(url, token, "tokens", 500, 1) }
	})
	usage := res.Steps[1]
	if usage.Status != StepFailed {
		t.Fatalf("usage step = %s: %s", usage.Status, usage.Message)
	}
	if !strings.Contains(usage.Message, "participant_seconds") {
		t.Fatalf("message does not name the declared unit: %q", usage.Message)
	}
}

// Below min_units is a shortfall, distinct from silence.
func TestSessionBelowMinUnitsFails(t *testing.T) {
	offer := sessionOffer()
	offer.Certification[1].Config["min_units"] = 100
	_, res := runSessionCert(t, offer, func(string) func(string, string) {
		return func(url, token string) { _ = postUsage(url, token, "participant_seconds", 3, 1) }
	})
	usage := res.Steps[1]
	if usage.Status != StepFailed || !strings.Contains(usage.Message, "below min_units") {
		t.Fatalf("usage step = %s: %s", usage.Status, usage.Message)
	}
}

// A forged token must not be able to certify someone else's runner.
func TestUsageTapRejectsAWrongToken(t *testing.T) {
	_, res := runSessionCert(t, sessionOffer(), func(string) func(string, string) {
		return func(url, _ string) { _ = postUsage(url, "certcb_forged", "participant_seconds", 99, 1) }
	})
	usage := res.Steps[1]
	if usage.Status != StepFailed {
		t.Fatalf("a forged callback token was accepted as usage evidence: %s", usage.Status)
	}
}

// With no external_base_url the runner has no address to report to.
// That is the operator's gap, and the message has to say so rather than
// blaming the runner for silence it could not break.
func TestSessionUsageWithoutABaseURLNamesTheOperatorsGap(t *testing.T) {
	cap := sessionCapability()
	reg := testRegistryWith(t, reportingRunner(t, nil), cap)
	e := New(reg, Options{Extractors: extractorRegistry()})
	t.Cleanup(e.Close)
	ch := make(chan offers.CertOutcome, 1)
	e.Report = func(_ offers.PairKey, out offers.CertOutcome) { ch <- out }
	sn, _ := reg.Get("h1")
	e.Certify(sn, cap, sessionOffer())
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not report")
	}
	res := e.PairResults("h1", "meet")
	usage := res[0].Steps[1]
	if usage.Status != StepError {
		t.Fatalf("usage step = %s, want error: %s", usage.Status, usage.Message)
	}
	if !strings.Contains(usage.Message, "external_base_url") {
		t.Fatalf("message does not name the missing config: %q", usage.Message)
	}
}

// A tap must not outlive the run that opened it, or a broker that
// certifies often accumulates live callbacks nobody will ever read.
//
// The recipe here has no usage step at all, which is the case that
// leaks if only the usage step closes taps.
func TestTapIsClosedWhenARunEndsWithoutReadingIt(t *testing.T) {
	offer := sessionOffer()
	offer.Certification = offer.Certification[:1] // open only, no usage step
	cap := sessionCapability()
	reg := testRegistryWith(t, reportingRunner(t, nil), cap)
	e := New(reg, Options{Extractors: extractorRegistry(), CallbackBaseURL: "https://broker.example"})
	t.Cleanup(e.Close)
	ch := make(chan offers.CertOutcome, 1)
	e.Report = func(_ offers.PairKey, out offers.CertOutcome) { ch <- out }
	sn, _ := reg.Get("h1")
	e.Certify(sn, cap, offer)
	select {
	case out := <-ch:
		if !out.Passed {
			t.Fatalf("setup: open-only run should have passed: %+v", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not report")
	}
	e.taps.mu.Lock()
	open := len(e.taps.taps)
	e.taps.mu.Unlock()
	if open != 0 {
		t.Fatalf("%d tap(s) still open after the run finished", open)
	}
}

// An abandoned tap is reaped rather than held forever.
func TestSweepDropsAbandonedTaps(t *testing.T) {
	taps := newUsageTaps()
	start := time.Now().UTC()
	if _, _, err := taps.open("run-1", "sess-1", start); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	if n := taps.sweep(start.Add(time.Minute), maxTapAge); n != 0 {
		t.Fatalf("swept %d taps that are still young", n)
	}
	if n := taps.sweep(start.Add(maxTapAge+time.Minute), maxTapAge); n != 1 {
		t.Fatalf("swept %d abandoned taps, want 1", n)
	}
}

// Usage totals are cumulative, so a late or duplicated event must not
// lower what the run observed.
func TestTapKeepsTheHighestTotal(t *testing.T) {
	taps := newUsageTaps()
	id, token, err := taps.open("run-1", "sess-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	for _, total := range []uint64{10, 40, 25, 40} {
		if !taps.record(id, token, UsageEvent{Unit: "seconds", Total: total}) {
			t.Fatalf("record(%d) rejected", total)
		}
	}
	obs := taps.close(id)
	if obs.highest != 40 {
		t.Fatalf("highest = %d, want 40", obs.highest)
	}
	if obs.unit != "seconds" {
		t.Fatalf("unit = %q", obs.unit)
	}
	if obs.count != 4 {
		t.Fatalf("events = %d, want 4", obs.count)
	}
	// A closed tap accepts nothing more: the run has already decided.
	if taps.record(id, token, UsageEvent{Unit: "seconds", Total: 99}) {
		t.Fatal("a closed tap still accepted usage")
	}
}

// One run's token must not open another run's tap.
func TestTapRejectsAnotherTapsToken(t *testing.T) {
	taps := newUsageTaps()
	now := time.Now().UTC()
	idA, _, _ := taps.open("run-a", "sess-a", now)
	_, tokenB, _ := taps.open("run-b", "sess-b", now)
	if taps.record(idA, tokenB, UsageEvent{Total: 5}) {
		t.Fatal("run B's token was accepted on run A's tap")
	}
}

// window_ms is what makes a correct-but-slow runner certifiable. A
// runner that posts its final usage shortly after terminate — which is
// where paid-session/v1 §7.2 puts it — must still pass.
func TestSessionUsageWaitsOutTheWindowForALateReport(t *testing.T) {
	offer := sessionOffer()
	offer.Certification[0].Config["hold_ms"] = 0
	offer.Certification[1].Config["window_ms"] = 5000
	out, res := runSessionCert(t, offer, func(string) func(string, string) {
		return func(url, token string) {
			time.Sleep(300 * time.Millisecond)
			_ = postUsage(url, token, "participant_seconds", 7, 1)
		}
	})
	if !out.Passed {
		t.Fatalf("a runner reporting 300ms late failed a 5s window: %+v", res.Steps)
	}
	if res.Steps[1].Evidence["units"] != uint64(7) {
		t.Fatalf("units = %v", res.Steps[1].Evidence["units"])
	}
}

// The window is a bound, not a sleep: evidence that is already in
// decides immediately.
func TestSessionUsageDoesNotWaitWhenEvidenceHasArrived(t *testing.T) {
	offer := sessionOffer()
	offer.Certification[1].Config["window_ms"] = 30000
	start := time.Now()
	out, _ := runSessionCert(t, offer, func(string) func(string, string) {
		return func(url, token string) { _ = postUsage(url, token, "participant_seconds", 9, 1) }
	})
	if !out.Passed {
		t.Fatal("run should have passed")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the step waited %s despite evidence arriving early", elapsed)
	}
}

// A window that expires still judges what did arrive, rather than
// throwing it away and reporting silence.
func TestSessionUsageJudgesPartialEvidenceWhenTheWindowExpires(t *testing.T) {
	offer := sessionOffer()
	offer.Certification[0].Config["hold_ms"] = 0
	offer.Certification[1].Config["min_units"] = 100
	offer.Certification[1].Config["window_ms"] = 300
	_, res := runSessionCert(t, offer, func(string) func(string, string) {
		return func(url, token string) { _ = postUsage(url, token, "participant_seconds", 4, 1) }
	})
	usage := res.Steps[1]
	if usage.Status != StepFailed {
		t.Fatalf("usage = %s: %s", usage.Status, usage.Message)
	}
	// The shortfall, not "reported nothing" — the runner did report.
	if !strings.Contains(usage.Message, "below min_units") {
		t.Fatalf("message = %q, want the shortfall named", usage.Message)
	}
}

// The spec's default window, honoured when a recipe does not set one.
// A runner that reports nothing does burn the whole window — that is
// the cost of not failing a correct runner that reports late.
func TestSessionUsageDefaultWindowIsTenSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the default 10s usage window")
	}
	if defaultUsageWindowMS != 10000 {
		t.Fatalf("defaultUsageWindowMS = %d, certification-steps §3.3 says 10000", defaultUsageWindowMS)
	}
	offer := sessionOffer()
	offer.Certification[0].Config["hold_ms"] = 0
	delete(offer.Certification[1].Config, "window_ms")
	start := time.Now()
	out, _ := runSessionCert(t, offer, nil)
	if out.Passed {
		t.Fatal("a silent runner certified")
	}
	if elapsed := time.Since(start); elapsed < 9*time.Second {
		t.Fatalf("the step gave up after %s; an unset window_ms should wait the §3.3 default", elapsed)
	}
}
