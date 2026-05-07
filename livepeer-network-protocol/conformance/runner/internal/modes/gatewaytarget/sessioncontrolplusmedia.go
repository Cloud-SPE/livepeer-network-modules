package gatewaytarget

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

const sessionControlMode = "session-control-plus-media@v0"

// SessionControlPlusMedia is the gateway-target driver for
// session-control-plus-media@v0. v0.1 covers the session-open POST
// (session-id + control_url + media descriptor); control-WS lifecycle
// + WebRTC media-plane verification ride along when the runner has
// fixtures asserting them.
type SessionControlPlusMedia struct {
	httpClient *http.Client
}

func NewSessionControlPlusMedia() *SessionControlPlusMedia {
	return &SessionControlPlusMedia{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (d *SessionControlPlusMedia) Mode() string { return sessionControlMode }

func (d *SessionControlPlusMedia) Run(ctx context.Context, gatewayURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}
	mock.Reset()

	body := strings.NewReader(fx.Request.Body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(gatewayURL, "/")+fx.Request.Path, body)
	if err != nil {
		return fail(fx, "build request: "+err.Error())
	}
	for k, v := range fx.Request.Headers {
		req.Header.Set(k, v)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fail(fx, "call gateway: "+err.Error())
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if fx.ResponseExpect.Status != 0 && resp.StatusCode != fx.ResponseExpect.Status {
		failures = append(failures, fmt.Sprintf("response.status: expected %d, got %d",
			fx.ResponseExpect.Status, resp.StatusCode))
	}

	if len(fx.ResponseExpect.BodyFieldsPresent) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			failures = append(failures, "response body not JSON: "+err.Error())
		} else {
			for _, field := range fx.ResponseExpect.BodyFieldsPresent {
				if _, ok := parsed[field]; !ok {
					failures = append(failures, "response body missing field: "+field)
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
