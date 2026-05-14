package sessioncontrolexternalmedia

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/controlws"
)

// nextSeq is a per-session atomic monotonic sequence counter for
// outbound envelopes. We keep it on the record indirectly via the
// driver's helpers below.
type seqState struct {
	v atomic.Uint64
}

func (s *seqState) next() uint64 { return s.v.Add(1) }

// recordSeq is a process-level map of session id → seq state. Keeping
// it driver-scoped via the Store (which already owns lifetime) would
// be cleaner; for v0 we attach a fresh state at WS upgrade time.
// Because each session has at most one active control-WS, the WS handler
// owns its seqState for the duration of the connection.

// ServeControlWS handles the GET upgrade at
// /v1/cap/{session_id}/control. Auth is the unguessable session_id in
// the path. Frame vocabulary is lifecycle-only — capability-defined
// command frames are rejected with a normal close.
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
		d.teardown(rec, "expired")
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}
	if rec.IsActive() {
		http.Error(w, "session already attached", http.StatusConflict)
		return
	}

	conn, err := d.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	rec.SetActive(true)
	defer rec.SetActive(false)

	d.runControlWS(r.Context(), conn, rec)
}

// runControlWS owns the per-connection lifetime: reader pump, writer
// pump, heartbeat, graceful close.
func (d *Driver) runControlWS(parent context.Context, conn *websocket.Conn, rec *SessionRecord) {
	defer conn.Close()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	out := make(chan outboundEvent, d.cfg.OutboundBufferMessages)
	rec.SetOutbound(out)
	defer rec.ClearOutbound()

	seq := &seqState{}

	// Emit session.started on attach (idempotent for the gateway).
	emitLifecycle(seq, out, controlws.TypeSessionStarted, nil)

	var wg sync.WaitGroup
	closeReason := newCloseReasonHolder()
	wg.Add(3)
	go func() { defer wg.Done(); d.runReader(ctx, conn, rec, cancel, closeReason) }()
	go func() { defer wg.Done(); d.runWriter(ctx, conn, out, cancel, closeReason) }()
	go func() { defer wg.Done(); d.runHeartbeat(ctx, conn, cancel, closeReason) }()

	wg.Wait()
	code, reason, set := closeReason.Get()
	d.applyCloseReason(conn, code, reason, set)
}

// runReader reads frames from the WS, parses envelopes, short-circuits
// reserved lifecycle types, and rejects unknown / capability-defined
// frames with a graceful close (per spec — this mode is lifecycle-only).
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
		env, err := controlws.Decode(data)
		if err != nil {
			continue
		}
		switch env.Type {
		case controlws.TypeSessionEnd:
			cr.Set(websocket.CloseNormalClosure, "session.end")
			d.teardown(rec, "session.end")
			cancel()
			return
		case controlws.TypeSessionTopup:
			// Topup is gateway → broker. For v0 the broker
			// accepts it and emits session.balance.refilled
			// optimistically (the payment-daemon plumbing
			// for in-session topup is a separate follow-up
			// per the spec).
			d.emitLifecycle(rec, controlws.TypeSessionBalanceRefilled, nil)
		default:
			if !controlws.IsLifecycle(env.Type) {
				// Capability-defined frames are not
				// permitted in this mode.
				cr.Set(websocket.ClosePolicyViolation,
					"only lifecycle frames permitted on session-control-external-media@v0 control-WS")
				return
			}
			// Other lifecycle types are server-emitted; the
			// gateway should not be sending them.
		}
	}
}

// runWriter pumps server-emitted envelopes onto the WS.
func (d *Driver) runWriter(ctx context.Context, conn *websocket.Conn, out <-chan outboundEvent, cancel context.CancelFunc, cr *closeReasonHolder) {
	defer cancel()
	dropAfter := d.cfg.BackpressureDropAfter
	if dropAfter <= 0 {
		dropAfter = 5 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-out:
			if !ok {
				return
			}
			deadline := time.Now().Add(dropAfter)
			_ = conn.SetWriteDeadline(deadline)
			if err := conn.WriteMessage(websocket.TextMessage, ev.Body); err != nil {
				cr.Set(websocket.CloseAbnormalClosure, "")
				return
			}
		}
	}
}

// runHeartbeat pings the WS every interval.
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

// emitUsageTick emits a session.usage.tick frame with the running unit total.
func (d *Driver) emitUsageTick(rec *SessionRecord, units uint64) {
	body, _ := json.Marshal(map[string]any{"units": units})
	d.emitLifecycle(rec, controlws.TypeSessionUsageTick, body)
}

// emitSessionEnded emits a terminal session.ended frame with a reason.
func (d *Driver) emitSessionEnded(rec *SessionRecord, reason string) {
	body, _ := json.Marshal(map[string]any{"reason": reason})
	d.emitLifecycle(rec, controlws.TypeSessionEnded, body)
}

// emitLifecycle marshals a control envelope with the next monotonic
// seq and posts it to the attached control-WS, if any. Drops silently
// when no WS is attached — lifecycle frames are best-effort in v0.
func (d *Driver) emitLifecycle(rec *SessionRecord, t string, body []byte) {
	env := controlws.Envelope{Type: t, Body: body}
	payload, err := json.Marshal(env)
	if err != nil {
		return
	}
	rec.Emit(outboundEvent{Body: payload})
}

// emitLifecycle (free function) is used at WS attach time before the
// record's outbound is published — writes directly to the local out
// channel.
func emitLifecycle(seq *seqState, out chan<- outboundEvent, t string, body []byte) {
	env := controlws.Envelope{Type: t, Seq: seq.next(), Body: body}
	payload, err := json.Marshal(env)
	if err != nil {
		return
	}
	select {
	case out <- outboundEvent{Seq: env.Seq, Body: payload}:
	default:
	}
}

// closeReasonHolder is a goroutine-safe holder for the WS close-frame
// code+reason the reader / writer / heartbeat goroutines agree on.
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
