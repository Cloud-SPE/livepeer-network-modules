// Package httpmultipart implements the runner-side driver for
// http-multipart@v0 fixtures.
//
// Wire-shape-wise this is identical to http-reqresp@v0 from the runner's
// perspective: the request body (a literal multipart/form-data payload
// from the YAML fixture) is sent as-is, the response is read, and the
// same assertions apply. The only meaningful differences are documented
// in the spec (request body shape, recommended size caps); they don't
// require special runner handling.
package httpmultipart

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

const Mode = "http-multipart@v0"

type Driver struct {
	httpClient *http.Client
}

func New() *Driver {
	return &Driver{httpClient: &http.Client{Timeout: 60 * time.Second}}
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
	for k, v := range fx.Request.Headers {
		req.Header.Set(k, substitutePlaceholders(v))
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
	for _, h := range fx.ResponseExpect.HeadersPresent {
		if resp.Header.Get(h) == "" {
			failures = append(failures, "response header missing: "+h)
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

func substitutePlaceholders(s string) string {
	return strings.ReplaceAll(s, "<runner-generated-payment-blob>", "runner-stub-payment")
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
