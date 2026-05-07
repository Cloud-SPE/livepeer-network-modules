package rtmpingresshlsegress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// Config wires the gateway-side RTMP listener.
type Config struct {
	// Addr is the TCP address the listener binds. Default `:1935`.
	Addr string

	// MaxConcurrent caps simultaneous publishes. Zero = unbounded.
	MaxConcurrent int

	// RequireStreamKey rejects publishes whose publishing-name lacks
	// `<session_id>/<stream_key>`. Default false: the publishing name
	// is treated as a bare session_id when no `/` is present.
	RequireStreamKey bool
}

// Listener accepts customer RTMP pushes, looks up the matching session
// in the registry, and relays the FLV stream to the broker's
// rtmp_ingest_url.
type Listener struct {
	cfg      Config
	sessions *Sessions

	mu      sync.Mutex
	tcp     net.Listener
	server  *rtmp.Server
	stopped atomic.Bool
	active  atomic.Int64
}

// NewListener builds a Listener bound to the given Sessions registry.
// Bind happens in Run.
func NewListener(cfg Config, sessions *Sessions) *Listener {
	if cfg.Addr == "" {
		cfg.Addr = ":1935"
	}
	return &Listener{cfg: cfg, sessions: sessions}
}

// Run starts the listener and blocks until ctx is canceled or a fatal
// listen error occurs.
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

	log.Printf("gateway-adapters/rtmp: bound %s", l.cfg.Addr)

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			h := &connHandler{
				listener: l,
				remote:   conn.RemoteAddr().String(),
			}
			return conn, &rtmp.ConnConfig{Handler: h}
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

// ActivePublishers returns the count of in-flight publishes.
func (l *Listener) ActivePublishers() int64 { return l.active.Load() }

// connHandler implements rtmp.Handler for one customer connection.
type connHandler struct {
	rtmp.DefaultHandler

	listener *Listener
	remote   string

	mu        sync.Mutex
	relay     *relay
	sessionID string
	closed    bool
}

func (h *connHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	publishingName := strings.TrimSpace(cmd.PublishingName)
	if publishingName == "" {
		return errors.New("rtmp: empty publishing name")
	}

	if cap := h.listener.cfg.MaxConcurrent; cap > 0 && h.listener.active.Load() >= int64(cap) {
		return fmt.Errorf("rtmp: max concurrent (%d) reached", cap)
	}

	sessionID, streamKey, structured := splitPublishingName(publishingName)
	if !structured {
		if h.listener.cfg.RequireStreamKey {
			return errors.New("rtmp: publishing name must be <session_id>/<stream_key>")
		}
		sessionID = publishingName
	}

	sess := h.listener.sessions.Lookup(sessionID)
	if sess == nil {
		return fmt.Errorf("rtmp: session %q not registered", sessionID)
	}
	if sess.StreamKey != "" && sess.StreamKey != streamKey {
		return errors.New("rtmp: stream key mismatch")
	}

	rel, err := dial(sess)
	if err != nil {
		return fmt.Errorf("rtmp: dial broker upstream: %w", err)
	}

	h.mu.Lock()
	h.relay = rel
	h.sessionID = sessionID
	h.mu.Unlock()
	h.listener.active.Add(1)
	log.Printf("gateway-adapters/rtmp: session_open session=%s remote=%s upstream=%s",
		sessionID, h.remote, sess.RTMPIngestURL)
	return nil
}

func (h *connHandler) OnAudio(timestamp uint32, payload io.Reader) error {
	h.mu.Lock()
	rel := h.relay
	closed := h.closed
	h.mu.Unlock()
	if rel == nil || closed {
		return nil
	}
	return rel.writeAudio(timestamp, payload)
}

func (h *connHandler) OnVideo(timestamp uint32, payload io.Reader) error {
	h.mu.Lock()
	rel := h.relay
	closed := h.closed
	h.mu.Unlock()
	if rel == nil || closed {
		return nil
	}
	return rel.writeVideo(timestamp, payload)
}

func (h *connHandler) OnClose() {
	h.evict()
}

func (h *connHandler) evict() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	rel := h.relay
	sessionID := h.sessionID
	h.relay = nil
	h.mu.Unlock()
	if rel != nil {
		_ = rel.Close()
		h.listener.active.Add(-1)
	}
	if sessionID != "" {
		log.Printf("gateway-adapters/rtmp: session_closed session=%s remote=%s", sessionID, h.remote)
	}
}

// splitPublishingName parses the path-based RTMP publishing name.
// Returns (session_id, stream_key, true) on a `<id>/<key>` shape;
// (raw, "", false) otherwise.
func splitPublishingName(name string) (string, string, bool) {
	name = strings.TrimPrefix(name, "/")
	idx := strings.IndexByte(name, '/')
	if idx <= 0 || idx == len(name)-1 {
		return name, "", false
	}
	return name[:idx], name[idx+1:], true
}
