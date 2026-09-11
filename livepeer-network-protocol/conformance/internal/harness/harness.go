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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// Livepeer wire headers (headers/livepeer-headers.md).
const (
	HdrCapability    = "Livepeer-Capability"
	HdrOffering      = "Livepeer-Offering"
	HdrPayment       = "Livepeer-Payment"
	HdrAuthorization = "Livepeer-Authorization"
	HdrProtocol      = "Livepeer-Protocol"
	HdrRequestID     = "Livepeer-Request-Id"

	HdrWorkUnits    = "Livepeer-Work-Units"
	HdrWorkUnitName = "Livepeer-Work-Unit"
	HdrJobID        = "Livepeer-Job-Id"
	HdrSettlement   = "Livepeer-Settlement"
	HdrError        = "Livepeer-Error"
)

// Error codes the suite asserts on (headers/livepeer-headers.md).
const (
	ErrTransportUnsupported  = "protocol_transport_unsupported"
	ErrJobInFlight           = "job_in_flight"
	ErrRequestIDReuse        = "request_id_reuse"
	ErrRefillRefused         = "refill_refused"
	ErrAuthorizationRequired = "authorization_required"
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

	// SettlementSigner is the eth address of the delegated settlement key
	// the broker-under-test was configured with. Empty means the run is
	// unsigned, and the signature scenarios skip rather than fail — a
	// third party pointing the suite at their own broker may not have
	// wired a key yet.
	SettlementSigner string

	// Offering coordinates the broker-under-test must serve (see README).
	JobCapability    string // paid-job capability id
	JobOfferingAll   string // offering declaring unary+stream+multipart
	JobOfferingUnary string // offering declaring only unary
	JobOfferingError string // offering whose backend always fails
	// JobOfferingSlow serves a backend that takes seconds, so a
	// concurrent retry lands while the original is in flight.
	JobOfferingSlow string
	// JobOfferingLongStream serves a long SSE body a client can sever.
	JobOfferingLongStream string
	// JobOfferingFractional is priced per many units rather than per
	// one, so the paid path is exercised at a denominator where
	// flooring and ceiling disagree.
	JobOfferingFractional string
	// SessionOfferingBounded declares refill: bounded.
	SessionOfferingBounded string
	// SessionOfferingShortLease has a fixed, very short lease so expiry
	// fires well before any heartbeat threshold.
	SessionOfferingShortLease string
	// SessionOfferingsBySchema maps a descriptor-schema nickname to the
	// offering id that serves it, so per-schema fixtures can skip
	// cleanly when an implementation does not serve that schema.
	SessionOfferingsBySchema map[string]string
	SessionCapability        string // paid-session capability id
	SessionOffering          string
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

	// AttachCredential is a bearer credential enrolled on the
	// broker-under-test for the suite to attach with (runner-attach
	// §3.1). Empty means the attach scenarios skip. AttachHostID is the
	// host id that enrollment records.
	AttachCredential string
	AttachHostID     string

	authMu           sync.Mutex
	sessionAuth      map[string]string
	sessionGateway   map[string]string
	sessionOffering  map[string]string
	sessionRevisions map[string]string
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

// GatewaySessionID mints a run-scoped gateway session id.
//
// Run-scoped for the same reason request ids are, and it was missed:
// the broker binds a gateway_session_id to a session for the life of
// the session store, so a fixed id works once and then every later run
// against the same broker fails with gateway_session_id_reuse. Auto
// mode never noticed because it starts a broker with an empty store.
func (c *Ctx) GatewaySessionID(tag string) string {
	return "gws-" + c.RunID + "-" + tag
}

// PaymentEnvelope base64-encodes a stub payment envelope. The mock
// payment daemon accepts any non-empty envelope; a real deployment
// would substitute genuine payment material here.
func PaymentEnvelope(seed string) string {
	return base64.StdEncoding.EncodeToString([]byte("conformance-payment:" + seed))
}

func (c *Ctx) authorization(capability, offering, protocol, requestID, sessionID, predecessor string, body []byte) string {
	amount, per, unit := int64(1), uint64(1), c.JobUnit
	if protocol == ProtoPaidSession {
		amount, unit = 10, c.SessionUnit
	} else if offering == c.JobOfferingFractional {
		amount, per = 100, 1000
	}
	maxUnits := uint64(1_000_000)
	maxDebit := new(big.Int).Mul(big.NewInt(amount), new(big.Int).SetUint64(maxUnits))
	maxDebit.Add(maxDebit, new(big.Int).SetUint64(per-1)).Div(maxDebit, new(big.Int).SetUint64(per))
	digest := sha256.Sum256(body)
	revision := uint64(0)
	if predecessor != "" {
		revision = 1
	}
	p := &pb.SpendAuthorizationPayload{
		Domain: "livepeer-spend-authorization/v1", Payer: bytes.Repeat([]byte{1}, 20), Payee: bytes.Repeat([]byte{2}, 20),
		ChainId: 42161, Denomination: "wei", AuthorizationId: "conf-auth-" + requestID, Revision: revision, PredecessorAuthorizationId: predecessor,
		RequestId: requestID, SessionId: sessionID, Protocol: protocol, Capability: capability, Offering: offering, BrokerUri: strings.TrimRight(c.BrokerURL, "/"),
		AcceptedPrice: &pb.AcceptedPrice{PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(amount).Bytes()}, UnitsPerPrice: per, WorkUnitName: unit, Capability: capability, Offering: offering, QuoteRef: &pb.QuoteRef{QuoteId: "conformance-quote", QuoteVersion: 1, ConstraintFingerprint: []byte{1}, RouteFingerprint: []byte{2}}},
		MaxDebitWei:   &pb.BigUInt{Value: maxDebit.Bytes()}, MaxTotalUnits: maxUnits, RequestDigest: digest[:],
	}
	wire, _ := proto.Marshal(&pb.SpendAuthorization{Payload: p})
	return base64.StdEncoding.EncodeToString(wire)
}

func gatewaySessionID(body []byte) string {
	var v struct {
		GatewaySessionID string `json:"gateway_session_id"`
	}
	_ = json.Unmarshal(body, &v)
	return v.GatewaySessionID
}

func (c *Ctx) rememberSessionAuthorization(sessionID, authorizationID, gatewayID, offering string) {
	if sessionID == "" || authorizationID == "" {
		return
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.sessionAuth == nil {
		c.sessionAuth = map[string]string{}
	}
	if c.sessionGateway == nil {
		c.sessionGateway = map[string]string{}
	}
	if c.sessionOffering == nil {
		c.sessionOffering = map[string]string{}
	}
	c.sessionAuth[sessionID] = authorizationID
	c.sessionGateway[sessionID] = gatewayID
	c.sessionOffering[sessionID] = offering
}

func (c *Ctx) SessionRevisionAuthorization(sessionID, requestID string) string {
	c.authMu.Lock()
	if c.sessionRevisions != nil {
		if prior := c.sessionRevisions[sessionID+"|"+requestID]; prior != "" {
			c.authMu.Unlock()
			return prior
		}
	}
	predecessor := c.sessionAuth[sessionID]
	gatewayID := c.sessionGateway[sessionID]
	offering := c.sessionOffering[sessionID]
	c.authMu.Unlock()
	authorization := c.authorization(c.SessionCapability, offering, ProtoPaidSession, requestID, gatewayID, predecessor, nil)
	c.authMu.Lock()
	if c.sessionRevisions == nil {
		c.sessionRevisions = map[string]string{}
	}
	c.sessionRevisions[sessionID+"|"+requestID] = authorization
	c.authMu.Unlock()
	return authorization
}

func (c *Ctx) rememberSessionRevision(sessionID, authorizationID string) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.sessionAuth == nil {
		c.sessionAuth = map[string]string{}
	}
	c.sessionAuth[sessionID] = authorizationID
}

// ---------------------------------------------------------------------------
// paid-job helpers

// JobRequest describes one POST /v1/job exchange.
type JobRequest struct {
	Offering          string
	RequestID         string
	Payment           string // already base64
	Body              []byte
	Accept            string
	ContentType       string
	OmitAuthorization bool
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
	if !jr.OmitAuthorization {
		req.Header.Set(HdrAuthorization, c.authorization(c.JobCapability, jr.Offering, ProtoPaidJob, jr.RequestID, "", "", jr.Body))
	}
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

// QuerySettlement reads a terminal claim back from the broker by id —
// a job id, a session id, or a work id. This is the channel an SDK that
// cannot read HTTP trailers depends on: without it, a streamed job's
// units are unreachable and the caller must choose between billing zero
// and blocking.
func (c *Ctx) QuerySettlement(id string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodGet, c.BrokerURL+"/v1/settlement/"+id, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// DoJobAbort starts a job exchange and severs the connection after
// reading readChunks chunks of the body — the "stream severed mid-body"
// case in paid-job §7. The broker's terminal accounting must still
// happen, and a later retry of the same request id must replay it.
func (c *Ctx) DoJobAbort(jr JobRequest, readChunks int) error {
	req, err := http.NewRequest(http.MethodPost, c.BrokerURL+"/v1/job", bytes.NewReader(jr.Body))
	if err != nil {
		return err
	}
	req.Header.Set(HdrCapability, c.JobCapability)
	req.Header.Set(HdrOffering, jr.Offering)
	req.Header.Set(HdrProtocol, ProtoPaidJob)
	req.Header.Set(HdrRequestID, jr.RequestID)
	req.Header.Set(HdrPayment, jr.Payment)
	req.Header.Set(HdrAuthorization, c.authorization(c.JobCapability, jr.Offering, ProtoPaidJob, jr.RequestID, "", "", jr.Body))
	if jr.Accept != "" {
		req.Header.Set("Accept", jr.Accept)
	}
	// A dedicated client so closing this connection cannot disturb
	// pooled connections other scenarios are using.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	buf := make([]byte, 512)
	for i := 0; i < readChunks; i++ {
		if _, err := resp.Body.Read(buf); err != nil {
			break
		}
	}
	// Sever without draining: this is the client hanging up mid-body.
	_ = resp.Body.Close()
	client.CloseIdleConnections()
	return nil
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
	req.Header.Set(HdrAuthorization, c.authorization(c.SessionCapability, c.SessionOffering, ProtoPaidSession, requestID, gatewaySessionID([]byte(body)), "", []byte(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.do(req)
	if err == nil && res.Status >= 200 && res.Status < 300 {
		m := res.JSON()
		c.rememberSessionAuthorization(FieldString(m, "session_id"), FieldString(m, "work_id"), gatewaySessionID([]byte(body)), c.SessionOffering)
	}
	return res, err
}

// OpenPaymentOnlySession probes the mandatory authorization boundary. The
// payment is present deliberately; it must fund no workload by itself.
func (c *Ctx) OpenPaymentOnlySession(requestID, payment, body string) (*HTTPResult, error) {
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

// SessionOfferingFor returns the offering serving a schema nickname, or
// "" when the implementation under test does not serve it.
func (c *Ctx) SessionOfferingFor(nickname string) string {
	if nickname == "default" {
		return c.SessionOffering
	}
	return c.SessionOfferingsBySchema[nickname]
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
	req.Header.Set(HdrAuthorization, c.authorization(c.SessionCapability, offering, ProtoPaidSession, requestID, gatewaySessionID([]byte(body)), "", []byte(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.do(req)
	if err == nil && res.Status >= 200 && res.Status < 300 {
		m := res.JSON()
		c.rememberSessionAuthorization(FieldString(m, "session_id"), FieldString(m, "work_id"), gatewaySessionID([]byte(body)), offering)
	}
	return res, err
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
	authorization := c.SessionRevisionAuthorization(sessionID, requestID)
	req.Header.Set(HdrAuthorization, authorization)
	res, err := c.do(req)
	if err == nil && res.Status >= 200 && res.Status < 300 {
		if raw, decErr := base64.StdEncoding.DecodeString(authorization); decErr == nil {
			var auth pb.SpendAuthorization
			if proto.Unmarshal(raw, &auth) == nil {
				c.rememberSessionRevision(sessionID, auth.GetPayload().GetAuthorizationId())
			}
		}
	}
	return res, err
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

// GetExchange asks what happened to an exchange, keyed on the id the
// CONSUMER issued — the only identifier a clearinghouse is guaranteed to
// still hold when a customer withholds the rest.
func (c *Ctx) GetExchange(requestID string) (*HTTPResult, error) {
	req, err := http.NewRequest(http.MethodGet, c.BrokerURL+"/v1/exchange/"+requestID, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}
