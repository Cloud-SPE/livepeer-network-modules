package chaintesting

import (
	"context"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
)

// FakeClock is a deterministic Clock implementation. Tests advance time
// manually via Advance(d); Sleep and Tickers fire when the fake clock has
// advanced past their target time.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
	sleeps  []*fakeSleep
}

// NewFakeClock returns a FakeClock initialised at start. If start is the
// zero value, uses 2026-04-26 00:00:00 UTC for deterministic test output.
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	}
	return &FakeClock{now: start}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake clock forward by d, firing any tickers and waking
// any sleeps that target before-or-equal the new time.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	tickers := append([]*fakeTicker(nil), c.tickers...)
	sleeps := c.sleeps
	c.sleeps = nil
	c.mu.Unlock()

	for _, t := range tickers {
		t.maybeFire(now)
	}
	for _, s := range sleeps {
		if !s.target.After(now) {
			s.wake()
		} else {
			c.mu.Lock()
			c.sleeps = append(c.sleeps, s)
			c.mu.Unlock()
		}
	}
}

// Sleep blocks until the fake clock advances past target time, ctx is
// cancelled, or the FakeClock is GC'd.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	target := c.now.Add(d)
	if !target.After(c.now) {
		c.mu.Unlock()
		return nil
	}
	s := &fakeSleep{
		target: target,
		ch:     make(chan struct{}),
	}
	c.sleeps = append(c.sleeps, s)
	c.mu.Unlock()

	select {
	case <-s.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NewTicker returns a Ticker that fires every d ticks of the fake clock.
func (c *FakeClock) NewTicker(d time.Duration) clock.Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{
		c:        c,
		interval: d,
		next:     c.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	c.tickers = append(c.tickers, t)
	return t
}

type fakeSleep struct {
	target time.Time
	ch     chan struct{}
	once   sync.Once
}

func (s *fakeSleep) wake() {
	s.once.Do(func() { close(s.ch) })
}

type fakeTicker struct {
	c        *FakeClock
	interval time.Duration
	next     time.Time
	ch       chan time.Time
	stopped  bool
	mu       sync.Mutex
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
}

func (t *fakeTicker) maybeFire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	for !t.next.After(now) {
		select {
		case t.ch <- t.next:
		default:
			// Receiver hasn't consumed previous tick; drop newer ones to
			// match real ticker semantics where rapid advancement
			// coalesces.
		}
		t.next = t.next.Add(t.interval)
	}
}
