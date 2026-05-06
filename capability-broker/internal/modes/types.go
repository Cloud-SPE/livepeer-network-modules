// Package modes defines the interaction-mode driver interface and registry.
//
// One driver per accepted mode (`http-reqresp@v0`, `http-stream@v0`, etc.).
// The dispatcher in internal/server looks up the driver by the
// Livepeer-Mode header value and calls Serve.
package modes

import (
	"context"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// Driver implements one interaction mode's wire shape. Drivers are stateless
// across requests; per-request state is in Params.
type Driver interface {
	// Mode returns the canonical mode-name@vN string this driver implements.
	// Must match the Livepeer-Mode header value exactly.
	Mode() string

	// Serve dispatches one paid request to the appropriate backend. The
	// middleware chain has already validated payment + headers; Serve owns
	// the forwarding and the work-units accounting.
	//
	// Implementations MUST set the Livepeer-Work-Units response header on
	// success so the Payment middleware can reconcile.
	Serve(ctx context.Context, p Params) error
}

// Params bundles everything a driver needs for one request.
type Params struct {
	Writer     http.ResponseWriter
	Request    *http.Request
	Capability *config.Capability
	Extractor  extractors.Extractor
	// LiveCounter is the running work-unit counter the payment
	// middleware polls during long-running sessions (plan 0015). Mode
	// drivers populate this when (and only when) they support interim
	// debit cadence — typically when the configured extractor exposes
	// a LiveCounter sibling (`bytes-counted` or `seconds-elapsed`).
	//
	// nil means the driver does not support interim debit; the
	// middleware falls through to the v0.2 single-debit path. The
	// HTTP-family modes that buffer-and-extract leave this nil.
	LiveCounter extractors.LiveCounter
	Backend     backend.Forwarder
	Auth        *backend.AuthApplier
}
