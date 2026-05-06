// Package secondselapsed implements the seconds-elapsed extractor per
// livepeer-network-protocol/extractors/seconds-elapsed.md.
//
// Reads `extractors.Response.Duration` (populated by the mode driver),
// applies the configured `granularity` (seconds-per-unit) and `rounding`
// (ceil | floor | round), returns the resulting non-negative integer.
package secondselapsed

import (
	"context"
	"fmt"
	"math"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

const Name = "seconds-elapsed"

type Extractor struct {
	granularity float64
	rounding    string // "ceil" | "floor" | "round"
}

var _ extractors.Extractor = (*Extractor)(nil)

func New(cfg map[string]any) (extractors.Extractor, error) {
	granularity := 1.0
	if g, ok := cfg["granularity"]; ok {
		switch v := g.(type) {
		case int:
			if v <= 0 {
				return nil, fmt.Errorf("seconds-elapsed: granularity must be > 0")
			}
			granularity = float64(v)
		case float64:
			if v <= 0 {
				return nil, fmt.Errorf("seconds-elapsed: granularity must be > 0")
			}
			granularity = v
		default:
			return nil, fmt.Errorf("seconds-elapsed: granularity must be a positive number")
		}
	}
	rounding := "ceil"
	if r, ok := cfg["rounding"].(string); ok && r != "" {
		switch r {
		case "ceil", "floor", "round":
			rounding = r
		default:
			return nil, fmt.Errorf("seconds-elapsed: rounding must be 'ceil' | 'floor' | 'round' (got %q)", r)
		}
	}
	return &Extractor{granularity: granularity, rounding: rounding}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	elapsed := resp.Duration.Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	units := elapsed / e.granularity
	var rounded float64
	switch e.rounding {
	case "ceil":
		rounded = math.Ceil(units)
	case "floor":
		rounded = math.Floor(units)
	case "round":
		rounded = math.Round(units)
	}
	if rounded < 0 {
		return 0, nil
	}
	return uint64(rounded), nil
}
