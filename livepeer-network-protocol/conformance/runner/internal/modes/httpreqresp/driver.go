// Package httpreqresp implements the runner-side driver for http-reqresp@v0
// fixtures. It sends one request to the broker, asserts the response shape,
// and inspects the mock backend's recorded call against the fixture's
// backend_assertions.
package httpreqresp

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

// Mode is the canonical mode-name@vN string this driver implements.
const Mode = "http-reqresp@v0"

// Driver runs http-reqresp@v0 fixtures.
type Driver struct {
	httpClient *http.Client
}

// New returns a Driver with default HTTP timeouts.
func New() *Driver {
	return &Driver{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Mode returns the canonical mode string.
func (d *Driver) Mode() string { return Mode }

// Run executes one fixture against the broker at brokerURL. The mock backend
// is configured per the fixture's backend_response before sending; recorded
// calls are inspected for backend_assertions afterward.
func (d *Driver) Run(ctx context.Context, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}

	// 1. Program the mock backend for this fixture.
	mock.Reset()
	mock.Set(mockbackend.Response{
		Status:  fx.BackendResponse.Status,
		Headers: copyMap(fx.BackendResponse.Headers),
		Body:    fx.BackendResponse.Body,
	})

	// 2. Build the inbound request to the broker.
	req, err := http.NewRequestWithContext(ctx, fx.Request.Method, brokerURL+fx.Request.Path,
		strings.NewReader(fx.Request.Body))
	if err != nil {
		return fail(fx, fmt.Sprintf("build request: %v", err))
	}
	hdrs, err := envelope.SubstituteHeaders(fx.Request.Headers)
	if err != nil {
		return fail(fx, fmt.Sprintf("build payment envelope: %v", err))
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}

	// 3. Send.
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fail(fx, fmt.Sprintf("call broker: %v", err))
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fail(fx, fmt.Sprintf("read response body: %v", err))
	}

	// 4. Assert the response shape.
	if resp.StatusCode != fx.ResponseExpect.Status {
		failures = append(failures, fmt.Sprintf("response.status: expected %d, got %d",
			fx.ResponseExpect.Status, resp.StatusCode))
	}
	for _, h := range fx.ResponseExpect.HeadersPresent {
		if resp.Header.Get(h) == "" {
			failures = append(failures, fmt.Sprintf("response header missing: %s", h))
		}
	}
	if fx.ResponseExpect.LivepeerWorkUnits != nil {
		got, _ := strconv.ParseUint(resp.Header.Get("Livepeer-Work-Units"), 10, 64)
		if got != *fx.ResponseExpect.LivepeerWorkUnits {
			failures = append(failures, fmt.Sprintf("Livepeer-Work-Units: expected %d, got %d",
				*fx.ResponseExpect.LivepeerWorkUnits, got))
		}
	}
	if fx.ResponseExpect.BodyPassthrough && string(respBody) != fx.BackendResponse.Body {
		failures = append(failures,
			fmt.Sprintf("body_passthrough: response body != backend body\n            want: %q\n             got: %q",
				fx.BackendResponse.Body, string(respBody)))
	}

	// 5. Assert backend-side observations.
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
				failures = append(failures,
					fmt.Sprintf("backend.livepeer_headers_present: expected %v, got %v",
						*ba.LivepeerHeadersPresent, has))
			}
		}
		if ba.AuthorizationHeaderPresent != nil {
			has := last.Headers.Get("Authorization") != ""
			if has != *ba.AuthorizationHeaderPresent {
				failures = append(failures,
					fmt.Sprintf("backend.authorization_header_present: expected %v, got %v",
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
