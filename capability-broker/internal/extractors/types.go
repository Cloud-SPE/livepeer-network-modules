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

// Request carries the parts of the inbound request available to extractors.
type Request struct {
	Method  string
	Body    []byte
	Headers http.Header
}

// Response carries the parts of the backend response available to extractors.
type Response struct {
	Status  int
	Body    []byte
	Headers http.Header
}

// Factory builds an Extractor from an extractor config map (the extractor
// branch of host-config.yaml's `work_unit.extractor`). The factory is
// expected to validate type-specific fields and return a clear error on
// malformed configuration.
type Factory func(config map[string]any) (Extractor, error)
