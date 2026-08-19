// Package sessionengine implements the paid-session/v1 lifecycle:
// open, usage-claim intake with exactly-once debit, lease/heartbeat
// enforcement, and two-outcome restart recovery — over the durable
// sessionstore. The HTTP/control-plane surface lives in the server
// package; this package is the authority.
package sessionengine

import (
	"context"
	"crypto/rand"
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
)

// OfferingSpec is the resolved offering the engine serves a session
// under — the declared axes plus pricing, supplied by the composition
// root from host config.
type OfferingSpec struct {
	Capability          string
	Offering            string
	BackendRef          string
	WorkUnit            string
	PricePerWorkUnitWei *big.Int
	DescriptorSchema    string
	DescriptorMaxBytes  int
	HeartbeatInterval   time.Duration // default 10s
	MissedThreshold     int           // default 3
	// BurnRatePerSecond estimates units consumed per second for the
	// funding-tracking lease default. <=0 means 1.
	BurnRatePerSecond float64
	LeaseMax          time.Duration // operator cap; <=0 means 1h
	// LeasePolicy is "funding-tracking" (default) or "fixed".
	LeasePolicy string
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
	if id, err := e.cfg.Store.SessionIDForRequest(req.RequestID); err == nil {
		return e.replayOpen(id)
	}

	now := e.cfg.Now()
	sessionID := "sess_" + uuid.NewString()
	workID := uuid.NewString()
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
		SessionID:         sessionID,
		GatewaySessionID:  req.GatewaySessionID,
		RunnerSessionID:   created.RunnerSessionID,
		WorkID:            workID,
		Capability:        req.Spec.Capability,
		Offering:          req.Spec.Offering,
		BackendRef:        req.Spec.BackendRef,
		Sender:            sender,
		CredentialHash:    sessionstore.HashSecret(credential),
		CallbackTokenHash: sessionstore.HashSecret(callbackToken),
		DescriptorSchema:  desc.Schema,
		DescriptorPublic:  desc.Public,
		DescriptorPrivate: desc.Private,
		Grants:            auditGrants(desc.Grants),
		Unit:              req.Spec.WorkUnit,
		LeaseExpiresAt:    lease,
		LastEventAt:       now,
		State:             sessionstore.StateActive,
		CapacityRef:       req.CapacityRef,
	}
	if err := e.cfg.Store.CreateIndexed(rec, req.RequestID); err != nil {
		if errors.Is(err, sessionstore.ErrExists) {
			// Concurrent open with the same request id won; converge.
			_ = e.runnerFor(req.Spec.BackendRef).TerminateSession(ctx, created.RunnerSessionID, ReasonOpenFailed)
			_ = e.cfg.Payment.CloseSession(ctx, sender, workID)
			e.release(req.CapacityRef)
			if id, lerr := e.cfg.Store.SessionIDForRequest(req.RequestID); lerr == nil {
				return e.replayOpen(id)
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

func (e *Engine) replayOpen(sessionID string) (*OpenResult, error) {
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	return &OpenResult{
		SessionID: rec.SessionID,
		WorkID:    rec.WorkID,
		State:     rec.State,
		Schema:    rec.DescriptorSchema,
		Public:    rec.DescriptorPublic,
		Lease:     rec.LeaseExpiresAt,
		Replayed:  true,
	}, nil
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
	units := new(big.Int).Div(bal, spec.PricePerWorkUnitWei)
	secs := float64(units.Int64()) / spec.burnRate()
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

	debitSeq := rec.DebitSeq
	if delta > 0 {
		debitSeq++
		if _, err := e.cfg.Payment.DebitBalance(ctx, payment.DebitBalanceRequest{
			Sender:    rec.Sender,
			WorkID:    rec.WorkID,
			WorkUnits: int64(delta),
			DebitSeq:  debitSeq,
		}); err != nil {
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
func (e *Engine) TopUp(ctx context.Context, sessionID string, paymentBytes []byte) (*TopUpResult, error) {
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
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
	res, err := e.cfg.Payment.ProcessPayment(ctx, payment.ProcessPaymentRequest{
		WorkID:       rec.WorkID,
		PaymentBytes: paymentBytes,
	})
	if err != nil {
		return nil, protoErr("payment_invalid", "top-up rejected: %v", err)
	}
	now := e.cfg.Now()
	lease := e.leaseFrom(ctx, now, rec.Sender, rec.WorkID, spec)
	if lease.Before(rec.LeaseExpiresAt) {
		lease = rec.LeaseExpiresAt // a top-up never shortens the lease
	}
	if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.LeaseExpiresAt = lease
		return nil
	}); err != nil {
		return nil, err
	}
	return &TopUpResult{Lease: lease, Balance: res.Balance}, nil
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
