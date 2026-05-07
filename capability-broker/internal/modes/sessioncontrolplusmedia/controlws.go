package sessioncontrolplusmedia

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// ControlWSConfig holds the broker-wide knobs for the control-WS path.
// Driver-owned; populated from CLI flags at server construction.
type ControlWSConfig struct {
	HeartbeatInterval         time.Duration
	MissedHeartbeatThreshold  int
	ReconnectWindow           time.Duration
	BackpressureDropAfter     time.Duration
	OutboundBufferMessages    int
	ReplayBufferMessages      int
	ReplayBufferBytes         int
	HandshakeTimeout          time.Duration
}

// DefaultControlWSConfig returns the recommended defaults from §10.1.
func DefaultControlWSConfig() ControlWSConfig {
	return ControlWSConfig{
		HeartbeatInterval:        10 * time.Second,
		MissedHeartbeatThreshold: 3,
		ReconnectWindow:          30 * time.Second,
		BackpressureDropAfter:    5 * time.Second,
		OutboundBufferMessages:   64,
		ReplayBufferMessages:     64,
		ReplayBufferBytes:        1 << 20,
		HandshakeTimeout:         10 * time.Second,
	}
}

// ControlEnvelope is the small fixed wrapper the broker parses on every
// WebSocket text frame. The payload `body` is opaque on the workload
// axis: the broker forwards it verbatim through the runner IPC.
type ControlEnvelope struct {
	Type string          `json:"type"`
	Seq  uint64          `json:"seq,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Reserved envelope types short-circuited by the broker.
const (
	TypeSessionStarted     = "session.started"
	TypeSessionEnd         = "session.end"
	TypeSessionEnded       = "session.ended"
	TypeSessionError       = "session.error"
	TypeSessionUsageTick   = "session.usage.tick"
	TypeSessionBalanceLow  = "session.balance.low"
	TypeSessionReconnected = "session.reconnected"

	TypeMediaNegotiateStart = "media.negotiate.start"
	TypeMediaSDPOffer       = "media.sdp.offer"
	TypeMediaSDPAnswer      = "media.sdp.answer"
	TypeMediaICECandidate   = "media.ice.candidate"
	TypeMediaReady          = "media.ready"
	TypeMediaFailed         = "media.failed"
)

// IsReserved reports whether the envelope type is broker-handled.
func IsReserved(t string) bool {
	switch t {
	case TypeSessionStarted, TypeSessionEnd, TypeSessionEnded,
		TypeSessionError, TypeSessionUsageTick,
		TypeSessionBalanceLow, TypeSessionReconnected,
		TypeMediaNegotiateStart, TypeMediaSDPOffer, TypeMediaSDPAnswer,
		TypeMediaICECandidate, TypeMediaReady, TypeMediaFailed:
		return true
	}
	return false
}

// ServeControlWS handles the GET upgrade at /v1/cap/{session_id}/control.
// The driver registers this on the broker's mux at server construction
// time; auth is the unguessable session_id in the path (§4.1, Q1).
func (d *Driver) ServeControlWS(w http.ResponseWriter, r *http.Request) {
	sessID := r.PathValue("session_id")
	if sessID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}
	rec := d.store.Get(sessID)
	if rec == nil {
		http.Error(w, "session not found", http.StatusUnauthorized)
		return
	}
	if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
		d.store.Remove(sessID)
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}

	wasActive := rec.IsActive()
	if wasActive {
		http.Error(w, "session already attached", http.StatusConflict)
		return
	}

	lastSeq := parseLastSeq(r)

	conn, err := d.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	rec.SetActive(true)
	defer func() {
		rec.SetActive(false)
	}()

	if d.backend != nil {
		d.backend.ReattachControl(sessID)
	}

	d.runControlWS(r.Context(), conn, rec, lastSeq)
}

// parseLastSeq reads the Last-Seq header (preferred) or last_seq query
// parameter on the upgrade request.
func parseLastSeq(r *http.Request) uint64 {
	if v := r.Header.Get("Last-Seq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	if v := r.URL.Query().Get("last_seq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// runControlWS owns the per-connection lifetime: reader pump, writer
// pump, heartbeat ticker, replay-on-reconnect, and graceful close.
func (d *Driver) runControlWS(parent context.Context, conn *websocket.Conn, rec *SessionRecord, lastSeq uint64) {
	defer conn.Close()
	defer func() {
		if !rec.Closing() && d.backend != nil {
			d.backend.DetachControl(rec.SessionID, websocket.CloseAbnormalClosure, "")
		}
	}()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	out := make(chan ControlEnvelope, d.cfg.OutboundBufferMessages)
	rec.mu.Lock()
	rec.outboundForRelay = out
	rec.mu.Unlock()
	defer func() {
		rec.mu.Lock()
		rec.outboundForRelay = nil
		rec.mu.Unlock()
	}()
	closeReason := newCloseReasonHolder()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); d.runReader(ctx, conn, rec, cancel, closeReason) }()
	go func() { defer wg.Done(); d.runWriter(ctx, conn, rec, out, cancel, closeReason) }()
	go func() { defer wg.Done(); d.runHeartbeat(ctx, conn, cancel, closeReason) }()

	if lastSeq > 0 {
		emitDirect(out, ControlEnvelope{
			Type: TypeSessionReconnected,
			Seq:  rec.NextSeq(),
		})
		for _, e := range rec.replay.Since(lastSeq) {
			env, err := decodeEnvelope(e.payload)
			if err != nil {
				continue
			}
			emitDirect(out, env)
		}
	} else {
		emitDirect(out, ControlEnvelope{
			Type: TypeSessionStarted,
			Seq:  rec.NextSeq(),
		})
		if rec.media != nil {
			emitDirect(out, ControlEnvelope{
				Type: TypeMediaNegotiateStart,
				Seq:  rec.NextSeq(),
			})
			go rec.media.emitLocalCandidates(ctx, out)
		}
	}

	if rec.control != nil {
		go d.relayBackendOutbound(ctx, rec, out)
	}

	wg.Wait()
	code, reason, set := closeReason.Get()
	d.applyCloseReason(conn, code, reason, set)
}

// runReader reads frames from the WS, parses envelopes, short-circuits
// reserved types, and forwards workload envelopes to the backend.
func (d *Driver) runReader(ctx context.Context, conn *websocket.Conn, rec *SessionRecord, cancel context.CancelFunc, cr *closeReasonHolder) {
	defer cancel()
	conn.SetReadLimit(1 << 20)
	pongDeadline := func() time.Time {
		if d.cfg.HeartbeatInterval <= 0 {
			return time.Time{}
		}
		w := time.Duration(d.cfg.MissedHeartbeatThreshold+1) * d.cfg.HeartbeatInterval
		return time.Now().Add(w)
	}
	if dl := pongDeadline(); !dl.IsZero() {
		_ = conn.SetReadDeadline(dl)
	}
	conn.SetPongHandler(func(string) error {
		if dl := pongDeadline(); !dl.IsZero() {
			_ = conn.SetReadDeadline(dl)
		}
		return nil
	})

	for {
		if ctx.Err() != nil {
			return
		}
		mt, data, err := conn.ReadMessage()
		if err != nil {
			cr.Set(websocket.CloseAbnormalClosure, "")
			return
		}
		if mt != websocket.TextMessage {
			continue
		}
		env, err := decodeEnvelope(data)
		if err != nil {
			continue
		}
		if env.Type == TypeSessionEnd {
			cr.Set(websocket.CloseNormalClosure, "session.end")
			d.tearDown(rec, "session.end")
			cancel()
			return
		}
		if rec.media != nil && (env.Type == TypeMediaSDPOffer || env.Type == TypeMediaICECandidate) {
			reply, hasReply, err := rec.media.HandleControlEnvelope(env)
			if err != nil {
				log.Printf("session-control-plus-media: session=%s media envelope: %v", rec.SessionID, err)
				continue
			}
			if hasReply {
				if reply.Seq == 0 {
					reply.Seq = rec.NextSeq()
				}
				if rec.outboundForRelay != nil {
					select {
					case rec.outboundForRelay <- reply:
					case <-ctx.Done():
						return
					}
				}
			}
			continue
		}
		if rec.control != nil {
			select {
			case rec.control.Inbound <- env:
			case <-ctx.Done():
				return
			}
		}
	}
}

// runWriter pumps server-emitted envelopes onto the WS, applying the
// outbound replay buffer + backpressure window.
func (d *Driver) runWriter(ctx context.Context, conn *websocket.Conn, rec *SessionRecord, out <-chan ControlEnvelope, cancel context.CancelFunc, cr *closeReasonHolder) {
	defer cancel()
	dropAfter := d.cfg.BackpressureDropAfter
	if dropAfter <= 0 {
		dropAfter = 5 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-out:
			if !ok {
				return
			}
			payload, err := json.Marshal(env)
			if err != nil {
				continue
			}
			rec.replay.Append(env.Seq, payload)
			deadline := time.Now().Add(dropAfter)
			_ = conn.SetWriteDeadline(deadline)
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				if errors.Is(err, websocket.ErrCloseSent) {
					cr.Set(websocket.CloseNormalClosure, "")
				} else {
					cr.Set(websocket.ClosePolicyViolation, livepeerheader.ErrBackpressureDrop)
				}
				return
			}
		}
	}
}

// runHeartbeat pings every interval; misses propagate via the read
// deadline set in runReader.
func (d *Driver) runHeartbeat(ctx context.Context, conn *websocket.Conn, cancel context.CancelFunc, cr *closeReasonHolder) {
	defer cancel()
	if d.cfg.HeartbeatInterval <= 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(d.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			deadline := time.Now().Add(d.cfg.HeartbeatInterval)
			if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				cr.Set(websocket.CloseAbnormalClosure, "")
				return
			}
		}
	}
}

// relayBackendOutbound copies runner→broker envelopes onto the writer
// pump's outbound channel. Stops when the runner-side channel closes
// or the per-connection context is canceled.
func (d *Driver) relayBackendOutbound(ctx context.Context, rec *SessionRecord, out chan<- ControlEnvelope) {
	if rec.control == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-rec.control.Outbound:
			if !ok {
				return
			}
			if env.Seq == 0 {
				env.Seq = rec.NextSeq()
			}
			emitDirect(out, env)
		}
	}
}

// emitDirect drops a server-emitted envelope into the writer channel.
// Non-blocking when the channel is full — the writer's deadline-based
// backpressure drop fires on the slow client.
func emitDirect(out chan<- ControlEnvelope, env ControlEnvelope) {
	select {
	case out <- env:
	default:
		go func() { out <- env }()
	}
}

// decodeEnvelope parses a JSON frame into a ControlEnvelope, returning
// an error on malformed JSON.
func decodeEnvelope(data []byte) (ControlEnvelope, error) {
	var env ControlEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ControlEnvelope{}, err
	}
	if env.Type == "" {
		return ControlEnvelope{}, errors.New("envelope: empty type")
	}
	return env, nil
}

// closeReasonHolder is a goroutine-safe holder for the WS close-frame
// code+reason that runWriter / runReader / runHeartbeat agree on.
type closeReasonHolder struct {
	mu     sync.Mutex
	code   int
	reason string
	set    bool
}

func newCloseReasonHolder() *closeReasonHolder { return &closeReasonHolder{} }

func (h *closeReasonHolder) Set(code int, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.set {
		return
	}
	h.code = code
	h.reason = reason
	h.set = true
}

func (h *closeReasonHolder) Get() (int, string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.code, h.reason, h.set
}

func (d *Driver) applyCloseReason(conn *websocket.Conn, code int, reason string, set bool) {
	if !set {
		return
	}
	deadline := time.Now().Add(time.Second)
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason), deadline)
}

// tearDown is invoked when a session reaches a terminal state (clean
// session.end, runner crash, reconnect window expiry). Idempotent —
// guarded by SessionRecord.markClosing.
func (d *Driver) tearDown(rec *SessionRecord, reason string) {
	if rec.markClosing() {
		return
	}
	if d.backend != nil {
		d.backend.Shutdown(rec.SessionID)
	}
	if rec.Cancel != nil {
		rec.Cancel()
	}
	d.store.Remove(rec.SessionID)
	log.Printf("session-control-plus-media: session=%s torn down reason=%s", rec.SessionID, reason)
}

// reconnectWatchdog is the per-store goroutine that fires the
// reconnect-window-expired teardown.
func (s *Store) reconnectWatchdog(ctx context.Context, d *Driver, window time.Duration) {
	if window <= 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, rec := range s.Snapshot() {
				if rec.IsActive() {
					continue
				}
				disc := rec.DisconnectedAt()
				if disc.IsZero() {
					continue
				}
				if now.Sub(disc) > window && !rec.Closing() {
					d.tearDown(rec, "control_disconnect_window_expired")
				}
			}
		}
	}
}
