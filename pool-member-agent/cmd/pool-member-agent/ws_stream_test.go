package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWSResponseStreamingCreditAndMultiplexing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cancelled := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/short" {
			w.Header().Set("Trailer", "X-Usage")
			w.Write([]byte("second"))
			w.Header().Set("X-Usage", "7")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		for i := 0; i < responseWindow+4; i++ {
			if _, err := w.Write(make([]byte, responseChunkSize)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer upstream.Close()
	messages := make(chan tunnelMessage, 32)
	x := newWSExchanges(ctx, func(m tunnelMessage) error { messages <- m; return nil })
	defer x.close()
	routes := runnerRoutes{"r": {URL: upstream.URL}}
	if err := x.start(routes, tunnelMessage{Type: "request", ID: "slow", URL: "/slow", ResponseMode: "chunks-v1"}); err != nil {
		t.Fatal(err)
	}
	next := func() tunnelMessage {
		select {
		case m := <-messages:
			return m
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return tunnelMessage{}
		}
	}
	if m := next(); m.Type != "response_start" {
		t.Fatalf("start=%+v", m)
	}
	for i := uint64(1); i <= responseWindow; i++ {
		m := next()
		if m.Type != "response_chunk" || m.Seq != i {
			t.Fatalf("chunk=%+v", m)
		}
	}
	select {
	case m := <-messages:
		t.Fatalf("exceeded credit: %+v", m)
	case <-time.After(30 * time.Millisecond):
	}
	// A slow request has exhausted its window; another request still completes.
	if err := x.start(routes, tunnelMessage{Type: "request", ID: "fast", URL: "/short", ResponseMode: "chunks-v1"}); err != nil {
		t.Fatal(err)
	}
	for {
		m := next()
		if m.ID != "fast" {
			t.Fatalf("slow request exceeded credit: %+v", m)
		}
		if m.Type == "response_end" {
			if m.Trailers["X-Usage"][0] != "7" {
				t.Fatal("lost trailers")
			}
			break
		}
	}
	x.control(tunnelMessage{Type: "response_ack", ID: "slow", Seq: 1})
	if m := next(); m.Type != "response_chunk" || m.Seq != 9 {
		t.Fatalf("credit did not resume read: %+v", m)
	}
	x.control(tunnelMessage{Type: "request_cancel", ID: "slow"})
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("runner HTTP request not cancelled")
	}
}
func TestWSLegacyResponseCompatibility(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "legacy") }))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	messages := make(chan tunnelMessage, 1)
	x := newWSExchanges(ctx, func(m tunnelMessage) error { messages <- m; return nil })
	defer x.close()
	x.start(runnerRoutes{"r": {URL: upstream.URL}}, tunnelMessage{Type: "request", ID: "old", URL: "/"})
	select {
	case msg := <-messages:
		body, _ := base64Decode(msg.BodyBase64)
		if msg.Type != "response" || string(body) != "legacy" {
			t.Fatalf("legacy=%+v", msg)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestInvalidCreditCancelsExchange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x := newWSExchanges(ctx, func(tunnelMessage) error { return nil })
	requestCtx, stop := context.WithCancel(ctx)
	defer stop()
	e := &wsExchange{cancel: stop, credit: make(chan struct{}, responseWindow), sent: 1}
	x.active["r"] = e
	x.control(tunnelMessage{Type: "response_ack", ID: "r", Seq: 2})
	select {
	case <-requestCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("invalid credit did not cancel")
	}
}
