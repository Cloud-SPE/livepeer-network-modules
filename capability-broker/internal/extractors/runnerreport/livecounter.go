package runnerreport

import (
	"sync/atomic"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// LiveCounter accumulates monotonic-positive deltas from a runner-reported
// stream. Read concurrently by the payment middleware; written
// concurrently by the IPC reader goroutine. Atomic.Uint64 satisfies
// both.
type LiveCounter struct {
	total atomic.Uint64
}

var _ extractors.LiveCounter = (*LiveCounter)(nil)

// Add records a runner-reported delta. Negative or zero deltas are
// callers' responsibility to filter — this method is a thin atomic
// wrapper.
func (lc *LiveCounter) Add(delta uint64) {
	if lc == nil {
		return
	}
	lc.total.Add(delta)
}

// CurrentUnits implements extractors.LiveCounter.
func (lc *LiveCounter) CurrentUnits() uint64 {
	if lc == nil {
		return 0
	}
	return lc.total.Load()
}
