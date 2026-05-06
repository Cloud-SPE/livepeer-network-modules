// Package httpreqresp implements the http-reqresp@v0 interaction-mode driver
// per livepeer-network-protocol/modes/http-reqresp.md.
//
// One HTTP request → one HTTP response. Single-debit with post-Serve
// reconciliation; the Payment middleware does the reconcile after this
// driver sets the Livepeer-Work-Units response header.
package httpreqresp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "http-reqresp@v0"

// Driver implements modes.Driver.
type Driver struct{}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a stateless http-reqresp driver.
func New() *Driver { return &Driver{} }

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve forwards the inbound request to the configured backend and writes the
// backend's response back to the gateway with Livepeer-Work-Units set.
func (d *Driver) Serve(ctx context.Context, p modes.Params) error {
	start := time.Now()
	body, err := io.ReadAll(p.Request.Body)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusBadRequest, livepeerheader.ErrInternalError,
			"read request body: "+err.Error())
		return nil
	}

	outHeaders := backend.StripLivepeerHeaders(p.Request.Header)
	if p.Auth != nil {
		if err := p.Auth.Apply(outHeaders, p.Capability.Backend.Auth); err != nil {
			livepeerheader.WriteError(p.Writer, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
				"inject backend auth: "+err.Error())
			return nil
		}
	}

	resp, err := p.Backend.Forward(ctx, backend.ForwardRequest{
		URL:     p.Capability.Backend.URL,
		Method:  p.Request.Method,
		Headers: outHeaders,
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
			"backend forward: "+err.Error())
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
			"read backend body: "+err.Error())
		return nil
	}

	actualUnits, err := p.Extractor.Extract(ctx, &extractors.Request{
		Method:  p.Request.Method,
		Body:    body,
		Headers: p.Request.Header,
	}, &extractors.Response{
		Status:   resp.StatusCode,
		Body:     respBody,
		Headers:  resp.Header,
		Duration: time.Since(start),
	})
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"extractor: "+err.Error())
		return nil
	}

	for k, vs := range resp.Header {
		if shouldCopyHeader(k) {
			for _, v := range vs {
				p.Writer.Header().Add(k, v)
			}
		}
	}
	p.Writer.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(actualUnits, 10))

	// If the backend returned a 5xx, surface it as backend_unavailable so the
	// gateway routes around. 4xx is passed through unchanged (caller's fault).
	if resp.StatusCode >= 500 {
		p.Writer.Header().Set(livepeerheader.Error, livepeerheader.ErrBackendUnavailable)
	}

	p.Writer.WriteHeader(resp.StatusCode)
	_, _ = p.Writer.Write(respBody)
	return nil
}

// shouldCopyHeader returns true if h is a backend response header the broker
// should pass through. Hop-by-hop and length-recomputed headers are skipped.
func shouldCopyHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length",
		"Connection",
		"Transfer-Encoding",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Upgrade":
		return false
	}
	return true
}
