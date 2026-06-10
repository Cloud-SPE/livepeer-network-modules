package workerconn

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
)

func TestSessionForwarderRoundTrip(t *testing.T) {
	upgrader := websocket.Upgrader{}
	forwarderCh := make(chan *SessionForwarder, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		forwarderCh <- NewSessionForwarder(conn)
	}))
	defer server.Close()

	workerURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(workerURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var req TunnelMessage
		if err := conn.ReadJSON(&req); err != nil {
			t.Errorf("ReadJSON() error = %v", err)
			return
		}
		if req.Type != MessageTypeRequest || req.Method != http.MethodPost || req.URL != "http://worker.local/v1" {
			t.Errorf("request = %+v", req)
		}
		resp := TunnelMessage{
			Type:       MessageTypeResponse,
			ID:         req.ID,
			StatusCode: http.StatusAccepted,
			Headers:    map[string][]string{"Content-Type": []string{"application/json"}},
			BodyBase64: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
		}
		if err := conn.WriteJSON(resp); err != nil {
			t.Errorf("WriteJSON() error = %v", err)
		}
	}()
	serverForwarder := <-forwarderCh
	resp, err := serverForwarder.Forward(context.Background(), backend.ForwardRequest{
		URL:    "http://worker.local/v1",
		Method: http.MethodPost,
		Body:   strings.NewReader(`{}`),
	})
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	<-done
}

func TestQUICSessionForwarderStreamsBody(t *testing.T) {
	tlsConf, err := ServerTLSConfig()
	if err != nil {
		t.Fatalf("ServerTLSConfig() error = %v", err)
	}
	listener, err := quic.ListenAddr("127.0.0.1:0", tlsConf, nil)
	if err != nil {
		t.Fatalf("ListenAddr() error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept(context.Background())
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			t.Errorf("AcceptStream() error = %v", err)
			return
		}
		msg, err := ReadQUICFrameHeader(stream)
		if err != nil {
			t.Errorf("ReadQUICFrameHeader() error = %v", err)
			return
		}
		if msg.Type != MessageTypeRequest || msg.URL != "http://worker.local/v1" {
			t.Errorf("request header = %+v", msg)
		}
		body, err := io.ReadAll(stream)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
			return
		}
		if string(body) != "stream-body" {
			t.Errorf("request body = %q", string(body))
		}
		if err := WriteQUICFrameHeader(stream, TunnelMessage{
			Type:       MessageTypeResponse,
			ID:         msg.ID,
			StatusCode: http.StatusCreated,
			Headers:    map[string][]string{"Livepeer-Work-Units": []string{"9"}},
		}); err != nil {
			t.Errorf("WriteQUICFrameHeader() error = %v", err)
			return
		}
		if _, err := stream.Write([]byte("stream-response")); err != nil {
			t.Errorf("Write() error = %v", err)
			return
		}
		_ = stream.Close()
	}()

	conn, err := quic.DialAddr(context.Background(), listener.Addr().String(), ClientTLSConfig(), nil)
	if err != nil {
		t.Fatalf("DialAddr() error = %v", err)
	}
	forwarder := NewQUICSessionForwarder(conn)
	resp, err := forwarder.Forward(context.Background(), backend.ForwardRequest{
		URL:    "http://worker.local/v1",
		Method: http.MethodPost,
		Body:   strings.NewReader("stream-body"),
	})
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("Livepeer-Work-Units") != "9" || string(got) != "stream-response" {
		t.Fatalf("response status=%d headers=%v body=%q", resp.StatusCode, resp.Header, string(got))
	}
	<-serverDone
}
