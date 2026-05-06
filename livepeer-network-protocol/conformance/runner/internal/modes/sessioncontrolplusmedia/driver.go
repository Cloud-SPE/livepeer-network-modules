// Package sessioncontrolplusmedia implements the runner-side driver for
// session-control-plus-media@v0 fixtures.
//
// v0.1 narrowed scope per plan 0012: session-open phase only. Same wire
// shape as rtmp-ingress-hls-egress (POST returning 202 with required body
// fields); the body fields differ (control_url, media.publish_url,
// media.publish_auth, expires_at) — those are asserted via the fixture's
// body_fields_present list.
package sessioncontrolplusmedia

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

const Mode = "session-control-plus-media@v0"

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
