// Package clock provides a testable time abstraction.
//
// Production code uses System(); tests use chain-commons/testing.FakeClock
// for deterministic time advancement.
package clock

import (
	"context"
	"time"
)

// Clock is the time abstraction used by services that care about wall-clock
// or sleeping. Test fakes implement deterministic alternatives.
type Clock interface {
	// Now returns the current time.
	Now() time.Time

	// Sleep blocks for d or until ctx is cancelled. Returns ctx.Err() on
	// cancellation; nil on natural expiry.
	Sleep(ctx context.Context, d time.Duration) error

	// NewTicker returns a Ticker that fires every d. The Ticker must be
	// stopped to free resources.
	NewTicker(d time.Duration) Ticker
}

// Ticker is the abstraction returned by Clock.NewTicker. The C() channel
// receives a value every tick interval until Stop() is called.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// System returns a Clock backed by the standard time package.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (systemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{t: time.NewTicker(d)}
}

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()                { s.t.Stop() }
