// Package gatewaytarget hosts the gateway-target driver implementations.
//
// Where the broker-target drivers in sibling packages send paid
// requests to the target as if they were a gateway, gateway-target
// drivers send unpaid customer requests to the target gateway and
// expect the gateway's adapter middleware to forward to a mockbackend
// configured as the upstream broker.
//
// v0.1: ws-realtime + rtmp-ingress + session-control gateway-target
// drivers ship as bridge-shape happy-path checks. The runner asserts
// the customer-leg behaviour the gateway-adapter exposes (upgrade,
// echo, clean close); the upstream-broker side is observed via the
// in-process mockbackend's recorded calls (Livepeer-* headers
// stripped, etc.).
package gatewaytarget

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

const wsRealtimeMode = "ws-realtime@v0"

// WSRealtime is the gateway-target driver for ws-realtime@v0.
type WSRealtime struct {
	dialer *websocket.Dialer
}

// NewWSRealtime returns a gateway-target ws-realtime driver.
func NewWSRealtime() *WSRealtime {
	return &WSRealtime{
		dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second},
	}
}

func (d *WSRealtime) Mode() string { return wsRealtimeMode }

// Run sends a customer-shaped WebSocket upgrade to the gateway under
// test, sends one probe frame, and asserts the echoed response.
//
// The gateway under test owns the payment-mint path and the broker
// wiring; the runner's mockbackend (already running on cfg.MockAddr)
// is what the gateway's adapter middleware should be pointed at as the
// upstream broker. Operators wire that out-of-band when starting the
// gateway under test.
func (d *WSRealtime) Run(ctx context.Context, gatewayURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}
	mock.Reset()

	wsURL, err := buildWSURL(gatewayURL, fx.Request.Path)
	if err != nil {
		return fail(fx, "build ws url: "+err.Error())
	}

	conn, resp, err := d.dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		extra := ""
		if resp != nil {
			extra = fmt.Sprintf(" (HTTP %d)", resp.StatusCode)
		}
		return fail(fx, "dial gateway websocket: "+err.Error()+extra)
	}
	defer conn.Close()

	const probe = "livepeer-conformance-gateway-ws-probe"
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(probe)); err != nil {
		failures = append(failures, "write probe frame: "+err.Error())
	} else {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			failures = append(failures, "read echoed frame: "+err.Error())
		} else if mt != websocket.TextMessage {
			failures = append(failures, fmt.Sprintf("echoed frame type: expected text, got %d", mt))
		} else if string(data) != probe {
			failures = append(failures, fmt.Sprintf("echoed frame body: expected %q, got %q", probe, string(data)))
		}
	}

	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))

	return report.Result{
		Name:     fx.Name,
		Mode:     fx.Mode,
		Pass:     len(failures) == 0,
		Failures: failures,
	}
}

func buildWSURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	if path != "" {
		u.Path = path
	}
	return u.String(), nil
}

func fail(fx fixtures.Fixture, msg string) report.Result {
	return report.Result{Name: fx.Name, Mode: fx.Mode, Pass: false, Failures: []string{msg}}
}
