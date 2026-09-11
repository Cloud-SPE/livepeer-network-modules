package backend

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A stream that keeps producing is never cut, however long it runs
// past any fixed budget; a stream that goes silent past the idle bound
// is. The five-minute total this replaces would have failed the first
// case for every encode longer than five minutes.
func TestStreamingBodyIsBoundedByIdleNotTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		switch r.URL.Path {
		case "/steady":
			for i := 0; i < 12; i++ {
				_, _ = io.WriteString(w, "data: tick\n\n")
				fl.Flush()
				time.Sleep(60 * time.Millisecond)
			}
			_, _ = io.WriteString(w, "data: end\n\n")
		case "/stall":
			_, _ = io.WriteString(w, "data: first\n\n")
			fl.Flush()
			time.Sleep(900 * time.Millisecond)
			_, _ = io.WriteString(w, "data: late\n\n")
		}
	}))
	defer srv.Close()
	// Idle 300ms; the steady stream runs ~720ms total, longer than any
	// single idle window but never silent for one.
	c := NewHTTPClientWithTimeouts(Timeouts{Idle: 300 * time.Millisecond})

	resp, err := c.Forward(context.Background(), ForwardRequest{URL: srv.URL + "/steady", Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.HasSuffix(string(body), "data: end\n\n") {
		t.Fatalf("steady stream: err=%v body=%q", err, body)
	}

	resp, err = c.Forward(context.Background(), ForwardRequest{URL: srv.URL + "/stall", Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != ErrIdle {
		t.Fatalf("stalled stream: err = %v, want ErrIdle", err)
	}
}

// Headers that never come are bounded separately from the body.
func TestResponseHeaderTimeoutStillApplies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewHTTPClientWithTimeouts(Timeouts{ResponseHeader: 200 * time.Millisecond})
	if _, err := c.Forward(context.Background(), ForwardRequest{URL: srv.URL, Method: http.MethodGet}); err == nil {
		t.Fatal("a backend that never sends headers must time out")
	}
}
