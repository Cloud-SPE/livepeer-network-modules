package responseheader

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

const Name = "response-header"

type Extractor struct {
	header       string
	defaultValue uint64
}

var _ extractors.Extractor = (*Extractor)(nil)

func New(cfg map[string]any) (extractors.Extractor, error) {
	header, ok := cfg["header"].(string)
	if !ok || header == "" {
		return nil, fmt.Errorf("response-header: header is required and must be a non-empty string")
	}

	defaultValue := uint64(0)
	if d, ok := cfg["default"]; ok {
		switch v := d.(type) {
		case int:
			if v < 0 {
				return nil, fmt.Errorf("response-header: default must be non-negative")
			}
			defaultValue = uint64(v)
		case float64:
			if v < 0 {
				return nil, fmt.Errorf("response-header: default must be non-negative")
			}
			defaultValue = uint64(v)
		default:
			return nil, fmt.Errorf("response-header: default must be a number")
		}
	}

	return &Extractor{header: header, defaultValue: defaultValue}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(_ context.Context, _ *extractors.Request, resp *extractors.Response) (uint64, error) {
	if resp.Headers == nil {
		log.Printf("response-header: no response headers; using default %d", e.defaultValue)
		return e.defaultValue, nil
	}
	raw := resp.Headers.Get(e.header)
	if raw == "" {
		log.Printf("response-header: header %q absent; using default %d", e.header, e.defaultValue)
		return e.defaultValue, nil
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		log.Printf("response-header: header %q is not an unsigned integer (%v); using default %d", e.header, err, e.defaultValue)
		return e.defaultValue, nil
	}
	return n, nil
}
