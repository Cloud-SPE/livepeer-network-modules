// Package sessionengine implements the paid-session/v1 lifecycle:
// open, usage-claim intake with exactly-once debit, lease/heartbeat
// enforcement, and two-outcome restart recovery — over the durable
// sessionstore. The HTTP/control-plane surface lives in the server
// package; this package is the authority.
package sessionengine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// ProtocolError is a claim/request the protocol rejects; it advances no
// state (paid-session/v1 §7.2). Code is a stable machine-readable tag.
type ProtocolError struct {
	Code   string
	Detail string
}

func (e *ProtocolError) Error() string { return e.Code + ": " + e.Detail }

func protoErr(code, format string, args ...any) error {
	return &ProtocolError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// RetryableError is a transient failure; the runner retries the same
// event and converges (exactly-once, §7.3).
type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return "retryable: " + e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// Terminal close reasons (stable, machine-readable).
const (
	ReasonGatewayClose           = "gateway_close"
	ReasonRunnerEnded            = "runner_ended"
	ReasonRunnerFailed           = "runner_failed"
	ReasonLeaseExpired           = "lease_expired"
	ReasonHeartbeatLost          = "heartbeat_lost"
	ReasonInsufficient           = "insufficient_balance"
	ReasonAuthorizationExhausted = "authorization_exhausted"
	ReasonRecoveryFailed         = "recovery_failed"
	ReasonOpenFailed             = "open_failed"
	// ReasonPaymentUnrecoverable ends a session whose payment identity
	// cannot be recovered — a rotation that could not settle, or one
	// that exhausted its bound. It names the consequence, not the
	// mechanism: a recipient rotation is infrastructure the customer
	// neither caused nor can act on, so the detail belongs in settlement
	// and operator telemetry rather than in a close reason.
	ReasonPaymentUnrecoverable = "payment_unrecoverable"
	ReasonOutputFailed         = "output_failed"
)

const OutputStallBackstop = 60 * time.Second

var safeEventCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// OfferingSpec is the resolved offering the engine serves a session
// under — the declared axes plus pricing, supplied by the composition
// root from host config.
type OfferingSpec struct {
	Capability          string
	Offering            string
	BackendRef          string
	WorkUnit            string
	PricePerWorkUnitWei *big.Int
	// PerUnits is the price denominator: PricePerWorkUnitWei buys this
	// many work units (offering-axes.md §6). 0 means 1.
	PerUnits           uint64
	DescriptorSchema   string
	DescriptorMaxBytes int
	HeartbeatInterval  time.Duration // default 10s
	MissedThreshold    int           // default 3
	// BurnRatePerSecond estimates units consumed per second for the
	// funding-tracking lease default. <=0 means 1.
	BurnRatePerSecond float64
	LeaseMax          time.Duration // operator cap; <=0 means 1h
	// LeasePolicy is "funding-tracking" (default) or "fixed".
	LeasePolicy string
	// Refill is "extensible" (default) or "bounded". A bounded
	// offering rejects top-up after open (offering-axes §3).
	Refill string
	// Metering and RunnerPaths are carried so the broker can compare
	// them against the runner's own declaration (§7.1.1).
	Metering    string
	RunnerPaths RunnerPaths
	// MinRunwayUnits is the bounded authorization reservation floor requested
	// at open and after usage advances; <=0 uses the heartbeat-derived floor.
	MinRunwayUnits int64
}

func (s *OfferingSpec) heartbeat() time.Duration {
	if s.HeartbeatInterval <= 0 {
		return 10 * time.Second
	}
	return s.HeartbeatInterval
}

func (s *OfferingSpec) missed() int {
	if s.MissedThreshold <= 0 {
		return 3
	}
	return s.MissedThreshold
}

func (s *OfferingSpec) burnRate() float64 {
	if s.BurnRatePerSecond <= 0 {
		return 1
	}
	return s.BurnRatePerSecond
}

func (s *OfferingSpec) leaseMax() time.Duration {
	if s.LeaseMax <= 0 {
		return time.Hour
	}
	return s.LeaseMax
}

// Config wires the engine's dependencies.
type Config struct {
	Store    *sessionstore.Store
	Payment  payment.Client
	Runner   func(backendRef string) RunnerClient // resolver, per backend
	Specs    func(sessionID string) *OfferingSpec // resolve spec for a stored session
	Callback CallbackConfig
	// ReleaseCapacity releases a held capacity slot; nil is a no-op.
	ReleaseCapacity func(capacityRef string)
	// OnWinddown observes each terminal winddown's stable reason; nil
	// is a no-op. Used for metrics; never for control flow.
	// The record is the terminal one — state, close reason and EndedAt
	// already written — so an observer can attribute the session
	// (BackendRef, Capability, Offering) without a second store read.
	OnWinddown func(rec sessionstore.Record, reason string)
	// OnEvent observes session happenings for push surfaces (the
	// control-WS binding). kind is a frame type; data its body. nil is
	// a no-op. Observability only — never control flow, and the HTTP
	// surface remains authoritative (paid-session §8).
	OnEvent func(sessionID, kind string, data map[string]any)
	// TerminalRetention bounds how long terminal records stay
	// queryable before eviction; <=0 means 1h.
	TerminalRetention time.Duration
	Now               func() time.Time // test hook; nil means time.Now
	Log               *slog.Logger
}

// CallbackConfig is the operator-configured external coordinates the
// runner posts events to. Never derived from inbound request headers.
type CallbackConfig struct {
	// BaseURL is the broker's externally-reachable base, e.g.
	// "https://broker.example.com". The event path is appended per
	// session.
	BaseURL string
}

// Engine is the paid-session authority.
type Engine struct {
	cfg  Config
	mu   sync.Mutex
	perS map[string]*sync.Mutex // serializes event processing per session
}

// New constructs an Engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Store == nil || cfg.Payment == nil || cfg.Runner == nil || cfg.Specs == nil {
		return nil, errors.New("sessionengine: Store, Payment, Runner, and Specs are required")
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.TerminalRetention <= 0 {
		cfg.TerminalRetention = time.Hour
	}
	var legacySessions, legacyReservations int
	if err := cfg.Store.ForEach(func(r *sessionstore.Record) error {
		if !r.Terminal() && r.AccountAuthorizationID == "" {
			legacySessions++
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("sessionengine: inspect session store for authorization-only cutover: %w", err)
	}
	if err := cfg.Store.ForEachReservation(func(r sessionstore.OpenReservation) error {
		if (r.Stage == sessionstore.ReservationPaid || r.Stage == sessionstore.ReservationRunnerCreated) && !r.AccountAuthorization {
			legacyReservations++
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("sessionengine: inspect open reservations for authorization-only cutover: %w", err)
	}
	if legacySessions > 0 || legacyReservations > 0 {
		return nil, fmt.Errorf("sessionengine: authorization-only cutover refused: drain %d legacy nonterminal sessions and %d legacy paid open reservations before upgrade", legacySessions, legacyReservations)
	}
	return &Engine{cfg: cfg, perS: map[string]*sync.Mutex{}}, nil
}

func (e *Engine) sessionMu(id string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.perS[id]
	if !ok {
		m = &sync.Mutex{}
		e.perS[id] = m
	}
	return m
}

// ---------------------------------------------------------------------------
// Open

// OpenRequest is one session-open, post header validation.
type OpenRequest struct {
	RequestID             string
	GatewaySessionID      string
	SessionParams         json.RawMessage
	PaymentBytes          []byte
	AuthorizationBytes    []byte
	InitialReservationWei *big.Int
	AcceptedQuoteRef      *pb.QuoteRef
	Spec                  *OfferingSpec
	CapacityRef           string
}

// OpenResult is returned to the gateway. Credential and Grants carry
// plaintext secrets — this is the only time they exist outside the
// runner and the gateway.
type OpenResult struct {
	SessionID  string
	WorkID     string
	State      string
	Schema     string
	Public     json.RawMessage
	Grants     []Grant
	Credential string
	Lease      time.Time
	Replayed   bool // true when the request id resolved to an existing session
}

// Open opens a session: validate payment, bind the runner, validate the
// descriptor, persist — failing closed on any post-payment error.
// Idempotent on RequestID: a replay returns the original session with
// Replayed=true and NO credential or grants (delivered exactly once).
func (e *Engine) Open(ctx context.Context, req OpenRequest) (*OpenResult, error) {
	if req.RequestID == "" {
		return nil, protoErr("request_id_required", "Livepeer-Request-Id is required")
	}
	if len(req.AuthorizationBytes) == 0 {
		return nil, protoErr("authorization_required", "Livepeer-Authorization is required")
	}
	if req.InitialReservationWei == nil || req.InitialReservationWei.Sign() < 0 {
		return nil, protoErr("payment_invalid", "authorization reservation is required")
	}
	if req.Spec == nil {
		return nil, errors.New("sessionengine: nil spec")
	}
	fingerprint := openFingerprint(req)
	if id, err := e.cfg.Store.SessionIDForRequest(req.RequestID); err == nil {
		return e.replayOpen(id, fingerprint)
	}
	// Claim the request id BEFORE any side effect (plan 0048 §2.2). Two
	// opens with one id could otherwise both pass the lookup above and
	// both fund and both create a runner session before one lost the
	// insert; and a crash between payment and persist left a funded
	// payee session with no record to find it by. The reservation is
	// what Recover undoes.
	// Everything that can fail without a side effect happens before the
	// reservation, so a failure here leaves nothing to release.
	now := e.cfg.Now()
	sessionID := "sess_" + uuid.NewString()
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(req.AuthorizationBytes, &auth); err != nil || auth.GetPayload() == nil {
		return nil, protoErr("payment_invalid", "account authorization is malformed")
	}
	accountPayload := auth.GetPayload()
	workID := accountPayload.GetAuthorizationId()
	credential, err := randomSecret("sc_")
	if err != nil {
		return nil, err
	}
	callbackToken, err := randomSecret("cb_")
	if err != nil {
		return nil, err
	}
	if err := e.cfg.Store.ReserveOpen(req.RequestID, fingerprint); err != nil {
		switch {
		case errors.Is(err, sessionstore.ErrExists):
			if id, lerr := e.cfg.Store.SessionIDForRequest(req.RequestID); lerr == nil {
				return e.replayOpen(id, fingerprint)
			}
			return nil, err
		case errors.Is(err, sessionstore.ErrOpenInFlight):
			// The same rule replay applies: one id, one content. A
			// different open under an in-flight id is a reuse, not a
			// retry, and is refused rather than told to try again.
			if held, lerr := e.cfg.Store.Reservation(req.RequestID); lerr == nil && !bytesEqual(held.Fingerprint, fingerprint) {
				return nil, protoErr("request_id_reuse", "request id reused with different open content")
			}
			return nil, protoErr("open_in_flight", "an open with this request id is in flight; retry")
		case errors.Is(err, sessionstore.ErrNonAdmissionIssued):
			return nil, protoErr("request_id_reuse", "broker already issued non-admission evidence for this request id")
		default:
			return nil, err
		}
	}
	releaseReservation := func() { _ = e.cfg.Store.ReleaseReservation(req.RequestID) }
	// A stage write that fails must fail the open closed: a reservation
	// that understates what was opened is one Recover cannot undo.
	recordStage := func(fn func(r *sessionstore.OpenReservation)) error {
		return e.cfg.Store.UpdateReservation(req.RequestID, func(r *sessionstore.OpenReservation) error { fn(r); return nil })
	}

	// Authorization first: no reserved runway, no runner binding.
	var sender []byte
	var credited *big.Int
	{
		ac, ok := e.cfg.Payment.(payment.AccountClient)
		if !ok {
			releaseReservation()
			return nil, protoErr("protocol_unsupported", "payment daemon does not support wholesale accounts")
		}
		admitted, err := ac.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: req.AuthorizationBytes, PaymentBytes: req.PaymentBytes, Reservation: req.InitialReservationWei})
		if err != nil {
			releaseReservation()
			return nil, protoErr("payment_invalid", "authorization admission rejected: %v", err)
		}
		if admitted == nil || admitted.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || admitted.Account == nil || !bytesEqual(admitted.Account.Payer, accountPayload.GetPayer()) {
			releaseReservation()
			return nil, &RetryableError{Err: errors.New("payment daemon returned an invalid account admission")}
		}
		sender = append([]byte(nil), accountPayload.GetPayer()...)
		credited = admitted.Credited
		if err := recordStage(func(r *sessionstore.OpenReservation) {
			r.Stage, r.WorkID, r.CapacityRef, r.BackendRef, r.Sender = sessionstore.ReservationPaid, workID, req.CapacityRef, req.Spec.BackendRef, sender
			r.AccountAuthorization = true
		}); err != nil {
			_, _ = ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: sender, AuthorizationID: workID, ActualUnits: 0, SettlementSeq: 1})
			releaseReservation()
			return nil, &RetryableError{Err: fmt.Errorf("record account admission: %w", err)}
		}
	}
	failClosed := func(stage string, cause error, runnerSessionID string) error {
		if runnerSessionID != "" {
			_ = e.runnerFor(req.Spec.BackendRef).TerminateSession(ctx, runnerSessionID, ReasonOpenFailed)
		}
		if ac, ok := e.cfg.Payment.(payment.AccountClient); ok {
			_, _ = ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: sender, AuthorizationID: accountPayload.GetAuthorizationId(), ActualUnits: 0, SettlementSeq: 1})
		}
		e.release(req.CapacityRef)
		releaseReservation()
		return fmt.Errorf("sessionengine: open failed at %s (failed closed): %w", stage, cause)
	}

	created, err := e.runnerFor(req.Spec.BackendRef).CreateSession(ctx, RunnerCreateRequest{
		SessionID:     sessionID,
		WorkID:        workID,
		Capability:    req.Spec.Capability,
		Offering:      req.Spec.Offering,
		SessionParams: req.SessionParams,
		CallbackURL:   e.callbackURL(sessionID),
		CallbackToken: callbackToken,
	})
	if err != nil {
		return nil, failClosed("runner create", err, "")
	}
	if err := recordStage(func(r *sessionstore.OpenReservation) {
		r.Stage, r.RunnerSessionID = sessionstore.ReservationRunnerCreated, created.RunnerSessionID
	}); err != nil {
		return nil, failClosed("record open stage", err, created.RunnerSessionID)
	}
	desc, err := ParseDescriptor(created.Runtime, req.Spec.DescriptorSchema, req.Spec.DescriptorMaxBytes)
	if err != nil {
		return nil, failClosed("descriptor validation", err, created.RunnerSessionID)
	}

	lease := now.Add(req.Spec.heartbeat() * time.Duration(req.Spec.missed()))
	if max := now.Add(req.Spec.leaseMax()); lease.After(max) {
		lease = max
	}

	rec := &sessionstore.Record{
		SessionID:                sessionID,
		GatewaySessionID:         req.GatewaySessionID,
		RunnerSessionID:          created.RunnerSessionID,
		WorkID:                   workID,
		Capability:               req.Spec.Capability,
		Offering:                 req.Spec.Offering,
		BackendRef:               req.Spec.BackendRef,
		QuoteID:                  req.AcceptedQuoteRef.GetQuoteId(),
		QuoteVersion:             req.AcceptedQuoteRef.GetQuoteVersion(),
		ConstraintFingerprint:    append([]byte(nil), req.AcceptedQuoteRef.GetConstraintFingerprint()...),
		RouteFingerprint:         append([]byte(nil), req.AcceptedQuoteRef.GetRouteFingerprint()...),
		Sender:                   sender,
		CredentialHash:           sessionstore.HashSecret(credential),
		CallbackTokenHash:        sessionstore.HashSecret(callbackToken),
		OpenFingerprint:          fingerprint,
		AccountAuthorizationID:   accountPayload.GetAuthorizationId(),
		AuthorizationMaxUnits:    accountPayload.GetMaxTotalUnits(),
		AuthorizationMaxDebitWei: new(big.Int).SetBytes(accountPayload.GetMaxDebitWei().GetValue()).String(),
		AuthorizationReservedWei: req.InitialReservationWei.String(),
		ReplayMaterial:           sealedReplayMaterial(credential, desc.Grants),
		FundedWei:                bigIntString(credited),
		DescriptorSchema:         desc.Schema,
		DescriptorPublic:         desc.Public,
		DescriptorPrivate:        desc.Private,
		Grants:                   auditGrants(desc.Grants),
		Unit:                     req.Spec.WorkUnit,
		LeaseExpiresAt:           lease,
		LastEventAt:              now,
		State:                    sessionstore.StateActive,
		CapacityRef:              req.CapacityRef,
	}
	if err := e.cfg.Store.CreateIndexed(rec, req.RequestID); err != nil {
		// A colliding gateway_session_id is the caller's own mistake and
		// no retry fixes it, so it fails as a protocol error rather than
		// converging on somebody else's session. The id has to resolve
		// to one session for the settlement lookup to be usable, and
		// silently accepting a duplicate would break that for BOTH
		// parties to the collision.
		if errors.Is(err, sessionstore.ErrGatewaySessionExists) {
			_ = e.runnerFor(req.Spec.BackendRef).TerminateSession(ctx, created.RunnerSessionID, ReasonOpenFailed)
			if ac, ok := e.cfg.Payment.(payment.AccountClient); ok {
				_, _ = ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: sender, AuthorizationID: workID, ActualUnits: 0, SettlementSeq: 1})
			}
			releaseReservation()
			e.release(req.CapacityRef)
			return nil, protoErr("gateway_session_id_reuse",
				"gateway_session_id is already bound to another session; choose an unused id")
		}
		if errors.Is(err, sessionstore.ErrExists) {
			// Concurrent open with the same request id won; converge.
			_ = e.runnerFor(req.Spec.BackendRef).TerminateSession(ctx, created.RunnerSessionID, ReasonOpenFailed)
			if ac, ok := e.cfg.Payment.(payment.AccountClient); ok {
				_, _ = ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: sender, AuthorizationID: workID, ActualUnits: 0, SettlementSeq: 1})
			}
			e.release(req.CapacityRef)
			if id, lerr := e.cfg.Store.SessionIDForRequest(req.RequestID); lerr == nil {
				return e.replayOpen(id, fingerprint)
			}
		}
		return nil, failClosed("persist", err, created.RunnerSessionID)
	}

	return &OpenResult{
		SessionID:  sessionID,
		WorkID:     workID,
		State:      sessionstore.StateActive,
		Schema:     desc.Schema,
		Public:     desc.Public,
		Grants:     desc.Grants,
		Credential: credential,
		Lease:      lease,
	}, nil
}

// replayOpen answers a retried open with the outcome the original call
// produced — credential and grants included.
//
// Delivering secrets exactly once reads as good hygiene and is the wrong
// trade: a gateway whose open response was lost in flight would hold a
// funded session it can never drive, with nothing to do but wait out the
// lease. Re-delivery is bounded instead — same request id, identical
// content, and the payment envelope that proves the same payer — so what
// is returned goes back to whoever bought it.
//
// A reused id with different content is request_id_reuse: the id is a
// promise about content, and answering it with somebody else's session
// would be worse than refusing.
func (e *Engine) replayOpen(sessionID string, fingerprint []byte) (*OpenResult, error) {
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if len(rec.OpenFingerprint) > 0 && !bytesEqual(rec.OpenFingerprint, fingerprint) {
		return nil, protoErr("request_id_reuse",
			"request id reused with different open content")
	}
	out := &OpenResult{
		SessionID: rec.SessionID,
		WorkID:    rec.WorkID,
		State:     rec.State,
		Schema:    rec.DescriptorSchema,
		Public:    rec.DescriptorPublic,
		Lease:     rec.LeaseExpiresAt,
		Replayed:  true,
	}
	if len(rec.ReplayMaterial) > 0 {
		var mat replayMaterial
		if err := json.Unmarshal(rec.ReplayMaterial, &mat); err != nil {
			return nil, fmt.Errorf("sessionengine: replay material: %w", err)
		}
		out.Credential = mat.Credential
		out.Grants = mat.Grants
	}
	return out, nil
}

// sealedReplayMaterial renders what a replay must return. Marshalling
// failure yields nil rather than an error: an open that succeeded must
// not fail because its replay copy could not be prepared, and a replay
// with no material degrades to the pre-existing behaviour rather than
// to a broken session.
func sealedReplayMaterial(credential string, grants []Grant) []byte {
	raw, err := json.Marshal(replayMaterial{Credential: credential, Grants: grants})
	if err != nil {
		return nil
	}
	return raw
}

// replayMaterial is what an idempotent open must be able to hand back.
// Sealed at rest and cleared at winddown.
type replayMaterial struct {
	Credential string  `json:"credential"`
	Grants     []Grant `json:"grants,omitempty"`
}

// openFingerprint binds a request id to the open it answered:
// capability, offering, the gateway's own session id, the opaque
// session_params, and the payment envelope. The envelope is what makes
// the fingerprint an identity check as well as a content check — an
// identical fingerprint means the same payer presented the same funded
// intent, which is the condition for handing the credential back.
func openFingerprint(req OpenRequest) []byte {
	h := sha256.New()
	if req.Spec != nil {
		h.Write([]byte(req.Spec.Capability))
		h.Write([]byte{0})
		h.Write([]byte(req.Spec.Offering))
	}
	h.Write([]byte{0})
	h.Write([]byte(req.GatewaySessionID))
	h.Write([]byte{0})
	h.Write(req.SessionParams)
	h.Write([]byte{0})
	h.Write(req.PaymentBytes)
	h.Write([]byte{0})
	h.Write(req.AuthorizationBytes)
	return h.Sum(nil)
}

// ---------------------------------------------------------------------------
// Events — exactly-once debit

// Event is one runner event, post transport auth.
type Event struct {
	EventID   string
	Sequence  uint64
	EventType string
	State     string
	UsageUnit string
	UsageTot  *uint64 // nil when the event carries no usage
	Reason    string
	Details   json.RawMessage
}

type outputHealth struct {
	State       string
	Since       time.Time
	FailureCode string
}

func parseOutputHealth(raw json.RawMessage) (*outputHealth, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > 2048 {
		return nil, protoErr("event_details_invalid", "details exceeds 2048 bytes")
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal(raw, &details); err != nil || details == nil {
		return nil, protoErr("event_details_invalid", "details must be a JSON object")
	}
	stateRaw, present := details["output_state"]
	if !present {
		return nil, nil
	}
	var state, sinceRaw, failure string
	if json.Unmarshal(stateRaw, &state) != nil || (state != "waiting" && state != "producing" && state != "stalled") {
		return nil, protoErr("output_state_invalid", "output_state must be waiting, producing, or stalled")
	}
	var since time.Time
	if rawSince, ok := details["output_state_since"]; ok {
		if json.Unmarshal(rawSince, &sinceRaw) != nil {
			return nil, protoErr("output_state_since_invalid", "output_state_since must be RFC3339")
		}
		var err error
		since, err = time.Parse(time.RFC3339Nano, sinceRaw)
		if err != nil {
			return nil, protoErr("output_state_since_invalid", "output_state_since must be RFC3339")
		}
	}
	if rawFailure, ok := details["last_failure_code"]; ok {
		if json.Unmarshal(rawFailure, &failure) != nil || !safeEventCode.MatchString(failure) {
			return nil, protoErr("last_failure_code_invalid", "last_failure_code must be a safe machine code")
		}
	}
	return &outputHealth{State: state, Since: since.UTC(), FailureCode: failure}, nil
}

// EventOutcome reports what processing decided.
type EventOutcome struct {
	Duplicate    bool
	DebitedUnits uint64
	Insufficient bool
	Terminal     bool
	OutputState  string
}

// ProcessEvent applies one runner event under the exactly-once
// contract: event dedup commits only together with durable debit
// progress. Processing is serialized per session.
func (e *Engine) ProcessEvent(ctx context.Context, sessionID string, ev Event) (*EventOutcome, error) {
	if ev.EventID == "" {
		return nil, protoErr("event_id_required", "event_id must be non-empty")
	}
	if ev.Sequence == 0 {
		return nil, protoErr("sequence_required", "sequence must be positive")
	}
	health, err := parseOutputHealth(ev.Details)
	if err != nil {
		return nil, err
	}
	if ev.Reason != "" && !safeEventCode.MatchString(ev.Reason) {
		return nil, protoErr("close_reason_invalid", "close_reason must be a safe machine code")
	}
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()

	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if rec.Closing() {
		return nil, protoErr("session_terminal", "session is %s (%s)", rec.State, rec.CloseReason)
	}
	spec := e.cfg.Specs(sessionID)
	if spec == nil {
		return nil, fmt.Errorf("sessionengine: no offering spec for session %s", sessionID)
	}

	// Dedup: processed iff sequence is at or below the committed
	// watermark. A retry of an uncommitted event re-presents its
	// sequence and proceeds.
	if ev.Sequence <= rec.LastSequence {
		return &EventOutcome{Duplicate: true}, nil
	}

	var delta uint64
	var authorizationCumulative uint64
	var authorizationExhausted bool
	if ev.UsageTot != nil {
		if ev.UsageUnit != rec.Unit {
			return nil, protoErr("usage_unit_mismatch", "unit %q does not match offering unit %q", ev.UsageUnit, rec.Unit)
		}
		if *ev.UsageTot < rec.ClaimedTotal {
			return nil, protoErr("usage_regression", "cumulative total %d below committed %d", *ev.UsageTot, rec.ClaimedTotal)
		}
		delta = *ev.UsageTot - rec.ClaimedTotal
		authorizationCumulative = *ev.UsageTot
		if authorizationCumulative >= rec.AuthorizationMaxUnits {
			authorizationExhausted = true
			if authorizationCumulative > rec.AuthorizationMaxUnits {
				authorizationCumulative = rec.AuthorizationMaxUnits
			}
			// The runner may report in coarse ticks and cross the cap in one
			// event. Debit only the signed cumulative allowance; excess work
			// is seller risk and cannot turn into involuntary payer credit.
			delta = 0
			if authorizationCumulative > rec.DebitedTotal {
				delta = authorizationCumulative - rec.DebitedTotal
			}
		}
	}

	var chargedWei *big.Int
	debitSeq := rec.DebitSeq
	if delta > 0 {
		ac, ok := e.cfg.Payment.(payment.AccountClient)
		if !ok {
			return nil, &RetryableError{Err: errors.New("wholesale account payment extension unavailable")}
		}
		remainingUnits := uint64(0)
		if rec.AuthorizationMaxUnits > authorizationCumulative {
			remainingUnits = rec.AuthorizationMaxUnits - authorizationCumulative
		}
		runwayUnits := uint64(1)
		if spec.MinRunwayUnits > 0 {
			runwayUnits = uint64(spec.MinRunwayUnits)
		}
		if runwayUnits > remainingUnits {
			runwayUnits = remainingUnits
		}
		target := payment.BillFor(spec.PricePerWorkUnitWei, spec.PerUnits, runwayUnits)
		if ev.EventType == "session.ended" || ev.EventType == "session.failed" {
			target = new(big.Int)
		}
		advanceSeq := rec.DebitSeq + 1
		if rec.PendingDebitSeq != 0 {
			advanceSeq = rec.PendingDebitSeq
		} else if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error { r.PendingDebitSeq = advanceSeq; return nil }); err != nil {
			return nil, &RetryableError{Err: err}
		}
		advanced, err := ac.AdvanceAuthorization(ctx, payment.AdvanceAuthorizationRequest{Payer: rec.Sender, AuthorizationID: rec.AccountAuthorizationID, CumulativeUnits: authorizationCumulative, TargetReserved: target, AdvanceSeq: advanceSeq})
		if err != nil {
			return nil, &RetryableError{Err: fmt.Errorf("advance account authorization: %w", err)}
		}
		if advanced == nil || advanced.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
			return nil, &RetryableError{Err: errors.New("payment daemon returned invalid authorization advance state")}
		}
		chargedWei, debitSeq = advanced.BilledDelta, advanceSeq
		if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error { r.AuthorizationReservedWei = target.String(); return nil }); err != nil {
			return nil, &RetryableError{Err: err}
		}
	}

	now := e.cfg.Now()
	terminalReason := ""
	switch ev.EventType {
	case "session.ended":
		terminalReason = ReasonRunnerEnded
		if ev.Reason != "" {
			terminalReason = ev.Reason
		}
	case "session.failed":
		terminalReason = ReasonRunnerFailed
		if ev.Reason != "" {
			terminalReason = ev.Reason
		}
	}
	if terminalReason == "" && authorizationExhausted {
		terminalReason = ReasonAuthorizationExhausted
	}

	// The atomic commit point: dedup watermark, totals, and debit
	// progress move together or not at all.
	err = e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.LastEventID = ev.EventID
		r.LastSequence = ev.Sequence
		r.LastEventAt = now
		if health != nil {
			if health.State != r.OutputState || r.OutputStateSince.IsZero() {
				r.OutputStateSince = health.Since
				if r.OutputStateSince.IsZero() {
					r.OutputStateSince = now
				}
			}
			r.OutputState = health.State
			r.LastFailureCode = health.FailureCode
			switch health.State {
			case "producing":
				r.LastOutputAt = now
				r.OutputStalledAt = time.Time{}
			case "stalled":
				if r.OutputStalledAt.IsZero() {
					r.OutputStalledAt = now
				}
			default:
				r.OutputStalledAt = time.Time{}
			}
		}
		if ev.UsageTot != nil {
			r.ClaimedTotal = *ev.UsageTot
		}
		if delta > 0 {
			r.DebitedTotal += delta
			r.DebitSeq = debitSeq
			r.PendingDebitSeq = 0
			if chargedWei != nil {
				r.BilledWei = addDecimal(r.BilledWei, chargedWei)
			}
		}
		r.LeaseExpiresAt = now.Add(spec.heartbeat() * time.Duration(spec.missed()))
		if max := now.Add(spec.leaseMax()); r.LeaseExpiresAt.After(max) {
			r.LeaseExpiresAt = max
		}
		return nil
	})
	if err == nil && ev.UsageTot != nil && e.cfg.OnEvent != nil {
		e.cfg.OnEvent(sessionID, "session.usage.tick", map[string]any{
			"sequence":      ev.Sequence,
			"unit":          rec.Unit,
			"claimed_total": *ev.UsageTot,
			"debited_units": delta,
		})
	}
	if err == nil && health != nil && e.cfg.OnEvent != nil {
		data := map[string]any{
			"output_state":       health.State,
			"output_state_since": health.Since.Format(time.RFC3339Nano),
		}
		if health.Since.IsZero() {
			data["output_state_since"] = now.Format(time.RFC3339Nano)
		}
		if health.FailureCode != "" {
			data["last_failure_code"] = health.FailureCode
		}
		e.cfg.OnEvent(sessionID, "session.output.health", data)
	}
	if err != nil {
		// Debit may have landed; the retry re-presents the same
		// sequence and debit_seq, the daemon dedupes, and the commit
		// retries. Exactly once either way.
		return nil, &RetryableError{Err: fmt.Errorf("commit: %w", err)}
	}

	out := &EventOutcome{DebitedUnits: delta}
	if health != nil {
		out.OutputState = health.State
	}

	if terminalReason != "" {
		e.winddownLocked(ctx, sessionID, terminalReason)
		out.Terminal = true
		return out, nil
	}

	return out, nil
}

// TopUpResult carries the new funding state and lease.
type TopUpResult struct {
	Lease   time.Time
	Balance *big.Int
}

// ReviseAuthorization atomically replaces an account-backed session's signed
// cumulative cap in the same transaction that reserves successor runway.
func (e *Engine) ReviseAuthorization(ctx context.Context, sessionID, requestID string, authorizationBytes, paymentBytes []byte, reservation *big.Int) (*TopUpResult, error) {
	if requestID == "" {
		return nil, protoErr("request_id_required", "Livepeer-Request-Id is required")
	}
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	fpHash := sha256.New()
	fpHash.Write(authorizationBytes)
	fpHash.Write([]byte{0})
	fpHash.Write(paymentBytes)
	fp := fpHash.Sum(nil)
	if prior, err := e.cfg.Store.TopUpRecall(sessionID, requestID, fp); err != nil {
		if errors.Is(err, sessionstore.ErrRequestIDReuse) {
			return nil, protoErr("request_id_reuse", "request id reused with different revision")
		}
		return nil, err
	} else if prior != nil {
		bal, _ := new(big.Int).SetString(prior.BalanceWei, 10)
		return &TopUpResult{Lease: prior.LeaseExpiresAt, Balance: bal}, nil
	}
	if rec.Closing() || rec.AccountAuthorizationID == "" {
		return nil, protoErr("refill_refused", "session does not accept an account authorization revision")
	}
	spec := e.cfg.Specs(sessionID)
	if spec == nil {
		return nil, errors.New("offering spec unavailable")
	}
	if spec.Refill == "bounded" {
		return nil, protoErr("refill_refused", "offering declares refill: bounded")
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(authorizationBytes, &auth); err != nil || auth.GetPayload() == nil {
		return nil, protoErr("payment_invalid", "authorization revision is malformed")
	}
	p := auth.GetPayload()
	if p.GetPredecessorAuthorizationId() != rec.AccountAuthorizationID || p.GetSessionId() != rec.GatewaySessionID || !bytesEqual(p.GetPayer(), rec.Sender) {
		return nil, protoErr("refill_refused", "authorization revision does not continue this session")
	}
	oldMaxDebit, _ := new(big.Int).SetString(rec.AuthorizationMaxDebitWei, 10)
	if oldMaxDebit == nil {
		oldMaxDebit = new(big.Int)
	}
	if p.GetMaxTotalUnits() < rec.AuthorizationMaxUnits || new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()).Cmp(oldMaxDebit) < 0 {
		return nil, protoErr("refill_refused", "authorization revision cannot reduce the cumulative cap")
	}
	ac, ok := e.cfg.Payment.(payment.AccountClient)
	if !ok {
		return nil, protoErr("protocol_unsupported", "payment daemon does not support wholesale accounts")
	}
	admitted, err := ac.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: authorizationBytes, PaymentBytes: paymentBytes, Reservation: reservation})
	if err != nil {
		return nil, protoErr("payment_invalid", "authorization revision rejected: %v", err)
	}
	if admitted == nil || admitted.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || admitted.Account == nil {
		return nil, &RetryableError{Err: errors.New("payment daemon returned invalid revision admission")}
	}
	now := e.cfg.Now()
	lease := now.Add(spec.heartbeat() * time.Duration(spec.missed()))
	if max := now.Add(spec.leaseMax()); lease.After(max) {
		lease = max
	}
	if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.AccountAuthorizationID, r.WorkID = p.GetAuthorizationId(), p.GetAuthorizationId()
		r.AuthorizationMaxUnits = p.GetMaxTotalUnits()
		r.AuthorizationMaxDebitWei = new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()).String()
		r.AuthorizationReservedWei = reservation.String()
		r.LeaseExpiresAt = lease
		if admitted.Credited != nil {
			r.FundedWei = addDecimal(r.FundedWei, admitted.Credited)
		}
		return nil
	}); err != nil {
		return nil, &RetryableError{Err: err}
	}
	if err := e.cfg.Store.TopUpRecord(sessionID, requestID, fp, lease, balanceString(admitted.Account.Available)); err != nil {
		return nil, err
	}
	return &TopUpResult{Lease: lease, Balance: admitted.Account.Available}, nil
}

func addDecimal(current string, delta *big.Int) string {
	sum, ok := new(big.Int).SetString(current, 10)
	if !ok || sum == nil {
		sum = new(big.Int)
	}
	return sum.Add(sum, delta).String()
}

func bigIntString(value *big.Int) string {
	if value == nil {
		return "0"
	}
	return value.String()
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// topUpFingerprint binds a request id to the top-up it paid for. The
// envelope is the whole content of the call — there is no body — so a
// reused id with different bytes is a different top-up.
func balanceString(b *big.Int) string {
	if b == nil {
		return "0"
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// End / winddown

// End is the gateway-initiated idempotent end (paid-session §3.4).
func (e *Engine) End(ctx context.Context, sessionID, reason string) (*sessionstore.Record, error) {
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if !rec.Terminal() {
		if reason == "" {
			reason = ReasonGatewayClose
		}
		e.winddownLocked(ctx, sessionID, reason)
	}
	return e.cfg.Store.Get(sessionID)
}

// winddownLocked runs the single idempotent terminal path: terminate
// runner, close payment, release capacity, record the stable reason.
// Callers hold the session mutex.
func (e *Engine) winddownLocked(ctx context.Context, sessionID, reason string) {
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil || rec.Terminal() {
		return
	}
	// A winddown already pending keeps its first reason: what ended the
	// session, not what the sweep noticed on the retry.
	if rec.State == sessionstore.StateWindingDown && rec.CloseReason != "" {
		reason = rec.CloseReason
	}
	// Two obligations, each retried until it is met (plan 0048 §2.3).
	runnerDone := rec.RunnerTerminated
	if !runnerDone {
		err := e.runnerFor(rec.BackendRef).TerminateSession(ctx, rec.RunnerSessionID, reason)
		if err == nil || errors.Is(err, ErrRunnerSessionGone) {
			runnerDone = true
		} else {
			e.cfg.Log.Warn("runner terminate failed; winddown pending, will retry on sweep",
				"session", sessionID, "err", err)
		}
	}
	paymentClosed := rec.PaymentClosed
	releasedAuthorizationWei := ""
	if !paymentClosed {
		var closeErr error
		if ac, ok := e.cfg.Payment.(payment.AccountClient); ok {
			var settled *payment.SettleAuthorizationResult
			settled, closeErr = ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: rec.Sender, AuthorizationID: rec.AccountAuthorizationID, ActualUnits: rec.DebitedTotal, SettlementSeq: rec.DebitSeq + 1})
			if closeErr == nil && (settled == nil || settled.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED)) {
				closeErr = errors.New("payment daemon returned invalid authorization settlement state")
			}
			if closeErr == nil && settled.Released != nil {
				releasedAuthorizationWei = settled.Released.String()
			}
		} else {
			closeErr = errors.New("wholesale account payment extension unavailable")
		}
		if closeErr != nil {
			e.cfg.Log.Warn("payment close failed; winddown pending, will retry on sweep",
				"session", sessionID, "err", closeErr)
		} else {
			paymentClosed = true
		}
	}
	now := e.cfg.Now()
	if !runnerDone || !paymentClosed {
		// Not terminal: the runner session or the payee session is
		// still open, so the capacity stays held and no outcome is
		// reported. Sweep and Recover call back here until both are met.
		_ = e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
			r.State = sessionstore.StateWindingDown
			r.CloseReason = reason
			r.ReplayMaterial = nil
			r.PaymentClosed = paymentClosed
			if paymentClosed {
				r.AuthorizationReservedWei = "0"
				r.AuthorizationReleasedWei = releasedAuthorizationWei
			}
			r.RunnerTerminated = runnerDone
			return nil
		})
		if e.cfg.OnEvent != nil {
			e.cfg.OnEvent(sessionID, "session.winding_down", map[string]any{
				"close_reason": reason, "runner_terminated": runnerDone, "authorization_settled": paymentClosed,
			})
		}
		return
	}
	state := sessionstore.StateEnded
	if reason == ReasonRunnerFailed || reason == ReasonRecoveryFailed || reason == ReasonOutputFailed {
		state = sessionstore.StateFailed
	}
	// The terminal write comes first and is checked. Releasing the
	// capacity or reporting the outcome on a record that did not
	// persist would hand the slot away and score the member for a
	// session the next sweep still sees as winding down — so a failed
	// write leaves the record pending, with its obligations recorded
	// as met, and the next sweep finishes the job.
	var ended sessionstore.Record
	if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.State = state
		r.CloseReason = reason
		// The replay window ends with the session. Secrets outliving
		// the thing they unlock is how a store becomes a liability.
		r.ReplayMaterial = nil
		r.PaymentClosed = true
		r.AuthorizationReservedWei = "0"
		if releasedAuthorizationWei != "" {
			r.AuthorizationReleasedWei = releasedAuthorizationWei
		}
		r.RunnerTerminated = true
		r.EndedAt = now
		r.CapacityRef = ""
		ended = *r
		return nil
	}); err != nil {
		e.cfg.Log.Warn("terminal write failed; winddown pending, will retry on sweep", "session", sessionID, "err", err)
		_ = e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
			r.State, r.CloseReason, r.PaymentClosed, r.RunnerTerminated = sessionstore.StateWindingDown, reason, true, true
			return nil
		})
		return
	}
	e.release(rec.CapacityRef)
	if e.cfg.OnWinddown != nil {
		e.cfg.OnWinddown(ended, reason)
	}
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(sessionID, "session.ended", map[string]any{
			"state":        state,
			"close_reason": reason,
		})
	}
}

// ---------------------------------------------------------------------------
// Sweep — lease + heartbeat enforcement, retention

// Sweep enforces leases and heartbeats across all active sessions and
// evicts terminal records past retention. Call on a ticker.
func (e *Engine) Sweep(ctx context.Context) {
	now := e.cfg.Now()
	type due struct {
		id     string
		reason string
	}
	var dues []due
	_ = e.cfg.Store.ForEach(func(r *sessionstore.Record) error {
		if r.Terminal() {
			return nil
		}
		if r.State == sessionstore.StateWindingDown {
			// Obligations outstanding: retry them, under the reason
			// already recorded.
			dues = append(dues, due{r.SessionID, r.CloseReason})
			return nil
		}
		spec := e.cfg.Specs(r.SessionID)
		if spec == nil {
			return nil
		}
		hb := spec.heartbeat()
		// Precedence: when both triggers are due, heartbeat_lost wins.
		// A dead runner is the more specific fact and points the
		// operator at the runner; reporting lease_expired would point
		// them at funding instead.
		if now.Sub(r.LastEventAt) > hb*time.Duration(spec.missed()) {
			dues = append(dues, due{r.SessionID, ReasonHeartbeatLost})
			return nil
		}
		if r.OutputState == "stalled" && !r.OutputStalledAt.IsZero() && now.Sub(r.OutputStalledAt) >= OutputStallBackstop {
			dues = append(dues, due{r.SessionID, ReasonOutputFailed})
			return nil
		}
		// Lease grace = one heartbeat interval (paid-session §5): a
		// top-up in flight at expiry never loses the race.
		if !r.LeaseExpiresAt.IsZero() && now.After(r.LeaseExpiresAt.Add(hb)) {
			dues = append(dues, due{r.SessionID, ReasonLeaseExpired})
		}
		return nil
	})
	for _, d := range dues {
		mu := e.sessionMu(d.id)
		mu.Lock()
		e.winddownLocked(ctx, d.id, d.reason)
		mu.Unlock()
	}
	if n, err := e.cfg.Store.EvictTerminal(now.Add(-e.cfg.TerminalRetention)); err == nil && n > 0 {
		e.cfg.Log.Info("evicted terminal sessions", "count", n)
	}
}

// ---------------------------------------------------------------------------
// Recovery — rebind or explicit terminal

// Recover runs at startup: every non-terminal session is either safely
// rebound (nothing to do — the durable record is the rebind) or moved
// to the explicit terminal outcome when the runner no longer holds it.
// Never mints a second work_id, never re-issues grants (§9.2).
func (e *Engine) Recover(ctx context.Context) {
	e.recoverReservations(ctx)
	var ids, pending []string
	_ = e.cfg.Store.ForEach(func(r *sessionstore.Record) error {
		switch {
		case r.Terminal():
		case r.State == sessionstore.StateWindingDown:
			pending = append(pending, r.SessionID)
		default:
			ids = append(ids, r.SessionID)
		}
		return nil
	})
	for _, id := range pending {
		rec, err := e.cfg.Store.Get(id)
		if err != nil {
			continue
		}
		mu := e.sessionMu(id)
		mu.Lock()
		e.winddownLocked(ctx, id, rec.CloseReason)
		mu.Unlock()
	}
	for _, id := range ids {
		rec, err := e.cfg.Store.Get(id)
		if err != nil {
			continue
		}
		_, qerr := e.runnerFor(rec.BackendRef).QuerySession(ctx, rec.RunnerSessionID)
		switch {
		case qerr == nil:
			ac, ok := e.cfg.Payment.(payment.AccountClient)
			var authStatus *payment.SpendAuthorizationStatus
			var authErr error
			if ok {
				authStatus, authErr = ac.GetSpendAuthorization(ctx, rec.Sender, rec.AccountAuthorizationID)
			} else {
				authErr = errors.New("wholesale account payment extension unavailable")
			}
			if authErr != nil || authStatus == nil || authStatus.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
				e.cfg.Log.Error("session authorization unavailable after restart; failing closed", "session", id, "err", authErr)
				_ = e.cfg.Store.Update(id, func(r *sessionstore.Record) error {
					r.PaymentClosed = true
					r.AuthorizationReservedWei = "0"
					return nil
				})
				mu := e.sessionMu(id)
				mu.Lock()
				e.winddownLocked(ctx, id, ReasonRecoveryFailed)
				mu.Unlock()
				continue
			}
			e.cfg.Log.Info("session rebound after restart", "session", id, "work_id", rec.WorkID)
		case errors.Is(qerr, ErrRunnerSessionGone):
			mu := e.sessionMu(id)
			mu.Lock()
			e.winddownLocked(ctx, id, ReasonRecoveryFailed)
			mu.Unlock()
		default:
			// Runner unreachable: leave active; the heartbeat sweep
			// fails closed if events never resume.
			e.cfg.Log.Warn("recovery query failed; leaving session active for heartbeat enforcement",
				"session", id, "err", qerr)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers

func (e *Engine) runnerFor(backendRef string) RunnerClient {
	return e.cfg.Runner(backendRef)
}

func (e *Engine) release(ref string) {
	if ref != "" && e.cfg.ReleaseCapacity != nil {
		e.cfg.ReleaseCapacity(ref)
	}
}

func (e *Engine) callbackURL(sessionID string) string {
	base := e.cfg.Callback.BaseURL
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/v1/session/%s/events", trimSlash(base), sessionID)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func auditGrants(grants []Grant) []sessionstore.GrantAudit {
	out := make([]sessionstore.GrantAudit, 0, len(grants))
	for _, g := range grants {
		out = append(out, sessionstore.GrantAudit{
			ID:         g.ID,
			Operations: g.Operations,
			SecretHash: sessionstore.HashSecret(g.Secret),
			ExpiresAt:  g.ExpiresAt,
		})
	}
	return out
}

func randomSecret(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// recoverReservations undoes every open a crash abandoned between its
// reservation and its record (plan 0048 §2.2): what the recorded stage
// says was opened is closed, the capacity is released, and the
// reservation is dropped. A gateway retrying the open afterwards starts
// clean; one that never retries is not left funding a session nobody
// holds.
func (e *Engine) recoverReservations(ctx context.Context) {
	var abandoned []sessionstore.OpenReservation
	_ = e.cfg.Store.ForEachReservation(func(r sessionstore.OpenReservation) error {
		abandoned = append(abandoned, r)
		return nil
	})
	for _, r := range abandoned {
		if r.Stage == sessionstore.ReservationRunnerCreated && r.RunnerSessionID != "" {
			if err := e.runnerFor(r.BackendRef).TerminateSession(ctx, r.RunnerSessionID, ReasonOpenFailed); err != nil && !errors.Is(err, ErrRunnerSessionGone) {
				e.cfg.Log.Warn("abandoned open: runner terminate failed; leaving the reservation for the next start",
					"request_id", r.RequestID, "err", err)
				continue
			}
		}
		if r.Stage == sessionstore.ReservationPaid || r.Stage == sessionstore.ReservationRunnerCreated {
			var closeErr error
			if ac, ok := e.cfg.Payment.(payment.AccountClient); ok {
				_, closeErr = ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: r.Sender, AuthorizationID: r.WorkID, ActualUnits: 0, SettlementSeq: 1})
			} else {
				closeErr = errors.New("wholesale account payment extension unavailable")
			}
			if closeErr != nil {
				e.cfg.Log.Warn("abandoned open: payment close failed; leaving the reservation for the next start",
					"request_id", r.RequestID, "err", closeErr)
				continue
			}
		}
		e.release(r.CapacityRef)
		_ = e.cfg.Store.ReleaseReservation(r.RequestID)
		e.cfg.Log.Warn("abandoned open undone", "request_id", r.RequestID, "stage", r.Stage)
	}
}
