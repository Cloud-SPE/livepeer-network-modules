package chaintesting

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
)

// FakeController is a programmable controller.Controller for tests.
//
// Tests configure addresses via SetAddress before consumers call
// Addresses(). Refresh() is a no-op unless a RefreshFunc is set.
type FakeController struct {
	current     atomic.Pointer[controller.Addresses]
	refreshFn   func(ctx context.Context) (controller.Addresses, error)
	subsMu      sync.Mutex
	subscribers []chan controller.Addresses
	clock       func() time.Time
}

// NewFakeController returns a FakeController initialized with the given
// addresses. clk may be nil; if set, Now() is used as ResolvedAt on each
// refresh.
func NewFakeController(initial controller.Addresses, clk func() time.Time) *FakeController {
	if clk == nil {
		clk = time.Now
	}
	if initial.ResolvedAt.IsZero() {
		initial.ResolvedAt = clk()
	}
	c := &FakeController{clock: clk}
	c.current.Store(&initial)
	return c
}

// Addresses returns the current snapshot. Lock-free.
func (c *FakeController) Addresses() controller.Addresses {
	if a := c.current.Load(); a != nil {
		return *a
	}
	return controller.Addresses{}
}

// Refresh swaps in a new snapshot via the configured RefreshFunc, or
// returns nil if none is set.
func (c *FakeController) Refresh(ctx context.Context) error {
	if c.refreshFn == nil {
		return nil
	}
	next, err := c.refreshFn(ctx)
	if err != nil {
		return err
	}
	if next.ResolvedAt.IsZero() {
		next.ResolvedAt = c.clock()
	}
	old := c.current.Load()
	c.current.Store(&next)
	if old == nil || addressesDiffer(*old, next) {
		c.notify(next)
	}
	return nil
}

// Subscribe returns a channel that receives notifications whenever a
// refresh changes any address.
func (c *FakeController) Subscribe() <-chan controller.Addresses {
	ch := make(chan controller.Addresses, 4)
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	c.subscribers = append(c.subscribers, ch)
	return ch
}

// SetAddress is a test helper for replacing one named address atomically.
// Notifies subscribers if the address changed.
func (c *FakeController) SetAddress(name string, addr chain.Address) {
	cur := c.Addresses()
	switch name {
	case "RoundsManager":
		cur.RoundsManager = addr
	case "BondingManager":
		cur.BondingManager = addr
	case "Minter":
		cur.Minter = addr
	case "TicketBroker":
		cur.TicketBroker = addr
	case "ServiceRegistry":
		cur.ServiceRegistry = addr
	case "LivepeerToken":
		cur.LivepeerToken = addr
	}
	cur.ResolvedAt = c.clock()
	c.current.Store(&cur)
	c.notify(cur)
}

// SetRefreshFunc installs a refresh callback used by Refresh().
func (c *FakeController) SetRefreshFunc(fn func(ctx context.Context) (controller.Addresses, error)) {
	c.refreshFn = fn
}

func (c *FakeController) notify(a controller.Addresses) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subscribers {
		select {
		case ch <- a:
		default:
		}
	}
}

func addressesDiffer(a, b controller.Addresses) bool {
	return a.RoundsManager != b.RoundsManager ||
		a.BondingManager != b.BondingManager ||
		a.Minter != b.Minter ||
		a.TicketBroker != b.TicketBroker ||
		a.ServiceRegistry != b.ServiceRegistry ||
		a.LivepeerToken != b.LivepeerToken
}

// Compile-time: FakeController implements controller.Controller.
var _ controller.Controller = (*FakeController)(nil)
