// Package extractors defines the work-unit extractor interface and registry.
//
// Extractors are declarative recipes per
// livepeer-network-protocol/extractors/. Each capability in host-config.yaml
// declares one extractor; the broker builds an instance via the registry and
// invokes it after the backend produces a response.
package extractors

import (
	"context"
	"net/http"
	"time"
)

// Extractor computes work units from a request/response pair.
type Extractor interface {
	// Name returns the extractor type identifier (e.g. "response-jsonpath",
	// "openai-usage"). Used for diagnostics and metrics labels.
	Name() string

	// Extract reads work units from the request and/or response. It MUST NOT
	// modify the inputs; bodies are pre-buffered byte slices owned by the
	// caller.
	Extract(ctx context.Context, req *Request, resp *Response) (uint64, error)
}

// LiveCounter is implemented by extractors that can be polled mid-flight
// for the running unit total. Mode drivers register a LiveCounter
// alongside their handler when they support interim debit (see plan 0015);
// the payment middleware polls it every tick to compute per-tick deltas.
//
// CurrentUnits MUST be safe to call from a goroutine that is not the
// goroutine producing the count. The trivial implementations in
// `extractors/bytescounted` and `extractors/secondselapsed` satisfy this
// via atomic.Uint64.Load and time.Since respectively.
//
// LiveCounter is not a replacement for Extractor: the final-flush path
// still calls Extract after the handler returns to produce the canonical
// end-of-session count. LiveCounter is the interim view; Extract is the
// authoritative end view. The two MUST agree at end-of-session.
type LiveCounter interface {
	// CurrentUnits returns the running work-unit total. Monotonic;
	// safe for concurrent reads from any goroutine.
	CurrentUnits() uint64
}

// Request carries the parts of the inbound request available to extractors.
type Request struct {
	Method  string
	Body    []byte
	Headers http.Header
}

// Response carries the parts of the backend response available to extractors.
type Response struct {
	Status   int
	Body     []byte
	Headers  http.Header
	// Duration is wall-clock time the broker spent on this request,
	// from start of mode dispatch to extractor invocation. Mode drivers
	// populate this for any mode where a meaningful per-request elapsed
	// time exists. Used by `seconds-elapsed`.
	Duration time.Duration
}

// Factory builds an Extractor from an extractor config map (the extractor
// branch of host-config.yaml's `work_unit.extractor`). The factory is
// expected to validate type-specific fields and return a clear error on
// malformed configuration.
type Factory func(config map[string]any) (Extractor, error)
