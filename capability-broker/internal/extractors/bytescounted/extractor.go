// Package bytescounted implements the bytes-counted extractor per
// livepeer-network-protocol/extractors/bytes-counted.md.
//
// Tally on-wire bytes flowing through the broker (request, response, or
// both), divided by a configurable granularity to convert to work-units.
// HTTP headers excluded by default; opt in via `headers: true`.
package bytescounted

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

const Name = "bytes-counted"

type Extractor struct {
	direction   string // "request" | "response" | "both"
	granularity uint64
	headers     bool
}

var _ extractors.Extractor = (*Extractor)(nil)

func New(cfg map[string]any) (extractors.Extractor, error) {
	direction := "response"
	if d, ok := cfg["direction"].(string); ok && d != "" {
		switch d {
		case "request", "response", "both":
			direction = d
		default:
			return nil, fmt.Errorf("bytes-counted: direction must be 'request' | 'response' | 'both' (got %q)", d)
		}
	}
	granularity := uint64(1)
	if g, ok := cfg["granularity"]; ok {
		switch v := g.(type) {
		case int:
			if v <= 0 {
				return nil, fmt.Errorf("bytes-counted: granularity must be > 0")
			}
			granularity = uint64(v)
		case float64:
			if v <= 0 {
				return nil, fmt.Errorf("bytes-counted: granularity must be > 0")
			}
			granularity = uint64(v)
		default:
			return nil, fmt.Errorf("bytes-counted: granularity must be a positive number")
		}
	}
	headers := false
	if h, ok := cfg["headers"].(bool); ok {
		headers = h
	}
	return &Extractor{direction: direction, granularity: granularity, headers: headers}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	var total uint64
	if e.direction == "request" || e.direction == "both" {
		total += uint64(len(req.Body))
		if e.headers {
			total += headerBytes(req.Headers)
		}
	}
	if e.direction == "response" || e.direction == "both" {
		total += uint64(len(resp.Body))
		if e.headers {
			total += headerBytes(resp.Headers)
		}
	}
	return total / e.granularity, nil
}

// headerBytes approximates the on-wire byte cost of an http.Header set.
// Each line is "<name>: <value>\r\n" — len(name) + 2 (": ") + len(value) + 2 (\r\n).
func headerBytes(h http.Header) uint64 {
	var n uint64
	for k, vs := range h {
		for _, v := range vs {
			n += uint64(len(k)) + 4 + uint64(len(v))
		}
	}
	return n
}
