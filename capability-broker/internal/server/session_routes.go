package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
)

// paid-session/v1 HTTP surface. The engine is the authority; these
// handlers translate wire ↔ engine and enforce transport auth:
// gateway calls carry the session credential, runner events carry the
// per-session callback token, and unknown-session vs bad-secret are
// indistinguishable 401s (no existence oracle).

const sessionProtocol = "paid-session/v1"

func (s *Server) registerSessionRoutes() {
	open := middleware.Chain(
		middleware.Recover, middleware.RequestID, middleware.Metrics, middleware.Headers,
	)(http.HandlerFunc(s.handleSessionOpen))
	s.mux.Handle("POST /v1/session", open)
	s.mux.HandleFunc("GET /v1/session/{id}", s.handleSessionStatus)
	s.mux.HandleFunc("POST /v1/session/{id}/topup", s.handleSessionTopUp)
	s.mux.HandleFunc("POST /v1/session/{id}/end", s.handleSessionEnd)
	s.mux.HandleFunc("POST /v1/session/{id}/events", s.handleSessionEvents)
	s.mux.HandleFunc("GET /v1/session/{id}/ws", s.handleSessionWS)
}

// sessionCapability finds the paid-session capability tuple.
func (s *Server) sessionCapability(capID, offID string) *config.Capability {
	cfg := s.currentConfig()
	for i := range cfg.Capabilities {
		c := &cfg.Capabilities[i]
		if c.ID == capID && c.OfferingID == offID &&
			strings.HasPrefix(c.Protocol, "paid-session/") && c.Session != nil {
			return c
		}
	}
	return nil
}

// specFromCapability maps host config to the engine's offering spec.
func specFromCapability(c *config.Capability) *sessionengine.OfferingSpec {
	price, _ := new(big.Int).SetString(c.Price.AmountWei, 10)
	hb := time.Duration(c.Session.Heartbeat.IntervalSeconds) * time.Second
	return &sessionengine.OfferingSpec{
		Capability:          c.ID,
		Offering:            c.OfferingID,
		BackendRef:          c.ID + "|" + c.OfferingID,
		WorkUnit:            c.WorkUnit.Name,
		PricePerWorkUnitWei: price,
		PerUnits:            c.Price.PerUnits,
		DescriptorSchema:    c.Session.DescriptorSchema,
		HeartbeatInterval:   hb,
		MissedThreshold:     c.Session.Heartbeat.MissedThreshold,
		BurnRatePerSecond:   c.Session.BurnRatePerSec,
		LeaseMax:            time.Duration(c.Session.LeaseMaxSeconds) * time.Second,
		LeasePolicy:         c.Session.AdvertisedLeasePolicy(),
		Refill:              c.Session.AdvertisedRefill(),
		Metering:            c.Session.AdvertisedMetering(),
		RunnerPaths: sessionengine.RunnerPaths{
			Create:    c.Session.Runner.CreatePath,
			Status:    c.Session.Runner.StatusPath,
			Terminate: c.Session.Runner.TerminatePath,
		},
		MinRunwayUnits: c.Session.MinRunwayUnits,
		MaxRotations:   c.Session.MaxRotations,
	}
}

// runnerClientFor builds the configured-path runner client for a
// backendRef ("capability|offering"). Auth: backend.auth env:// bearer.
func (s *Server) runnerClientFor(backendRef string) sessionengine.RunnerClient {
	capID, offID, _ := strings.Cut(backendRef, "|")
	c := s.sessionCapability(capID, offID)
	if c == nil {
		return &unroutableRunner{ref: backendRef}
	}
	token := ""
	if c.Backend.Auth.Method == "bearer" {
		if v, ok := strings.CutPrefix(c.Backend.Auth.SecretRef, "env://"); ok {
			token = os.Getenv(v)
		}
	}
	return &sessionengine.HTTPRunnerClient{
		BaseURL: c.Backend.URL,
		Paths: sessionengine.RunnerPaths{
			Create:    c.Session.Runner.CreatePath,
			Status:    c.Session.Runner.StatusPath,
			Terminate: c.Session.Runner.TerminatePath,
		},
		AuthToken: token,
	}
}

// unroutableRunner reports a backendRef whose capability disappeared
// from config (e.g. removed by a runtime reload while sessions ran).
type unroutableRunner struct{ ref string }

func (u *unroutableRunner) CreateSession(context.Context, sessionengine.RunnerCreateRequest) (*sessionengine.RunnerCreateResult, error) {
	return nil, fmt.Errorf("no backend for %s", u.ref)
}
func (u *unroutableRunner) QuerySession(context.Context, string) (*sessionengine.RunnerStatus, error) {
	return nil, fmt.Errorf("no backend for %s", u.ref)
}
func (u *unroutableRunner) TerminateSession(context.Context, string, string) error {
	return nil // nothing to terminate; treat as idempotent success
}

// ---------------------------------------------------------------------------
// handlers

type sessionOpenBody struct {
	GatewaySessionID string          `json:"gateway_session_id"`
	SessionParams    json.RawMessage `json:"session_params,omitempty"`
}

func (s *Server) handleSessionOpen(w http.ResponseWriter, r *http.Request) {
	if s.sessionEngine == nil {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported,
			livepeerheader.ErrProtocolUnsupported, "no paid-session capabilities are served")
		return
	}
	if p := r.Header.Get(livepeerheader.Protocol); p != sessionProtocol {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported,
			livepeerheader.ErrProtocolUnsupported, "this endpoint serves "+sessionProtocol+"; got "+p)
		return
	}
	capID := r.Header.Get(livepeerheader.Capability)
	offID := r.Header.Get(livepeerheader.Offering)
	c := s.sessionCapability(capID, offID)
	if c == nil {
		livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed,
			"no paid-session offering "+capID+"/"+offID)
		return
	}
	paymentBytes, err := base64.StdEncoding.DecodeString(r.Header.Get(livepeerheader.Payment))
	if err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
			"Livepeer-Payment is not valid base64")
		return
	}
	if spec, ok := s.lookupSpec(capID, offID); ok {
		if err := middleware.ValidateExpectedPriceForRequest(paymentBytes, capID, offID, spec); err != nil {
			livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch,
				"expected price mismatch: "+err.Error())
			return
		}
	}
	var body sessionOpenBody
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || (len(raw) > 0 && json.Unmarshal(raw, &body) != nil) {
		livepeerheader.WriteBadRequest(w, "body must be a JSON object")
		return
	}

	// An omitted gateway_session_id reaches the same failure as a
	// colliding one, by the other road: the session opens, work is
	// served, and the settlement carries an empty value for the only
	// key its consumer issued itself — a record that can never be bound
	// to the session it is evidence for.
	//
	// It was refused only when it collided, so a client that never sent
	// the field at all got no signal from anywhere. Two such clients are
	// not even detected as colliding: the broker retains two
	// unresolvable records instead of refusing one open.
	if strings.TrimSpace(body.GatewaySessionID) == "" {
		livepeerheader.WriteBadRequest(w,
			"gateway_session_id is required: it is the only settlement identifier you issue "+
				"yourself, and without it the signed record cannot be bound to your session")
		return
	}

	res, err := s.sessionEngine.Open(r.Context(), sessionengine.OpenRequest{
		RequestID:        r.Header.Get(livepeerheader.RequestID),
		GatewaySessionID: body.GatewaySessionID,
		SessionParams:    body.SessionParams,
		PaymentBytes:     paymentBytes,
		Spec:             specFromCapability(c),
	})
	if err != nil {
		observability.RecordSessionOpen("failed")
		s.writeSessionError(w, err)
		return
	}
	if res.Replayed {
		observability.RecordSessionOpen("replayed")
	} else {
		observability.RecordSessionOpen("opened")
	}

	rec, _ := s.sessionStore.Get(res.SessionID)
	// Grants is omitted rather than emitted as null when a descriptor
	// carries none. A map[string]any has no omitempty, so a nil slice
	// marshalled to `"grants": null` — and the schema requires an array
	// when the key is present, so a consumer validating the descriptor
	// rejected an otherwise good open. Most descriptors have grants;
	// the ones that do not were the broken case.
	runtime := map[string]any{
		"schema": res.Schema,
		"public": res.Public,
	}
	if len(res.Grants) > 0 {
		runtime["grants"] = res.Grants
	}
	resp := map[string]any{
		"session_id": res.SessionID,
		"work_id":    res.WorkID,
		"state":      res.State,
		"runtime":    runtime,
		"lease":      map[string]any{"expires_at": res.Lease.Format(time.RFC3339)},
		"control":    s.sessionControlURLs(res.SessionID),
	}
	// Re-delivered on a replay too: an idempotent open converges on the
	// usable recorded outcome, or a lost response leaves a funded
	// session nobody can drive (paid-session §3.1).
	if res.Credential != "" {
		resp["credential"] = res.Credential
	}
	if rec != nil {
		resp["balance"] = s.balanceObject(r, rec, specFromCapability(c))
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authSession(w, r)
	if !ok {
		return
	}
	spec := s.specForRecord(rec)
	resp := map[string]any{
		"session_id":         rec.SessionID,
		"gateway_session_id": rec.GatewaySessionID,
		"work_id":            rec.WorkID,
		"state":              rec.State,
		"runtime": map[string]any{
			"schema": rec.DescriptorSchema,
			"public": rec.DescriptorPublic,
		},
		"usage": map[string]any{
			"unit":          rec.Unit,
			"claimed_total": rec.ClaimedTotal,
		},
		"lease":      map[string]any{"expires_at": rec.LeaseExpiresAt.Format(time.RFC3339)},
		"started_at": rec.CreatedAt.Format(time.RFC3339),
	}
	if rec.RotationGeneration > 0 {
		// Recorded, not announced: a completed rotation is settlement-only
		// from the customer's side (paid-session §3.3 rotation rules).
		resp["rotation"] = map[string]any{
			"generation":          rec.RotationGeneration,
			"predecessor_work_id": rec.PredecessorWorkID,
		}
	}
	if spec != nil {
		resp["balance"] = s.balanceObject(r, rec, spec)
	}
	if rec.Terminal() {
		resp["ended_at"] = rec.EndedAt.Format(time.RFC3339)
		resp["close_reason"] = rec.CloseReason
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSessionTopUp(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authSession(w, r)
	if !ok {
		return
	}
	paymentBytes, err := base64.StdEncoding.DecodeString(r.Header.Get(livepeerheader.Payment))
	if err != nil || len(paymentBytes) == 0 {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
			"missing or invalid Livepeer-Payment header")
		return
	}
	res, err := s.sessionEngine.TopUpRebind(r.Context(), rec.SessionID,
		r.Header.Get(livepeerheader.RequestID),
		r.Header.Get(livepeerheader.RebindFrom), paymentBytes)
	if err != nil {
		s.writeSessionError(w, err)
		return
	}
	// Reload: a top-up that rebinds moves the session onto a new payment
	// identity, and rec is the pre-rebind snapshot. Returning its
	// work_id hands the gateway back the predecessor it just rotated
	// away from, which it would then keep minting against.
	fresh, _ := s.sessionStore.Get(rec.SessionID)
	workID := rec.WorkID
	if fresh != nil {
		workID = fresh.WorkID
	}
	resp := map[string]any{
		"session_id": rec.SessionID,
		"work_id":    workID,
		"lease":      map[string]any{"expires_at": res.Lease.Format(time.RFC3339)},
	}
	if spec := s.specForRecord(rec); spec != nil && fresh != nil {
		resp["balance"] = s.balanceObject(r, fresh, spec)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSettlement serves a session's settlement record by session_id or
// by any work_id the session has held.
//
// It exists because the record's normal path — a response header — runs
// through a customer-controlled SDK that can drop it, and because after a
// rotation a reader may hold a work_id that is no longer current.
// Authorisation is the session credential, same as every other session
// read; the record is regenerated per query rather than cached, so its
// issued_at is a statement about now.
func (s *Server) handleSettlement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// A paid-job exchange first. A streamed job delivers its terminal
	// claim in an HTTP trailer, which Go reads and HTTPX, Fetch and
	// reqwest do not — so a caller that could not see it asks here
	// instead, keyed on the job id the broker handed back. Possession of
	// that id is the authorisation: it is broker-minted and unguessable,
	// and the record's integrity comes from its signature rather than
	// from the channel.
	if s.jobIdem != nil {
		if job, jerr := s.jobIdem.ByJobID(id); jerr == nil && job != nil {
			s.writeJobSettlement(w, job)
			return
		}
	}

	rec, err := s.sessionStore.Get(id)
	if err != nil {
		// Then the gateway's own session id. This is the key a
		// clearinghouse actually holds: session_id is broker-local and
		// reaches it only through the customer-controlled SDK. It is
		// tried before work_id because it is unique by construction,
		// while a work_id is shared by every session on one ticket
		// session.
		rec, err = s.sessionStore.GetByGatewaySessionID(id)
	}
	if err != nil {
		// Last, a payment identity, current or superseded.
		rec, err = s.sessionStore.GetByWorkID(id)
	}
	if errors.Is(err, sessionstore.ErrAmbiguous) {
		// Answering with one of them would be a correctly signed record
		// for the WRONG session, which a caller cannot detect from the
		// record alone. Naming the usable key matters too: a caller told
		// only "ambiguous" has nothing to try next.
		livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrAmbiguousIdentifier,
			"this work_id covers more than one session; query by gateway_session_id or session_id")
		return
	}
	if err != nil || rec == nil {
		writeUniformUnauthorized(w)
		return
	}
	// Authorization is possession of the id, not the session credential.
	//
	// The direct query exists so a clearinghouse can read a settlement
	// WITHOUT it crossing the customer's SDK. A clearinghouse is not the
	// gateway and does not hold the gateway's credential, so requiring
	// it would defeat the reason the surface exists — and it would differ
	// from the paid-job side, where possession of the broker-minted job
	// id is the bar.
	//
	// The id is broker-minted and unguessable, and the record's
	// integrity comes from its signature rather than from this channel,
	// so possession is the same bar the job path already sets. An
	// operator wanting caller authentication puts mTLS in front; the
	// contract does not change.
	spec := s.specForRecord(rec)
	if spec == nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"no offering spec for this session")
		return
	}
	set := s.sessionEngine.SettlementFor(rec, spec)
	if set == nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"settlement unavailable")
		return
	}
	encoded, err := settlement.Encode(set, s.settlementSigner)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"encode settlement: "+err.Error())
		return
	}
	w.Header().Set(livepeerheader.Settlement, encoded)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":          set.GetSessionId(),
		"gateway_session_id":  set.GetGatewaySessionId(),
		"work_id":             set.GetWorkId(),
		"predecessor_work_id": set.GetPredecessorWorkId(),
		"rotation_generation": set.GetRotationGeneration(),
		"state":               set.GetState(),
		"unit":                set.GetWorkUnitName(),
		"claimed_units":       set.GetClaimedUnits(),
		"debited_units":       set.GetDebitedUnits(),
		"billed_value_wei":    new(big.Int).SetBytes(set.GetBilledValueWei().GetValue()).String(),
		"amount_wei":          new(big.Int).SetBytes(set.GetAmountWei().GetValue()).String(),
		"per_units":           set.GetPerUnits(),
		"settlement_seq":      set.GetSettlementSeq(),
		"issued_at":           set.GetIssuedAt(),
	})
}

func (s *Server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.authSession(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	_ = json.Unmarshal(raw, &body)
	final, err := s.sessionEngine.End(r.Context(), rec.SessionID, body.Reason)
	if err != nil {
		s.writeSessionError(w, err)
		return
	}
	// The final settlement rides the close, so a gateway that ends a
	// session holds its accounting without a second call. It is also
	// retrievable afterwards (GET /v1/settlement/{id}) — a settlement
	// delivered once through a channel that can drop it is not a
	// settlement a clearinghouse can rely on.
	if set, err := s.sessionEngine.RecordSettlement(r.Context(), rec.SessionID); err == nil && set != nil {
		if encoded, err := settlement.Encode(set, s.settlementSigner); err == nil {
			w.Header().Set(livepeerheader.Settlement, encoded)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":   final.SessionID,
		"work_id":      final.WorkID,
		"state":        final.State,
		"close_reason": final.CloseReason,
		"ended_at":     final.EndedAt.Format(time.RFC3339),
	})
}

// writeJobSettlement answers a settlement query for a paid-job exchange.
//
// An exchange still in flight reports its state rather than a claim: a
// caller must be able to distinguish "not finished" from "finished at
// zero", because treating the second as the first bills for nothing and
// treating the first as the second fails open.
func (s *Server) writeJobSettlement(w http.ResponseWriter, job *sessionstore.JobRecord) {
	if job.State == sessionstore.JobAccountingPending {
		// Delivered, not yet settled. 202 with a distinct state because
		// the remedy differs from a job still running: there is nothing
		// left to wait for from the backend, only from the ledger, and
		// the exchange WILL reach a terminal settlement — either signed
		// after the debit lands, or DEBIT_FAILED once retries are spent.
		// A clearinghouse holds the encumbrance until then instead of
		// booking or writing off.
		body := map[string]any{
			"job_id":     job.JobID,
			"state":      job.State,
			"work_units": job.WorkUnits,
			"unit":       job.Unit,
		}
		if job.Pending != nil {
			body["debit_attempts"] = job.Pending.Attempts
		}
		w.Header().Set(livepeerheader.Error, livepeerheader.ErrAccountingPending)
		writeJSON(w, http.StatusAccepted, body)
		return
	}
	if job.State != sessionstore.JobTerminal {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"job_id": job.JobID,
			"state":  job.State,
		})
		return
	}
	if job.Settlement != "" {
		w.Header().Set(livepeerheader.Settlement, job.Settlement)
	}
	w.Header().Set(livepeerheader.JobID, job.JobID)
	w.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(job.WorkUnits, 10))
	w.Header().Set(livepeerheader.WorkUnitName, job.Unit)
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":     job.JobID,
		"state":      job.State,
		"status":     job.Status,
		"work_units": job.WorkUnits,
		"unit":       job.Unit,
		"ended_at":   job.EndedAt.Format(time.RFC3339),
		// Present only when the exchange produced one: a stub payment
		// has no settlement to sign, and a caller must not read an
		// absent envelope as an empty claim.
		"settlement": job.Settlement,
	})
}

type sessionEventBody struct {
	EventID   string `json:"event_id"`
	Sequence  uint64 `json:"sequence"`
	EventType string `json:"event_type"`
	EventTime string `json:"event_time"`
	State     string `json:"state"`
	Usage     *struct {
		Unit  string `json:"unit"`
		Total uint64 `json:"total"`
	} `json:"usage"`
	CloseReason string `json:"close_reason"`
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	// Auth before any session-existence disclosure: unknown session and
	// bad token produce identical 401s.
	id := r.PathValue("id")
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	rec, err := s.sessionStore.Get(id)
	if err != nil || token == "" || !sessionstore.VerifySecret(rec.CallbackTokenHash, token) {
		observability.RecordSessionEvent("unauthorized")
		writeUniformUnauthorized(w)
		return
	}
	var body sessionEventBody
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, &body) != nil {
		livepeerheader.WriteBadRequest(w, "body must be a JSON event envelope")
		return
	}
	ev := sessionengine.Event{
		EventID:   body.EventID,
		Sequence:  body.Sequence,
		EventType: body.EventType,
		State:     body.State,
		Reason:    body.CloseReason,
	}
	if body.Usage != nil {
		ev.UsageUnit = body.Usage.Unit
		ev.UsageTot = &body.Usage.Total
	}
	out, err := s.sessionEngine.ProcessEvent(r.Context(), id, ev)
	if err != nil {
		var re *sessionengine.RetryableError
		if errors.As(err, &re) {
			observability.RecordSessionEvent("retryable")
		} else {
			observability.RecordSessionEvent("rejected")
		}
		s.writeSessionError(w, err)
		return
	}
	if out.Duplicate {
		observability.RecordSessionEvent("duplicate")
	} else {
		observability.RecordSessionEvent("accepted")
		observability.RecordSessionDebit(out.DebitedUnits)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted":  true,
		"duplicate": out.Duplicate,
		"terminal":  out.Terminal,
	})
}

// ---------------------------------------------------------------------------
// helpers

// authSession authenticates a gateway control call by session
// credential. Unknown session and bad credential are indistinguishable.
func (s *Server) authSession(w http.ResponseWriter, r *http.Request) (*sessionstore.Record, bool) {
	if s.sessionEngine == nil {
		writeUniformUnauthorized(w)
		return nil, false
	}
	id := r.PathValue("id")
	cred, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	rec, err := s.sessionStore.Get(id)
	if err != nil || cred == "" || !sessionstore.VerifySecret(rec.CredentialHash, cred) {
		writeUniformUnauthorized(w)
		return nil, false
	}
	return rec, true
}

func writeUniformUnauthorized(w http.ResponseWriter) {
	livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
		"unauthorized")
}

func (s *Server) specForRecord(rec *sessionstore.Record) *sessionengine.OfferingSpec {
	c := s.sessionCapability(rec.Capability, rec.Offering)
	if c == nil {
		return nil
	}
	return specFromCapability(c)
}

// balanceObject builds the normative balance object (paid-session §6).
func (s *Server) balanceObject(r *http.Request, rec *sessionstore.Record, spec *sessionengine.OfferingSpec) map[string]any {
	obj := map[string]any{
		"claimed_units": rec.ClaimedTotal,
		"debited_units": rec.DebitedTotal,
		"unit":          rec.Unit,
		// True whenever the next top-up would be refused — including
		// from open on a bounded offering, so the advertisement always
		// precedes the refusal.
		"will_refuse_next_refill": rec.Terminal() ||
			rec.State == sessionstore.StateWindingDown ||
			spec.Refill == "bounded",
	}
	status := "ok"
	if bal, err := s.payment.GetBalance(r.Context(), rec.Sender, rec.WorkID); err == nil && bal != nil &&
		spec.PricePerWorkUnitWei != nil && spec.PricePerWorkUnitWei.Sign() > 0 {
		runway := payment.RunwayUnits(bal, spec.PricePerWorkUnitWei, spec.PerUnits)
		obj["runway_units"] = runway
		burn := spec.BurnRatePerSecond
		if burn <= 0 {
			burn = 1
		}
		obj["runway_seconds_estimate"] = int64(float64(runway) / burn)
		switch {
		case runway <= 0:
			status = "exhausted"
		case spec.MinRunwayUnits > 0 && runway < spec.MinRunwayUnits*2:
			status = "low"
		}
	}
	obj["status"] = status
	return obj
}

func (s *Server) sessionControlURLs(sessionID string) map[string]string {
	base := strings.TrimRight(s.currentConfig().ExternalBaseURL, "/")
	wsBase := base
	if strings.HasPrefix(wsBase, "https://") {
		wsBase = "wss://" + strings.TrimPrefix(wsBase, "https://")
	} else if strings.HasPrefix(wsBase, "http://") {
		wsBase = "ws://" + strings.TrimPrefix(wsBase, "http://")
	}
	return map[string]string{
		"status_url": base + "/v1/session/" + sessionID,
		"topup_url":  base + "/v1/session/" + sessionID + "/topup",
		"end_url":    base + "/v1/session/" + sessionID + "/end",
		"events_ws":  wsBase + "/v1/session/" + sessionID + "/ws",
	}
}

func (s *Server) writeSessionError(w http.ResponseWriter, err error) {
	var pe *sessionengine.ProtocolError
	if errors.As(err, &pe) {
		status := http.StatusBadRequest
		code := pe.Code
		switch pe.Code {
		case "payment_invalid":
			status, code = http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid
		case "refill_refused":
			status, code = http.StatusConflict, livepeerheader.ErrRefillRefused
		case "session_terminal":
			status = http.StatusConflict
		case "request_id_reuse":
			// 400 per headers §error table — same as the job path.
			status, code = http.StatusBadRequest, livepeerheader.ErrRequestIDReuse
		case "request_id_required":
			status = http.StatusBadRequest
		case "rebind_refused":
			status, code = http.StatusConflict, livepeerheader.ErrRebindRefused
		case "recipient_rotated":
			status, code = http.StatusConflict, livepeerheader.ErrRecipientRotated
		case "gateway_session_id_reuse":
			// 409, not 400: the request is well formed and the id is
			// merely taken — the same shape as refill_refused, and a
			// caller distinguishes "retry with a new id" from "this
			// request is malformed" by the status alone.
			status, code = http.StatusConflict, livepeerheader.ErrGatewaySessionIDReuse
		}
		livepeerheader.WriteError(w, status, code, pe.Detail)
		return
	}
	var re *sessionengine.RetryableError
	if errors.As(err, &re) {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"transient failure; retry: "+re.Error())
		return
	}
	if errors.Is(err, sessionstore.ErrNotFound) {
		writeUniformUnauthorized(w)
		return
	}
	var de *sessionengine.DescriptorError
	if errors.As(err, &de) {
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable, de.Error())
		return
	}
	livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
