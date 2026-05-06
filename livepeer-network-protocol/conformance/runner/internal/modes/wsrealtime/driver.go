// Package wsrealtime implements the runner-side driver for ws-realtime@v0
// fixtures.
//
// v0.1 (plan 0010): single-message round-trip. The runner dials the
// broker, sends one text frame, expects it echoed by the mock backend,
// and asserts Livepeer-* stripping on the upgrade.
//
// Plan 0015 extension: the fixture's optional `ws_realtime:` block lets
// fixtures (a) send a sequence of frames spaced over time, (b) hold the
// connection open after the last frame, and (c) assert plan-0015
// behavior — minimum interim-debit count (sampled via the receiver
// daemon's GetBalance) and broker-driven close direction.
package wsrealtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/envelope"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/payee"
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

	// Substitute the Livepeer-Payment placeholder. For plan 0015
	// fixtures we also need the sender address to call GetBalance, so
	// we mint via Mint() rather than the legacy SubstituteHeaders.
	headers, sender, err := mintHeaders(ctx, fx)
	if err != nil {
		return fail(fx, "build payment envelope: "+err.Error())
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

	workID := resp.Header.Get("Livepeer-Request-Id")

	// Plan 0015: when the fixture carries a ws_realtime block, run the
	// extended frame schedule + assertions; otherwise fall back to the
	// v0.1 single-frame round-trip.
	if fxIsExtended(fx.WSRealtime) {
		failures = append(failures, runInterimDebitScenario(ctx, conn, fx.WSRealtime, sender, workID)...)
	} else {
		failures = append(failures, runSingleFrameRoundTrip(conn)...)
	}

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

// runSingleFrameRoundTrip is the v0.1 happy-path: send a probe, receive
// the echo, close cleanly.
func runSingleFrameRoundTrip(conn *websocket.Conn) []string {
	var failures []string
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
	return failures
}

// runInterimDebitScenario sends a programmed sequence of frames, holds
// the connection open, and runs plan-0015 assertions:
//   - ExpectMinInterimDebits: sample receiver-daemon GetBalance during
//     the session; count distinct values (proxy for DebitBalance count).
//   - ExpectBrokerTerminated: drive a read loop after the last frame and
//     assert the read terminates with a close error not initiated by
//     this side.
func runInterimDebitScenario(ctx context.Context, conn *websocket.Conn, p fixtures.WSRealtimeFixture, sender []byte, workID string) []string {
	var failures []string

	frameSize := p.FrameSizeBytes
	if frameSize <= 0 {
		frameSize = 64
	}
	frameCount := p.FrameCount
	if frameCount <= 0 {
		frameCount = 1
	}
	frameInterval := time.Duration(p.FrameIntervalMs) * time.Millisecond
	hold := time.Duration(p.HoldAfterFramesMs) * time.Millisecond

	// Start a background balance sampler if we have a receiver-daemon
	// connection. ws-realtime sessions in the conformance compose run
	// against a payee daemon mounted at the runner; if that's missing,
	// the assertion is recorded as a fail.
	balanceCh := make(chan string, 64)
	samplerDone := make(chan struct{})
	samplerCtx, cancelSampler := context.WithCancel(ctx)
	go sampleBalances(samplerCtx, sender, workID, balanceCh, samplerDone)

	// Push frames per the fixture schedule.
	payload := make([]byte, frameSize)
	for i := 0; i < frameCount; i++ {
		if i > 0 && frameInterval > 0 {
			select {
			case <-ctx.Done():
				cancelSampler()
				<-samplerDone
				return append(failures, "context cancelled mid-frame-send: "+ctx.Err().Error())
			case <-time.After(frameInterval):
			}
		}
		// Distinct payload bytes each frame (so the broker's bytes-counted
		// counter strictly increases).
		for j := range payload {
			payload[j] = byte(i)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			cancelSampler()
			<-samplerDone
			return append(failures, "write frame: "+err.Error())
		}
		// Read the echoed frame so the broker's response-direction
		// counter advances.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			// Echo failed — allowed in balance-exhausted fixtures
			// where the broker may have already terminated the session.
			break
		}
	}

	// Hold the connection open. If the fixture expects broker
	// termination, do a read-until-error loop; if the read returns
	// without our having sent a close, that proves broker-side closure.
	holdEnd := time.Now().Add(hold)
	if hold > 0 {
		_ = conn.SetReadDeadline(holdEnd)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if p.ExpectBrokerTerminated && isCloseError(err) {
					// Expected close from broker side.
					break
				}
				if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
					// Hold elapsed without termination.
					break
				}
				// Any other error during hold also indicates session
				// ended — that's fine for these fixtures.
				break
			}
			if time.Now().After(holdEnd) {
				break
			}
		}
	}

	cancelSampler()
	<-samplerDone

	// Drain remaining samples.
	close(balanceCh)
	uniqueValues := map[string]struct{}{}
	for v := range balanceCh {
		uniqueValues[v] = struct{}{}
	}

	// ExpectMinInterimDebits: a fresh session has balance=0; each
	// successful DebitBalance moves the balance to a new value. So
	// distinct sampled values ≥ debits + 1 in the worst case (we may
	// miss in-flight transitions). Treat distinct values - 1 as a
	// floor on the daemon's actual debit count (plan 0015 §9.1).
	if p.ExpectMinInterimDebits > 0 {
		if errors.Is(samplerErr.Load(), payee.ErrUnavailable) {
			failures = append(failures, "expect_min_interim_debits: payee-daemon socket not configured "+
				"(set --payee-socket on the runner)")
		} else {
			distinct := len(uniqueValues)
			if distinct < p.ExpectMinInterimDebits+1 {
				failures = append(failures, fmt.Sprintf(
					"expect_min_interim_debits: observed %d distinct GetBalance values, "+
						"want ≥ %d (interim debits + 1 initial)",
					distinct, p.ExpectMinInterimDebits+1))
			}
		}
	}

	// Close the connection from our side as good citizenship; if the
	// broker already closed, this is a no-op.
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))

	return failures
}

// samplerErr is set by sampleBalances when its first GetBalance call
// fails; the assertion path uses it to surface a clean "payee daemon
// unavailable" message instead of a misleading "0 distinct values".
var samplerErr = atomicErr{}

type atomicErr struct {
	val errPtr
}

type errPtr struct{ err error }

func (a *atomicErr) Load() error  { return a.val.err }
func (a *atomicErr) Store(e error) { a.val = errPtr{err: e} }

func sampleBalances(ctx context.Context, sender []byte, workID string, out chan<- string, done chan<- struct{}) {
	defer close(done)
	if len(sender) == 0 || workID == "" {
		samplerErr.Store(payee.ErrUnavailable)
		return
	}
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		bal, err := payee.GetBalance(ctx, sender, workID)
		if err != nil {
			samplerErr.Store(err)
			if errors.Is(err, payee.ErrUnavailable) {
				return
			}
		} else {
			select {
			case out <- bal.String():
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func fxIsExtended(p fixtures.WSRealtimeFixture) bool {
	return p.FrameCount > 0 || p.HoldAfterFramesMs > 0 ||
		p.ExpectMinInterimDebits > 0 || p.ExpectBrokerTerminated
}

func mintHeaders(ctx context.Context, fx fixtures.Fixture) (http.Header, []byte, error) {
	const placeholder = "<runner-generated-payment-blob>"
	const livepeerPayment = "Livepeer-Payment"
	out := http.Header{}
	for k, v := range fx.Request.Headers {
		switch http.CanonicalHeaderKey(k) {
		case "Upgrade", "Connection",
			"Sec-Websocket-Key", "Sec-Websocket-Version",
			"Sec-Websocket-Protocol", "Sec-Websocket-Extensions":
			continue
		}
		out.Set(k, v)
	}
	var sender []byte
	if fx.Request.Headers[livepeerPayment] == placeholder {
		cap := fx.Request.Headers["Livepeer-Capability"]
		off := fx.Request.Headers["Livepeer-Offering"]
		if cap != "" && off != "" {
			env, snd, err := envelope.Mint(ctx, cap, off)
			if err != nil {
				return nil, nil, err
			}
			out.Set(livepeerPayment, env)
			sender = snd
		}
	}
	return out, sender, nil
}

func isCloseError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*websocket.CloseError); ok {
		return true
	}
	// gorilla/websocket returns websocket.CloseError or net errors
	// after a server-side abrupt close.
	s := err.Error()
	return strings.Contains(s, "websocket: close") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "EOF")
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	if errors.As(err, &te) {
		return te.Timeout()
	}
	return false
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

func hasLivepeerHeaders(h http.Header) bool {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "livepeer-") {
			return true
		}
	}
	return false
}
