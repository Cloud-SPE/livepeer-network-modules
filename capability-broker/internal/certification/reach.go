package certification

import (
	"context"
	"encoding/json"
	"fmt"
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
	default:
		return evidence, fmt.Sprintf("public.%s has scheme %q; reach knows ws(s) and http(s)", field, target.Scheme)
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
