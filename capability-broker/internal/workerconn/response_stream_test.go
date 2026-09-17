package workerconn

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/gorilla/websocket"
)

func streamPair(t *testing.T) (*SessionForwarder, *websocket.Conn) {
	t.Helper()
	ready := make(chan *SessionForwarder, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ready <- NewSessionForwarder(c)
	}))
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := <-ready
	t.Cleanup(func() { c.Close(); f.Close(); server.Close() })
	return f, c
}
func TestIncrementalResponseBeforeCompletion(t *testing.T) {
	f, c := streamPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	firstRead := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var req TunnelMessage
		if err := c.ReadJSON(&req); err != nil {
			done <- err
			return
		}
		if req.ResponseMode != "chunks-v1" {
			done <- io.ErrUnexpectedEOF
			return
		}
		c.WriteJSON(TunnelMessage{Type: "response_start", ID: req.ID, StatusCode: 200, Headers: map[string][]string{"Content-Type": {"text/event-stream"}, "Content-Length": {"999"}}})
		c.WriteJSON(TunnelMessage{Type: "response_chunk", ID: req.ID, Seq: 1, BodyBase64: base64.StdEncoding.EncodeToString([]byte("data: first\n\n"))})
		select {
		case <-firstRead:
		case <-ctx.Done():
			done <- ctx.Err()
			return
		}
		var ack TunnelMessage
		if err := c.ReadJSON(&ack); err != nil {
			done <- err
			return
		}
		if ack.Type != "response_ack" || ack.Seq != 1 {
			done <- io.ErrUnexpectedEOF
			return
		}
		c.WriteJSON(TunnelMessage{Type: "response_chunk", ID: req.ID, Seq: 2, BodyBase64: base64.StdEncoding.EncodeToString([]byte{0, 255, 1})})
		c.WriteJSON(TunnelMessage{Type: "response_end", ID: req.ID, Seq: 2, Trailers: map[string][]string{"X-Usage": {"42"}}})
		done <- nil
	}()
	resp, err := f.Forward(ctx, backend.ForwardRequest{URL: "http://runner"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ContentLength != -1 || resp.Header.Get("Content-Length") != "" {
		t.Fatal("stream length was forced")
	}
	p := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(resp.Body, p); err != nil {
		t.Fatal(err)
	}
	if string(p) != "data: first\n\n" {
		t.Fatalf("first=%q", p)
	}
	close(firstRead)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != string([]byte{0, 255, 1}) || resp.Trailer.Get("X-Usage") != "42" {
		t.Fatalf("rest=%v trailers=%v", rest, resp.Trailer)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestStreamingErrorsAreNotEOF(t *testing.T) {
	for _, kind := range []string{"response_error", "bad_sequence", "disconnect"} {
		t.Run(kind, func(t *testing.T) {
			f, c := streamPair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			go func() {
				var req TunnelMessage
				c.ReadJSON(&req)
				c.WriteJSON(TunnelMessage{Type: "response_start", ID: req.ID, StatusCode: 200})
				switch kind {
				case "disconnect":
					c.Close()
				case "bad_sequence":
					c.WriteJSON(TunnelMessage{Type: "response_chunk", ID: req.ID, Seq: 2, BodyBase64: "YQ=="})
				default:
					c.WriteJSON(TunnelMessage{Type: "response_error", ID: req.ID, Error: "interrupted"})
				}
			}()
			resp, err := f.Forward(ctx, backend.ForwardRequest{URL: "http://runner"})
			if err != nil {
				if kind == "disconnect" {
					return
				}
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if _, err := io.ReadAll(resp.Body); err == nil {
				t.Fatal("interrupted stream reported success")
			}
		})
	}
}
func TestStreamCloseSendsCancellation(t *testing.T) {
	f, c := streamPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	observed := make(chan TunnelMessage, 1)
	go func() {
		var req TunnelMessage
		c.ReadJSON(&req)
		c.WriteJSON(TunnelMessage{Type: "response_start", ID: req.ID, StatusCode: 200})
		var msg TunnelMessage
		c.ReadJSON(&msg)
		observed <- msg
	}()
	resp, err := f.Forward(ctx, backend.ForwardRequest{URL: "http://runner"})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case msg := <-observed:
		if msg.Type != "request_cancel" {
			t.Fatalf("got %+v", msg)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestQueuedCompletionSurvivesPeerDisconnect(t *testing.T) {
	f, c := streamPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		var req TunnelMessage
		c.ReadJSON(&req)
		c.WriteJSON(TunnelMessage{Type: "response_start", ID: req.ID, StatusCode: 200})
		c.WriteJSON(TunnelMessage{Type: "response_chunk", ID: req.ID, Seq: 1, BodyBase64: "YQ=="})
		c.WriteJSON(TunnelMessage{Type: "response_end", ID: req.ID, Seq: 1})
		c.Close()
	}()
	resp, err := f.Forward(ctx, backend.ForwardRequest{URL: "http://runner"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	select {
	case <-f.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || string(data) != "a" {
		t.Fatalf("body=%q err=%v", data, err)
	}
}
func TestCloseUnblocksBodyRead(t *testing.T) {
	f, c := streamPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		var req TunnelMessage
		c.ReadJSON(&req)
		c.WriteJSON(TunnelMessage{Type: "response_start", ID: req.ID, StatusCode: 200})
		var ignored TunnelMessage
		c.ReadJSON(&ignored)
	}()
	resp, err := f.Forward(ctx, backend.ForwardRequest{URL: "http://runner"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := resp.Body.Read(make([]byte, 1)); done <- err }()
	resp.Body.Close()
	select {
	case err := <-done:
		if err == nil || err == io.EOF {
			t.Fatalf("close read error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("body read leaked after Close")
	}
}
