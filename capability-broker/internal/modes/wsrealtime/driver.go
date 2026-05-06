// Package wsrealtime implements the ws-realtime@v0 interaction-mode driver
// per livepeer-network-protocol/modes/ws-realtime.md.
//
// WebSocket upgrade on GET /v1/cap; bidirectional frame relay between the
// gateway-side connection and a backend WebSocket. Livepeer-* headers are
// stripped from the outbound upgrade; backend auth (if any) is injected.
//
// Plan 0015: when the configured extractor is `bytes-counted` (or
// `seconds-elapsed`), the dispatch layer publishes a LiveCounter into
// `Params.LiveCounter` and into the request context. The pumpFrames
// loop here increments the bytes-counted variant on every relayed
// frame; the payment middleware polls the counter on each interim-debit
// tick and issues per-tick DebitBalance calls.
package wsrealtime

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/bytescounted"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "ws-realtime@v0"

// Driver implements modes.Driver.
type Driver struct {
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer
}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a stateless ws-realtime driver.
func New() *Driver {
	return &Driver{
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			CheckOrigin:      func(r *http.Request) bool { return true },
		},
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		},
	}
}

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve upgrades the inbound connection, dials the backend, and relays
// frames bidirectionally until either side closes.
func (d *Driver) Serve(ctx context.Context, p modes.Params) error {
	// Resolve the backend WS URL (http(s):// → ws(s)://).
	backendURL, err := httpToWS(p.Capability.Backend.URL)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"backend url: "+err.Error())
		return nil
	}

	// Build outbound headers: strip Livepeer-*, inject backend auth.
	outHeaders := backend.StripLivepeerHeaders(p.Request.Header)
	if p.Auth != nil {
		if err := p.Auth.Apply(outHeaders, p.Capability.Backend.Auth); err != nil {
			livepeerheader.WriteError(p.Writer, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
				"inject backend auth: "+err.Error())
			return nil
		}
	}
	// gorilla/websocket Dialer.DialContext consumes a few headers (e.g.,
	// Sec-WebSocket-Key) directly; our outHeaders for the upgrade body just
	// needs Authorization + any application headers. Filter out the
	// upgrade-control headers (which gorilla sets itself).
	outHeaders.Del("Upgrade")
	outHeaders.Del("Connection")
	outHeaders.Del("Sec-Websocket-Key")
	outHeaders.Del("Sec-Websocket-Version")
	outHeaders.Del("Sec-Websocket-Protocol")
	outHeaders.Del("Sec-Websocket-Extensions")
	outHeaders.Del("Host")

	// Build response headers for the 101. gorilla/websocket writes the 101
	// itself rather than going through the wrapped ResponseWriter, so any
	// headers we want on the upgrade response (e.g. Livepeer-Request-Id
	// set by the RequestID middleware) MUST be passed explicitly here.
	upgradeRespHeaders := http.Header{}
	for _, name := range []string{livepeerheader.RequestID, livepeerheader.Error} {
		if v := p.Writer.Header().Get(name); v != "" {
			upgradeRespHeaders.Set(name, v)
		}
	}

	// Upgrade inbound (gateway → broker).
	inbound, err := d.upgrader.Upgrade(p.Writer, p.Request, upgradeRespHeaders)
	if err != nil {
		// Upgrade has already written the error response.
		return nil
	}
	defer inbound.Close()

	// Dial outbound (broker → backend).
	out, _, err := d.dialer.DialContext(ctx, backendURL, outHeaders)
	if err != nil {
		_ = inbound.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "backend dial: "+err.Error()),
			time.Now().Add(time.Second))
		return nil
	}
	defer out.Close()

	// Bidirectional relay. Either side's close (or read error) terminates
	// both copies.
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Plan 0015: the dispatch layer hands us a *bytescounted.LiveCounter
	// when the configured extractor is `bytes-counted`. We type-assert
	// once here and pass the writable counter into the pumps; the
	// payment middleware reads CurrentUnits via the LiveCounter
	// interface published into the request context.
	var byteCtr *bytescounted.LiveCounter
	if lc, ok := p.LiveCounter.(*bytescounted.LiveCounter); ok {
		byteCtr = lc
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); pumpFrames(relayCtx, inbound, out, byteCtr, cancel) }()
	go func() { defer wg.Done(); pumpFrames(relayCtx, out, inbound, byteCtr, cancel) }()
	wg.Wait()
	return nil
}

// pumpFrames reads frames from src and writes them to dst until either an
// error occurs or the context is canceled. On exit, cancel is called so the
// peer pump unblocks.
//
// When byteCtr is non-nil, the on-wire payload byte count of every
// successfully-relayed frame is added to the counter (plan 0015). nil
// means the configured extractor doesn't have a running view; the pump
// runs frame-relay-only.
func pumpFrames(ctx context.Context, src, dst *websocket.Conn, byteCtr *bytescounted.LiveCounter, cancel context.CancelFunc) {
	defer cancel()
	for {
		if ctx.Err() != nil {
			return
		}
		mt, data, err := src.ReadMessage()
		if err != nil {
			return
		}
		if err := dst.WriteMessage(mt, data); err != nil {
			return
		}
		if byteCtr != nil {
			byteCtr.AddBytes(uint64(len(data)))
		}
	}
}

// httpToWS converts an http(s):// URL to ws(s)://. Anything that doesn't
// parse cleanly returns an error.
func httpToWS(u string) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
		// already a WS URL
	default:
		return "", &schemeError{scheme: parsed.Scheme}
	}
	return parsed.String(), nil
}

type schemeError struct{ scheme string }

func (e *schemeError) Error() string {
	return "unsupported backend URL scheme for ws-realtime: " + e.scheme + " (want http/https/ws/wss)"
}
