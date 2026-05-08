// Package poolhints implements a BoltDB-backed cache of computed
// (prev, next) positional hints, keyed by round.
//
// Walking the transcoder pool linked list is multiple eth_calls;
// caching by round means same-round hint requests after the first
// are a fast path. Cache survives daemon restart since BoltDB is
// durable.
//
// All persistence goes through chain-commons.providers.store — never
// raw bbolt.
package poolhints

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// Bucket name in the daemon's shared BoltDB store. Stable across versions.
const bucketName = "protocol_daemon_pool_hints"

// Cache wraps a chain-commons store.Store with the protocol-daemon-specific
// schema for pool-hint records.
type Cache struct {
	store store.Store
}

// New constructs a Cache over the given store. Returns an error if the
// underlying bucket can't be created.
func New(s store.Store) (*Cache, error) {
	if s == nil {
		return nil, errors.New("poolhints: store is required")
	}
	if _, err := s.Bucket(bucketName); err != nil {
		return nil, fmt.Errorf("poolhints: open bucket: %w", err)
	}
	return &Cache{store: s}, nil
}

// Put writes the (prev, next) hints for (round, orchAddress).
func (c *Cache) Put(round chain.RoundNumber, orchAddr chain.Address, hints types.PoolHints) error {
	bucket, err := c.store.Bucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Put(makeKey(round, orchAddr), encodeHints(hints))
}

// Get reads the cached hints for (round, orchAddress). Returns
// (zero, false, nil) on cache miss.
func (c *Cache) Get(round chain.RoundNumber, orchAddr chain.Address) (types.PoolHints, bool, error) {
	bucket, err := c.store.Bucket(bucketName)
	if err != nil {
		return types.PoolHints{}, false, err
	}
	value, err := bucket.Get(makeKey(round, orchAddr))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return types.PoolHints{}, false, nil
		}
		return types.PoolHints{}, false, err
	}
	hints, err := decodeHints(value)
	if err != nil {
		return types.PoolHints{}, false, err
	}
	return hints, true, nil
}

// Delete removes the entry for (round, orchAddress). No-op on miss.
func (c *Cache) Delete(round chain.RoundNumber, orchAddr chain.Address) error {
	bucket, err := c.store.Bucket(bucketName)
	if err != nil {
		return err
	}
	return bucket.Delete(makeKey(round, orchAddr))
}

// PurgeBefore removes all entries for rounds strictly less than `cutoff`.
// Used to keep the cache bounded; callers invoke after each round boundary.
func (c *Cache) PurgeBefore(cutoff chain.RoundNumber) (int, error) {
	bucket, err := c.store.Bucket(bucketName)
	if err != nil {
		return 0, err
	}
	var toDelete [][]byte
	if err := bucket.ForEach(func(key, _ []byte) error {
		round, _, ok := parseKey(key)
		if !ok {
			return nil
		}
		if round < cutoff {
			cp := make([]byte, len(key))
			copy(cp, key)
			toDelete = append(toDelete, cp)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	for _, k := range toDelete {
		if err := bucket.Delete(k); err != nil {
			return 0, err
		}
	}
	return len(toDelete), nil
}

// makeKey builds the BoltDB key: 8 bytes BE round || 20 bytes orchAddr.
func makeKey(round chain.RoundNumber, orchAddr chain.Address) []byte {
	out := make([]byte, 8+20)
	binary.BigEndian.PutUint64(out[:8], uint64(round))
	copy(out[8:], orchAddr[:])
	return out
}

// parseKey extracts (round, orchAddr) from a key. Returns ok=false on
// malformed input.
func parseKey(key []byte) (chain.RoundNumber, chain.Address, bool) {
	if len(key) != 8+20 {
		return 0, chain.Address{}, false
	}
	round := chain.RoundNumber(binary.BigEndian.Uint64(key[:8]))
	var addr chain.Address
	copy(addr[:], key[8:])
	return round, addr, true
}

// encodeHints serializes (prev, next) as 40 bytes (20 + 20).
func encodeHints(h types.PoolHints) []byte {
	out := make([]byte, 40)
	copy(out[0:20], h.Prev[:])
	copy(out[20:40], h.Next[:])
	return out
}

// decodeHints parses a 40-byte value into PoolHints.
func decodeHints(b []byte) (types.PoolHints, error) {
	if len(b) != 40 {
		return types.PoolHints{}, fmt.Errorf("poolhints: malformed value (len=%d)", len(b))
	}
	var h types.PoolHints
	copy(h.Prev[:], b[0:20])
	copy(h.Next[:], b[20:40])
	return h, nil
}

// keyPrefix returns the 8-byte big-endian encoding of round, useful for
// `Scan(prefix)` over all addresses at a given round.
func keyPrefix(round chain.RoundNumber) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(round))
	return out
}

// CountForRound reports how many cache entries exist at the given round.
// Useful for tests and observability.
func (c *Cache) CountForRound(round chain.RoundNumber) (int, error) {
	bucket, err := c.store.Bucket(bucketName)
	if err != nil {
		return 0, err
	}
	count := 0
	prefix := keyPrefix(round)
	if err := bucket.Scan(prefix, func(key, _ []byte) error {
		// Defensive: the underlying scan returns prefix matches, but
		// double-check before counting.
		if bytes.HasPrefix(key, prefix) {
			count++
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return count, nil
}
