// Package responsejsonpath implements the response-jsonpath extractor per
// livepeer-network-protocol/extractors/response-jsonpath.md.
//
// Extracts a non-negative integer from a JSONPath in the response body.
// Multi-value matches are summed; missing/non-numeric matches fall back to
// the configured default (zero unless overridden). The path grammar is the
// spec's required minimum subset.
package responsejsonpath

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// Name is the extractor type identifier registered in the extractors registry.
const Name = "response-jsonpath"

// Extractor implements extractors.Extractor.
type Extractor struct {
	path         string
	defaultValue uint64
}

// Compile-time interface check.
var _ extractors.Extractor = (*Extractor)(nil)

// New is the factory function registered with the extractors registry. The
// config map is the value of `work_unit.extractor` from host-config.yaml.
func New(cfg map[string]any) (extractors.Extractor, error) {
	path, ok := cfg["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("response-jsonpath: path is required and must be a non-empty string")
	}
	defaultValue := uint64(0)
	if d, present := cfg["default"]; present {
		switch v := d.(type) {
		case int:
			if v < 0 {
				return nil, fmt.Errorf("response-jsonpath: default must be non-negative")
			}
			defaultValue = uint64(v)
		case float64:
			if v < 0 {
				return nil, fmt.Errorf("response-jsonpath: default must be non-negative")
			}
			defaultValue = uint64(v)
		default:
			return nil, fmt.Errorf("response-jsonpath: default must be a number (got %T)", d)
		}
	}
	return &Extractor{path: path, defaultValue: defaultValue}, nil
}

// Name returns the registered extractor type identifier.
func (e *Extractor) Name() string { return Name }

// Extract evaluates the configured JSONPath against the response body.
// Per the spec:
//   - Path doesn't match → return defaultValue, log warning.
//   - Result is non-numeric → return defaultValue, log warning.
//   - Result is negative → return defaultValue, log warning.
//   - Multi-value match → sum if all non-negative integers; else default.
func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	if len(resp.Body) == 0 {
		log.Printf("response-jsonpath: empty response body; using default %d", e.defaultValue)
		return e.defaultValue, nil
	}
	var data any
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		log.Printf("response-jsonpath: response body is not JSON (%v); using default %d", err, e.defaultValue)
		return e.defaultValue, nil
	}
	results, err := eval(e.path, data)
	if err != nil {
		log.Printf("response-jsonpath: path %q error: %v; using default %d", e.path, err, e.defaultValue)
		return e.defaultValue, nil
	}
	if len(results) == 0 {
		log.Printf("response-jsonpath: path %q matched nothing; using default %d", e.path, e.defaultValue)
		return e.defaultValue, nil
	}
	var sum uint64
	for _, r := range results {
		n, err := toNonNegativeInt(r)
		if err != nil {
			log.Printf("response-jsonpath: path %q matched non-numeric value (%v); using default %d", e.path, err, e.defaultValue)
			return e.defaultValue, nil
		}
		sum += n
	}
	return sum, nil
}

func toNonNegativeInt(v any) (uint64, error) {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(n), nil
	case int:
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(n), nil
	case uint64:
		return n, nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
