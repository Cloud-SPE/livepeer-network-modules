package backend

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// HTTPClient forwards requests to a URL-configured backend — the
// standalone broker's own runners (`backend.url`); attached runners go
// over the agent's tunnel and never touch this client.
//
// No total-response timeout. A paid-job stream — a VOD or ABR encode
// whose terminal result and work-unit trailer arrive when the encode
// ends — legitimately runs longer than any fixed budget, and
// http.Client.Timeout counts the whole body, so a five-minute total
// killed every long encode before its claim arrived (found by the
// transcode team, 2026-09-05). What a stream needs instead: a bound on
// the wait for headers, and a bound on silence — a body that stops
// producing bytes is dead; one that keeps producing is working.
type HTTPClient struct {
	client      *http.Client
	idleTimeout time.Duration
}

// Timeouts are the client's bounds. Zero takes the default.
type Timeouts struct {
	// Connect bounds the TCP dial (default 10s).
	Connect time.Duration
	// ResponseHeader bounds the wait for the status line and headers
	// (default 60s): a runner that has not started answering.
	ResponseHeader time.Duration
	// Idle bounds silence between body bytes (default 2m): a runner
	// that stopped mid-stream. Reset on every read.
	Idle time.Duration
}

func (t Timeouts) withDefaults() Timeouts {
	if t.Connect <= 0 {
		t.Connect = 10 * time.Second
	}
	if t.ResponseHeader <= 0 {
		t.ResponseHeader = 60 * time.Second
	}
	if t.Idle <= 0 {
		t.Idle = 2 * time.Minute
	}
	return t
}

// NewHTTPClient returns a Forwarder with the default timeouts.
func NewHTTPClient() *HTTPClient { return NewHTTPClientWithTimeouts(Timeouts{}) }

// NewHTTPClientWithTimeouts returns a Forwarder with the given bounds.
func NewHTTPClientWithTimeouts(t Timeouts) *HTTPClient {
	t = t.withDefaults()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: t.Connect}).DialContext
	transport.ResponseHeaderTimeout = t.ResponseHeader
	return &HTTPClient{client: &http.Client{Transport: transport}, idleTimeout: t.Idle}
}

// Forward issues the outbound request and returns the response. The
// body is wrapped so that a read that waits longer than the idle bound
// fails with ErrIdle; the caller closes it as before.
//
// The caller is responsible for:
//   - Stripping Livepeer-* headers via StripLivepeerHeaders before invoking.
//   - Injecting backend-specific auth via AuthApplier.Apply before invoking.
//   - Closing resp.Body after reading.
func (c *HTTPClient) Forward(ctx context.Context, req ForwardRequest) (*http.Response, error) {
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, req.Body)
	if err != nil {
		return nil, err
	}
	if req.Headers != nil {
		httpReq.Header = req.Headers
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	resp.Body = &idleBody{rc: resp.Body, idle: c.idleTimeout}
	return resp, nil
}

// ErrIdle is the error a body read returns when the backend produced
// nothing for the idle bound.
var ErrIdle = errors.New("backend: response body idle past the idle timeout")

// idleBody fails a Read that outlasts the idle bound. Each read starts a
// fresh timer, so a stream that keeps producing is never cut, however
// long it runs.
type idleBody struct {
	rc   io.ReadCloser
	idle time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := b.rc.Read(p)
		ch <- result{n, err}
	}()
	timer := time.NewTimer(b.idle)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-timer.C:
		// Closing unblocks the pending read; its result is dropped.
		_ = b.rc.Close()
		return 0, ErrIdle
	}
}

func (b *idleBody) Close() error { return b.rc.Close() }

// Compile-time interface check.
var _ Forwarder = (*HTTPClient)(nil)
