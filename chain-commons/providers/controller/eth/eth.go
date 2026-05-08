// Package eth provides a controller.Controller backed by on-chain
// Controller.getContract(bytes32) calls via providers/rpc.RPC.
//
// At construction time, performs the initial resolve (RPC required to be
// reachable). Background goroutine refreshes every Config.RefreshInterval
// and notifies subscribers on any address change.
package eth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// getContractSelector is keccak256("getContract(bytes32)")[:4].
var getContractSelector = crypto.Keccak256([]byte("getContract(bytes32)"))[:4]

// Options wires the resolver.
type Options struct {
	RPC               rpc.RPC
	ControllerAddr    chain.Address
	ContractOverrides map[string]chain.Address
	SkipController    bool
	RefreshInterval   time.Duration
	Clock             clock.Clock
	Logger            logger.Logger
}

// New constructs a controller.Controller. Performs the initial resolve.
// Errors if RPC is missing or the initial resolve fails (and
// SkipController is false).
func New(ctx context.Context, opts Options) (controller.Controller, error) {
	if opts.RPC == nil {
		return nil, errors.New("controller-eth: RPC is required")
	}
	if !opts.SkipController && opts.ControllerAddr == (chain.Address{}) {
		return nil, errors.New("controller-eth: ControllerAddr required (or set SkipController + ContractOverrides)")
	}
	if opts.RefreshInterval == 0 {
		opts.RefreshInterval = 1 * time.Hour
	}
	if opts.Clock == nil {
		opts.Clock = clock.System()
	}

	c := &ethController{
		rpc:        opts.RPC,
		addr:       opts.ControllerAddr,
		overrides:  opts.ContractOverrides,
		skip:       opts.SkipController,
		clock:      opts.Clock,
		logger:     opts.Logger,
		refreshInterval: opts.RefreshInterval,
		stop:       make(chan struct{}),
	}

	addrs, err := c.resolveAll(ctx)
	if err != nil {
		return nil, err
	}
	c.current.Store(&addrs)

	c.wg.Add(1)
	go c.refreshLoop()

	return c, nil
}

type ethController struct {
	rpc       rpc.RPC
	addr      chain.Address
	overrides map[string]chain.Address
	skip      bool
	clock     clock.Clock
	logger    logger.Logger

	current atomic.Pointer[controller.Addresses]

	subsMu      sync.Mutex
	subscribers []chan controller.Addresses

	refreshInterval time.Duration
	stop            chan struct{}
	wg              sync.WaitGroup
}

func (c *ethController) Addresses() controller.Addresses {
	if a := c.current.Load(); a != nil {
		return *a
	}
	return controller.Addresses{}
}

func (c *ethController) Refresh(ctx context.Context) error {
	addrs, err := c.resolveAll(ctx)
	if err != nil {
		c.logf("controller.refresh_failed", logger.Err(err))
		return err
	}
	old := c.current.Load()
	c.current.Store(&addrs)
	if old == nil || addressesDiffer(*old, addrs) {
		c.notify(addrs)
	}
	return nil
}

func (c *ethController) Subscribe() <-chan controller.Addresses {
	ch := make(chan controller.Addresses, 4)
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	c.subscribers = append(c.subscribers, ch)
	return ch
}

func (c *ethController) refreshLoop() {
	defer c.wg.Done()
	t := c.clock.NewTicker(c.refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C():
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = c.Refresh(ctx)
			cancel()
		}
	}
}

func (c *ethController) resolveAll(ctx context.Context) (controller.Addresses, error) {
	var addrs controller.Addresses

	if c.skip {
		// Use overrides exclusively.
		applyOverrides(&addrs, c.overrides)
		addrs.ResolvedAt = c.clock.Now()
		return addrs, nil
	}

	for _, name := range controller.Names {
		if a, ok := c.overrides[name]; ok {
			c.logf("controller.override_applied",
				logger.String("name", name),
				logger.String("address", a.Hex()),
			)
			setAddress(&addrs, name, a)
			continue
		}
		a, err := c.callGetContract(ctx, name)
		if err != nil {
			return controller.Addresses{}, fmt.Errorf("controller.resolve %s: %w", name, err)
		}
		setAddress(&addrs, name, a)
	}
	addrs.ResolvedAt = c.clock.Now()
	return addrs, nil
}

func (c *ethController) callGetContract(ctx context.Context, name string) (chain.Address, error) {
	nameHash := crypto.Keccak256([]byte(name))
	calldata := make([]byte, 0, 4+32)
	calldata = append(calldata, getContractSelector...)
	calldata = append(calldata, nameHash...) // already 32 bytes

	addr := c.addr
	out, err := c.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: calldata,
	}, nil)
	if err != nil {
		return chain.Address{}, err
	}
	if len(out) < 32 {
		return chain.Address{}, fmt.Errorf("getContract(%s) returned %d bytes, want 32", name, len(out))
	}
	// Address is the rightmost 20 bytes of the 32-byte ABI-encoded return.
	var result chain.Address
	copy(result[:], out[12:32])
	return result, nil
}

func setAddress(a *controller.Addresses, name string, addr chain.Address) {
	switch name {
	case "RoundsManager":
		a.RoundsManager = addr
	case "BondingManager":
		a.BondingManager = addr
	case "Minter":
		a.Minter = addr
	case "TicketBroker":
		a.TicketBroker = addr
	case "ServiceRegistry":
		a.ServiceRegistry = addr
	case "LivepeerToken":
		a.LivepeerToken = addr
	}
}

func applyOverrides(a *controller.Addresses, overrides map[string]chain.Address) {
	for name, addr := range overrides {
		setAddress(a, name, addr)
	}
}

func (c *ethController) notify(a controller.Addresses) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subscribers {
		select {
		case ch <- a:
		default:
		}
	}
}

func (c *ethController) logf(msg string, fields ...logger.Field) {
	if c.logger == nil {
		return
	}
	c.logger.Info(msg, fields...)
}

// Close stops the refresh goroutine.
func (c *ethController) Close() error {
	close(c.stop)
	c.wg.Wait()
	return nil
}

func addressesDiffer(a, b controller.Addresses) bool {
	return a.RoundsManager != b.RoundsManager ||
		a.BondingManager != b.BondingManager ||
		a.Minter != b.Minter ||
		a.TicketBroker != b.TicketBroker ||
		a.ServiceRegistry != b.ServiceRegistry ||
		a.LivepeerToken != b.LivepeerToken
}

// Compile-time: ethController satisfies controller.Controller.
var _ controller.Controller = (*ethController)(nil)

// AbiEncodeAddress is exposed for tests that need to construct mock RPC
// responses for getContract.
func AbiEncodeAddress(a chain.Address) []byte {
	out := make([]byte, 32)
	copy(out[12:], a[:])
	_ = common.Address{}
	return out
}
