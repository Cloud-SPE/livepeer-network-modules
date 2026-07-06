// Package httpstream implements the http-stream@v0 interaction-mode driver
// per livepeer-network-protocol/modes/http-stream.md.
//
// Single HTTP request → streaming response (SSE or HTTP chunked). The
// Livepeer-Work-Units value is reported as an HTTP trailer (declared via
// the Trailer response header before WriteHeader, value set after body).
//
// v0.1 implementation note: the broker buffers the full backend response
// before emitting it to the gateway. This preserves the wire-format
// guarantees of the spec (chunked transfer encoding + trailer) while
// keeping the work-unit extractor simple. True chunk-by-chunk pass-through
// (with progressive flushing) is a follow-up; the spec's Conformance
// section is satisfied by the buffered shape.
package httpstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/openaiusage"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "http-stream@v0"

// Driver implements modes.Driver.
type Driver struct{}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a stateless http-stream driver.
func New() *Driver { return &Driver{} }

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve forwards the inbound request to the configured backend, streams the
// backend response back, and emits Livepeer-Work-Units as an HTTP trailer.
func (d *Driver) Serve(ctx context.Context, p modes.Params) error {
	start := time.Now()
	body, err := io.ReadAll(p.Request.Body)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusBadRequest, livepeerheader.ErrInternalError,
			"read request body: "+err.Error())
		return nil
	}
	body, injectedUsage := maybeInjectOpenAIStreamUsage(body, p.Capability, p.Extractor)

	outHeaders := backend.StripLivepeerHeaders(p.Request.Header)
	if injectedUsage {
		// The backend (openai-chat-runner / vLLM) only honors
		// stream_options.include_usage when the request is declared
		// application/json. Some gateways forward chat jobs with a different
		// (or absent) Content-Type, which makes the backend drop the usage
		// block and leaves us unable to meter work units. We just confirmed
		// the body is JSON, so normalize the header to match.
		outHeaders.Set("Content-Type", "application/json")
	}
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
		reportOutcome(p, poolreport.OutcomeBackendFailure, time.Since(start))
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
			"read backend body: "+err.Error())
		reportOutcome(p, poolreport.OutcomeBackendFailure, time.Since(start))
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
		Trailers: resp.Trailer,
		Duration: time.Since(start),
	})
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"extractor: "+err.Error())
		return nil
	}

	// Declare the trailer BEFORE WriteHeader. Go's http server will use
	// chunked transfer encoding (no Content-Length) and append the trailer
	// after the body when we set its value below. Use Add so any
	// Trailer values already declared by upstream middleware (e.g. the
	// payment middleware's X-Livepeer-Settlement trailer) survive.
	p.Writer.Header().Add("Trailer", livepeerheader.WorkUnits)

	for k, vs := range resp.Header {
		if shouldCopyHeader(k) {
			for _, v := range vs {
				p.Writer.Header().Add(k, v)
			}
		}
	}

	if resp.StatusCode >= 500 {
		p.Writer.Header().Set(livepeerheader.Error, livepeerheader.ErrBackendUnavailable)
	}

	p.Writer.WriteHeader(resp.StatusCode)

	// Write body. Flush after to ensure the chunk hits the wire before
	// the trailer is computed (a real streaming impl would flush per
	// backend chunk; this is the v0.1 shape).
	_, _ = p.Writer.Write(respBody)
	if flusher, ok := p.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	// Emit the trailer. Go's http server moves declared-trailer headers to
	// the trailer slot on the wire; the http.ResponseWriter.Header() map
	// retains the value so the Payment / Metrics middleware can still read
	// it after the handler returns.
	p.Writer.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(actualUnits, 10))
	reportOutcome(p, classifyHTTPStatus(resp.StatusCode), time.Since(start))

	return nil
}

func shouldCopyHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length",
		"Connection",
		"Transfer-Encoding",
		"Trailer",
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

func classifyHTTPStatus(statusCode int) string {
	switch {
	case statusCode >= 500:
		return poolreport.OutcomeBackendFailure
	case statusCode >= 400:
		return poolreport.OutcomeCallerFailure
	default:
		return poolreport.OutcomeSuccess
	}
}

func reportOutcome(p modes.Params, outcome string, latency time.Duration) {
	if p.PoolReporter == nil || p.Capability == nil {
		return
	}
	backendID := p.Capability.Backend.ID
	if backendID == "" {
		backendID = p.Capability.Backend.URL
	}
	poolreport.ReportBestEffort(p.PoolReporter, poolreport.BackendOutcome{
		BackendID:        backendID,
		CapabilityID:     p.Capability.ID,
		OfferingID:       p.Capability.OfferingID,
		MemberEthAddress: p.MemberEthAddress,
		Outcome:          outcome,
		LatencyMetricMS:  latency.Milliseconds(),
		OccurredAt:       time.Now().UTC(),
	})
}

// maybeInjectOpenAIStreamUsage forces stream_options.include_usage=true on
// openai:chat-completions requests metered by the openai-usage extractor, so
// the streamed backend response carries a usage block the extractor can read.
// It returns the (possibly rewritten) body and whether a rewrite occurred.
//
// Injection is deliberately independent of the inbound Content-Type: the only
// requirement is that the body parses as JSON. The backend gates its own usage
// handling on an application/json Content-Type, so the caller normalizes the
// outbound header when this returns true. Gating here on the inbound header
// (as a previous version did) let non-application/json gateway requests slip
// through unmetered.
func maybeInjectOpenAIStreamUsage(body []byte, cap *config.Capability, ext extractors.Extractor) ([]byte, bool) {
	if cap == nil || ext == nil {
		return body, false
	}
	if cap.ID != "openai:chat-completions" || ext.Name() != openaiusage.Name {
		return body, false
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("openai-usage: request body not JSON; cannot inject stream_options.include_usage: %v", err)
		return body, false
	}
	streamOptions, _ := payload["stream_options"].(map[string]any)
	if streamOptions == nil {
		streamOptions = map[string]any{}
	}
	streamOptions["include_usage"] = true
	payload["stream_options"] = streamOptions
	rewritten, err := json.Marshal(payload)
	if err != nil {
		log.Printf("openai-usage: request rewrite failed; cannot inject stream_options.include_usage: %v", err)
		return body, false
	}
	return rewritten, true
}
