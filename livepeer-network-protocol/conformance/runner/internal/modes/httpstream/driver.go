// Package httpstream implements the runner-side driver for http-stream@v0
// fixtures.
//
// Differences from http-reqresp@v0: the runner reads the full response body
// (to ensure the http.Client populates resp.Trailer), then asserts:
//   - Trailer: Livepeer-Work-Units is declared in response headers.
//   - The trailer slot carries the expected Livepeer-Work-Units value.
//
// Backend assertions are identical to http-reqresp@v0.
package httpstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/envelope"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

const Mode = "http-stream@v0"

type Driver struct {
	httpClient *http.Client
}

func New() *Driver {
	return &Driver{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (d *Driver) Mode() string { return Mode }

func (d *Driver) Run(ctx context.Context, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}

	mock.Reset()
	mock.Set(mockbackend.Response{
		Status:  fx.BackendResponse.Status,
		Headers: copyMap(fx.BackendResponse.Headers),
		Body:    fx.BackendResponse.Body,
	})

	req, err := http.NewRequestWithContext(ctx, fx.Request.Method, brokerURL+fx.Request.Path,
		strings.NewReader(fx.Request.Body))
	if err != nil {
		return fail(fx, "build request: "+err.Error())
	}
	hdrs, err := envelope.SubstituteHeaders(fx.Request.Headers)
	if err != nil {
		return fail(fx, "build payment envelope: "+err.Error())
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fail(fx, "call broker: "+err.Error())
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fail(fx, "read response body: "+err.Error())
	}

	if resp.StatusCode != fx.ResponseExpect.Status {
		failures = append(failures, fmt.Sprintf("response.status: expected %d, got %d",
			fx.ResponseExpect.Status, resp.StatusCode))
	}

	// http-stream@v0 contract: Livepeer-Work-Units MUST be carried in the
	// trailer slot, not the regular header. Go's HTTP client populates
	// resp.Trailer with declared trailer values once the body is fully
	// read, and removes the announcement from resp.Header — so checking
	// resp.Trailer is the correct test for both "declared" and "value set".
	trailerVal := resp.Trailer.Get("Livepeer-Work-Units")
	headerVal := resp.Header.Get("Livepeer-Work-Units")

	if trailerVal == "" {
		if headerVal != "" {
			failures = append(failures,
				fmt.Sprintf("Livepeer-Work-Units in response header (%q) not in trailer — http-stream@v0 requires the trailer slot", headerVal))
		} else {
			failures = append(failures, "Livepeer-Work-Units missing from response trailer (and header)")
		}
	}

	var actualUnits uint64
	if trailerVal != "" {
		actualUnits, _ = strconv.ParseUint(trailerVal, 10, 64)
	} else if headerVal != "" {
		actualUnits, _ = strconv.ParseUint(headerVal, 10, 64)
	}

	if fx.ResponseExpect.LivepeerWorkUnits != nil && actualUnits != *fx.ResponseExpect.LivepeerWorkUnits {
		failures = append(failures,
			fmt.Sprintf("Livepeer-Work-Units: expected %d, got %d (trailer=%q header=%q)",
				*fx.ResponseExpect.LivepeerWorkUnits, actualUnits, trailerVal, headerVal))
	}

	for _, h := range fx.ResponseExpect.HeadersPresent {
		// For http-stream, accept the value from either the regular
		// header slot OR the trailer slot.
		if resp.Header.Get(h) == "" && resp.Trailer.Get(h) == "" {
			failures = append(failures, "response header/trailer missing: "+h)
		}
	}

	if fx.ResponseExpect.BodyPassthrough && string(respBody) != fx.BackendResponse.Body {
		failures = append(failures,
			fmt.Sprintf("body_passthrough: response body != backend body\n            want: %q\n             got: %q",
				fx.BackendResponse.Body, string(respBody)))
	}

	last := mock.LastCall()
	if last == nil {
		failures = append(failures, "backend_assertions: no calls recorded by mock backend")
	} else {
		ba := fx.BackendAssertions
		if ba.Method != "" && last.Method != ba.Method {
			failures = append(failures, fmt.Sprintf("backend.method: expected %s, got %s",
				ba.Method, last.Method))
		}
		if ba.BodyReceivedRaw != "" && last.Body != ba.BodyReceivedRaw {
			failures = append(failures,
				fmt.Sprintf("backend.body_received_raw mismatch\n            want: %q\n             got: %q",
					ba.BodyReceivedRaw, last.Body))
		}
		if ba.LivepeerHeadersPresent != nil {
			has := hasLivepeerHeaders(last.Headers)
			if has != *ba.LivepeerHeadersPresent {
				failures = append(failures, fmt.Sprintf("backend.livepeer_headers_present: expected %v, got %v",
					*ba.LivepeerHeadersPresent, has))
			}
		}
		if ba.AuthorizationHeaderPresent != nil {
			has := last.Headers.Get("Authorization") != ""
			if has != *ba.AuthorizationHeaderPresent {
				failures = append(failures, fmt.Sprintf("backend.authorization_header_present: expected %v, got %v",
					*ba.AuthorizationHeaderPresent, has))
			}
		}
	}

	return report.Result{
		Name:     fx.Name,
		Mode:     fx.Mode,
		Pass:     len(failures) == 0,
		Failures: failures,
	}
}

func fail(fx fixtures.Fixture, msg string) report.Result {
	return report.Result{Name: fx.Name, Mode: fx.Mode, Pass: false, Failures: []string{msg}}
}

func hasLivepeerHeaders(h http.Header) bool {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "livepeer-") {
			return true
		}
	}
	return false
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
