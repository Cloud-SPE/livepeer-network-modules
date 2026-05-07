package sessioncontrolplusmedia

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeBackend is an in-memory Backend used by C4 tests. It records
// every Attach / Detach / Reattach / Shutdown call and drops envelopes
// onto a per-session inbound channel that the test reads.
type fakeBackend struct {
	mu        sync.Mutex
	sessions  map[string]*fakeSession
	detaches  []string
	reattach  []string
	shutdown  []string
}

type fakeSession struct {
	inbound  chan ControlEnvelope
	outbound chan ControlEnvelope
	done     chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{sessions: make(map[string]*fakeSession)}
}

func (f *fakeBackend) AttachControl(_ context.Context, sessionID string) (BackendControl, error) {
	s := &fakeSession{
		inbound:  make(chan ControlEnvelope, 16),
		outbound: make(chan ControlEnvelope, 16),
		done:     make(chan struct{}),
	}
	f.mu.Lock()
	f.sessions[sessionID] = s
	f.mu.Unlock()
	return BackendControl{
		Inbound:  s.inbound,
		Outbound: s.outbound,
		Done:     s.done,
		Cancel:   func() {},
	}, nil
}

func (f *fakeBackend) DetachControl(sessionID string, _ int, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detaches = append(f.detaches, sessionID)
}

func (f *fakeBackend) ReattachControl(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reattach = append(f.reattach, sessionID)
}

func (f *fakeBackend) Shutdown(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdown = append(f.shutdown, sessionID)
}

func (f *fakeBackend) sessionFor(id string) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[id]
}

func TestRunnerRelayWorkloadEnvelope(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 16})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 200 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	d := New(store, cfg)
	fb := newFakeBackend()
	d.SetBackend(fb)

	ctrl, _ := fb.AttachControl(context.Background(), "sess_relay")
	rec := &SessionRecord{
		SessionID: "sess_relay",
		OpenedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	_ = store.Add(rec)
	bc := ctrl
	rec.control = &bc

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_relay/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read started: %v", err)
	}

	workload := ControlEnvelope{Type: "set_persona", Body: []byte(`{"name":"luna"}`)}
	pl, _ := json.Marshal(workload)
	if err := conn.WriteMessage(websocket.TextMessage, pl); err != nil {
		t.Fatalf("write workload: %v", err)
	}

	sess := fb.sessionFor("sess_relay")
	if sess == nil {
		t.Fatal("session not registered in fake backend")
	}
	select {
	case env := <-sess.inbound:
		if env.Type != "set_persona" {
			t.Fatalf("backend got type=%q want set_persona", env.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not receive workload envelope")
	}

	emit := ControlEnvelope{Type: "say.text", Body: []byte(`{"text":"hi"}`)}
	sess.outbound <- emit
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, data, err := conn.ReadMessage()
		if err != nil {
			continue
		}
		var got ControlEnvelope
		if json.Unmarshal(data, &got) == nil && got.Type == "say.text" {
			return
		}
	}
	t.Fatal("did not see relayed runner envelope on the WS")
}

func TestSessionEndShortCircuitsAndShutsDownRunner(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 16})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 200 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	d := New(store, cfg)
	fb := newFakeBackend()
	d.SetBackend(fb)

	ctrl, _ := fb.AttachControl(context.Background(), "sess_end")
	rec := &SessionRecord{
		SessionID: "sess_end",
		OpenedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	_ = store.Add(rec)
	bc := ctrl
	rec.control = &bc

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_end/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = conn.ReadMessage()

	end := ControlEnvelope{Type: TypeSessionEnd}
	pl, _ := json.Marshal(end)
	_ = conn.WriteMessage(websocket.TextMessage, pl)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fb.mu.Lock()
		if len(fb.shutdown) > 0 {
			fb.mu.Unlock()
			return
		}
		fb.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Backend.Shutdown was not invoked after session.end")
}

func TestDisconnectFiresDetachReconnectFiresReattach(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 16})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 200 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	cfg.ReconnectWindow = 5 * time.Second
	d := New(store, cfg)
	fb := newFakeBackend()
	d.SetBackend(fb)

	ctrl, _ := fb.AttachControl(context.Background(), "sess_d")
	rec := &SessionRecord{
		SessionID: "sess_d",
		OpenedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	_ = store.Add(rec)
	bc := ctrl
	rec.control = &bc

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_d/control"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	_ = conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = conn1.ReadMessage()
	_ = conn1.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fb.mu.Lock()
		gotDetach := len(fb.detaches) > 0
		fb.mu.Unlock()
		if gotDetach {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fb.mu.Lock()
	if len(fb.detaches) == 0 {
		fb.mu.Unlock()
		t.Fatal("DetachControl was not invoked on WS close")
	}
	fb.mu.Unlock()

	hdr := http.Header{}
	hdr.Set("Last-Seq", "0")
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer conn2.Close()
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _ = conn2.ReadMessage()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fb.mu.Lock()
		gotReattach := len(fb.reattach) > 0
		fb.mu.Unlock()
		if gotReattach {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("ReattachControl was not invoked on reconnect")
}
