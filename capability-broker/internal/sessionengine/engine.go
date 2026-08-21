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
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/google/uuid"
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
	ReasonGatewayClose   = "gateway_close"
	ReasonRunnerEnded    = "runner_ended"
	ReasonRunnerFailed   = "runner_failed"
	ReasonLeaseExpired   = "lease_expired"
	ReasonHeartbeatLost  = "heartbeat_lost"
	ReasonInsufficient   = "insufficient_balance"
	ReasonRecoveryFailed = "recovery_failed"
	ReasonOpenFailed     = "open_failed"
	// ReasonPaymentUnrecoverable ends a session whose payment identity
	// cannot be recovered — a rotation that could not settle, or one
	// that exhausted its bound. It names the consequence, not the
	// mechanism: a recipient rotation is infrastructure the customer
	// neither caused nor can act on, so the detail belongs in settlement
	// and operator telemetry rather than in a close reason.
	ReasonPaymentUnrecoverable = "payment_unrecoverable"
)

// DefaultMaxRotations bounds rebinds when an offering does not say.
// Three is enough to ride out a payee restart or two; more than that in
// one session is a loop, not bad luck.
const DefaultMaxRotations = 3

const ()

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
	// MaxRotations caps rebinds onto a rotated payment identity; <=0
	// means DefaultMaxRotations.
	MaxRotations int

	// MinRunwayUnits is the SufficientBalance floor checked after each
	// debit; <=0 disables the check.
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
	// AllocDebitSeq allocates the next debit sequence for a work_id.
	// The seq space belongs to the work_id — two sessions can share one
	// — so this must come from durable per-work_id state, not from a
	// per-session counter. Nil falls back to a per-session increment,
	// which is correct only when no work_id is ever shared.
	AllocDebitSeq func(workID string) (uint64, error)
	// ReleaseCapacity releases a held capacity slot; nil is a no-op.
	ReleaseCapacity func(capacityRef string)
	// OnWinddown observes each terminal winddown's stable reason; nil
	// is a no-op. Used for metrics; never for control flow.
	OnWinddown func(reason string)
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

	fallbackSeqMu sync.Mutex
	fallbackSeq   map[string]uint64
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
	RequestID        string
	GatewaySessionID string
	SessionParams    json.RawMessage
	PaymentBytes     []byte
	Spec             *OfferingSpec
	CapacityRef      string
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
	if req.Spec == nil {
		return nil, errors.New("sessionengine: nil spec")
	}
	fingerprint := openFingerprint(req)
	if id, err := e.cfg.Store.SessionIDForRequest(req.RequestID); err == nil {
		return e.replayOpen(id, fingerprint)
	}

	now := e.cfg.Now()
	sessionID := "sess_" + uuid.NewString()
	// The payee daemon keys its session — and the recipient rand every
	// ticket in this payment was minted against — on the payment's
	// recipient_rand_hash. Minting our own id here binds the session to
	// a rand the sender never saw, so nothing it pays with can validate.
	// Stub payments carry no ticket params; the request id stands in,
	// exactly as the job path does.
	workID, ok := payment.DerivePayeeWorkID(req.PaymentBytes)
	if !ok {
		workID = req.RequestID
	}
	credential, err := randomSecret("sc_")
	if err != nil {
		return nil, err
	}
	callbackToken, err := randomSecret("cb_")
	if err != nil {
		return nil, err
	}

	// Payment first: no funded runway, no runner binding.
	if _, err := e.cfg.Payment.OpenSession(ctx, payment.OpenSessionRequest{
		WorkID:              workID,
		Capability:          req.Spec.Capability,
		Offering:            req.Spec.Offering,
		PricePerWorkUnitWei: req.Spec.PricePerWorkUnitWei,
		PerUnits:            req.Spec.PerUnits,
		WorkUnit:            req.Spec.WorkUnit,
	}); err != nil {
		return nil, &RetryableError{Err: fmt.Errorf("payment open: %w", err)}
	}
	payRes, err := e.cfg.Payment.ProcessPayment(ctx, payment.ProcessPaymentRequest{
		WorkID:       workID,
		PaymentBytes: req.PaymentBytes,
	})
	if err != nil {
		return nil, protoErr("payment_invalid", "payment rejected: %v", err)
	}
	// A daemon that rejects every ticket returns no error — it reports
	// the rejection in the result. Opening anyway produces a session
	// with no funded runway that dies at the first lease check, which
	// reads as a broker fault rather than the payment fault it is.
	if err := rejectionErr(payRes); err != nil {
		_ = e.cfg.Payment.CloseSession(ctx, payRes.Sender, workID)
		e.release(req.CapacityRef)
		return nil, err
	}
	sender := payRes.Sender

	failClosed := func(stage string, cause error, runnerSessionID string) error {
		if runnerSessionID != "" {
			_ = e.runnerFor(req.Spec.BackendRef).TerminateSession(ctx, runnerSessionID, ReasonOpenFailed)
		}
		_ = e.cfg.Payment.CloseSession(ctx, sender, workID)
		e.release(req.CapacityRef)
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
	desc, err := ParseDescriptor(created.Runtime, req.Spec.DescriptorSchema, req.Spec.DescriptorMaxBytes)
	if err != nil {
		return nil, failClosed("descriptor validation", err, created.RunnerSessionID)
	}

	lease := e.leaseFrom(ctx, now, sender, workID, req.Spec)

	rec := &sessionstore.Record{
		SessionID:           sessionID,
		GatewaySessionID:    req.GatewaySessionID,
		RunnerSessionID:     created.RunnerSessionID,
		WorkID:              workID,
		Capability:          req.Spec.Capability,
		Offering:            req.Spec.Offering,
		BackendRef:          req.Spec.BackendRef,
		Sender:              sender,
		CredentialHash:      sessionstore.HashSecret(credential),
		CallbackTokenHash:   sessionstore.HashSecret(callbackToken),
		OpenFingerprint:     fingerprint,
		ReplayMaterial:      sealedReplayMaterial(credential, desc.Grants),
		FundedWei:           creditedString(payRes),
		GenerationFundedWei: creditedString(payRes),
		DescriptorSchema:    desc.Schema,
		DescriptorPublic:    desc.Public,
		DescriptorPrivate:   desc.Private,
		Grants:              auditGrants(desc.Grants),
		Unit:                req.Spec.WorkUnit,
		LeaseExpiresAt:      lease,
		LastEventAt:         now,
		State:               sessionstore.StateActive,
		CapacityRef:         req.CapacityRef,
	}
	if err := e.cfg.Store.CreateIndexed(rec, req.RequestID); err != nil {
		if errors.Is(err, sessionstore.ErrExists) {
			// Concurrent open with the same request id won; converge.
			_ = e.runnerFor(req.Spec.BackendRef).TerminateSession(ctx, created.RunnerSessionID, ReasonOpenFailed)
			_ = e.cfg.Payment.CloseSession(ctx, sender, workID)
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
	return h.Sum(nil)
}

// allocDebitSeq allocates from the work_id's sequence space, falling
// back to a per-session increment when no allocator is wired — which is
// correct for a single session and is what the unit-test harness uses.
func (e *Engine) allocDebitSeq(workID string) (uint64, error) {
	if e.cfg.AllocDebitSeq != nil {
		return e.cfg.AllocDebitSeq(workID)
	}
	e.fallbackSeqMu.Lock()
	defer e.fallbackSeqMu.Unlock()
	if e.fallbackSeq == nil {
		e.fallbackSeq = map[string]uint64{}
	}
	e.fallbackSeq[workID]++
	return e.fallbackSeq[workID], nil
}

// leaseFrom computes the funding-tracking lease default: runway units at
// the declared burn rate, capped by the operator max (paid-session §5).
func (e *Engine) leaseFrom(ctx context.Context, now time.Time, sender []byte, workID string, spec *OfferingSpec) time.Time {
	max := now.Add(spec.leaseMax())
	// A fixed-policy offering grants its full window regardless of
	// funded runway; runway is managed out of band for these.
	if spec.LeasePolicy == "fixed" {
		return max
	}
	bal, err := e.cfg.Payment.GetBalance(ctx, sender, workID)
	if err != nil || bal == nil || spec.PricePerWorkUnitWei == nil || spec.PricePerWorkUnitWei.Sign() <= 0 {
		return max
	}
	units := payment.RunwayUnits(bal, spec.PricePerWorkUnitWei, spec.PerUnits)
	secs := float64(units) / spec.burnRate()
	lease := now.Add(time.Duration(secs * float64(time.Second)))
	if lease.After(max) {
		return max
	}
	return lease
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
}

// EventOutcome reports what processing decided.
type EventOutcome struct {
	Duplicate    bool
	DebitedUnits uint64
	Insufficient bool
	Terminal     bool
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
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()

	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if rec.Terminal() {
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
	if ev.UsageTot != nil {
		if ev.UsageUnit != rec.Unit {
			return nil, protoErr("usage_unit_mismatch", "unit %q does not match offering unit %q", ev.UsageUnit, rec.Unit)
		}
		if *ev.UsageTot < rec.ClaimedTotal {
			return nil, protoErr("usage_regression", "cumulative total %d below committed %d", *ev.UsageTot, rec.ClaimedTotal)
		}
		delta = *ev.UsageTot - rec.ClaimedTotal
	}

	var chargedWei *big.Int
	debitSeq := rec.DebitSeq
	if delta > 0 {
		// The seq space belongs to the work_id, not to this session: two
		// sessions opened from payments minted on one ticket session
		// share a work_id, and per-session counters would collide — the
		// payee would deduplicate the second session's debits away.
		//
		// Reserve durably before debiting, so a retry re-presents the
		// same number rather than allocating a fresh one.
		if rec.PendingDebitSeq != 0 {
			debitSeq = rec.PendingDebitSeq
		} else {
			next, err := e.allocDebitSeq(rec.WorkID)
			if err != nil {
				return nil, &RetryableError{Err: fmt.Errorf("debit seq: %w", err)}
			}
			if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
				r.PendingDebitSeq = next
				return nil
			}); err != nil {
				return nil, &RetryableError{Err: fmt.Errorf("reserve debit seq: %w", err)}
			}
			debitSeq = next
		}
		debitRes, err := e.cfg.Payment.DebitBalance(ctx, payment.DebitBalanceRequest{
			Sender:    rec.Sender,
			WorkID:    rec.WorkID,
			WorkUnits: int64(delta),
			DebitSeq:  debitSeq,
		})
		if debitRes != nil && debitRes.DebitedWei != nil {
			chargedWei = debitRes.DebitedWei
		}
		if err != nil {
			// Nothing committed: the retry really retries, with the
			// same sequence and the same debit_seq — the daemon
			// dedupes if the debit actually landed.
			return nil, &RetryableError{Err: fmt.Errorf("debit: %w", err)}
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
	}

	// The atomic commit point: dedup watermark, totals, and debit
	// progress move together or not at all.
	err = e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.LastEventID = ev.EventID
		r.LastSequence = ev.Sequence
		r.LastEventAt = now
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
	if err != nil {
		// Debit may have landed; the retry re-presents the same
		// sequence and debit_seq, the daemon dedupes, and the commit
		// retries. Exactly once either way.
		return nil, &RetryableError{Err: fmt.Errorf("commit: %w", err)}
	}

	out := &EventOutcome{DebitedUnits: delta}

	if terminalReason != "" {
		e.winddownLocked(ctx, sessionID, terminalReason)
		out.Terminal = true
		return out, nil
	}

	// Runway check on cadence: not lost on retry, because the retry of
	// a committed event returns Duplicate and the *next* usage event
	// re-evaluates.
	if delta > 0 && spec.MinRunwayUnits > 0 {
		res, err := e.cfg.Payment.SufficientBalance(ctx, payment.SufficientBalanceRequest{
			Sender:       rec.Sender,
			WorkID:       rec.WorkID,
			MinWorkUnits: spec.MinRunwayUnits,
		})
		if err == nil && !res.Sufficient {
			e.winddownLocked(ctx, sessionID, ReasonInsufficient)
			out.Insufficient = true
			out.Terminal = true
		}
	}
	return out, nil
}

// rejectionErr converts an all-rejected ticket batch into a protocol
// error. A partially-rejected batch still credits something and is left
// alone: the balance it produced is the honest one, and the session's
// own runway checks enforce the consequences.
//
// INVALID_RECIPIENT_RAND is called out by name because it is the
// signal that the payee rotated its recipient rand under a live payer —
// the case the rotation design has to answer. Until it does, saying so
// beats a generic rejection.
func rejectionErr(res *payment.ProcessPaymentResult) error {
	if res == nil || res.TicketsRejected == 0 || len(res.TicketStatus) == 0 {
		return nil
	}
	if int(res.TicketsRejected) < len(res.TicketStatus) {
		return nil
	}
	if res.DominantRejection == payment.PaymentRejectionReasonInvalidRecipientRand {
		// A code, not a message to match on: the gateway's remedy is
		// mechanical — re-fetch params, re-mint, retry declaring
		// Livepeer-Rebind-From.
		return protoErr("recipient_rotated",
			"payee rejected every ticket: its recipient rand rotated; re-fetch ticket params and rebind")
	}
	return protoErr("payment_invalid",
		"payee rejected every ticket (reason %d)", res.DominantRejection)
}

// ---------------------------------------------------------------------------
// Top-up

// TopUpResult carries the new funding state and lease.
type TopUpResult struct {
	Lease   time.Time
	Balance *big.Int
}

// TopUp credits the existing payee-side payment session for the same
// work_id and extends the lease (paid-session §3.3): funding and
// lifetime move together. Refused with refill_refused on terminal or
// winding-down sessions — never accept payment that won't be honored
// with lease.
//
// Idempotent on requestID. A retry after a lost response replays the
// recorded answer without touching the daemon; the same id with a
// different envelope is request_id_reuse. Without this a gateway had no
// safe retry: the identical envelope is absorbed by nonce replay, but a
// freshly minted one — what an SDK retry actually sends — funds twice.
func (e *Engine) TopUp(ctx context.Context, sessionID, requestID string, paymentBytes []byte) (*TopUpResult, error) {
	return e.topUp(ctx, sessionID, requestID, "", paymentBytes)
}

// TopUpRebind is TopUp carrying a declared recipient rotation:
// rebindFrom is the work_id the session is moving off. See rebindLocked
// for why the rotation is declared rather than inferred.
func (e *Engine) TopUpRebind(ctx context.Context, sessionID, requestID, rebindFrom string, paymentBytes []byte) (*TopUpResult, error) {
	return e.topUp(ctx, sessionID, requestID, rebindFrom, paymentBytes)
}

func (e *Engine) topUp(ctx context.Context, sessionID, requestID, rebindFrom string, paymentBytes []byte) (*TopUpResult, error) {
	if requestID == "" {
		return nil, protoErr("request_id_required", "Livepeer-Request-Id is required")
	}
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()

	fp := topUpFingerprint(sessionID, paymentBytes)
	if prior, err := e.cfg.Store.TopUpRecall(sessionID, requestID, fp); err != nil {
		if errors.Is(err, sessionstore.ErrRequestIDReuse) {
			return nil, protoErr("request_id_reuse", "request id reused with a different payment envelope")
		}
		return nil, err
	} else if prior != nil {
		bal, _ := new(big.Int).SetString(prior.BalanceWei, 10)
		return &TopUpResult{Lease: prior.LeaseExpiresAt, Balance: bal}, nil
	}

	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if rec.Terminal() || rec.State == sessionstore.StateWindingDown {
		return nil, protoErr("refill_refused", "session is %s (%s)", rec.State, rec.CloseReason)
	}
	spec := e.cfg.Specs(sessionID)
	if spec == nil {
		return nil, fmt.Errorf("sessionengine: no offering spec for session %s", sessionID)
	}
	// A bounded offering takes no top-up after open. The refusal is
	// never a surprise: balance advertises will_refuse_next_refill from
	// the moment the session opens (§3.3 — never accept payment we will
	// not honour with lease).
	//
	// A bounded session cannot survive a rotation either: the successor
	// identity starts at a zero balance and the predecessor's remaining
	// funding is stranded, so a rebind would mean paying twice for one
	// session. It is refused here and the session ends at its lease.
	if spec.Refill == "bounded" {
		return nil, protoErr("refill_refused", "offering declares refill: bounded")
	}

	if rebindFrom != "" {
		return e.rebindLocked(ctx, rec, spec, requestID, rebindFrom, fp, paymentBytes)
	}
	return e.topUpLocked(ctx, rec, spec, requestID, fp, paymentBytes)
}

// topUpLocked funds a session against the identity it already holds.
// Callers hold the session mutex.
func (e *Engine) topUpLocked(ctx context.Context, rec *sessionstore.Record, spec *OfferingSpec,
	requestID string, fp []byte, paymentBytes []byte) (*TopUpResult, error) {

	sessionID := rec.SessionID
	res, err := e.cfg.Payment.ProcessPayment(ctx, payment.ProcessPaymentRequest{
		WorkID:       rec.WorkID,
		PaymentBytes: paymentBytes,
	})
	if err != nil {
		return nil, protoErr("payment_invalid", "top-up rejected: %v", err)
	}
	// An all-rejected top-up must not extend the lease: the session
	// would run on runway nobody funded. Nonce replay is the exception —
	// it means the daemon already credited exactly this envelope, so the
	// lease it bought was already granted. That is the crash window
	// between a credit and its idempotency record, and answering with
	// the current state is what makes the retry safe.
	if err := rejectionErr(res); err != nil {
		if !alreadyCredited(res) {
			return nil, err
		}
		out := &TopUpResult{Lease: rec.LeaseExpiresAt, Balance: res.Balance}
		if err := e.cfg.Store.TopUpRecord(sessionID, requestID, fp,
			out.Lease, balanceString(out.Balance)); err != nil {
			return nil, err
		}
		return out, nil
	}
	now := e.cfg.Now()
	lease := e.leaseFrom(ctx, now, rec.Sender, rec.WorkID, spec)
	if lease.Before(rec.LeaseExpiresAt) {
		lease = rec.LeaseExpiresAt // a top-up never shortens the lease
	}
	if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.LeaseExpiresAt = lease
		addFunded(r, res)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := e.cfg.Store.TopUpRecord(sessionID, requestID, fp, lease, balanceString(res.Balance)); err != nil {
		return nil, err
	}
	return &TopUpResult{Lease: lease, Balance: res.Balance}, nil
}

// rebindLocked moves a live session onto a rotated payment identity.
// Callers hold the session mutex.
//
// The rotation is DECLARED (rebindFrom), never inferred from a payment
// whose identity differs from the session's: inference would absorb an
// ordinary gateway concurrency bug — session A retried with session B's
// freshly minted payment — as a silent identity change instead of
// refusing it.
//
// Three guards, of which the second does the real work:
//
//  1. rebindFrom is this session's current work_id;
//  2. the successor payment CREDITS. Tickets minted against a fake or
//     stale identity cannot validate, so a credit is proof the successor
//     is genuine — nothing the gateway asserts is trusted, and no
//     rotation bookkeeping has to survive a broker restart;
//  3. the sender the payee sealed matches the session's.
//
// Note what this must NOT do to verify: asking the payee for the stable
// tuple's ticket params mints a fresh rand when no ticket session is
// open, so the verification would itself cause a rotation.
func (e *Engine) rebindLocked(ctx context.Context, rec *sessionstore.Record, spec *OfferingSpec,
	requestID, rebindFrom string, fp []byte, paymentBytes []byte) (*TopUpResult, error) {

	if rebindFrom != rec.WorkID {
		return nil, protoErr("rebind_refused",
			"declared predecessor %q is not this session's payment identity", rebindFrom)
	}
	if err := e.rotationAllowed(ctx, rec, spec); err != nil {
		return nil, err
	}
	newWorkID, ok := payment.DerivePayeeWorkID(paymentBytes)
	if !ok {
		return nil, protoErr("rebind_refused", "payment carries no ticket params to rebind onto")
	}
	if newWorkID == rec.WorkID {
		// Nothing rotated. Not an error — the gateway may have declared
		// a rebind on a retry that raced the original — so this is an
		// ordinary top-up.
		return e.topUpLocked(ctx, rec, spec, requestID, fp, paymentBytes)
	}

	// Settle the predecessor before anything else. Carrying unsettled
	// work across an identity change would make the ledger unauditable,
	// and a rebind that cannot settle does not proceed.
	if outstanding := rec.ClaimedTotal - rec.DebitedTotal; outstanding > 0 {
		seq := rec.DebitSeq + 1
		if _, err := e.cfg.Payment.DebitBalance(ctx, payment.DebitBalanceRequest{
			Sender:    rec.Sender,
			WorkID:    rec.WorkID,
			WorkUnits: int64(outstanding),
			DebitSeq:  seq,
		}); err != nil {
			e.winddownLocked(ctx, rec.SessionID, ReasonPaymentUnrecoverable)
			return nil, protoErr("rebind_refused",
				"could not settle the predecessor identity; session wound down: %v", err)
		}
		if err := e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error {
			r.DebitedTotal += outstanding
			r.DebitSeq = seq
			return nil
		}); err != nil {
			return nil, err
		}
	}

	if _, err := e.cfg.Payment.OpenSession(ctx, payment.OpenSessionRequest{
		WorkID:              newWorkID,
		Capability:          spec.Capability,
		Offering:            spec.Offering,
		PricePerWorkUnitWei: spec.PricePerWorkUnitWei,
		PerUnits:            spec.PerUnits,
		WorkUnit:            spec.WorkUnit,
	}); err != nil {
		return nil, &RetryableError{Err: fmt.Errorf("rebind open: %w", err)}
	}
	res, err := e.cfg.Payment.ProcessPayment(ctx, payment.ProcessPaymentRequest{
		WorkID:       newWorkID,
		PaymentBytes: paymentBytes,
	})
	if err != nil {
		_ = e.cfg.Payment.CloseSession(ctx, rec.Sender, newWorkID)
		return nil, protoErr("payment_invalid", "rebind payment rejected: %v", err)
	}
	// Guard 2. An all-rejected batch proves nothing about the successor,
	// so the session stays where it is rather than moving onto an
	// identity that may not exist.
	if rejErr := rejectionErr(res); rejErr != nil {
		_ = e.cfg.Payment.CloseSession(ctx, rec.Sender, newWorkID)
		return nil, protoErr("rebind_refused",
			"successor identity did not credit: %v", rejErr)
	}
	// Guard 3.
	if !bytesEqual(res.Sender, rec.Sender) {
		_ = e.cfg.Payment.CloseSession(ctx, res.Sender, newWorkID)
		return nil, protoErr("rebind_refused", "successor payment is from a different sender")
	}

	now := e.cfg.Now()
	lease := e.leaseFrom(ctx, now, rec.Sender, newWorkID, spec)
	if lease.Before(rec.LeaseExpiresAt) {
		lease = rec.LeaseExpiresAt
	}
	// Persist the new binding BEFORE closing the predecessor. A crash
	// between the two leaks an open payee session that no longer bills;
	// the other order would strand a credit the session could never
	// spend.
	generation := rec.RotationGeneration + 1
	if err := e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error {
		r.PredecessorWorkID = r.WorkID
		r.WorkID = newWorkID
		r.RotationGeneration = generation
		r.GenerationStartUnits = r.DebitedTotal
		// Funding is per identity: the successor starts from what this
		// payment credited, while the cumulative total carries on.
		r.GenerationFundedWei = "0"
		addFunded(r, res)
		// debit_seq is per payee session: the successor's sequence space
		// starts fresh, and daemon idempotency is keyed
		// (sender, work_id, debit_seq) so nothing collides.
		r.DebitSeq = 0
		r.LeaseExpiresAt = lease
		return nil
	}); err != nil {
		return nil, err
	}
	if err := e.cfg.Payment.CloseSession(ctx, rec.Sender, rebindFrom); err != nil {
		e.cfg.Log.Warn("rebind: predecessor payee session left open",
			"session", rec.SessionID, "predecessor_work_id", rebindFrom, "err", err)
	}

	if err := e.cfg.Store.TopUpRecord(rec.SessionID, requestID, fp, lease, balanceString(res.Balance)); err != nil {
		return nil, err
	}
	e.cfg.Log.Info("session rebound after recipient rotation",
		"session", rec.SessionID, "generation", generation,
		"predecessor_work_id", rebindFrom, "work_id", newWorkID)
	if e.cfg.OnEvent != nil {
		// Control-plane signalling between broker and gateway, not a
		// session lifecycle event: a completed rotation is settlement-
		// only from the customer's side.
		e.cfg.OnEvent(rec.SessionID, "session.rebound", map[string]any{
			"rotation_generation": generation,
			"predecessor_work_id": rebindFrom,
			"work_id":             newWorkID,
		})
	}
	return &TopUpResult{Lease: lease, Balance: res.Balance}, nil
}

// rotationAllowed enforces the two bounds on rebinding. Both end the
// session rather than refusing in place, because a session that cannot
// take another identity has no way forward: its current one is already
// rejecting payment.
//
// The count bound is obvious. The other one is the useful one: a
// generation that delivered no units at all means the rebind bought
// nothing, and a payer that keeps funding rebinds against a payee that
// keeps rotating is in a loop that costs deposit and produces no work.
// One such round is enough to call it.
func (e *Engine) rotationAllowed(ctx context.Context, rec *sessionstore.Record, spec *OfferingSpec) error {
	max := spec.MaxRotations
	if max <= 0 {
		max = DefaultMaxRotations
	}
	if int(rec.RotationGeneration) >= max {
		e.winddownLocked(ctx, rec.SessionID, ReasonPaymentUnrecoverable)
		return protoErr("rebind_refused",
			"rotation bound reached (%d); session ended", max)
	}
	if rec.RotationGeneration > 0 && rec.DebitedTotal == rec.GenerationStartUnits {
		e.winddownLocked(ctx, rec.SessionID, ReasonPaymentUnrecoverable)
		return protoErr("rebind_refused",
			"previous rotation delivered no work; refusing to fund another and ending the session")
	}
	return nil
}

// addFunded accumulates credited value on a record. Callers hold the
// store update.
func addFunded(r *sessionstore.Record, res *payment.ProcessPaymentResult) {
	if res == nil || res.CreditedEV == nil || res.CreditedEV.Sign() <= 0 {
		return
	}
	r.FundedWei = addDecimal(r.FundedWei, res.CreditedEV)
	r.GenerationFundedWei = addDecimal(r.GenerationFundedWei, res.CreditedEV)
}

func addDecimal(current string, delta *big.Int) string {
	sum, ok := new(big.Int).SetString(current, 10)
	if !ok || sum == nil {
		sum = new(big.Int)
	}
	return sum.Add(sum, delta).String()
}

func creditedString(res *payment.ProcessPaymentResult) string {
	if res == nil || res.CreditedEV == nil {
		return "0"
	}
	return res.CreditedEV.String()
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
func topUpFingerprint(sessionID string, paymentBytes []byte) []byte {
	h := sha256.New()
	h.Write([]byte(sessionID))
	h.Write([]byte{0})
	h.Write(paymentBytes)
	return h.Sum(nil)
}

// alreadyCredited reports a rejection that means "this exact envelope
// was accepted before": every ticket bounced on nonce replay.
func alreadyCredited(res *payment.ProcessPaymentResult) bool {
	return res != nil && res.DominantRejection == payment.PaymentRejectionReasonNonceReplay
}

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
	if err := e.runnerFor(rec.BackendRef).TerminateSession(ctx, rec.RunnerSessionID, reason); err != nil {
		e.cfg.Log.Warn("runner terminate failed; continuing winddown",
			"session", sessionID, "err", err)
	}
	paymentClosed := rec.PaymentClosed
	if !paymentClosed {
		if err := e.cfg.Payment.CloseSession(ctx, rec.Sender, rec.WorkID); err != nil {
			e.cfg.Log.Warn("payment close failed; will retry on sweep",
				"session", sessionID, "err", err)
		} else {
			paymentClosed = true
		}
	}
	e.release(rec.CapacityRef)
	now := e.cfg.Now()
	state := sessionstore.StateEnded
	if reason == ReasonRunnerFailed || reason == ReasonRecoveryFailed {
		state = sessionstore.StateFailed
	}
	_ = e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.State = state
		r.CloseReason = reason
		// The replay window ends with the session. Secrets outliving
		// the thing they unlock is how a store becomes a liability.
		r.ReplayMaterial = nil
		r.PaymentClosed = paymentClosed
		r.EndedAt = now
		r.CapacityRef = ""
		return nil
	})
	if e.cfg.OnWinddown != nil {
		e.cfg.OnWinddown(reason)
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
	var ids []string
	_ = e.cfg.Store.ForEach(func(r *sessionstore.Record) error {
		if !r.Terminal() {
			ids = append(ids, r.SessionID)
		}
		return nil
	})
	for _, id := range ids {
		rec, err := e.cfg.Store.Get(id)
		if err != nil {
			continue
		}
		_, qerr := e.runnerFor(rec.BackendRef).QuerySession(ctx, rec.RunnerSessionID)
		switch {
		case qerr == nil:
			// Re-assert the payee-side session before accepting events.
			// OpenSession is idempotent by contract, so this is a no-op
			// when the payment daemon kept its state — and it is what
			// makes the broker survive a payment daemon that restarted
			// independently of us. Without it the first post-restart
			// debit fails with "session not found" and the runner
			// retries forever against a session nobody holds.
			if spec := e.cfg.Specs(id); spec != nil {
				res, oerr := e.cfg.Payment.OpenSession(ctx, payment.OpenSessionRequest{
					WorkID:              rec.WorkID,
					Capability:          rec.Capability,
					Offering:            rec.Offering,
					PricePerWorkUnitWei: spec.PricePerWorkUnitWei,
					PerUnits:            spec.PerUnits,
					WorkUnit:            rec.Unit,
				})
				// AlreadyOpen=false means the payment layer did not have
				// this session — it lost state. The session can no longer
				// be billed, so fail closed rather than serve unmetered.
				if oerr != nil || res == nil || !res.AlreadyOpen {
					e.cfg.Log.Warn("payment session could not be re-asserted on rebind; winding down",
						"session", id, "err", oerr)
					mu := e.sessionMu(id)
					mu.Lock()
					e.winddownLocked(ctx, id, ReasonRecoveryFailed)
					mu.Unlock()
					continue
				}
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
