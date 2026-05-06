// Package wsrealtime implements the runner-side driver for ws-realtime@v0
// fixtures.
//
// The runner dials the broker at brokerURL with a WebSocket upgrade,
// sends a single text frame, expects the same frame back (echoed by the
// runner's mock backend through the broker's relay), closes cleanly, and
// asserts that the mock backend received the upgrade with no Livepeer-*
// headers.
//
// v0.1 narrowed scope per plan 0010: a single-message round-trip is
// sufficient to prove the wire shape works.
package wsrealtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

const Mode = "ws-realtime@v0"

const probeMessage = "livepeer-conformance-ws-probe"

type Driver struct {
	dialer *websocket.Dialer
}

func New() *Driver {
	return &Driver{dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second}}
}

func (d *Driver) Mode() string { return Mode }

func (d *Driver) Run(ctx context.Context, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}

	// The mock backend's /ws handler is set up at construction; for
	// ws-realtime, the fixture's BackendResponse fields don't apply.
	mock.Reset()

	// Convert http(s):// brokerURL to ws(s)://; append /v1/cap.
	wsURL, err := buildWSURL(brokerURL, fx.Request.Path)
	if err != nil {
		return fail(fx, "build ws url: "+err.Error())
	}

	// Build upgrade request headers from the fixture.
	headers := http.Header{}
	for k, v := range fx.Request.Headers {
		// gorilla/websocket sets Upgrade/Connection/Sec-WebSocket-* itself;
		// passing them ourselves causes a duplicate-header rejection.
		switch http.CanonicalHeaderKey(k) {
		case "Upgrade", "Connection",
			"Sec-Websocket-Key", "Sec-Websocket-Version",
			"Sec-Websocket-Protocol", "Sec-Websocket-Extensions":
			continue
		}
		headers.Set(k, substitutePlaceholders(v))
	}

	conn, resp, err := d.dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		extra := ""
		if resp != nil {
			extra = fmt.Sprintf(" (HTTP %d)", resp.StatusCode)
		}
		return fail(fx, "dial broker websocket: "+err.Error()+extra)
	}
	defer conn.Close()

	// Status check: gorilla returns 101 in resp.StatusCode on successful upgrade.
	if fx.ResponseExpect.Status != 0 && resp.StatusCode != fx.ResponseExpect.Status {
		failures = append(failures, fmt.Sprintf("response.status: expected %d, got %d",
			fx.ResponseExpect.Status, resp.StatusCode))
	}

	// HeadersPresent on the upgrade response (e.g., Livepeer-Request-Id).
	for _, h := range fx.ResponseExpect.HeadersPresent {
		if resp.Header.Get(h) == "" {
			failures = append(failures, "upgrade-response header missing: "+h)
		}
	}

	// Send a probe frame and expect it echoed back through the broker's
	// relay from the mock backend.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(probeMessage)); err != nil {
		failures = append(failures, "write probe frame: "+err.Error())
	} else {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			failures = append(failures, "read echoed frame: "+err.Error())
		} else if mt != websocket.TextMessage {
			failures = append(failures, fmt.Sprintf("echoed frame type: expected text, got %d", mt))
		} else if string(data) != probeMessage {
			failures = append(failures, fmt.Sprintf("echoed frame body: expected %q, got %q",
				probeMessage, string(data)))
		}
	}

	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))

	// Backend assertions: the mock backend's /ws handler recorded the
	// upgrade headers; verify Livepeer-* stripped + auth absent.
	last := mock.LastCall()
	if last == nil {
		failures = append(failures, "backend_assertions: no calls recorded by mock backend")
	} else {
		ba := fx.BackendAssertions
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

func buildWSURL(brokerURL, path string) (string, error) {
	u, err := url.Parse(brokerURL)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = path
	return u.String(), nil
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
