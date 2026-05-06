// Package rtmpingresshlsegress implements the runner-side driver for
// rtmp-ingress-hls-egress@v0 fixtures.
//
// v0.1 narrowed scope per plan 0011: session-open phase only. The runner
// sends the session-open POST, asserts 202, and verifies the response
// body contains the required URL fields (rtmp_ingest_url,
// hls_playback_url, control_url, expires_at). Actual RTMP push and HLS
// retrieval are deferred to a follow-up plan.
package rtmpingresshlsegress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

const Mode = "rtmp-ingress-hls-egress@v0"

type Driver struct {
	httpClient *http.Client
}

func New() *Driver {
	return &Driver{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (d *Driver) Mode() string { return Mode }

func (d *Driver) Run(ctx context.Context, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	return runSessionOpen(ctx, d.httpClient, brokerURL, fx, mock)
}

// runSessionOpen is shared between rtmp-ingress and session-control-plus-media
// (both have session-open POST in v0.1).
func runSessionOpen(ctx context.Context, client *http.Client, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}

	mock.Reset()

	req, err := http.NewRequestWithContext(ctx, fx.Request.Method, brokerURL+fx.Request.Path,
		strings.NewReader(fx.Request.Body))
	if err != nil {
		return fail(fx, "build request: "+err.Error())
	}
	for k, v := range fx.Request.Headers {
		req.Header.Set(k, substitutePlaceholders(v))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fail(fx, "call broker: "+err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
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

	if len(fx.ResponseExpect.BodyFieldsPresent) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			failures = append(failures, "response body is not JSON: "+err.Error())
		} else {
			for _, field := range fx.ResponseExpect.BodyFieldsPresent {
				if v := lookupDotted(parsed, field); v == "" || v == "<nil>" {
					failures = append(failures,
						fmt.Sprintf("response body field %q is missing or empty", field))
				}
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

// lookupDotted returns the string value of dotted path "a.b.c" in m, or
// "" if any segment is missing or non-string. Numbers and bools are
// fmt-stringified.
func lookupDotted(m map[string]any, path string) string {
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		v, exists := mm[p]
		if !exists {
			return ""
		}
		cur = v
	}
	if cur == nil {
		return ""
	}
	return fmt.Sprintf("%v", cur)
}

func fail(fx fixtures.Fixture, msg string) report.Result {
	return report.Result{Name: fx.Name, Mode: fx.Mode, Pass: false, Failures: []string{msg}}
}

func substitutePlaceholders(s string) string {
	return strings.ReplaceAll(s, "<runner-generated-payment-blob>", "runner-stub-payment")
}
