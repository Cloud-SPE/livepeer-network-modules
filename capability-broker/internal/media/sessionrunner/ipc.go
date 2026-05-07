package sessionrunner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	srpb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/sessionrunner/v1"
)

// IPC is the broker-side gRPC client to a runner subprocess. One IPC
// per Runner; created after Supervisor.Launch and torn down at
// teardown.
type IPC struct {
	conn       *grpc.ClientConn
	control    srpb.SessionRunnerControlClient
	media      srpb.SessionRunnerMediaClient
	socketPath string
}

// DialIPC opens the gRPC connection to the runner's unix socket. The
// caller is expected to call Close at teardown.
func DialIPC(ctx context.Context, socketPath string) (*IPC, error) {
	if socketPath == "" {
		return nil, errors.New("session-runner: empty socket path")
	}
	dial := func(_ context.Context, addr string) (net.Conn, error) {
		return net.Dial("unix", addr)
	}
	conn, err := grpc.NewClient(
		"unix:"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dial),
	)
	if err != nil {
		return nil, fmt.Errorf("session-runner: dial: %w", err)
	}
	return &IPC{
		conn:       conn,
		control:    srpb.NewSessionRunnerControlClient(conn),
		media:      srpb.NewSessionRunnerMediaClient(conn),
		socketPath: socketPath,
	}, nil
}

// Close tears down the gRPC connection. Idempotent.
func (i *IPC) Close() error {
	if i == nil || i.conn == nil {
		return nil
	}
	err := i.conn.Close()
	i.conn = nil
	return err
}

// Health invokes SessionRunnerControl.Health and reports whether the
// runner is "ready". The Runner.Health probe wraps this.
func (i *IPC) Health(ctx context.Context) error {
	if i == nil || i.control == nil {
		return errors.New("session-runner: ipc not initialized")
	}
	resp, err := i.control.Health(ctx, &srpb.HealthRequest{})
	if err != nil {
		return err
	}
	if resp.GetStatus() != "ready" {
		return fmt.Errorf("session-runner: not ready (status=%q)", resp.GetStatus())
	}
	return nil
}

// Shutdown invokes SessionRunnerControl.Shutdown. The Runner.Shutdown
// path falls through to SIGKILL on failure.
func (i *IPC) Shutdown(ctx context.Context, graceful bool) error {
	if i == nil || i.control == nil {
		return errors.New("session-runner: ipc not initialized")
	}
	_, err := i.control.Shutdown(ctx, &srpb.ShutdownRequest{Graceful: graceful})
	return err
}

// WorkUnitReports opens the runner-side ReportWorkUnits server-stream
// client. The runner sends monotonic deltas; the broker accumulates
// them into the runnerreport extractor's LiveCounter via the caller
// supplied accumulate closure.
type WorkUnitReports struct {
	stream srpb.SessionRunnerControl_ReportWorkUnitsClient
	cancel context.CancelFunc
}

// OpenWorkUnitReports opens the runner→broker work-unit report
// stream. The broker initiates the call and the runner answers with
// a server-stream of monotonic deltas.
func (i *IPC) OpenWorkUnitReports(ctx context.Context) (*WorkUnitReports, error) {
	if i == nil || i.control == nil {
		return nil, errors.New("session-runner: ipc not initialized")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := i.control.ReportWorkUnits(streamCtx, &srpb.WorkUnitReportRequest{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("session-runner: report work units: %w", err)
	}
	return &WorkUnitReports{stream: stream, cancel: cancel}, nil
}

// Recv blocks for one report. Returns io.EOF at end-of-stream.
func (w *WorkUnitReports) Recv() (uint64, error) {
	if w == nil || w.stream == nil {
		return 0, errors.New("session-runner: report stream closed")
	}
	rep, err := w.stream.Recv()
	if err != nil {
		return 0, err
	}
	return rep.GetDelta(), nil
}

// Close tears down the stream. Idempotent.
func (w *WorkUnitReports) Close() error {
	if w == nil {
		return nil
	}
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.stream = nil
	return nil
}

// EnvelopeRelay is the bidirectional control-envelope stream between
// broker and runner. The session-control-plus-media driver wires its
// per-session inbound/outbound channels into this struct's hooks in
// commit C4.
type EnvelopeRelay struct {
	stream srpb.SessionRunnerControl_EnvelopesClient

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

// OpenEnvelopeRelay opens the gRPC bidi stream and returns a relay
// handle. Caller invokes Run to start the per-direction goroutines.
func (i *IPC) OpenEnvelopeRelay(ctx context.Context) (*EnvelopeRelay, error) {
	if i == nil || i.control == nil {
		return nil, errors.New("session-runner: ipc not initialized")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := i.control.Envelopes(streamCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("session-runner: envelopes stream: %w", err)
	}
	return &EnvelopeRelay{stream: stream, cancel: cancel}, nil
}

// Send writes one envelope from broker → runner.
func (e *EnvelopeRelay) Send(env *srpb.ControlEnvelope) error {
	if e == nil || e.stream == nil {
		return errors.New("session-runner: envelope relay closed")
	}
	return e.stream.Send(env)
}

// Recv blocks for one envelope from runner → broker.
func (e *EnvelopeRelay) Recv() (*srpb.ControlEnvelope, error) {
	if e == nil || e.stream == nil {
		return nil, errors.New("session-runner: envelope relay closed")
	}
	return e.stream.Recv()
}

// Close tears down the gRPC bidi stream. Idempotent.
func (e *EnvelopeRelay) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	if e.stream != nil {
		_ = e.stream.CloseSend()
		e.stream = nil
	}
	return nil
}

// MediaRelay is the bidirectional raw-RTP stream between broker and
// runner. Lands in commit C5/C6.
type MediaRelay struct {
	stream srpb.SessionRunnerMedia_FramesClient
	cancel context.CancelFunc
}

// OpenMediaRelay opens the gRPC bidi RTP stream.
func (i *IPC) OpenMediaRelay(ctx context.Context) (*MediaRelay, error) {
	if i == nil || i.media == nil {
		return nil, errors.New("session-runner: ipc not initialized")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := i.media.Frames(streamCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("session-runner: media stream: %w", err)
	}
	return &MediaRelay{stream: stream, cancel: cancel}, nil
}

// Send writes one media frame.
func (m *MediaRelay) Send(f *srpb.MediaFrame) error {
	if m == nil || m.stream == nil {
		return errors.New("session-runner: media relay closed")
	}
	return m.stream.Send(f)
}

// Recv blocks for one media frame.
func (m *MediaRelay) Recv() (*srpb.MediaFrame, error) {
	if m == nil || m.stream == nil {
		return nil, errors.New("session-runner: media relay closed")
	}
	return m.stream.Recv()
}

// Close tears down the media stream. Idempotent.
func (m *MediaRelay) Close() error {
	if m == nil {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.stream != nil {
		_ = m.stream.CloseSend()
		m.stream = nil
	}
	return nil
}

// dialDeadline wraps an existing context with a hard upper bound used
// by the connection-establishment path.
func dialDeadline(ctx context.Context, max time.Duration) (context.Context, context.CancelFunc) {
	if max <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, max)
}
