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
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
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
			if s.isQuarantined(capID, offID) {
				return nil // withheld: runner contradicts its configuration
			}
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
	resp := map[string]any{
		"session_id": res.SessionID,
		"work_id":    res.WorkID,
		"state":      res.State,
		"runtime": map[string]any{
			"schema": res.Schema,
			"public": res.Public,
			"grants": res.Grants,
		},
		"lease":   map[string]any{"expires_at": res.Lease.Format(time.RFC3339)},
		"control": s.sessionControlURLs(res.SessionID),
	}
	if !res.Replayed {
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
	res, err := s.sessionEngine.TopUp(r.Context(), rec.SessionID,
		r.Header.Get(livepeerheader.RequestID), paymentBytes)
	if err != nil {
		s.writeSessionError(w, err)
		return
	}
	fresh, _ := s.sessionStore.Get(rec.SessionID)
	resp := map[string]any{
		"session_id": rec.SessionID,
		"work_id":    rec.WorkID,
		"lease":      map[string]any{"expires_at": res.Lease.Format(time.RFC3339)},
	}
	if spec := s.specForRecord(rec); spec != nil && fresh != nil {
		resp["balance"] = s.balanceObject(r, fresh, spec)
	}
	writeJSON(w, http.StatusOK, resp)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":   final.SessionID,
		"work_id":      final.WorkID,
		"state":        final.State,
		"close_reason": final.CloseReason,
		"ended_at":     final.EndedAt.Format(time.RFC3339),
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
