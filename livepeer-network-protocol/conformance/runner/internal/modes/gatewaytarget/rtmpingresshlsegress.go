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

const rtmpMode = "rtmp-ingress-hls-egress@v0"

// RTMPIngressHLSEgress is the gateway-target driver for
// rtmp-ingress-hls-egress@v0. v0.1 covers the session-open POST only;
// the actual RTMP push + HLS playback verification mirrors the
// broker-target end-to-end fixture and lands once the gateway-side
// adapter's RTMP listener is in place.
type RTMPIngressHLSEgress struct {
	httpClient *http.Client
}

func NewRTMPIngressHLSEgress() *RTMPIngressHLSEgress {
	return &RTMPIngressHLSEgress{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (d *RTMPIngressHLSEgress) Mode() string { return rtmpMode }

func (d *RTMPIngressHLSEgress) Run(ctx context.Context, gatewayURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
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
