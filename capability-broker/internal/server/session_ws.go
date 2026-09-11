package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

// Control-WS binding (paid-session/v1 §8): an optional push mirror of
// the HTTP control surface. Attach requires the session credential;
// broker→gateway frames are session.usage.tick, session.output.health,
// session.balance, session.ended; gateway→broker frames are session.topup and
// session.end, each acknowledged. The HTTP surface stays authoritative
// — a gateway ignoring the WS loses nothing but latency.

// wsFrame is the wire shape both directions.
type wsFrame struct {
	Type string         `json:"type"`
	Body map[string]any `json:"body,omitempty"`
}

// sessionWSHub fans engine events out to attached gateway connections.
type sessionWSHub struct {
	mu    sync.Mutex
	conns map[string]map[*wsConn]struct{} // session id → connections
}

type wsConn struct {
	c  *websocket.Conn
	mu sync.Mutex // serializes writes (gorilla requires one writer)
}

func (w *wsConn) send(f wsFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.c.WriteJSON(f)
}

func newSessionWSHub() *sessionWSHub {
	return &sessionWSHub{conns: map[string]map[*wsConn]struct{}{}}
}

func (h *sessionWSHub) attach(sessionID string, c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[sessionID] == nil {
		h.conns[sessionID] = map[*wsConn]struct{}{}
	}
	h.conns[sessionID][c] = struct{}{}
}

func (h *sessionWSHub) detach(sessionID string, c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns[sessionID], c)
	if len(h.conns[sessionID]) == 0 {
		delete(h.conns, sessionID)
	}
}

// listeners returns the current connections for a session (copy).
func (h *sessionWSHub) listeners(sessionID string) []*wsConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*wsConn, 0, len(h.conns[sessionID]))
	for c := range h.conns[sessionID] {
		out = append(out, c)
	}
	return out
}

// broadcast pushes a frame to every attached connection; send failures
// drop silently (the reader loop reaps dead connections).
func (h *sessionWSHub) broadcast(sessionID string, f wsFrame) {
	for _, c := range h.listeners(sessionID) {
		if err := c.send(f); err != nil {
			log.Printf("session ws push failed session=%s type=%s: %v", sessionID, f.Type, err)
		}
	}
}

// onEngineEvent is wired as the engine's OnEvent hook. Usage ticks are
// followed by a session.balance frame so the §6 object reaches push
// consumers at least on every low/will_refuse transition (we emit it on
// every tick a listener is attached — strictly more than the minimum).
func (s *Server) onEngineEvent(sessionID, kind string, data map[string]any) {
	if s.sessionWS == nil || len(s.sessionWS.listeners(sessionID)) == 0 {
		return
	}
	s.sessionWS.broadcast(sessionID, wsFrame{Type: kind, Body: data})
	if kind == "session.usage.tick" {
		if rec, err := s.sessionStore.Get(sessionID); err == nil {
			if spec := s.specForRecord(rec); spec != nil {
				s.sessionWS.broadcast(sessionID, wsFrame{
					Type: "session.balance",
					Body: s.balanceObjectCtx(context.Background(), rec, spec),
				})
			}
		}
	}
}

var sessionWSUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	// Gateways are servers, not browsers; Origin is meaningless here.
	CheckOrigin: func(*http.Request) bool { return true },
}

// handleSessionWS upgrades GET /v1/session/{id}/ws after credential
// auth (same uniform-401 discipline as the HTTP surface).
func (s *Server) handleSessionWS(w http.ResponseWriter, r *http.Request) {
	if s.sessionEngine == nil {
		writeUniformUnauthorized(w)
		return
	}
	id := r.PathValue("id")
	cred, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	rec, err := s.sessionStore.Get(id)
	if err != nil || cred == "" || !sessionstore.VerifySecret(rec.CredentialHash, cred) {
		writeUniformUnauthorized(w)
		return
	}
	conn, err := sessionWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error
	}
	wc := &wsConn{c: conn}
	s.sessionWS.attach(id, wc)
	defer func() {
		s.sessionWS.detach(id, wc)
		_ = conn.Close()
	}()

	for {
		var f wsFrame
		if err := conn.ReadJSON(&f); err != nil {
			return
		}
		s.handleSessionWSFrame(r.Context(), id, wc, f)
	}
}

// handleSessionWSFrame executes one gateway→broker frame and always
// answers with an ack or error frame (§8: every gateway-initiated frame
// is acknowledged).
func (s *Server) handleSessionWSFrame(ctx context.Context, sessionID string, wc *wsConn, f wsFrame) {
	fail := func(code, msg string) {
		_ = wc.send(wsFrame{Type: "error", Body: map[string]any{"op": f.Type, "code": code, "message": msg}})
	}
	switch f.Type {
	case "session.topup":
		// The WS is a mirror of the HTTP verb, so it carries the same
		// idempotency key — in-frame, since a frame has no headers. A
		// gateway that reconnects and re-sends must not fund twice.
		requestID, _ := f.Body["request_id"].(string)
		if requestID == "" {
			fail("request_id_required", "body.request_id is required")
			return
		}
		authHeader, _ := f.Body["authorization_header"].(string)
		authorizationBytes, err := base64.StdEncoding.DecodeString(authHeader)
		if err != nil || len(authorizationBytes) == 0 {
			fail(livepeerheader.ErrAuthorizationRequired, "body.authorization_header must be a base64 Livepeer-Authorization")
			return
		}
		var paymentBytes []byte
		if hdr, _ := f.Body["payment_header"].(string); hdr != "" {
			paymentBytes, err = base64.StdEncoding.DecodeString(hdr)
			if err != nil {
				fail("payment_invalid", "body.payment_header must be base64 payment bytes")
				return
			}
		}
		rec, err := s.sessionStore.Get(sessionID)
		if err != nil {
			fail("session_not_found", err.Error())
			return
		}
		var auth paymentsv1.SpendAuthorization
		if proto.Unmarshal(authorizationBytes, &auth) != nil || auth.GetPayload() == nil {
			fail("payment_invalid", "authorization revision is malformed")
			return
		}
		p := auth.GetPayload()
		spec := s.specForRecord(rec)
		emptyDigest := sha256.Sum256(nil)
		if spec == nil || p.GetDomain() != "livepeer-spend-authorization/v1" || p.GetChainId() == 0 || p.GetDenomination() != "wei" || p.GetProtocol() != sessionProtocol || p.GetRequestId() != requestID || p.GetSessionId() != rec.GatewaySessionID || p.GetCapability() != rec.Capability || p.GetOffering() != rec.Offering || strings.TrimRight(p.GetBrokerUri(), "/") != strings.TrimRight(s.cfg.ExternalBaseURL, "/") || !bytes.Equal(p.GetRequestDigest(), emptyDigest[:]) {
			fail(livepeerheader.ErrPaymentEnvelopeMismatch, "authorization revision scope does not match session.topup")
			return
		}
		price := p.GetAcceptedPrice()
		wantPer := spec.PerUnits
		if wantPer == 0 {
			wantPer = 1
		}
		if price == nil || price.GetCapability() != rec.Capability || price.GetOffering() != rec.Offering || price.GetWorkUnitName() != spec.WorkUnit || price.GetUnitsPerPrice() != wantPer || new(big.Int).SetBytes(price.GetPricePerUnitWei().GetValue()).Cmp(spec.PricePerWorkUnitWei) != 0 {
			fail(livepeerheader.ErrPaymentEnvelopeMismatch, "authorization revision price does not match session")
			return
		}
		if err := middleware.ValidateCallerProof(authorizationBytes, p.GetCallerPublicKey(), stringValue(f.Body["caller_proof"])); err != nil {
			fail("payment_invalid", err.Error())
			return
		}
		res, err := s.sessionEngine.ReviseAuthorization(ctx, sessionID, requestID, authorizationBytes, paymentBytes, sessionRunwayReservation(p, spec))
		if err != nil {
			fail(sessionErrCode(err), err.Error())
			return
		}
		_ = wc.send(wsFrame{Type: "ack", Body: map[string]any{
			"op":    "session.topup",
			"lease": map[string]any{"expires_at": res.Lease.Format(time.RFC3339)},
		}})
	case "session.end":
		reason, _ := f.Body["reason"].(string)
		final, err := s.sessionEngine.End(ctx, sessionID, reason)
		if err != nil {
			fail(sessionErrCode(err), err.Error())
			return
		}
		_ = wc.send(wsFrame{Type: "ack", Body: map[string]any{
			"op":           "session.end",
			"state":        final.State,
			"close_reason": final.CloseReason,
		}})
	default:
		fail("unknown_frame", "unsupported frame type "+f.Type)
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

// sessionErrCode maps engine errors to stable frame codes.
func sessionErrCode(err error) string {
	var pe *sessionengine.ProtocolError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return "internal_error"
}

// balanceObjectCtx is balanceObject without an *http.Request (push path).
func (s *Server) balanceObjectCtx(ctx context.Context, rec *sessionstore.Record, spec *sessionengine.OfferingSpec) map[string]any {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://internal/balance", nil)
	return s.balanceObject(req, rec, spec)
}
