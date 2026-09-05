// Package scenarios holds the executable conformance fixtures for the
// v1 protocols. Each scenario pins a normative clause; the harness and
// fakes provide the transport and counterparties.
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
)

// All returns the full suite.
func All() []harness.Scenario {
	return append(append(append(jobScenarios(), sessionScenarios()...), descriptorScenarios()...), attachScenarios()...)
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
		{Name: "paid-job/in-flight-retry-refused", Spec: "paid-job §4", Run: func(c *harness.Ctx) error {
			reqID := c.RequestID("inflight")
			payment := harness.PaymentEnvelope("inflight")
			body := []byte(`{"prompt":"slow"}`)
			type res struct {
				r   *harness.JobResponse
				err error
			}
			done := make(chan res, 1)
			go func() {
				r, err := c.DoJob(harness.JobRequest{
					Offering: c.JobOfferingSlow, RequestID: reqID, Payment: payment, Body: body,
				})
				done <- res{r, err}
			}()
			// Let the first exchange reach the backend, then retry it.
			time.Sleep(700 * time.Millisecond)
			retry, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingSlow, RequestID: reqID, Payment: payment, Body: body,
			})
			if err != nil {
				return err
			}
			if retry.Status != 409 || retry.Header.Get(harness.HdrError) != harness.ErrJobInFlight {
				return fmt.Errorf("in-flight retry: status %d error %q, want 409 %s",
					retry.Status, retry.Header.Get(harness.HdrError), harness.ErrJobInFlight)
			}
			first := <-done
			if first.err != nil {
				return fmt.Errorf("original exchange: %w", first.err)
			}
			if first.r.Status != 200 {
				return fmt.Errorf("original exchange status %d", first.r.Status)
			}
			// Once it is terminal the same id replays rather than refusing.
			replay, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingSlow, RequestID: reqID, Payment: payment, Body: body,
			})
			if err != nil {
				return err
			}
			if replay.Status != first.r.Status {
				return fmt.Errorf("post-completion replay status %d, want %d", replay.Status, first.r.Status)
			}
			return nil
		}},
		{Name: "paid-job/fractional-pricing-exchange", Spec: "offering-axes §6", Run: func(c *harness.Ctx) error {
			// An offering priced per many units, not per one. Every
			// other fixture here is per_units 1 — the single denominator
			// at which flooring and ceiling agree — so this is the case
			// where a rounding defect can actually surface.
			if c.JobOfferingFractional == "" {
				return fmt.Errorf("%w: no fractional-priced offering configured (see README)", harness.ErrSkip)
			}
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingFractional, RequestID: c.RequestID("fractional"),
				Payment: harness.PaymentEnvelope("fractional"),
				Body:    []byte(`{"model":"m","messages":[]}`),
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("status %d: %s", r.Status, r.Body)
			}
			// Work units are a count, so the denominator must not touch
			// them — it prices them. A broker that folded per_units into
			// the claim would show up right here.
			units := r.Header.Get(harness.HdrWorkUnits)
			if units == "" || units == "0" {
				return fmt.Errorf("work units = %q on a successful exchange", units)
			}
			if got := r.Header.Get(harness.HdrWorkUnitName); got != c.JobUnit {
				return fmt.Errorf("work unit = %q; want %q", got, c.JobUnit)
			}
			// And the claim must be queryable, like any other job.
			jobID := r.Header.Get(harness.HdrJobID)
			if jobID == "" {
				return fmt.Errorf("no %s", harness.HdrJobID)
			}
			q, err := c.QuerySettlement(jobID)
			if err != nil {
				return err
			}
			if q.Status != 200 {
				return fmt.Errorf("settlement query status %d: %s", q.Status, q.Body)
			}
			if got := q.Header.Get(harness.HdrWorkUnits); got != units {
				return fmt.Errorf("queried units %q != exchange units %q", got, units)
			}
			return nil
		}},
		{Name: "paid-job/settlement-signature-verifies", Spec: "livepeer-headers §Livepeer-Settlement", Run: func(c *harness.Ctx) error {
			// The assertion a clearinghouse gates money on. Every other
			// rule in this suite can hold while the signature is absent
			// or forged, and the record would still be worthless as
			// evidence — so an unsigned suite grades everything except
			// the part that decides whether the money moves.
			if c.SettlementSigner == "" {
				return fmt.Errorf("%w: run is unsigned (no delegated settlement key configured)",
					harness.ErrSkip)
			}
			env, err := harness.SignedPaymentEnvelope(c.JobCapability, c.JobOfferingAll,
				c.JobUnit, 1, 1, 42, "signed")
			if err != nil {
				return err
			}
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("signed"),
				Payment: env,
				Body:    []byte(`{"model":"m","messages":[]}`),
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("status %d: %s", r.Status, r.Body)
			}
			jobID := r.Header.Get(harness.HdrJobID)
			q, err := c.QuerySettlement(jobID)
			if err != nil {
				return err
			}
			if q.Status != 200 {
				return fmt.Errorf("settlement query status %d: %s", q.Status, q.Body)
			}
			var body struct {
				Settlement string `json:"settlement"`
			}
			if err := json.Unmarshal(q.Body, &body); err != nil {
				return fmt.Errorf("decode settlement query: %w", err)
			}
			if body.Settlement == "" {
				return fmt.Errorf("settlement query returned no envelope")
			}
			signer, err := harness.RecoverSettlementSigner(body.Settlement)
			if err != nil {
				return err
			}
			if !strings.EqualFold(signer, c.SettlementSigner) {
				return fmt.Errorf("settlement recovered to %s; the delegated key is %s — a "+
					"record that does not recover to a delegated key is not evidence",
					signer, c.SettlementSigner)
			}
			return nil
		}},

		{Name: "paid-job/tampered-settlement-fails-verification", Spec: "livepeer-headers §Livepeer-Settlement", Run: func(c *harness.Ctx) error {
			// The signature has to actually bind the payload. A verifier
			// that accepts an altered record is worse than none: it
			// converts "signed" from a guarantee into decoration.
			if c.SettlementSigner == "" {
				return fmt.Errorf("%w: run is unsigned", harness.ErrSkip)
			}
			env, err := harness.SignedPaymentEnvelope(c.JobCapability, c.JobOfferingAll,
				c.JobUnit, 1, 1, 42, "tamper")
			if err != nil {
				return err
			}
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("tamper"),
				Payment: env,
				Body:    []byte(`{"model":"m","messages":[]}`),
			})
			if err != nil {
				return err
			}
			jobID := r.Header.Get(harness.HdrJobID)
			q, err := c.QuerySettlement(jobID)
			if err != nil {
				return err
			}
			var body struct {
				Settlement string `json:"settlement"`
			}
			if err := json.Unmarshal(q.Body, &body); err != nil {
				return err
			}
			tampered, err := harness.TamperSettlementUnits(body.Settlement)
			if err != nil {
				return err
			}
			signer, err := harness.RecoverSettlementSigner(tampered)
			if err != nil {
				return nil // recovery refused outright: also correct
			}
			if strings.EqualFold(signer, c.SettlementSigner) {
				return fmt.Errorf("a settlement with altered units still recovered to the "+
					"delegated key %s — the signature does not bind the payload", signer)
			}
			return nil
		}},

		{Name: "paid-job/exchange-lookup-by-request-id", Spec: "paid-job §5.3.0", Run: func(c *harness.Ctx) error {
			// A clearinghouse holds only the id it issued. Every other
			// lookup is keyed on something the customer holds, so a
			// customer that withheld the settlement could force a
			// conservative full charge the broker had evidence against.
			env, err := harness.SignedPaymentEnvelope(c.JobCapability, c.JobOfferingAll,
				c.JobUnit, 1, 1, 42, "lookup")
			if err != nil {
				return err
			}
			reqID := c.RequestID("lookup")
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: reqID, Payment: env,
				Body: []byte(`{"model":"m","messages":[]}`),
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("exchange status %d: %s", r.Status, r.Body)
			}
			q, err := c.GetExchange(reqID)
			if err != nil {
				return err
			}
			if q.Status != 200 {
				return fmt.Errorf("lookup by request id status %d: %s", q.Status, q.Body)
			}
			var body struct {
				Outcome    string `json:"outcome"`
				JobID      string `json:"job_id"`
				Settlement string `json:"settlement"`
			}
			if err := json.Unmarshal(q.Body, &body); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			if body.Outcome != "SETTLED" {
				return fmt.Errorf("outcome = %q; want SETTLED", body.Outcome)
			}
			// The rule that matters: SETTLED is a claim about money and
			// requires the evidence, not merely a terminal state.
			if body.Settlement == "" {
				return fmt.Errorf("SETTLED with no signed settlement — that reports an " +
					"exchange as costed when nothing supports the figure")
			}
			if body.JobID == "" {
				return fmt.Errorf("no broker job id; a consumer cannot correlate or poll")
			}
			return nil
		}},

		{Name: "paid-job/unknown-request-id-is-not-a-claim", Spec: "paid-job §5.3.0", Run: func(c *harness.Ctx) error {
			// Silence and a signed non-admission are different answers.
			// A broker that has not been asked to attest has made no
			// claim, and a consumer must not read one into a 404.
			q, err := c.GetExchange(c.RequestID("never-happened"))
			if err != nil {
				return err
			}
			if q.Status != 404 {
				return fmt.Errorf("unknown request id status %d; want 404", q.Status)
			}
			var body struct {
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(q.Body, &body); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			if body.Outcome != "NO_RECORD" {
				return fmt.Errorf("outcome = %q; want NO_RECORD — an unasked broker has made "+
					"no claim, which is not the same as NOT_ADMITTED", body.Outcome)
			}
			return nil
		}},

		{Name: "paid-job/no-undeliverable-trailer-advertised", Spec: "paid-job §3.2", Run: func(c *harness.Ctx) error {
			// A trailer rides only on a chunked response. A unary
			// exchange is Content-Length delimited, so any trailer it
			// advertises is dropped by the transport without a word —
			// and a client that waits for the advertised name waits
			// forever. Advertise it only where it can be sent.
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("trailer-unary"),
				Payment: harness.PaymentEnvelope("trailer-unary"),
				Body:    []byte(`{"model":"m","messages":[]}`),
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("unary status %d: %s", r.Status, r.Body)
			}
			// Go promotes advertised trailers off a chunked response
			// into the trailer map; what stays in the header block is an
			// advertisement on a response that cannot carry one.
			if r.Header.Get("Content-Length") == "" {
				return nil // not Content-Length delimited: the advertisement is honest
			}
			if declared := r.Header.Get("Trailer"); strings.Contains(declared, harness.HdrSettlement) {
				return fmt.Errorf("unary response advertises %s as a trailer it cannot send "+
					"(Trailer: %q); the settlement for a unary exchange is retrieved from "+
					"GET /v1/settlement/{id}", harness.HdrSettlement, declared)
			}
			// Whatever the transport, the record must still be reachable.
			jobID := r.Header.Get(harness.HdrJobID)
			if jobID == "" {
				return fmt.Errorf("no %s", harness.HdrJobID)
			}
			q, err := c.QuerySettlement(jobID)
			if err != nil {
				return err
			}
			if q.Status != 200 {
				return fmt.Errorf("settlement query status %d: %s — a unary exchange that "+
					"advertises no trailer MUST be queryable, or its settlement is "+
					"unreachable entirely", q.Status, q.Body)
			}
			return nil
		}},
		{Name: "paid-job/streamed-claim-is-queryable", Spec: "paid-job §3.2", Run: func(c *harness.Ctx) error {
			// A streamed job's terminal claim arrives in a trailer. Go
			// reads trailers; HTTPX, Fetch and reqwest do not. If the
			// trailer were the only channel, those clients would have to
			// choose between billing zero — which fails open — and
			// blocking. So the claim must also be queryable.
			r, err := c.DoJob(harness.JobRequest{
				Offering: c.JobOfferingAll, RequestID: c.RequestID("stream-query"),
				Payment: harness.PaymentEnvelope("stream-query"),
				Body:    []byte(`{"model":"m","messages":[]}`), Accept: "text/event-stream",
			})
			if err != nil {
				return err
			}
			if r.Status != 200 {
				return fmt.Errorf("stream status %d: %s", r.Status, r.Body)
			}
			jobID := r.Header.Get(harness.HdrJobID)
			if jobID == "" {
				return fmt.Errorf("no %s to query with", harness.HdrJobID)
			}

			q, err := c.QuerySettlement(jobID)
			if err != nil {
				return err
			}
			if q.Status != 200 {
				return fmt.Errorf("settlement query status %d: %s", q.Status, q.Body)
			}
			units := q.Header.Get(harness.HdrWorkUnits)
			if units == "" {
				return fmt.Errorf("query returned no %s", harness.HdrWorkUnits)
			}
			if q.Header.Get(harness.HdrWorkUnitName) == "" {
				return fmt.Errorf("query returned no %s", harness.HdrWorkUnitName)
			}
			// The queried claim must equal the trailer's, or the two
			// channels disagree about what was billed.
			if trailer := r.Trailer.Get(harness.HdrWorkUnits); trailer != "" && trailer != units {
				return fmt.Errorf("queried units %q != trailer units %q", units, trailer)
			}
			return nil
		}},
		{Name: "paid-job/severed-stream-replays-terminal", Spec: "paid-job §7", Run: func(c *harness.Ctx) error {
			reqID := c.RequestID("severed")
			payment := harness.PaymentEnvelope("severed")
			body := []byte(`{"prompt":"long"}`)
			// Hang up mid-body: read a little, then close.
			if err := c.DoJobAbort(harness.JobRequest{
				Offering: c.JobOfferingLongStream, RequestID: reqID, Payment: payment, Body: body,
				Accept: "text/event-stream",
			}, 2); err != nil {
				return fmt.Errorf("severed request: %w", err)
			}
			// The exchange must settle to a terminal outcome that a
			// retry replays — not stay in flight forever.
			deadline := time.Now().Add(30 * time.Second)
			for {
				r, err := c.DoJob(harness.JobRequest{
					Offering: c.JobOfferingLongStream, RequestID: reqID, Payment: payment, Body: body,
					Accept: "text/event-stream",
				})
				if err != nil {
					return err
				}
				if r.Header.Get(harness.HdrError) == harness.ErrJobInFlight {
					if time.Now().After(deadline) {
						return fmt.Errorf("severed exchange still in flight after 30s")
					}
					time.Sleep(500 * time.Millisecond)
					continue
				}
				if r.Header.Get(harness.HdrWorkUnits) == "" {
					return fmt.Errorf("replay after severed stream carried no work-units claim")
				}
				return nil
			}
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
		`{"gateway_session_id":"`+c.GatewaySessionID(tag)+`","session_params":{"room_hint":"conf"}}`)
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
		{Name: "paid-session/settlement-resolves-gateway-session-id", Spec: "paid-session §3.3.1", Run: func(c *harness.Ctx) error {
			// The gateway's own id is the ONLY lookup key a
			// clearinghouse issues itself. session_id is broker-local
			// and reaches it through the customer-controlled SDK — the
			// channel the settlement signature exists to distrust — and
			// a work_id can cover several sessions, so a query by it can
			// return a correctly signed record for the wrong one.
			gatewayID := c.GatewaySessionID("settle-" + c.RequestID("gwslookup"))
			r, err := c.OpenSession(c.RequestID("gwslookup"), harness.PaymentEnvelope("gwslookup"),
				`{"gateway_session_id":"`+gatewayID+`","session_params":{}}`)
			if err != nil {
				return err
			}
			if r.Status != 201 && r.Status != 200 {
				return fmt.Errorf("open status %d: %s", r.Status, r.Body)
			}
			var open struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal([]byte(r.Body), &open); err != nil {
				return fmt.Errorf("decode open: %w", err)
			}

			q, err := c.QuerySettlement(gatewayID)
			if err != nil {
				return err
			}
			if q.Status != 200 {
				return fmt.Errorf("settlement query by gateway_session_id status %d: %s "+
					"— a consumer that cannot look up by the one key it issued itself has "+
					"no way to find its own record", q.Status, q.Body)
			}
			var got struct {
				SessionID        string `json:"session_id"`
				GatewaySessionID string `json:"gateway_session_id"`
			}
			if err := json.Unmarshal([]byte(q.Body), &got); err != nil {
				return fmt.Errorf("decode settlement query: %w", err)
			}
			if got.GatewaySessionID != gatewayID {
				return fmt.Errorf("query returned gateway_session_id %q; want %q",
					got.GatewaySessionID, gatewayID)
			}
			if open.SessionID != "" && got.SessionID != open.SessionID {
				return fmt.Errorf("query resolved to session %q; want the opened %q",
					got.SessionID, open.SessionID)
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
			orID := c.GatewaySessionID("or")
			body := `{"gateway_session_id":"` + orID + `","session_params":{}}`
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
			// A replay converges on the USABLE outcome: the same
			// credential comes back. Withholding it would leave a
			// gateway whose response was lost holding a funded session
			// it can never drive, which is the failure idempotent opens
			// exist to prevent.
			if got, want := harness.FieldString(second.JSON(), "credential"),
				harness.FieldString(first.JSON(), "credential"); got != want {
				return fmt.Errorf("replay credential %q != recorded %q", got, want)
			}

			// The id is a promise about content. Reusing it for a
			// different open must be refused, not answered with the
			// first session's keys.
			third, err := c.OpenSession(reqID, harness.PaymentEnvelope("openreplay-different"),
				`{"gateway_session_id":"`+orID+`","session_params":{"changed":true}}`)
			if err != nil {
				return err
			}
			if third.Status != 400 || third.Header.Get(harness.HdrError) != harness.ErrRequestIDReuse {
				return fmt.Errorf("reused id with different content: status %d error %q; want 400 %s",
					third.Status, third.Header.Get(harness.HdrError), harness.ErrRequestIDReuse)
			}
			return nil
		}},
		{Name: "paid-session/lease-expiry-winddown", Spec: "paid-session §5/§10", Run: func(c *harness.Ctx) error {
			if c.SessionOfferingShortLease == "" {
				return fmt.Errorf("%w: no short-lease offering configured (see README)", harness.ErrSkip)
			}
			r, err := c.OpenSessionOffering(c.SessionOfferingShortLease, c.RequestID("lease"),
				harness.PaymentEnvelope("lease"), `{"gateway_session_id":"`+c.GatewaySessionID("lease")+`","session_params":{}}`)
			if err != nil {
				return err
			}
			if r.Status != 201 && r.Status != 200 {
				return fmt.Errorf("open status %d: %s", r.Status, r.Body)
			}
			m := r.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(500 * time.Millisecond)
				st, err := c.SessionStatus(sessionID, credential)
				if err != nil || st.Status != 200 {
					continue
				}
				sm := st.JSON()
				if state := harness.FieldString(sm, "state"); state == "ended" || state == "failed" {
					if reason := harness.FieldString(sm, "close_reason"); reason != "lease_expired" {
						return fmt.Errorf("terminal reason %q, want lease_expired", reason)
					}
					if len(c.Runner.Terminated()) == 0 {
						return fmt.Errorf("lease expired but runner never terminated")
					}
					return nil
				}
			}
			return fmt.Errorf("session outlived its lease with no winddown")
		}},
		{Name: "paid-session/topup-replay-is-idempotent", Spec: "paid-session §3.3", Run: func(c *harness.Ctx) error {
			open, _, err := openHappySession(c, "topup-idem")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")

			reqID := c.RequestID("topup-idem-1")
			first, err := c.SessionTopUp(sessionID, credential, reqID, harness.PaymentEnvelope("topup-idem-1"))
			if err != nil {
				return err
			}
			if first.Status != 200 && first.Status != 201 {
				return fmt.Errorf("%w: offering refused the first top-up (status %d): %s",
					harness.ErrSkip, first.Status, first.Body)
			}
			firstLease := harness.FieldString(first.JSON(), "lease.expires_at")

			// Same id, same envelope: the recorded outcome, verbatim.
			replay, err := c.SessionTopUp(sessionID, credential, reqID, harness.PaymentEnvelope("topup-idem-1"))
			if err != nil {
				return err
			}
			if replay.Status != first.Status {
				return fmt.Errorf("replay status %d != original %d: %s", replay.Status, first.Status, replay.Body)
			}
			if got := harness.FieldString(replay.JSON(), "lease.expires_at"); got != firstLease {
				return fmt.Errorf("replay lease %q != recorded %q — a retry funded the session again", got, firstLease)
			}

			// Same id, different envelope: the id is a promise about
			// content, so this is a caller bug and must not be answered
			// with the first top-up's outcome.
			reuse, err := c.SessionTopUp(sessionID, credential, reqID, harness.PaymentEnvelope("topup-idem-different"))
			if err != nil {
				return err
			}
			if reuse.Status != 400 || reuse.Header.Get(harness.HdrError) != harness.ErrRequestIDReuse {
				return fmt.Errorf("reused id with a different envelope: status %d error %q; want 400 %s",
					reuse.Status, reuse.Header.Get(harness.HdrError), harness.ErrRequestIDReuse)
			}
			return nil
		}},
		{Name: "paid-session/bounded-refill-advertised-then-refused", Spec: "paid-session §3.3/§6", Run: func(c *harness.Ctx) error {
			if c.SessionOfferingBounded == "" {
				return fmt.Errorf("%w: no bounded-refill offering configured (see README)", harness.ErrSkip)
			}
			r, err := c.OpenSessionOffering(c.SessionOfferingBounded, c.RequestID("bounded"),
				harness.PaymentEnvelope("bounded"), `{"gateway_session_id":"`+c.GatewaySessionID("b")+`","session_params":{}}`)
			if err != nil {
				return err
			}
			if r.Status != 201 && r.Status != 200 {
				return fmt.Errorf("open status %d: %s", r.Status, r.Body)
			}
			m := r.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")

			// The advertisement MUST precede the refusal.
			warned, ok := harness.Field(m, "balance.will_refuse_next_refill").(bool)
			if !ok || !warned {
				return fmt.Errorf("bounded offering did not advertise will_refuse_next_refill at open: %v",
					harness.Field(m, "balance"))
			}
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil {
				return err
			}
			if w, ok := harness.Field(st.JSON(), "balance.will_refuse_next_refill").(bool); !ok || !w {
				return fmt.Errorf("status did not advertise will_refuse_next_refill")
			}

			tu, err := c.SessionTopUp(sessionID, credential, c.RequestID("bounded-topup"), harness.PaymentEnvelope("bounded-topup"))
			if err != nil {
				return err
			}
			if tu.Status/100 == 2 {
				return fmt.Errorf("bounded offering accepted a top-up (status %d)", tu.Status)
			}
			if code := tu.Header.Get(harness.HdrError); code != harness.ErrRefillRefused {
				return fmt.Errorf("refusal error %q, want %s", code, harness.ErrRefillRefused)
			}
			// A refused top-up is not a winddown.
			st2, _ := c.SessionStatus(sessionID, credential)
			if state := harness.FieldString(st2.JSON(), "state"); state == "ended" || state == "failed" {
				return fmt.Errorf("refused top-up wound the session down (%s)", state)
			}
			return nil
		}},
		{Name: "paid-session/control-ws-push-and-ack", Spec: "paid-session §8", Run: func(c *harness.Ctx) error {
			open, cb, err := openHappySession(c, "ws")
			if err != nil {
				return err
			}
			m := open.JSON()
			wsURL := harness.WSURLFromControl(m)
			if wsURL == "" {
				// The binding is optional: an implementation that does
				// not advertise it is conformant.
				return fmt.Errorf("%w: implementation advertises no control.events_ws (the binding is optional)", harness.ErrSkip)
			}
			sessionID := harness.FieldString(m, "session_id")
			credential := harness.FieldString(m, "credential")

			// Uniform-401 discipline applies to the upgrade too.
			if _, resp, err := c.DialControlWS(wsURL, "sc_wrong"); err == nil {
				return fmt.Errorf("upgrade succeeded with a bad credential")
			} else if resp == nil || resp.StatusCode != 401 {
				code := 0
				if resp != nil {
					code = resp.StatusCode
				}
				return fmt.Errorf("bad-credential upgrade status %d, want 401", code)
			}

			conn, _, err := c.DialControlWS(wsURL, credential)
			if err != nil {
				return fmt.Errorf("upgrade with valid credential: %w", err)
			}
			defer conn.Close()

			// A runner usage claim must reach the attached gateway.
			if st, _, err := c.Runner.PostEvent(cb, fmt.Sprintf(
				`{"event_id":"evt_ws1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":6}}`,
				c.SessionUnit)); err != nil || st != 200 {
				return fmt.Errorf("usage event: %d %v", st, err)
			}
			tick, err := conn.ReadUntil("session.usage.tick", 10*time.Second)
			if err != nil {
				return fmt.Errorf("no usage push: %w", err)
			}
			if total, ok := tick.Body["claimed_total"].(float64); !ok || total != 6 {
				return fmt.Errorf("pushed tick claimed_total %v, want 6", tick.Body["claimed_total"])
			}
			if _, err := conn.ReadUntil("session.balance", 10*time.Second); err != nil {
				return fmt.Errorf("no balance push: %w", err)
			}

			// Gateway-initiated frames are acknowledged.
			if err := conn.SendTopUp(c.RequestID("ws-topup"), harness.PaymentEnvelope("ws-topup")); err != nil {
				return err
			}
			ack, err := conn.ReadUntil("ack", 10*time.Second)
			if err != nil {
				return fmt.Errorf("no topup ack: %w", err)
			}
			if op, _ := ack.Body["op"].(string); op != "session.topup" {
				return fmt.Errorf("ack op %q, want session.topup", op)
			}

			// Ending over the WS must both ack and push the terminal.
			if err := conn.Send(harness.WSFrame{Type: "session.end",
				Body: map[string]any{"reason": "gateway_close"}}); err != nil {
				return err
			}
			if _, err := conn.ReadUntil("session.ended", 10*time.Second); err != nil {
				return fmt.Errorf("no terminal push after end: %w", err)
			}
			// HTTP remains authoritative: status agrees with the push.
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil {
				return err
			}
			if state := harness.FieldString(st.JSON(), "state"); state != "ended" && state != "failed" {
				return fmt.Errorf("WS reported ended but status says %q", state)
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

			// This scenario runs against a payment layer that survives
			// the restart, so the REBIND branch is the required outcome:
			// a broker that terminates here is failing recovery, not
			// exercising the other branch (which
			// paid-session/restart-terminal covers deterministically).
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

			if state := harness.FieldString(sm, "state"); state == "ended" || state == "failed" {
				return fmt.Errorf("session went terminal (%s/%s) despite a surviving payment layer; rebind was required",
					state, harness.FieldString(sm, "close_reason"))
			}

			// Rebound. Usage continues from the surviving
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
		{Name: "paid-session/restart-terminal-when-unbillable", Spec: "paid-session §9.2", Run: func(c *harness.Ctx) error {
			if c.RestartBrokerLosingPayment == nil {
				return fmt.Errorf("%w: suite cannot discard the payment layer's state (URL mode)", harness.ErrSkip)
			}
			open, cb, err := openHappySession(c, "restartterm")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID := harness.FieldString(m, "session_id")
			credential := harness.FieldString(m, "credential")
			workID := harness.FieldString(m, "work_id")
			if st, _, err := c.Runner.PostEvent(cb, fmt.Sprintf(
				`{"event_id":"evt_t1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":%q,"total":5}}`,
				c.SessionUnit)); err != nil || st != 200 {
				return fmt.Errorf("pre-restart usage event: %d %v", st, err)
			}
			terminatedBefore := len(c.Runner.Terminated())

			if err := c.RestartBrokerLosingPayment(); err != nil {
				return fmt.Errorf("restart: %w", err)
			}

			// The session cannot be billed any more, so §9.2's terminal
			// branch is required: never serve work you cannot charge for.
			//
			// Recovery is asynchronous — a broker whose runners reconnect
			// after restart cannot reach them at the instant it comes
			// back — so this waits. The window is deliberately far
			// shorter than any heartbeat deadline: a broker that only
			// fails the session closed when the heartbeat sweep notices
			// has not taken the recovery branch, and still fails here.
			var sm map[string]any
			var state string
			deadline := time.Now().Add(5 * time.Second)
			for {
				st, err := c.SessionStatus(sessionID, credential)
				if err != nil {
					return err
				}
				if st.Status != 200 {
					return fmt.Errorf("status after restart: %d %s", st.Status, st.Body)
				}
				sm = st.JSON()
				state = harness.FieldString(sm, "state")
				if state == "ended" || state == "failed" || time.Now().After(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if state != "ended" && state != "failed" {
				return fmt.Errorf("session still %s 5s after losing its payment layer; must reach a terminal outcome", state)
			}
			if reason := harness.FieldString(sm, "close_reason"); reason == "" {
				return fmt.Errorf("terminal with no close_reason")
			}
			// Forbidden outcomes still hold on this branch.
			if got := harness.FieldString(sm, "work_id"); got != workID {
				return fmt.Errorf("work_id changed: %q -> %q", workID, got)
			}
			if len(c.Runner.Terminated()) <= terminatedBefore {
				return fmt.Errorf("terminal outcome but runner left serving")
			}
			return nil
		}},
		{Name: "paid-session/heartbeat-enforcement", Spec: "paid-session §5", Run: func(c *harness.Ctx) error {
			if c.SessionOfferingFastHB == "" {
				return fmt.Errorf("%w: no fast-heartbeat offering configured (see README)", harness.ErrSkip)
			}
			r, err := c.OpenSessionOffering(c.SessionOfferingFastHB, c.RequestID("hb"),
				harness.PaymentEnvelope("hb"), `{"gateway_session_id":"`+c.GatewaySessionID("hb")+`","session_params":{}}`)
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
		{Name: "descriptor/malformed-grant-fails-closed", Spec: "runtime-descriptor §2.4/§6", Run: func(c *harness.Ctx) error {
			return expectOpenRejected(c, "malformed_grant")
		}},
		{Name: "descriptor/grants-not-re-emitted-after-restart", Spec: "runtime-descriptor §2.4/§6", Run: func(c *harness.Ctx) error {
			if c.RestartBroker == nil {
				return fmt.Errorf("%w: suite does not own the broker process (URL mode)", harness.ErrSkip)
			}
			open, _, err := openHappySession(c, "grantrestart")
			if err != nil {
				return err
			}
			m := open.JSON()
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			if err := c.RestartBroker(); err != nil {
				return fmt.Errorf("restart: %w", err)
			}
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil {
				return err
			}
			if st.Status != 200 {
				return fmt.Errorf("status after restart: %d", st.Status)
			}
			if harness.Field(st.JSON(), "runtime.grants") != nil {
				return fmt.Errorf("status re-emitted grants after restart")
			}
			if strings.Contains(string(st.Body), fakes.GrantSecretSentinel) {
				return fmt.Errorf("grant secret surfaced after restart")
			}
			return nil
		}},
		{Name: "descriptor/oversize-fails-closed", Spec: "runtime-descriptor §3", Run: func(c *harness.Ctx) error {
			return expectOpenRejected(c, "oversize")
		}},
		{Name: "descriptor/schema-mismatch-fails-closed", Spec: "runtime-descriptor §2.1/§3", Run: func(c *harness.Ctx) error {
			return expectOpenRejected(c, "schema_mismatch")
		}},
		schemaFixture("sfu-room", "default", "sfu-room/v1",
			[]string{"url", "room", "mint_url", "status_url"}),
		schemaFixture("rtmp-hls", "rtmp-hls", "rtmp-hls/v1",
			[]string{"rtmp_url", "hls_url", "key_issue_url", "status_url"}),
		schemaFixture("scope-passthrough", "scope-passthrough", "scope-passthrough/v1",
			[]string{"scope_url", "status_url"}),
		schemaFixture("trickle-egress", "trickle-egress", "trickle-egress/v1",
			[]string{"control_url", "preview_url", "status_url"}),
	}
}

// schemaFixture asserts one shipped schema's public-by-contract field
// set: exactly the declared fields appear, and the schema's private and
// grant material never surfaces. This is the check runtime-descriptor §6
// promises — "a schema change that moves a sensitive field into public
// fails conformance rather than review" — and it only holds for schemas
// that actually have a fixture.
func schemaFixture(name, offering, schema string, publicFields []string) harness.Scenario {
	return harness.Scenario{
		Name: "descriptor/" + name + "-public-by-contract",
		Spec: "runtime-descriptor §6",
		Run: func(c *harness.Ctx) error {
			off := c.SessionOfferingFor(offering)
			if off == "" {
				return fmt.Errorf("%w: no %s offering configured (see README)", harness.ErrSkip, offering)
			}
			r, err := c.OpenSessionOffering(off, c.RequestID(name),
				harness.PaymentEnvelope(name),
				fmt.Sprintf(`{"gateway_session_id":%q,"session_params":{"conformance_mode":%q}}`, c.GatewaySessionID(name), offering))
			if err != nil {
				return err
			}
			if r.Status != 201 && r.Status != 200 {
				return fmt.Errorf("open status %d: %s", r.Status, r.Body)
			}
			m := r.JSON()
			if got := harness.FieldString(m, "runtime.schema"); got != schema {
				return fmt.Errorf("schema %q, want %q", got, schema)
			}
			pub, ok := harness.Field(m, "runtime.public").(map[string]any)
			if !ok {
				return fmt.Errorf("public part missing or not an object")
			}
			allowed := map[string]bool{}
			for _, f := range publicFields {
				allowed[f] = true
			}
			for k := range pub {
				if !allowed[k] {
					return fmt.Errorf("field %q appeared in %s public part; declared set is %v", k, schema, publicFields)
				}
			}
			// Private material and grant secrets must not surface on
			// open (the grant secret is legitimate at open) or status.
			if strings.Contains(string(r.Body), fakes.PrivateSentinel) {
				return fmt.Errorf("private sentinel leaked in open response for %s", schema)
			}
			sessionID, credential := harness.FieldString(m, "session_id"), harness.FieldString(m, "credential")
			st, err := c.SessionStatus(sessionID, credential)
			if err != nil {
				return err
			}
			for _, probe := range []string{fakes.PrivateSentinel, fakes.GrantSecretSentinel} {
				if strings.Contains(string(st.Body), probe) {
					return fmt.Errorf("sensitive material leaked in status for %s", schema)
				}
			}
			return nil
		},
	}
}

// expectOpenRejected opens with a misbehaving-descriptor mode and
// requires the open to fail closed: non-2xx AND the partially-created
// runner session terminated.
func expectOpenRejected(c *harness.Ctx, mode string) error {
	terminatedBefore := len(c.Runner.Terminated())
	r, err := c.OpenSession(c.RequestID(mode), harness.PaymentEnvelope(mode),
		fmt.Sprintf(`{"gateway_session_id":%q,"session_params":{"conformance_mode":%q}}`, c.GatewaySessionID(mode), mode))
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
