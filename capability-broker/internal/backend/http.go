package backend

import (
	"context"
	"net/http"
	"time"
)

// HTTPClient is a Forwarder backed by net/http.
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient returns a Forwarder using a default-configured http.Client.
// The client respects context cancellation; callers MUST cancel the context
// to abort an in-flight request.
//
// v0.1: a single shared client; per-capability timeout configuration lands
// alongside the http-stream mode driver in plan 0006.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// Forward issues the outbound request and returns the response.
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
	return c.client.Do(httpReq)
}

// Compile-time interface check.
var _ Forwarder = (*HTTPClient)(nil)
