// Package store provides a BoltDB-backed key-value store, plus a Bucket
// abstraction that services use for their own persistent state.
//
// Two implementations: bolt (production, durable) and memory (testing).
// Services receive a Store instance and request named Buckets; bucket
// schemas are owned by the consuming service, not by this package.
package store

import "errors"

// ErrNotFound is returned by Bucket.Get when the key is not present.
var ErrNotFound = errors.New("store: key not found")

// Store is the persistence root. Daemons share one Store across services.
type Store interface {
	// Bucket returns a Bucket with the given name, creating it if absent.
	Bucket(name string) (Bucket, error)

	// Update runs fn in a single read-write transaction across multiple
	// buckets. Use for atomicity when a single logical operation touches
	// multiple buckets.
	Update(fn func(tx Tx) error) error

	// View runs fn in a single read-only transaction.
	View(fn func(tx Tx) error) error

	// Close flushes pending writes and releases the underlying file.
	Close() error
}

// Tx is a multi-bucket transaction handle.
type Tx interface {
	// Bucket returns the named bucket within this transaction.
	Bucket(name string) (Bucket, error)
}

// Bucket is a key-value namespace within a Store.
type Bucket interface {
	// Put writes value at key.
	Put(key, value []byte) error

	// Get reads the value at key. Returns ErrNotFound if absent.
	Get(key []byte) ([]byte, error)

	// Delete removes the key. No-op if absent.
	Delete(key []byte) error

	// ForEach calls fn for every (key, value) pair in the bucket. Stops on
	// the first non-nil return.
	ForEach(fn func(key, value []byte) error) error

	// Scan calls fn for every (key, value) pair whose key starts with prefix.
	Scan(prefix []byte, fn func(key, value []byte) error) error
}
