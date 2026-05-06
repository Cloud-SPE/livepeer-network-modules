// Package httpmultipart implements the http-multipart@v0 interaction-mode
// driver per livepeer-network-protocol/modes/http-multipart.md.
//
// Multipart/form-data request body → regular HTTP response. Identical
// payment lifecycle to http-reqresp; the only delta is that the request
// body is multipart and may be larger. v0.1 reads the full body into
// memory before forwarding (suitable for the spec's recommended 100 MiB
// upload cap); a streaming-forward implementation is a follow-up.
package httpmultipart

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "http-multipart@v0"

// Driver implements modes.Driver.
type Driver struct{}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a stateless http-multipart driver.
func New() *Driver { return &Driver{} }

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve forwards a multipart-form-data request to the backend and writes
// the regular HTTP response back. Content-Type (with boundary) is
// preserved on the outbound request unchanged; the broker treats the body
// as opaque bytes.
func (d *Driver) Serve(ctx context.Context, p modes.Params) error {
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
		Status:  resp.StatusCode,
		Body:    respBody,
		Headers: resp.Header,
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

	if resp.StatusCode >= 500 {
		p.Writer.Header().Set(livepeerheader.Error, livepeerheader.ErrBackendUnavailable)
	}

	p.Writer.WriteHeader(resp.StatusCode)
	_, _ = p.Writer.Write(respBody)
	return nil
}

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
