// Package secondselapsed implements the seconds-elapsed extractor per
// livepeer-network-protocol/extractors/seconds-elapsed.md.
//
// Reads `extractors.Response.Duration` (populated by the mode driver),
// applies the configured `granularity` (seconds-per-unit) and `rounding`
// (ceil | floor | round), returns the resulting non-negative integer.
//
// Plan 0015 adds a LiveCounter sibling: long-running mode drivers can
// hand the payment middleware a session-start timestamp closure that
// returns the running unit total. Extract still produces the canonical
// end-of-session count from the buffered Response.Duration.
package secondselapsed

import (
	"context"
	"fmt"
	"math"
	"time"

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
	return e.unitsFor(resp.Duration), nil
}

// unitsFor applies granularity + rounding to a duration. Shared by Extract
// (canonical end-of-session count) and LiveCounter.CurrentUnits (running
// poll). Single implementation keeps the two paths in lockstep.
func (e *Extractor) unitsFor(d time.Duration) uint64 {
	elapsed := d.Seconds()
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
		return 0
	}
	return uint64(rounded)
}

// LiveCounter is the per-session running counter built by NewLiveCounter.
// It computes elapsed time relative to a fixed start instant; no
// extractor- or session-level mutable state is needed.
type LiveCounter struct {
	start       time.Time
	granularity float64
	rounding    string
	now         func() time.Time // injectable for tests
}

var _ extractors.LiveCounter = (*LiveCounter)(nil)

// NewLiveCounter returns a LiveCounter anchored at the supplied start
// instant. Mode drivers pass the moment the session opened; subsequent
// CurrentUnits calls return time.Since(start) converted to units via the
// extractor's granularity + rounding.
func (e *Extractor) NewLiveCounter(start time.Time) *LiveCounter {
	return &LiveCounter{
		start:       start,
		granularity: e.granularity,
		rounding:    e.rounding,
		now:         time.Now,
	}
}

// CurrentUnits returns the running elapsed-time unit total.
//
// Implements extractors.LiveCounter. Goroutine-safe: time.Now is
// concurrency-safe and start is read-only after construction.
func (lc *LiveCounter) CurrentUnits() uint64 {
	if lc == nil {
		return 0
	}
	now := lc.now
	if now == nil {
		now = time.Now
	}
	d := now().Sub(lc.start)
	if d < 0 {
		d = 0
	}
	elapsed := d.Seconds()
	units := elapsed / lc.granularity
	var rounded float64
	switch lc.rounding {
	case "ceil":
		rounded = math.Ceil(units)
	case "floor":
		rounded = math.Floor(units)
	case "round":
		rounded = math.Round(units)
	}
	if rounded < 0 {
		return 0
	}
	return uint64(rounded)
}
