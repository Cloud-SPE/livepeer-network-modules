package sessioncontrolplusmedia

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/runnerreport"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/sessionrunner"
	mediawebrtc "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/webrtc"
	srpb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/sessionrunner/v1"
)

// Reserved IPC envelope types the broker emits at runner-side. The
// session-runner is expected to react gracefully — pause emission on
// disconnected, resume on reconnected.
const (
	IPCRunnerControlDisconnected = "runner.control_disconnected"
	IPCRunnerControlReconnected  = "runner.control_reconnected"
)

// CapabilityResolver is a per-broker hook that maps a session id to
// the capability backend the supervisor should launch the runner
// with. v0.1: the driver retains the capability used at session-open
// in its session record; the resolver reads it back at Backend.AttachControl
// time.
type CapabilityResolver func(sessionID string) (sessionrunner.CapabilityBackend, bool)

// RunnerBackend is the production Backend impl that wires control-WS
// envelopes through the gRPC IPC stream defined in
// livepeer-network-protocol/proto/livepeer/sessionrunner/v1.
type RunnerBackend struct {
	supervisor *sessionrunner.Supervisor
	resolver   CapabilityResolver
	rtcEngine  *mediawebrtc.Engine
	store      *Store

	mu       sync.Mutex
	sessions map[string]*runnerSession
}

type runnerSession struct {
	runner    *sessionrunner.Runner
	ipc       *sessionrunner.IPC
	relay     *sessionrunner.EnvelopeRelay
	mediaRel  *MediaRelay
	mediaPC   *mediawebrtc.Relay
	mediaIPC  *sessionrunner.MediaRelay
	reports   *sessionrunner.WorkUnitReports
	inbound   chan ControlEnvelope
	outbound  chan ControlEnvelope
	done      chan struct{}
	cancel    context.CancelFunc
}

// NewRunnerBackend wraps the per-broker supervisor in a Backend the
// session-control-plus-media driver consumes.
func NewRunnerBackend(sup *sessionrunner.Supervisor, resolver CapabilityResolver, rtcEngine *mediawebrtc.Engine, store *Store) *RunnerBackend {
	return &RunnerBackend{
		supervisor: sup,
		resolver:   resolver,
		rtcEngine:  rtcEngine,
		store:      store,
		sessions:   make(map[string]*runnerSession),
	}
}

// AttachControl is invoked by Driver.Serve at session-open time. It
// launches the per-session runner subprocess, waits for Health(),
// opens the IPC envelope stream, and returns the broker-side channels
// the control-WS pumps relay against.
func (b *RunnerBackend) AttachControl(ctx context.Context, sessionID string) (BackendControl, error) {
	if b == nil {
		return BackendControl{}, errors.New("runner-backend: nil receiver")
	}
	cap, ok := b.resolver(sessionID)
	if !ok {
		return BackendControl{}, errors.New("runner-backend: capability resolver returned no backend")
	}

	runCtx, cancel := context.WithCancel(ctx)
	runner, err := b.supervisor.Launch(runCtx, sessionID, cap)
	if err != nil {
		cancel()
		return BackendControl{}, err
	}

	ipc, err := sessionrunner.DialIPC(runCtx, runner.SocketPath())
	if err != nil {
		_ = runner.Kill()
		cancel()
		return BackendControl{}, err
	}

	if err := runner.Health(runCtx, ipc.Health); err != nil {
		_ = ipc.Close()
		_ = runner.Kill()
		cancel()
		return BackendControl{}, err
	}

	relay, err := ipc.OpenEnvelopeRelay(runCtx)
	if err != nil {
		_ = ipc.Close()
		_ = runner.Kill()
		cancel()
		return BackendControl{}, err
	}

	var pcRelay *mediawebrtc.Relay
	var mediaIPC *sessionrunner.MediaRelay
	var mediaRel *MediaRelay
	if b.rtcEngine != nil {
		pcRelay, err = b.rtcEngine.NewRelay()
		if err != nil {
			_ = relay.Close()
			_ = ipc.Close()
			_ = runner.Kill()
			cancel()
			return BackendControl{}, err
		}
		mediaIPC, err = ipc.OpenMediaRelay(runCtx)
		if err != nil {
			_ = pcRelay.Close()
			_ = relay.Close()
			_ = ipc.Close()
			_ = runner.Kill()
			cancel()
			return BackendControl{}, err
		}
		mediaRel = NewMediaRelay(sessionID, pcRelay, mediaIPC)
		go mediaRel.Run(runCtx)
		if rec := b.store.Get(sessionID); rec != nil {
			rec.mu.Lock()
			rec.media = mediaRel
			rec.mu.Unlock()
		}
	}

	var reports *sessionrunner.WorkUnitReports
	if rec := b.store.Get(sessionID); rec != nil {
		if _, ok := rec.LiveCounter.(*runnerreport.LiveCounter); ok {
			reports, err = ipc.OpenWorkUnitReports(runCtx)
			if err != nil {
				log.Printf("session-runner: session=%s open report stream: %v", sessionID, err)
				reports = nil
			}
		}
	}

	sess := &runnerSession{
		runner:   runner,
		ipc:      ipc,
		relay:    relay,
		mediaRel: mediaRel,
		mediaPC:  pcRelay,
		mediaIPC: mediaIPC,
		reports:  reports,
		inbound:  make(chan ControlEnvelope, 64),
		outbound: make(chan ControlEnvelope, 64),
		done:     make(chan struct{}),
		cancel:   cancel,
	}

	b.mu.Lock()
	b.sessions[sessionID] = sess
	b.mu.Unlock()

	go b.pumpInbound(runCtx, sessionID, sess)
	go b.pumpOutbound(runCtx, sessionID, sess)
	if reports != nil {
		go b.pumpReports(runCtx, sessionID, sess)
	}

	return BackendControl{
		Inbound:  sess.inbound,
		Outbound: sess.outbound,
		Done:     sess.done,
		Cancel:   cancel,
	}, nil
}

// DetachControl is invoked when the control-WS drops without
// session.end. The runner stays alive; the IPC carries a
// `runner.control_disconnected` envelope so the workload can pause.
func (b *RunnerBackend) DetachControl(sessionID string, code int, reason string) {
	b.mu.Lock()
	sess := b.sessions[sessionID]
	b.mu.Unlock()
	if sess == nil {
		return
	}
	body := []byte(`{"code":` + intStr(code) + `,"reason":` + jsonString(reason) + `}`)
	_ = sess.relay.Send(&srpb.ControlEnvelope{
		Type: IPCRunnerControlDisconnected,
		Body: body,
	})
}

// ReattachControl mirrors DetachControl: emitted when the customer
// reconnects within the window.
func (b *RunnerBackend) ReattachControl(sessionID string) {
	b.mu.Lock()
	sess := b.sessions[sessionID]
	b.mu.Unlock()
	if sess == nil {
		return
	}
	_ = sess.relay.Send(&srpb.ControlEnvelope{Type: IPCRunnerControlReconnected})
}

// Shutdown is the per-session teardown invoked by Driver.tearDown via
// SessionRecord.Cancel. Idempotent.
func (b *RunnerBackend) Shutdown(sessionID string) {
	b.mu.Lock()
	sess := b.sessions[sessionID]
	delete(b.sessions, sessionID)
	b.mu.Unlock()
	if sess == nil {
		return
	}
	if sess.reports != nil {
		_ = sess.reports.Close()
	}
	if sess.mediaIPC != nil {
		_ = sess.mediaIPC.Close()
	}
	if sess.mediaPC != nil {
		_ = sess.mediaPC.Close()
	}
	if sess.relay != nil {
		_ = sess.relay.Close()
	}
	if sess.ipc != nil {
		_ = sess.ipc.Shutdown(context.Background(), true)
		_ = sess.ipc.Close()
	}
	if sess.runner != nil {
		_ = sess.runner.Shutdown(context.Background(), nil)
	}
	close(sess.done)
	if sess.cancel != nil {
		sess.cancel()
	}
}

// pumpInbound copies broker → runner envelopes onto the gRPC stream.
func (b *RunnerBackend) pumpInbound(ctx context.Context, sessionID string, sess *runnerSession) {
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-sess.inbound:
			if !ok {
				return
			}
			pb := &srpb.ControlEnvelope{Type: env.Type, Seq: env.Seq, Body: env.Body}
			if err := sess.relay.Send(pb); err != nil {
				log.Printf("session-runner: session=%s send: %v", sessionID, err)
				return
			}
			sess.runner.Touch()
		}
	}
}

// pumpReports drains the runner-reported work-unit deltas onto the
// session record's LiveCounter. Stops on EOF / runner crash.
func (b *RunnerBackend) pumpReports(ctx context.Context, sessionID string, sess *runnerSession) {
	rec := b.store.Get(sessionID)
	if rec == nil {
		return
	}
	lc, ok := rec.LiveCounter.(*runnerreport.LiveCounter)
	if !ok || lc == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		delta, err := sess.reports.Recv()
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("session-runner: session=%s report recv: %v", sessionID, err)
			return
		}
		if delta == 0 {
			continue
		}
		lc.Add(delta)
		sess.runner.Touch()
	}
}

// pumpOutbound copies runner → broker envelopes onto the outbound
// channel the writer pump consumes.
func (b *RunnerBackend) pumpOutbound(ctx context.Context, sessionID string, sess *runnerSession) {
	for {
		pb, err := sess.relay.Recv()
		if err != nil {
			return
		}
		sess.runner.Touch()
		env := ControlEnvelope{Type: pb.GetType(), Seq: pb.GetSeq(), Body: pb.GetBody()}
		select {
		case <-ctx.Done():
			return
		case sess.outbound <- env:
		}
	}
}

// intStr is a tiny zero-alloc int-to-decimal-string helper used by
// the control envelopes' JSON body. Keeps this file free of fmt.
func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	buf := [20]byte{}
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// jsonString returns a minimal JSON-encoded string, used to inline a
// reason value into the runner.control_disconnected body without
// pulling in encoding/json from this hot path.
func jsonString(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"', '\\':
			b = append(b, '\\', byte(r))
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r >= 0x20 && r < 0x7f {
				b = append(b, byte(r))
			} else {
				b = append(b, '?')
			}
		}
	}
	b = append(b, '"')
	return string(b)
}
