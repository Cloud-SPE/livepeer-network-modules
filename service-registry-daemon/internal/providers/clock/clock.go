// Package clock holds the Clock provider — system time abstraction so
// tests can drive time deterministically.
package clock

import "time"

// Clock returns the current wall time. Implementations must be safe
// for concurrent use.
type Clock interface {
	Now() time.Time
}

// System is the production clock backed by time.Now().
type System struct{}

// Now returns time.Now().UTC() — UTC always, so manifest IssuedAt and
// audit-log timestamps are consistent across hosts in different zones.
func (System) Now() time.Time { return time.Now().UTC() }

// Fixed is a test clock that returns a fixed time. Use Advance to
// move it.
type Fixed struct {
	T time.Time
}

func (f *Fixed) Now() time.Time { return f.T.UTC() }

// Advance moves the fixed clock forward by d.
func (f *Fixed) Advance(d time.Duration) { f.T = f.T.Add(d) }
