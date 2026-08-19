// Package harness holds the shared scenario context, wire constants, and
// HTTP helpers the conformance scenarios drive a broker-under-test with.
//
// The suite is deliberately self-contained: it does not import the
// reference broker. Everything here is derived from the normative specs
// (livepeer-network-protocol/protocols/*.md and headers/livepeer-headers.md).
package harness

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
)

// Livepeer wire headers (headers/livepeer-headers.md).
const (
	HdrCapability = "Livepeer-Capability"
	HdrOffering   = "Livepeer-Offering"
	HdrPayment    = "Livepeer-Payment"
	HdrProtocol   = "Livepeer-Protocol"
	HdrRequestID  = "Livepeer-Request-Id"

	HdrWorkUnits    = "Livepeer-Work-Units"
	HdrWorkUnitName = "Livepeer-Work-Unit"
	HdrJobID        = "Livepeer-Job-Id"
	HdrError        = "Livepeer-Error"
)

// Error codes the suite asserts on (headers/livepeer-headers.md).
const (
	ErrTransportUnsupported = "protocol_transport_unsupported"
	ErrJobInFlight          = "job_in_flight"
	ErrRequestIDReuse       = "request_id_reuse"
	ErrRefillRefused        = "refill_refused"
)

// Protocol tags.
const (
	ProtoPaidJob     = "paid-job/v1"
	ProtoPaidSession = "paid-session/v1"
)

// ErrSkip marks a scenario as skipped rather than failed. Wrap it with
// the reason: fmt.Errorf("%w: needs broker clock control", ErrSkip).
var ErrSkip = errors.New("SKIP")

// Ctx is the shared state handed to every scenario.
type Ctx struct {
	BrokerURL string
	HTTP      *http.Client

	Backend *fakes.JobBackend
	Runner  *fakes.SessionRunner

	// Offering coordinates the broker-under-test must serve (see README).
	JobCapability     string // paid-job capability id
	JobOfferingAll    string // offering declaring unary+stream+multipart
	JobOfferingUnary  string // offering declaring only unary
	JobOfferingError  string // offering whose backend always fails
	SessionCapability string // paid-session capability id
	SessionOffering   string
	// SessionOfferingFastHB is an offering with a deliberately short
	// heartbeat interval so liveness enforcement is observable in
	// seconds. Empty means the scenario skips.
	SessionOfferingFastHB string

	// RestartBroker restarts the broker-under-test in place, keeping
	// its durable state. Non-nil only when the suite owns the process
	// (auto mode); nil means restart scenarios skip.
	RestartBroker func() error

	// RestartBrokerLosingPayment restarts the broker with its own
	// session store intact but the payment layer's state discarded —
	// the "runner still has it, payment layer does not" case that
	// §9.2's terminal branch exists for. Nil when unavailable.
	RestartBrokerLosingPayment func() error
	JobUnit                    string // declared work unit for the job offerings
	SessionUnit                string // declared work unit for the session offering

	// RunID makes request ids unique across runs against a long-lived
	// broker (idempotency records outlive the suite).
	RunID string
}

// NewRunID returns a short random run nonce.
func NewRunID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RequestID mints a run-scoped idempotency key.
func (c *Ctx) RequestID(tag string) string {
	return "conf-" + c.RunID + "-" + tag
}

// PaymentEnvelope base64-encodes a stub payment envelope. The mock
// payment daemon accepts any non-empty envelope; a real deployment
// would substitute genuine payment material here.
func PaymentEnvelope(seed string) string {
	return base64.StdEncoding.EncodeToString([]byte("conformance-payment:" + seed))
}

// ---------------------------------------------------------------------------
// paid-job helpers

// JobRequest describes one POST /v1/job exchange.
type JobRequest struct {
	Offering    string
	RequestID   string
	Payment     string // already base64
	Body        []byte
	Accept      string
	ContentType string
}

// JobResponse is the fully-read exchange result. Trailer values are
// populated after the body has been consumed; TrailerAnnounced records
// the trailer keys the server advertised before the body.
type JobResponse struct {
	Status           int
	Header           http.Header
	Trailer          http.Header
	TrailerAnnounced []string
	Body             []byte
}

// DoJob runs one paid-job exchange and reads the body to EOF so
// trailers are observable.
func (c *Ctx) DoJob(jr JobRequest) (*JobResponse, error) {
	req, err := http.NewRequest(http.MethodPost, c.BrokerURL+"/v1/job", bytes.NewReader(jr.Body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(HdrCapability, c.JobCapability)
	req.Header.Set(HdrOffering, jr.Offering)
	req.Header.Set(HdrProtocol, ProtoPaidJob)
	req.Header.Set(HdrRequestID, jr.RequestID)
	req.Header.Set(HdrPayment, jr.Payment)
	if jr.Accept != "" {
		req.Header.Set("Accept", jr.Accept)
	}
	if jr.ContentType != "" {
		req.Header.Set("Content-Type", jr.ContentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Keys present in resp.Trailer before the body is read are the
	// server-advertised (Trailer header) names.
	announced := make([]string, 0, len(resp.Trailer))
	for k := range resp.Trailer {
		announced = append(announced, k)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &JobResponse{
		Status:           resp.StatusCode,
		Header:           resp.Header,
		Trailer:          resp.Trailer,
		TrailerAnnounced: announced,
		Body:             body,
	}, nil
}

// ---------------------------------------------------------------------------
// paid-session helpers

// HTTPResult is a fully-read response.
type HTTPResult struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into a generic map (nil on non-JSON).
func (r *HTTPResult) JSON() map[string]any {
	var m map[string]any
	if json.Unmarshal(r.Body, &m) != nil {
		return nil
	}
	return m
}

func (c *Ctx) do(req *http.Request) (*HTTPResult, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &HTTPResult{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// OpenSession opens a paid session (POST /v1/session).
func (c *Ctx) OpenSession(requestID, payment, body string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodPost, c.BrokerURL+"/v1/session", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(HdrCapability, c.SessionCapability)
	req.Header.Set(HdrOffering, c.SessionOffering)
	req.Header.Set(HdrProtocol, ProtoPaidSession)
	req.Header.Set(HdrRequestID, requestID)
	req.Header.Set(HdrPayment, payment)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// OpenSessionOffering opens against a named offering of the session
// capability (used by the fast-heartbeat scenario).
func (c *Ctx) OpenSessionOffering(offering, requestID, payment, body string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodPost, c.BrokerURL+"/v1/session", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(HdrCapability, c.SessionCapability)
	req.Header.Set(HdrOffering, offering)
	req.Header.Set(HdrProtocol, ProtoPaidSession)
	req.Header.Set(HdrRequestID, requestID)
	req.Header.Set(HdrPayment, payment)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// SessionStatus fetches GET /v1/session/{id} with the session credential.
func (c *Ctx) SessionStatus(sessionID, credential string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodGet, c.BrokerURL+"/v1/session/"+sessionID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	return c.do(req)
}

// SessionTopUp posts a top-up envelope.
func (c *Ctx) SessionTopUp(sessionID, credential, requestID, payment string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodPost, c.BrokerURL+"/v1/session/"+sessionID+"/topup", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set(HdrRequestID, requestID)
	req.Header.Set(HdrPayment, payment)
	return c.do(req)
}

// SessionEnd requests session end.
func (c *Ctx) SessionEnd(sessionID, credential, reason string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodPost, c.BrokerURL+"/v1/session/"+sessionID+"/end",
		strings.NewReader(fmt.Sprintf(`{"reason":%q}`, reason)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// PostEventRaw posts an event envelope to an arbitrary callback URL
// with an arbitrary token (for the uniform-401 probes). Well-behaved
// runner traffic goes through fakes.SessionRunner.PostEvent instead.
func (c *Ctx) PostEventRaw(callbackURL, token, body string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodPost, callbackURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// Get fetches an unauthenticated broker URL (registry surfaces, healthz).
func (c *Ctx) Get(path string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodGet, c.BrokerURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// ---------------------------------------------------------------------------
// small assertion conveniences

// FieldString digs a dotted path out of a decoded JSON map.
func FieldString(m map[string]any, path string) string {
	v := Field(m, path)
	s, _ := v.(string)
	return s
}

// FieldNumber digs a dotted path returning the float64 JSON number.
func FieldNumber(m map[string]any, path string) (float64, bool) {
	v := Field(m, path)
	n, ok := v.(float64)
	return n, ok
}

// Field digs a dotted path out of nested JSON maps.
func Field(m map[string]any, path string) any {
	cur := any(m)
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return cur
}

// ParseLease parses an RFC3339 lease.expires_at value.
func ParseLease(m map[string]any) (time.Time, error) {
	s := FieldString(m, "lease.expires_at")
	if s == "" {
		return time.Time{}, fmt.Errorf("lease.expires_at missing")
	}
	return time.Parse(time.RFC3339, s)
}
