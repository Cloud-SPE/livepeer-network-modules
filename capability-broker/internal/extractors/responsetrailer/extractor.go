// Package responsetrailer implements the response-trailer extractor.
//
// Reads a numeric work-unit value from a named HTTP trailer on the
// backend response. Trailers (declared via the upstream's `Trailer:`
// response header, set after the body is sent) are the protocol's
// canonical mechanism for streaming-mode work-unit reporting: the
// runner doesn't know the final count until the stream completes.
//
// Configuration:
//
//	work_unit:
//	  name: tokens
//	  extractor:
//	    type: response-trailer
//	    trailer: X-Livepeer-Work-Units
//	    default: 0
//
// Behaviour mirrors response-header: missing or non-numeric trailer
// values fall back to the configured default and emit a log warning.
package responsetrailer

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
)

const Name = "response-trailer"

type Extractor struct {
	trailer      string
	defaultValue uint64
}

var _ extractors.Extractor = (*Extractor)(nil)

func New(cfg map[string]any) (extractors.Extractor, error) {
	trailer, ok := cfg["trailer"].(string)
	if !ok || trailer == "" {
		return nil, fmt.Errorf("response-trailer: trailer is required and must be a non-empty string")
	}

	defaultValue := uint64(0)
	if d, ok := cfg["default"]; ok {
		switch v := d.(type) {
		case int:
			if v < 0 {
				return nil, fmt.Errorf("response-trailer: default must be non-negative")
			}
			defaultValue = uint64(v)
		case float64:
			if v < 0 {
				return nil, fmt.Errorf("response-trailer: default must be non-negative")
			}
			defaultValue = uint64(v)
		default:
			return nil, fmt.Errorf("response-trailer: default must be a number")
		}
	}

	return &Extractor{trailer: trailer, defaultValue: defaultValue}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(_ context.Context, _ *extractors.Request, resp *extractors.Response) (uint64, error) {
	if resp.Trailers == nil {
		log.Printf("response-trailer: no response trailers; using default %d", e.defaultValue)
		return e.defaultValue, nil
	}
	raw := resp.Trailers.Get(e.trailer)
	if raw == "" {
		log.Printf("response-trailer: trailer %q absent; using default %d", e.trailer, e.defaultValue)
		return e.defaultValue, nil
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		log.Printf("response-trailer: trailer %q is not an unsigned integer (%v); using default %d", e.trailer, err, e.defaultValue)
		return e.defaultValue, nil
	}
	return n, nil
}
