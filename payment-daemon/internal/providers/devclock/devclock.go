// Package devclock is a dev-mode Clock. Returns deterministic round +
// L1 block values; advances on demand via Tick. Plan 0016 replaces this
// with a real RoundsManager poller.
package devclock

import (
	"math/big"
	"sync"
)

// DevClock implements providers.Clock with manually-advanced state.
type DevClock struct {
	mu             sync.Mutex
	round          int64
	roundBlockHash []byte
	l1Block        *big.Int
}

// New returns a DevClock at round=1, l1Block=1, with a stable
// canned block hash so wire-format encodings are reproducible.
func New() *DevClock {
	return &DevClock{
		round:          1,
		roundBlockHash: []byte("dev-round-block-hash-32-bytes!!!"), // exactly 32 bytes
		l1Block:        big.NewInt(1),
	}
}

// LastInitializedRound returns the current round.
func (c *DevClock) LastInitializedRound() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.round
}

// LastInitializedL1BlockHash returns the canned block hash.
func (c *DevClock) LastInitializedL1BlockHash() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.roundBlockHash...)
}

// LastSeenL1Block returns the current L1 block.
func (c *DevClock) LastSeenL1Block() *big.Int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return new(big.Int).Set(c.l1Block)
}

// GetTranscoderPoolSize returns a fixed dev-mode pool size of 100 so
// reserveAlloc math has a non-zero divisor without any chain integration.
func (c *DevClock) GetTranscoderPoolSize() *big.Int {
	return big.NewInt(100)
}

// Tick advances the dev clock's L1 block by `n` and rolls the round
// forward by 1 every 100 blocks (matching production's ~100-block
// rounds on Arbitrum). Test-only hook.
func (c *DevClock) Tick(n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.l1Block.Add(c.l1Block, big.NewInt(n))
	if c.l1Block.Int64()/100 != (c.l1Block.Int64()-n)/100 {
		c.round++
	}
}
