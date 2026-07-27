// Package workerconn holds the broker-side registry for outbound connected
// worker hosts. The concrete QUIC/WebSocket session protocol can plug into
// this package by registering a Forwarder per virtual backend.
package workerconn

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
)

const VirtualBackendScheme = "worker"
const BackendIDHeader = "X-Livepeer-Worker-Backend-Id"

type Registry struct {
	mu       sync.RWMutex
	backends map[string]backend.Forwarder
}

func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]backend.Forwarder)}
}

func (r *Registry) Register(backendID string, forwarder backend.Forwarder) error {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return fmt.Errorf("backend_id is required")
	}
	if forwarder == nil {
		return fmt.Errorf("forwarder is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[backendID] = forwarder
	return nil
}

func (r *Registry) Unregister(backendID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.backends, strings.TrimSpace(backendID))
}

func (r *Registry) ConnectedBackendIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.backends))
	for id := range r.backends {
		ids = append(ids, id)
	}
	return ids
}

func (r *Registry) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	backendID := u.Host
	if backendID == "" {
		backendID = strings.TrimPrefix(u.Path, "/")
	}
	if backendID == "" {
		return nil, fmt.Errorf("worker virtual backend id is required")
	}
	r.mu.RLock()
	forwarder := r.backends[backendID]
	r.mu.RUnlock()
	if forwarder == nil {
		return nil, fmt.Errorf("worker virtual backend %q is not connected", backendID)
	}
	next := req
	next.URL = workerRelativeURL(u)
	if next.Headers == nil {
		next.Headers = make(http.Header)
	}
	next.Headers.Set(BackendIDHeader, backendID)
	return forwarder.Forward(ctx, next)
}

func workerRelativeURL(u *url.URL) string {
	out := *u
	out.Scheme = "http"
	out.Host = "worker.local"
	return out.String()
}

type Forwarder struct {
	Fallback backend.Forwarder
	Registry *Registry
}

func NewForwarder(fallback backend.Forwarder, registry *Registry) *Forwarder {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Forwarder{Fallback: fallback, Registry: registry}
}

func (f *Forwarder) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == VirtualBackendScheme {
		return f.Registry.Forward(ctx, req)
	}
	if f.Fallback == nil {
		return nil, fmt.Errorf("fallback forwarder is not configured")
	}
	return f.Fallback.Forward(ctx, req)
}

var _ backend.Forwarder = (*Registry)(nil)
var _ backend.Forwarder = (*Forwarder)(nil)

// HTTPTransport returns an http.RoundTripper that serves requests addressed to
// the "worker://" virtual scheme via this registry's connected worker sessions,
// delegating every other scheme to base (http.DefaultTransport when nil). It
// lets a standard *http.Client — e.g. the health prober — reach connected
// worker backends exactly the way the dispatch path does, instead of failing
// with an "unsupported protocol scheme worker" error.
func (r *Registry) HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if r == nil {
		return base
	}
	return &registryTransport{registry: r, base: base}
}

type registryTransport struct {
	registry *Registry
	base     http.RoundTripper
}

func (t *registryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.EqualFold(req.URL.Scheme, VirtualBackendScheme) {
		return t.base.RoundTrip(req)
	}
	fr := backend.ForwardRequest{
		URL:     req.URL.String(),
		Method:  req.Method,
		Headers: req.Header.Clone(),
		Body:    req.Body,
	}
	// GET probes (and other bodyless requests) carry a nil Body; hand the
	// forwarder an empty reader so it never dereferences a nil io.Reader.
	if fr.Body == nil {
		fr.Body = http.NoBody
	}
	return t.registry.Forward(req.Context(), fr)
}

var _ http.RoundTripper = (*registryTransport)(nil)
