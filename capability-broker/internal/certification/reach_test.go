package certification

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/gorilla/websocket"
)

func descriptorFor(t *testing.T, public map[string]any, grants ...sessionengine.Grant) *sessionengine.Descriptor {
	t.Helper()
	pub, _ := json.Marshal(public)
	return &sessionengine.Descriptor{Schema: "pcm-transcript/v1", Public: pub, Grants: grants}
}

// A wss field is reached by completing the upgrade with the named grant;
// a runner that refuses the grant, or an address nobody answers, is a
// failure that names the host.
func TestReachDialsAWebSocketWithTheGrant(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = c.Close()
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/sessions/s1/stream"
	grant := sessionengine.Grant{ID: "g1", Operations: []string{"stream-attach"}, Secret: "s3cret"}

	ev, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"url": wsURL}, grant),
		map[string]any{"field": "url", "grant": "stream-attach"})
	if msg != "" || ev["reach_ms"] == nil {
		t.Fatalf("reach = %q %v", msg, ev)
	}
	// Without the grant the runner refuses, and so does the step.
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"url": wsURL}),
		map[string]any{"field": "url"}); msg == "" {
		t.Fatal("an ungranted dial the runner refused must fail")
	}
	// A grant the descriptor does not carry is named.
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"url": wsURL}),
		map[string]any{"grant": "stream-attach"}); !strings.Contains(msg, "no grant") {
		t.Fatalf("msg = %q", msg)
	}
	// Nobody home.
	if ev, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"url": "ws://127.0.0.1:1/x"}),
		map[string]any{"timeout_ms": 500}); msg == "" || ev["reached_host"] != "127.0.0.1:1" {
		t.Fatalf("reach = %q %v", msg, ev)
	}
}

// An https field is reached by a 2xx.
func TestReachGetsAnHTTPField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live/index.m3u8" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"hls_url": srv.URL + "/live/index.m3u8"}),
		map[string]any{"field": "hls_url"}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"hls_url": srv.URL + "/nope"}),
		map[string]any{"field": "hls_url"}); !strings.Contains(msg, "404") {
		t.Fatalf("msg = %q", msg)
	}
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"room": "r"}),
		map[string]any{"field": "url"}); !strings.Contains(msg, "absent") {
		t.Fatalf("msg = %q", msg)
	}
}

// An rtmp field is reached by completing the RTMP handshake against
// whatever answers — here a fake that speaks S0+S1 — and a peer that
// does not answer the handshake fails by name.
func TestReachPerformsTheRTMPHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 1537)
				if _, err := io.ReadFull(c, buf); err != nil || buf[0] != 3 {
					return
				}
				s1 := make([]byte, 1537)
				s1[0] = 3
				_, _ = c.Write(s1)
			}()
		}
	}()
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"rtmp_url": "rtmp://" + ln.Addr().String() + "/live"}),
		map[string]any{"field": "rtmp_url", "timeout_ms": 2000}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := reachDescriptor(context.Background(), descriptorFor(t, map[string]any{"rtmp_url": "rtmp://127.0.0.1:1/live"}),
		map[string]any{"field": "rtmp_url", "timeout_ms": 500}); !strings.Contains(msg, "rtmp handshake") {
		t.Fatalf("msg = %q", msg)
	}
}
