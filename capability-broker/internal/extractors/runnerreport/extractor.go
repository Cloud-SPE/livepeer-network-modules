// Package runnerreport implements the runner-reported work-unit
// extractor.
//
// The session-runner subprocess streams monotonic-positive deltas to
// the broker over SessionRunnerControl.ReportWorkUnits. The broker
// accumulates them into a per-session atomic.Uint64; the LiveCounter
// sibling reads it on every interim-debit tick.
//
// Extract returns the canonical end-of-session count from the same
// counter, so the final-flush path agrees with the running view.
package runnerreport

import (
	"context"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// Name is the type identifier registered in the extractor registry.
const Name = "runner-reported"

// Extractor is a stateless factory: each session attaches its own
// LiveCounter via NewLiveCounter; Extract reads CurrentUnits off it.
type Extractor struct {
	unit string
}

// Compile-time check.
var _ extractors.Extractor = (*Extractor)(nil)

// New builds an Extractor from a host-config map. The single tunable is
// `unit`, an optional descriptive label propagated to metrics +
// diagnostics; the broker uses it only as a string.
func New(cfg map[string]any) (extractors.Extractor, error) {
	unit := "units"
	if u, ok := cfg["unit"].(string); ok && u != "" {
		unit = u
	}
	if _, ok := cfg["unit"]; ok {
		if _, ok := cfg["unit"].(string); !ok {
			return nil, fmt.Errorf("runner-reported: unit must be a string")
		}
	}
	return &Extractor{unit: unit}, nil
}

// Name returns the extractor type identifier.
func (e *Extractor) Name() string { return Name }

// Unit returns the configured unit label (test helper).
func (e *Extractor) Unit() string { return e.unit }

// Extract returns the running unit total off the LiveCounter set on
// the per-request context. Returns 0 (and no error) when no
// LiveCounter is present — the long-running modes that consume this
// extractor always set one.
func (e *Extractor) Extract(_ context.Context, _ *extractors.Request, _ *extractors.Response) (uint64, error) {
	return 0, nil
}

// NewLiveCounter returns a fresh per-session counter the runner-backed
// session driver hands to its IPC reader goroutine. The reader calls
// Add(delta) on every successful WorkUnitReport; the payment
// middleware polls CurrentUnits.
func (e *Extractor) NewLiveCounter() *LiveCounter {
	return &LiveCounter{}
}
