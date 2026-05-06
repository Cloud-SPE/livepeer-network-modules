// Package backend defines how the broker forwards a request to an upstream
// backend. v0.1 implements transport=http only; transports for other modes
// (RTMP, WebSocket) land alongside their respective mode drivers.
package backend

import (
	"context"
	"io"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
)

// Forwarder is the interface the broker uses to issue outbound calls. The
// caller is responsible for body buffering, header stripping (via
// StripLivepeerHeaders), and auth injection (via InjectAuth) before invoking.
type Forwarder interface {
	Forward(ctx context.Context, req ForwardRequest) (*http.Response, error)
}

// ForwardRequest carries everything needed for one outbound call.
type ForwardRequest struct {
	URL     string         // absolute URL of the backend
	Method  string         // typically copied from inbound (POST for http-reqresp)
	Headers http.Header    // SHOULD be Livepeer-stripped + backend-auth injected
	Body    io.Reader      // request body
}

// SecretResolver resolves backend-auth secret references (e.g., "vault://..."
// or "env://...") to plain-text values. The broker calls this once per
// request when auth.method is "bearer".
type SecretResolver interface {
	Resolve(ref string) (string, error)
}

// AuthApplier applies a backend's auth method to outbound headers.
type AuthApplier struct {
	Secrets SecretResolver
}

// NewAuthApplier returns an applier that uses the given secret resolver.
func NewAuthApplier(s SecretResolver) *AuthApplier {
	return &AuthApplier{Secrets: s}
}

// Apply injects the configured auth header(s) into h. It mutates h in place.
// Methods supported in v0.1: "" / "none" (no-op) and "bearer" (sets
// Authorization: Bearer <resolved-secret>).
func (a *AuthApplier) Apply(h http.Header, auth config.AuthConfig) error {
	switch auth.Method {
	case "", "none":
		return nil
	case "bearer":
		if a == nil || a.Secrets == nil {
			return errNoResolver
		}
		secret, err := a.Secrets.Resolve(auth.SecretRef)
		if err != nil {
			return err
		}
		h.Set("Authorization", "Bearer "+secret)
		return nil
	default:
		return errUnsupportedAuth(auth.Method)
	}
}

type errUnsupportedAuth string

func (e errUnsupportedAuth) Error() string {
	return "backend.auth.method not supported: " + string(e)
}

// errNoResolver is returned when an auth method requires a secret resolver
// but none was provided.
var errNoResolver = staticErr("backend.auth.method=bearer requires a configured secret resolver")

type staticErr string

func (e staticErr) Error() string { return string(e) }
