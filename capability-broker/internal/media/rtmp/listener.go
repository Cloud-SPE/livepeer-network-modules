// Package rtmp implements the broker's RTMP ingest listener over
// github.com/yutopp/go-rtmp. It accepts publish connections at a
// configurable TCP address (default :1935), validates stream keys
// against an open-session store, and pipes the inbound FLV byte stream
// to a per-session sink (the FFmpeg subprocess wrapper in
// internal/media/encoder).
//
// Customer-facing auth (API keys, mTLS, AuthWebhookURL-style
// integration) lives gateway-side; the broker's stream-key check is
// defense-in-depth.
package rtmp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yutopp/go-rtmp"
)

// DuplicatePolicy governs what the broker does when a publish lands
// for a session that already has an active publisher.
type DuplicatePolicy string

const (
	// DuplicateReject is the safer default: the second publisher gets
	// an RTMP _error response, the first stream is left running.
	DuplicateReject DuplicatePolicy = "reject"
	// DuplicateReplace kicks the first publisher and accepts the new
	// one. Friendlier for auto-reconnect encoders.
	DuplicateReplace DuplicatePolicy = "replace"
)

// SessionLookup is the broker-side hook the listener calls in its
// OnPublish callback. Returns (sink, accept, replaceFirst). On accept
// the listener pipes FLV bytes into sink.WriteFLV; replaceFirst signals
// the listener to evict any currently-active publisher for that
// session before accepting.
type SessionLookup interface {
	// LookupAndAccept resolves (sessionID, streamKey) to a sink. Returns
	// (nil, false, false) on missing session or key mismatch.
	// replaceFirst is true when the duplicate-policy says replace and a
	// prior publisher exists.
	LookupAndAccept(sessionID, streamKey string) (sink Sink, ok bool, replaceFirst bool)
}

// Sink is the per-session consumer the listener writes into. The
// FFmpeg encoder wrapper in internal/media/encoder satisfies this
// interface; tests can use a fake.
type Sink interface {
	// WriteFLV is called from the RTMP handler goroutine for every
	// audio/video tag. Implementations MUST be safe for concurrent
	// calls per session (the listener serializes per-connection but
	// the contract is loose).
	WriteFLV(p []byte) (int, error)
	// Close terminates the sink. The listener calls this on RTMP
	// disconnect or when a duplicate-replace evicts the prior
	// publisher.
	Close() error
	// Touch is called on every audio/video tag with the current time.
	// The session store reads this for the idle-timeout watchdog;
	// pushing it through the sink keeps cross-package wiring minimal.
	Touch(now time.Time)
}

// Config wires the listener.
type Config struct {
	Addr             string
	MaxConcurrent    int
	DuplicatePolicy  DuplicatePolicy
	RequireStreamKey bool
}

// Listener accepts RTMP publish connections.
type Listener struct {
	cfg    Config
	lookup SessionLookup

	mu       sync.Mutex
	tcp      net.Listener
	server   *rtmp.Server
	stopped  atomic.Bool
	active   atomic.Int64
	sessions sync.Map
}

// New builds a Listener bound to lookup. Bind happens in Run.
func New(cfg Config, lookup SessionLookup) *Listener {
	if cfg.Addr == "" {
		cfg.Addr = ":1935"
	}
	if cfg.DuplicatePolicy == "" {
		cfg.DuplicatePolicy = DuplicateReject
	}
	return &Listener{cfg: cfg, lookup: lookup}
}

// Run starts the listener and blocks until ctx is canceled or a fatal
// listen error occurs. It is safe to call once per Listener.
func (l *Listener) Run(ctx context.Context) error {
	l.mu.Lock()
	if l.tcp != nil {
		l.mu.Unlock()
		return errors.New("rtmp listener: already running")
	}
	tcp, err := net.Listen("tcp", l.cfg.Addr)
	if err != nil {
		l.mu.Unlock()
		return fmt.Errorf("rtmp listen %s: %w", l.cfg.Addr, err)
	}
	l.tcp = tcp
	l.mu.Unlock()

	log.Printf("rtmp listener: bound %s", l.cfg.Addr)

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			h := &connHandler{
				listener: l,
				remote:   conn.RemoteAddr().String(),
			}
			return conn, &rtmp.ConnConfig{
				Handler: h,
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024 / 8,
				},
			}
		},
	})
	l.mu.Lock()
	l.server = srv
	l.mu.Unlock()

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(tcp)
		if l.stopped.Load() {
			serveErr <- nil
			return
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
		l.stop()
		<-serveErr
		return nil
	case err := <-serveErr:
		l.stop()
		return err
	}
}

func (l *Listener) stop() {
	if !l.stopped.CompareAndSwap(false, true) {
		return
	}
	l.mu.Lock()
	srv := l.server
	tcp := l.tcp
	l.server = nil
	l.tcp = nil
	l.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	if tcp != nil {
		_ = tcp.Close()
	}
}

// trackPublisher is called by connHandler when a publish is accepted.
// Returns the prior handler (or nil) so the caller can evict it under
// the replace policy.
func (l *Listener) trackPublisher(sessionID string, h *connHandler) *connHandler {
	v, loaded := l.sessions.Swap(sessionID, h)
	if !loaded {
		l.active.Add(1)
		return nil
	}
	prior, _ := v.(*connHandler)
	return prior
}

func (l *Listener) untrackPublisher(sessionID string, h *connHandler) {
	v, ok := l.sessions.Load(sessionID)
	if !ok {
		return
	}
	if cur, _ := v.(*connHandler); cur == h {
		l.sessions.Delete(sessionID)
		l.active.Add(-1)
	}
}

// ActivePublishers returns the count of in-flight publish sessions.
// Exposed for metrics.
func (l *Listener) ActivePublishers() int64 { return l.active.Load() }
