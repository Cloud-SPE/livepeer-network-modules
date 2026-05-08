// Package chaincommonsadapter implements service-registry-daemon's
// clock.Clock interface by delegating to chain-commons' Clock provider.
//
// chain-commons.Clock has a richer surface (Now / Sleep / NewTicker);
// the registry daemon's Clock only needs Now(). Adapter is a strict
// subset projection.
//
// Pre-drafted ahead of plan 0005.
package chaincommonsadapter

import (
	"errors"
	"time"

	cclock "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
)

// New wraps a chain-commons clock.Clock as a registry-daemon
// clock.Clock. Returns an error if c is nil. The returned clock's
// Now() always returns time in UTC, matching the registry daemon's
// canonical-form invariant for IssuedAt and audit-log timestamps.
func New(c cclock.Clock) (clock.Clock, error) {
	if c == nil {
		return nil, errors.New("chaincommonsadapter.New: clock is required")
	}
	return &adapter{inner: c}, nil
}

type adapter struct {
	inner cclock.Clock
}

// Now implements clock.Clock. Always returns UTC.
func (a *adapter) Now() time.Time { return a.inner.Now().UTC() }

// Compile-time: adapter satisfies clock.Clock.
var _ clock.Clock = (*adapter)(nil)
