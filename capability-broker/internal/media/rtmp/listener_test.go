package rtmp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

type stubLookup struct {
	mu       sync.Mutex
	called   int
	sessID   string
	streamK  string
	accept   bool
	replace  bool
	lastSink Sink
}

func (s *stubLookup) LookupAndAccept(sessionID, streamKey string) (Sink, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called++
	s.sessID = sessionID
	s.streamK = streamKey
	if !s.accept {
		return nil, false, false
	}
	sink := NewDiscardSink()
	s.lastSink = sink
	return sink, true, s.replace
}

func TestListener_BindAndShutdown(t *testing.T) {
	lookup := &stubLookup{accept: true}
	l := New(Config{Addr: "127.0.0.1:0"}, lookup)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not exit on ctx cancel")
	}
}

func TestListener_AcceptsTCP(t *testing.T) {
	lookup := &stubLookup{accept: true}
	l := New(Config{Addr: "127.0.0.1:0", RequireStreamKey: true}, lookup)
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	l.tcp = tcp
	addr := tcp.Addr().String()
	t.Cleanup(func() { _ = tcp.Close() })

	c, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = c.Close()
}

func TestListener_TrackUntrack(t *testing.T) {
	l := New(Config{}, &stubLookup{accept: true})
	h1 := &connHandler{listener: l}
	h2 := &connHandler{listener: l}

	if prior := l.trackPublisher("s1", h1); prior != nil {
		t.Fatalf("first track: prior=%v want=nil", prior)
	}
	if got := l.ActivePublishers(); got != 1 {
		t.Fatalf("ActivePublishers=%d want 1", got)
	}
	prior := l.trackPublisher("s1", h2)
	if prior != h1 {
		t.Fatalf("replace track: prior=%v want=h1", prior)
	}
	if got := l.ActivePublishers(); got != 1 {
		t.Fatalf("ActivePublishers after replace=%d want 1", got)
	}

	l.untrackPublisher("s1", h1)
	if got := l.ActivePublishers(); got != 1 {
		t.Fatalf("untrack non-current handler should be no-op; got=%d", got)
	}
	l.untrackPublisher("s1", h2)
	if got := l.ActivePublishers(); got != 0 {
		t.Fatalf("ActivePublishers after final untrack=%d want 0", got)
	}
}
