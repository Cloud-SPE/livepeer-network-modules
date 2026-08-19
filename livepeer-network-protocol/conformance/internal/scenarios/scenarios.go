// Package scenarios holds the executable conformance fixtures for the
// v1 protocols. Each scenario pins a normative clause; the harness and
// fakes provide the transport and counterparties.
package scenarios

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
)

// All returns the full suite.
func All() []harness.Scenario {
	return append(append(jobScenarios(), sessionScenarios()...), descriptorScenarios()...)
}

// ---------------------------------------------------------------------------
// paid-job §7

func jobScenarios() []harness.Scenario {
	return []harness.Scenario{
		{Name: "paid-job/unary-exchange", Spec: "paid-job §7", Run: func(c *harness.Ctx) error {
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("unary"),
				Payment: harness.PaymentEnvelope("unary"), Body: []byte(`{"prompt":"hi"}`),
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("status %d: %s", r.Status, r.Body)
			}
			if got := r.Header.Get(harness.HdrWorkUnits); got != "42" {
				return fmt.Errorf("Work-Units %q, want 42", got)
			}
			if got := r.Header.Get(harness.HdrWorkUnitName); got != c.JobUnit {
				return fmt.Errorf("Work-Unit %q, want %q", got, c.JobUnit)
			}
			if r.Header.Get(harness.HdrJobID) == "" {
				return fmt.Errorf("missing Livepeer-Job-Id")
			}
			return nil
		}},
		{Name: "paid-job/stream-trailer-claim", Spec: "paid-job §3.2/§7", Run: func(c *harness.Ctx) error {
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("stream"),
				Payment: harness.PaymentEnvelope("stream"), Body: []byte(`{"prompt":"hi"}`),
				Accept: "text/event-stream",
			})
			if err != nil {
				return err
			}
			if r.Status != 200 || !bytes.Contains(r.Body, []byte("[DONE]")) {
				return fmt.Errorf("stream status %d body %q", r.Status, r.Body)
			}
			announced := false
			for _, k := range r.TrailerAnnounced {
				if k == harness.HdrWorkUnits {
					announced = true
				}
			}
			if !announced {
				return fmt.Errorf("Trailer not advertised for %s (announced: %v)", harness.HdrWorkUnits, r.TrailerAnnounced)
			}
			if got := r.Trailer.Get(harness.HdrWorkUnits); got != "21" {
				return fmt.Errorf("trailer Work-Units %q, want 21", got)
			}
			return nil
		}},
		{Name: "paid-job/multipart-exchange", Spec: "paid-job §2/§7", Run: func(c *harness.Ctx) error {
			body := "--conf\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n\r\naudio\r\n--conf--\r\n"
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("multipart"),
				Payment: harness.PaymentEnvelope("multipart"), Body: []byte(body),
				ContentType: "multipart/form-data; boundary=conf",
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("status %d: %s", r.Status, r.Body)
			}
			if got := r.Header.Get(harness.HdrWorkUnits); got != "7" {
				return fmt.Errorf("Work-Units %q, want 7", got)
			}
			return nil
		}},
		{Name: "paid-job/undeclared-transport-refused", Spec: "paid-job §2", Run: func(c *harness.Ctx) error {
			backendBefore := c.Backend.Hits()
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingUnary, RequestID: c.RequestID("badtransport"),
				Payment: harness.PaymentEnvelope("badtransport"), Body: []byte(`{}`),
				Accept: "text/event-stream",
			})
			if err != nil {
				return err
			}
			if r.Status != 400 || r.Header.Get(harness.HdrError) != harness.ErrTransportUnsupported {
				return fmt.Errorf("status %d error %q, want 400 %s", r.Status, r.Header.Get(harness.HdrError), harness.ErrTransportUnsupported)
			}
			if c.Backend.Hits() != backendBefore {
				return fmt.Errorf("refusal reached the backend")
			}
			return nil
		}},
		{Name: "paid-job/error-claims-zero", Spec: "paid-job §3.2/§5", Run: func(c *harness.Ctx) error {
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingError, RequestID: c.RequestID("backend-error"),
				Payment: harness.PaymentEnvelope("backend-error"), Body: []byte(`{}`),
			})
			if err != nil {
				return err
			}
			if r.Status/100 == 2 {
				return fmt.Errorf("expected error status, got %d", r.Status)
			}
			if got := r.Header.Get(harness.HdrWorkUnits); got != "0" {
				return fmt.Errorf("error response Work-Units %q, want 0", got)
			}
			return nil
		}},
		{Name: "paid-job/idempotent-replay", Spec: "paid-job §4", Run: func(c *harness.Ctx) error {
			reqID := c.RequestID("replay")
			payment := harness.PaymentEnvelope("replay")
			body := []byte(`{"prompt":"replay"}`)
			first, err := c.DoJob(harness.JobRequest{Offering: c.JobOfferingAll, RequestID: reqID, Payment: payment, Body: body})
			if err != nil {
				return err
			}
			if first.Status != 200 {
				return fmt.Errorf("first status %d", first.Status)
			}
			hitsBefore := c.Backend.Hits()
			replay, err := c.DoJob(harness.JobRequest{Offering: c.JobOfferingAll, RequestID: reqID, Payment: payment, Body: body})
			if err != nil {
				return err
			}
			if replay.Status != first.Status {
				return fmt.Errorf("replay status %d != original %d", replay.Status, first.Status)
			}
			if replay.Header.Get(harness.HdrWorkUnits) != first.Header.Get(harness.HdrWorkUnits) {
				return fmt.Errorf("replay Work-Units %q != original %q",
					replay.Header.Get(harness.HdrWorkUnits), first.Header.Get(harness.HdrWorkUnits))
			}
			if replay.Header.Get(harness.HdrJobID) != first.Header.Get(harness.HdrJobID) {
				return fmt.Errorf("replay changed job id")
			}
			if c.Backend.Hits() != hitsBefore {
				return fmt.Errorf("replay re-executed the backend")
			}
			return nil
		}},
		{Name: "paid-job/request-id-reuse-rejected", Spec: "paid-job §4", Run: func(c *harness.Ctx) error {
			reqID := c.RequestID("reuse")
			if _, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: reqID,
				Payment: harness.PaymentEnvelope("reuse-a"), Body: []byte(`{"a":1}`),
			}); err != nil {
				return err
			}
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: reqID,
				Payment: harness.PaymentEnvelope("reuse-DIFFERENT"), Body: []byte(`{"totally":"different-content"}`),
			})
			if err != nil {
				return err
			}
			if r.Status != 400 || r.Header.Get(harness.HdrError) != harness.ErrRequestIDReuse {
				return fmt.Errorf("status %d error %q, want 400 %s", r.Status, r.Header.Get(harness.HdrError), harness.ErrRequestIDReuse)
			}
			return nil
		}},
	}
}

// ---------------------------------------------------------------------------
// paid-session §10

// openHappySession opens a session and returns (openResult, create record).
func openHappySession(c *harness.Ctx, tag string) (*harness.HTTPResult, fakes.CreateSeen, error) {
	r, err := c.OpenSession(c.RequestID(tag), harness.PaymentEnvelope(tag),
		`{"gateway_session_id":"gws-`+tag+`","session_params":{"room_hint":"conf"}}`)
	if err != nil {
		return nil, fakes.CreateSeen{}, err
	}
	if r.Status != 201 && r.Status != 200 {
		return nil, fakes.CreateSeen{}, fmt.Errorf("open status %d: %s", r.Status, r.Body)
	}
	cb, ok := c.Runner.LastCreate()
	if !ok {
		return nil, fakes.CreateSeen{}, fmt.Errorf("broker never called the runner")
	}
	return r, cb, nil
}

func sessionScenarios() []harness.Scenario {
	return []harness.Scenario{
		{Name: "paid-session/happy-path", Spec: "paid-session §10", Run: func(c *harness.Ctx) error {
			open, cb, err := openHappySession(c, "happy")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID := harness.FieldString(m, "session_id")
			credential := harness.FieldString(m, "credential")
			if sessionID == "" || credential == "" || harness.FieldString(m, "work_id") == "" {
				return fmt.Errorf("open response incomplete: %s", open.Body)
			}
			leaseBefore, err := harness.ParseLease(m)
			if err != nil {
				return err
			}
			// usage claim
			status, body, err := c.Runner.PostEvent(cb,
				`{"event_id":"evt_h1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":"`+c.SessionUnit+`","total":9}}`)
			if err != nil || status != 200 {
				return fmt.Errorf("usage event status %d body %s err %v", status, body, err)
			}
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil || st.Status != 200 {
				return fmt.Errorf("status %d err %v", st.Status, err)
			}
			if n, ok := harness.FieldNumber(st.JSON(), "usage.claimed_total"); !ok || n != 9 {
				return fmt.Errorf("claimed_total %v, want 9", harness.Field(st.JSON(), "usage.claimed_total"))
			}
			// top-up extends the lease
			tu, err := c.SessionTopUp(sessionID, credential, c.RequestID("happy-topup"), harness.PaymentEnvelope("happy-topup"))
			if err != nil || tu.Status != 200 {
				return fmt.Errorf("topup status %d err %v: %s", tu.Status, err, tu.Body)
			}
			leaseAfter, err := harness.ParseLease(tu.JSON())
			if err != nil {
				return err
			}
			if leaseAfter.Before(leaseBefore) {
				return fmt.Errorf("top-up shortened the lease: %v -> %v", leaseBefore, leaseAfter)
			}
			// end idempotently, runner terminated
			end1, err := c.SessionEnd(sessionID, credential, "gateway_close")
			if err != nil || end1.Status != 200 {
				return fmt.Errorf("end status %d err %v", end1.Status, err)
			}
			end2, err := c.SessionEnd(sessionID, credential, "second-reason")
			if err != nil || end2.Status != 200 {
				return fmt.Errorf("repeat end status %d err %v", end2.Status, err)
			}
			if harness.FieldString(end2.JSON(), "close_reason") != harness.FieldString(end1.JSON(), "close_reason") {
				return fmt.Errorf("repeat end changed close_reason")
			}
			if len(c.Runner.Terminated()) == 0 {
				return fmt.Errorf("runner session never terminated")
			}
			return nil
		}},
		{Name: "paid-session/open-status-public-identical", Spec: "paid-session §3.2, descriptor §2.2", Run: func(c *harness.Ctx) error {
			open, _, err := openHappySession(c, "pubid")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID := harness.FieldString(m, "session_id")
			credential := harness.FieldString(m, "credential")
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil || st.Status != 200 {
				return fmt.Errorf("status %d err %v", st.Status, err)
			}
			openPub := fmt.Sprintf("%v", harness.Field(m, "runtime.public"))
			statusPub := fmt.Sprintf("%v", harness.Field(st.JSON(), "runtime.public"))
			if openPub != statusPub || openPub == "<nil>" {
				return fmt.Errorf("public views differ:\n open: %s\n stat: %s", openPub, statusPub)
			}
			if harness.Field(st.JSON(), "runtime.grants") != nil {
				return fmt.Errorf("status returned grants")
			}
			return nil
		}},
		{Name: "paid-session/duplicate-and-reordered-events-safe", Spec: "paid-session §7.2/§10", Run: func(c *harness.Ctx) error {
			open, cb, err := openHappySession(c, "dup")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			ev := func(id string, seq int, total int) (int, []byte, error) {
				return c.Runner.PostEvent(cb, fmt.Sprintf(
					`{"event_id":%q,"sequence":%d,"event_type":"session.usage.tick","usage":{"unit":%q,"total":%d}}`,
					id, seq, c.SessionUnit, total))
			}
			if s, b, err := ev("evt_1", 1, 5); err != nil || s != 200 {
				return fmt.Errorf("evt1: %d %s %v", s, b, err)
			}
			if s, b, err := ev("evt_2", 2, 11); err != nil || s != 200 {
				return fmt.Errorf("evt2: %d %s %v", s, b, err)
			}
			// duplicate delivery of evt_2 — safe, no double count
			if s, _, err := ev("evt_2", 2, 11); err != nil || s != 200 {
				return fmt.Errorf("dup evt2 status %d err %v", s, err)
			}
			// reordered stale event (seq 1 again) — safe no-op
			if s, _, err := ev("evt_1", 1, 5); err != nil || s != 200 {
				return fmt.Errorf("reordered evt1 status %d err %v", s, err)
			}
			st, _ := c.SessionStatus(sessionID, credential)
			if n, _ := harness.FieldNumber(st.JSON(), "usage.claimed_total"); n != 11 {
				return fmt.Errorf("claimed_total %v after duplicates, want 11", n)
			}
			return nil
		}},
		{Name: "paid-session/unit-mismatch-advances-nothing", Spec: "paid-session §7.2", Run: func(c *harness.Ctx) error {
			open, cb, err := openHappySession(c, "unit")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			s, _, err := c.Runner.PostEvent(cb,
				`{"event_id":"evt_u1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":"bananas","total":50}}`)
			if err != nil {
				return err
			}
			if s/100 == 2 {
				return fmt.Errorf("unit mismatch accepted (status %d)", s)
			}
			// correct event with the same sequence still lands in full
			s2, b2, err := c.Runner.PostEvent(cb, fmt.Sprintf(
				`{"event_id":"evt_u1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":50}}`, c.SessionUnit))
			if err != nil || s2 != 200 {
				return fmt.Errorf("correct retry: %d %s %v", s2, b2, err)
			}
			st, _ := c.SessionStatus(sessionID, credential)
			if n, _ := harness.FieldNumber(st.JSON(), "usage.claimed_total"); n != 50 {
				return fmt.Errorf("claimed_total %v, want 50", n)
			}
			return nil
		}},
		{Name: "paid-session/empty-event-id-rejected", Spec: "paid-session §7.2", Run: func(c *harness.Ctx) error {
			_, cb, err := openHappySession(c, "emptyid")
			if err != nil {
				return err
			}
			s, _, err := c.Runner.PostEvent(cb,
				`{"event_id":"","sequence":1,"event_type":"session.usage.tick","usage":{"unit":"`+c.SessionUnit+`","total":5}}`)
			if err != nil {
				return err
			}
			if s/100 == 2 {
				return fmt.Errorf("empty event_id accepted (status %d)", s)
			}
			return nil
		}},
		{Name: "paid-session/open-idempotent-no-sibling", Spec: "paid-session §3.1", Run: func(c *harness.Ctx) error {
			reqID := c.RequestID("openreplay")
			payment := harness.PaymentEnvelope("openreplay")
			body := `{"gateway_session_id":"gws-or","session_params":{}}`
			first, err := c.OpenSession(reqID, payment, body)
			if err != nil || (first.Status != 201 && first.Status != 200) {
				return fmt.Errorf("first open %d err %v", first.Status, err)
			}
			second, err := c.OpenSession(reqID, payment, body)
			if err != nil || (second.Status != 201 && second.Status != 200) {
				return fmt.Errorf("replay open %d err %v", second.Status, err)
			}
			a := harness.FieldString(first.JSON(), "session_id")
			b := harness.FieldString(second.JSON(), "session_id")
			if a == "" || a != b {
				return fmt.Errorf("replay minted a sibling: %q vs %q", a, b)
			}
			if harness.FieldString(second.JSON(), "credential") != "" {
				return fmt.Errorf("replay re-delivered the credential")
			}
			return nil
		}},
		{Name: "paid-session/uniform-401s", Spec: "paid-session §4/§7.2", Run: func(c *harness.Ctx) error {
			open, cb, err := openHappySession(c, "auth")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			// control surface: bad credential vs unknown session
			bad, err := c.SessionStatus(sessionID, "sc_wrong")
			if err != nil {
				return err
			}
			unknown, err := c.SessionStatus("sess_does-not-exist", credential)
			if err != nil {
				return err
			}
			if bad.Status != 401 || unknown.Status != 401 || !bytes.Equal(bad.Body, unknown.Body) {
				return fmt.Errorf("control 401s distinguishable: %d %q vs %d %q", bad.Status, bad.Body, unknown.Status, unknown.Body)
			}
			// events surface: bad token vs unknown session
			resBad, err := c.PostEventRaw(cb.CallbackURL, "cb_wrong",
				`{"event_id":"evt_x","sequence":9,"event_type":"session.heartbeat"}`)
			if err != nil {
				return err
			}
			unknownURL := strings.Replace(cb.CallbackURL, harness.FieldString(m, "session_id"), "sess_does-not-exist", 1)
			resUnknown, err := c.PostEventRaw(unknownURL, cb.CallbackToken,
				`{"event_id":"evt_x","sequence":9,"event_type":"session.heartbeat"}`)
			if err != nil {
				return err
			}
			if resBad.Status != 401 || resUnknown.Status != 401 || !bytes.Equal(resBad.Body, resUnknown.Body) {
				return fmt.Errorf("events 401s distinguishable: %d %q vs %d %q", resBad.Status, resBad.Body, resUnknown.Status, resUnknown.Body)
			}
			return nil
		}},
		{Name: "paid-session/restart-rebind", Spec: "paid-session §9.2", Run: func(c *harness.Ctx) error {
			if c.RestartBroker == nil {
				return fmt.Errorf("%w: suite does not own the broker process (URL mode); run in auto mode or demonstrate restart with your own harness", harness.ErrSkip)
			}
			open, cb, err := openHappySession(c, "restart")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID := harness.FieldString(m, "session_id")
			credential := harness.FieldString(m, "credential")
			workID := harness.FieldString(m, "work_id")

			// Claim usage before the restart so we can prove the
			// watermark and debit progress survived.
			if st, _, err := c.Runner.PostEvent(cb, fmt.Sprintf(
				`{"event_id":"evt_r1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":12}}`,
				c.SessionUnit)); err != nil || st != 200 {
				return fmt.Errorf("pre-restart usage event: %d %v", st, err)
			}

			if err := c.RestartBroker(); err != nil {
				return fmt.Errorf("restart: %w", err)
			}

			// §9.2 requires ONE OF two outcomes, and forbids a specific
			// set of bad ones. Both branches are conformant; which one a
			// broker takes depends on whether its payment layer also
			// survived (an in-process mock daemon, for example, does not).
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil {
				return err
			}
			if st.Status != 200 {
				return fmt.Errorf("status after restart: %d %s (session unreachable — neither rebound nor terminal)", st.Status, st.Body)
			}
			sm := st.JSON()
			// Forbidden in BOTH branches: a second work id.
			if got := harness.FieldString(sm, "work_id"); got != workID {
				return fmt.Errorf("work_id changed across restart: %q -> %q", workID, got)
			}
			// Forbidden in BOTH branches: silently skipped usage.
			if n, _ := harness.FieldNumber(sm, "usage.claimed_total"); n != 12 {
				return fmt.Errorf("claimed_total %v after restart, want 12 (usage lost)", n)
			}

			state := harness.FieldString(sm, "state")
			if state == "ended" || state == "failed" {
				// Branch 2: explicit terminal. It must be labelled, and
				// the runner must not be left serving.
				reason := harness.FieldString(sm, "close_reason")
				if reason == "" {
					return fmt.Errorf("terminal after restart with no close_reason")
				}
				if len(c.Runner.Terminated()) == 0 {
					return fmt.Errorf("terminal after restart (%s) but runner left serving", reason)
				}
				return nil
			}

			// Branch 1: rebound. Usage continues from the surviving
			// watermark — the pre-restart event is still a duplicate,
			// and the next sequence is accepted.
			if s2, _, err := c.Runner.PostEvent(cb, fmt.Sprintf(
				`{"event_id":"evt_r1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":12}}`,
				c.SessionUnit)); err != nil || s2 != 200 {
				return fmt.Errorf("post-restart duplicate: %d %v", s2, err)
			}
			if s3, b3, err := c.Runner.PostEvent(cb, fmt.Sprintf(
				`{"event_id":"evt_r2","sequence":2,"event_type":"session.usage.tick","usage":{"unit":%q,"total":20}}`,
				c.SessionUnit)); err != nil || s3 != 200 {
				return fmt.Errorf("post-restart usage event: %d %s %v", s3, b3, err)
			}
			st2, _ := c.SessionStatus(sessionID, credential)
			if n, _ := harness.FieldNumber(st2.JSON(), "usage.claimed_total"); n != 20 {
				return fmt.Errorf("claimed_total %v after post-restart event, want 20", n)
			}
			tu, err := c.SessionTopUp(sessionID, credential, c.RequestID("restart-topup"), harness.PaymentEnvelope("restart-topup"))
			if err != nil || tu.Status != 200 {
				return fmt.Errorf("topup after restart: %d %v", tu.Status, err)
			}
			end, err := c.SessionEnd(sessionID, credential, "gateway_close")
			if err != nil || end.Status != 200 {
				return fmt.Errorf("end after restart: %d %v", end.Status, err)
			}
			return nil
		}},
		{Name: "paid-session/heartbeat-enforcement", Spec: "paid-session §5", Run: func(c *harness.Ctx) error {
			if c.SessionOfferingFastHB == "" {
				return fmt.Errorf("%w: no fast-heartbeat offering configured (see README)", harness.ErrSkip)
			}
			r, err := c.OpenSessionOffering(c.SessionOfferingFastHB, c.RequestID("hb"),
				harness.PaymentEnvelope("hb"), `{"gateway_session_id":"gws-hb","session_params":{}}`)
			if err != nil {
				return err
			}
			if r.Status != 201 && r.Status != 200 {
				return fmt.Errorf("open status %d: %s", r.Status, r.Body)
			}
			m := r.JSON()
			sessionID := harness.FieldString(m, "session_id")
			credential := harness.FieldString(m, "credential")

			// Send nothing. interval 1s x threshold 2 means the sweeper
			// must tear this down; poll until it does.
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(500 * time.Millisecond)
				st, err := c.SessionStatus(sessionID, credential)
				if err != nil || st.Status != 200 {
					continue
				}
				sm := st.JSON()
				state := harness.FieldString(sm, "state")
				if state == "ended" || state == "failed" {
					reason := harness.FieldString(sm, "close_reason")
					if reason != "heartbeat_lost" {
						return fmt.Errorf("terminal reason %q, want heartbeat_lost", reason)
					}
					// Enforcement means the runner was actually torn
					// down, not just a record flipped.
					if len(c.Runner.Terminated()) == 0 {
						return fmt.Errorf("session marked %s but runner never terminated", state)
					}
					// And the winddown is idempotent from the outside.
					if end, err := c.SessionEnd(sessionID, credential, "late"); err == nil && end.Status == 200 {
						if harness.FieldString(end.JSON(), "close_reason") != "heartbeat_lost" {
							return fmt.Errorf("post-terminal end changed the close reason")
						}
					}
					return nil
				}
			}
			return fmt.Errorf("session never wound down after missed heartbeats (still active after 20s)")
		}},
	}
}

// ---------------------------------------------------------------------------
// runtime-descriptor §6

func descriptorScenarios() []harness.Scenario {
	return []harness.Scenario{
		{Name: "descriptor/no-private-or-grant-leak", Spec: "runtime-descriptor §4/§6", Run: func(c *harness.Ctx) error {
			open, _, err := openHappySession(c, "leak")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil {
				return err
			}
			for _, probe := range [][2]string{{"open", string(open.Body)}, {"status", string(st.Body)}} {
				if strings.Contains(probe[1], fakes.PrivateSentinel) {
					return fmt.Errorf("private sentinel leaked in %s response", probe[0])
				}
			}
			// The grant secret is LEGITIMATE in the open response (delivered
			// once) and forbidden everywhere else.
			if !strings.Contains(string(open.Body), fakes.GrantSecretSentinel) {
				return fmt.Errorf("grant secret missing from open (must be delivered exactly once)")
			}
			if strings.Contains(string(st.Body), fakes.GrantSecretSentinel) {
				return fmt.Errorf("grant secret leaked in status response")
			}
			return nil
		}},
		{Name: "descriptor/unknown-top-level-key-fails-closed", Spec: "runtime-descriptor §2/§3", Run: func(c *harness.Ctx) error {
			return expectOpenRejected(c, "unknown_key")
		}},
		{Name: "descriptor/oversize-fails-closed", Spec: "runtime-descriptor §3", Run: func(c *harness.Ctx) error {
			return expectOpenRejected(c, "oversize")
		}},
		{Name: "descriptor/schema-mismatch-fails-closed", Spec: "runtime-descriptor §2.1/§3", Run: func(c *harness.Ctx) error {
			return expectOpenRejected(c, "schema_mismatch")
		}},
	}
}

// expectOpenRejected opens with a misbehaving-descriptor mode and
// requires the open to fail closed: non-2xx AND the partially-created
// runner session terminated.
func expectOpenRejected(c *harness.Ctx, mode string) error {
	terminatedBefore := len(c.Runner.Terminated())
	r, err := c.OpenSession(c.RequestID(mode), harness.PaymentEnvelope(mode),
		fmt.Sprintf(`{"gateway_session_id":"gws-%s","session_params":{"conformance_mode":%q}}`, mode, mode))
	if err != nil {
		return err
	}
	if r.Status/100 == 2 {
		return fmt.Errorf("open succeeded despite %s descriptor", mode)
	}
	if len(c.Runner.Terminated()) <= terminatedBefore {
		return fmt.Errorf("runner session not terminated on fail-closed open")
	}
	return nil
}
