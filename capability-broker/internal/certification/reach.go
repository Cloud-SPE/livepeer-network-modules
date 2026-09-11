package certification

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/gorilla/websocket"
)

// reachDescriptor dials the public address a descriptor names, the way
// a caller would, and reports whether anyone answered.
//
// Config (certification-steps §3.2, `reach`):
//
//	field      the key in runtime.public holding the address (default "url")
//	grant      the grant operation whose secret to present (optional)
//	timeout_ms dial budget (default 5000)
//
// What "answered" means follows the scheme, not the schema: wss/ws is a
// completed WebSocket upgrade; https/http is a 2xx GET. The broker reads
// exactly one field the step named and one grant the step named, and
// interprets nothing else in the descriptor — runtime-descriptor §4's
// rule holds because the pool's author, not the broker, says which
// field is the door.
func reachDescriptor(ctx context.Context, desc *sessionengine.Descriptor, cfg map[string]any) (map[string]any, string) {
	field := stringOr(cfg, "field", "url")
	timeout := time.Duration(intOr(cfg, "timeout_ms", 5000)) * time.Millisecond
	var pub map[string]any
	if err := json.Unmarshal(desc.Public, &pub); err != nil {
		return nil, "public is not an object"
	}
	raw, _ := pub[field].(string)
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Sprintf("public.%s is absent or not a string", field)
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Sprintf("public.%s: %v", field, err)
	}
	header := http.Header{}
	if op := stringOr(cfg, "grant", ""); op != "" {
		secret := ""
		for _, g := range desc.Grants {
			for _, o := range g.Operations {
				if o == op {
					secret = g.Secret
				}
			}
		}
		if secret == "" {
			return nil, fmt.Sprintf("descriptor carries no grant for %q", op)
		}
		header.Set("Authorization", "Bearer "+secret)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	evidence := map[string]any{"reached_host": target.Host, "reached_scheme": target.Scheme}
	switch target.Scheme {
	case "wss", "ws":
		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, raw, header)
		if err != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			evidence["status"] = status
			return evidence, fmt.Sprintf("websocket dial %s: %v", target.Host, err)
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.Close()
	case "https", "http":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return evidence, err.Error()
		}
		req.Header = header
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return evidence, fmt.Sprintf("get %s: %v", target.Host, err)
		}
		_ = resp.Body.Close()
		evidence["status"] = resp.StatusCode
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return evidence, fmt.Sprintf("get %s: %d", target.Host, resp.StatusCode)
		}
	case "rtmps", "rtmp":
		// An ingest answers the RTMP handshake: C0 (version 3) and C1
		// (1536 bytes) in, S0 and S1 back. That proves the member's
		// RTMPS port, the agent's forward, and the runner's router all
		// answer, without publishing a stream — publishing needs an
		// encoder and is not a certification step.
		if err := rtmpHandshake(ctx, raw, target); err != nil {
			return evidence, fmt.Sprintf("rtmp handshake %s: %v", target.Host, err)
		}
	default:
		return evidence, fmt.Sprintf("public.%s has scheme %q; reach knows ws(s), http(s) and rtmp(s)", field, target.Scheme)
	}
	evidence["reach_ms"] = time.Since(start).Milliseconds()
	return evidence, ""
}

func stringOr(cfg map[string]any, key, def string) string {
	if v, ok := cfg[key].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// rtmpHandshake performs the client half of the RTMP handshake and
// expects the server half back.
func rtmpHandshake(ctx context.Context, raw string, target *url.URL) error {
	host := target.Host
	if target.Port() == "" {
		if target.Scheme == "rtmps" {
			host = net.JoinHostPort(target.Hostname(), "443")
		} else {
			host = net.JoinHostPort(target.Hostname(), "1935")
		}
	}
	d := &net.Dialer{}
	var conn net.Conn
	var err error
	if target.Scheme == "rtmps" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: target.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", host)
	} else {
		conn, err = d.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c1 := make([]byte, 1536)
	_, _ = rand.Read(c1[8:])
	copy(c1[4:8], []byte{0, 0, 0, 0})
	if _, err := conn.Write(append([]byte{3}, c1...)); err != nil {
		return fmt.Errorf("send C0+C1: %w", err)
	}
	s0s1 := make([]byte, 1+1536)
	if _, err := io.ReadFull(conn, s0s1); err != nil {
		return fmt.Errorf("read S0+S1: %w", err)
	}
	if s0s1[0] != 3 {
		return fmt.Errorf("S0 version %d, want 3", s0s1[0])
	}
	return nil
}
