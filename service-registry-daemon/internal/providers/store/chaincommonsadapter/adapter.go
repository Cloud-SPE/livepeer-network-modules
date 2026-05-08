// Package chaincommonsadapter implements service-registry-daemon's
// store.Store interface by delegating to chain-commons' Store provider.
//
// Shape mismatch (resolved by this adapter):
//   - registry-daemon's Store: flat (bucket []byte, key []byte) signatures.
//     Each call passes the bucket name explicitly.
//   - chain-commons' Store: Bucket(name) returns a handle; Get/Put/Delete
//     are methods on the handle.
//
// The adapter caches Bucket handles keyed by name (lock-free read after
// first miss; sync.Map). Each call resolves the handle and delegates.
//
// Pre-drafted ahead of plan 0005. ErrNotFound mapping: chain-commons.store.ErrNotFound
// is mapped to registry's local store.ErrNotFound so callers using
// errors.Is(err, store.ErrNotFound) keep working unchanged.
package chaincommonsadapter

import (
	"errors"
	"sync"

	cstore "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
)

// New wraps a chain-commons store.Store as a registry-daemon store.Store.
// Returns an error if s is nil.
func New(s cstore.Store) (store.Store, error) {
	if s == nil {
		return nil, errors.New("chaincommonsadapter.New: store is required")
	}
	return &adapter{inner: s}, nil
}

type adapter struct {
	inner   cstore.Store
	buckets sync.Map // map[string]cstore.Bucket
}

// bucket resolves (and caches) the chain-commons Bucket handle for name.
func (a *adapter) bucket(name []byte) (cstore.Bucket, error) {
	key := string(name)
	if cached, ok := a.buckets.Load(key); ok {
		return cached.(cstore.Bucket), nil
	}
	b, err := a.inner.Bucket(key)
	if err != nil {
		return nil, err
	}
	// LoadOrStore handles concurrent first-miss racing; one wins, others
	// drop their handle. cstore.Bucket is goroutine-safe per its contract.
	actual, _ := a.buckets.LoadOrStore(key, b)
	return actual.(cstore.Bucket), nil
}

// translateErr maps chain-commons.store.ErrNotFound to the registry's
// local store.ErrNotFound so existing errors.Is checks keep working.
// Other errors pass through unchanged.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cstore.ErrNotFound) {
		return store.ErrNotFound
	}
	return err
}

// Get implements store.Store.
func (a *adapter) Get(bucket, key []byte) ([]byte, error) {
	b, err := a.bucket(bucket)
	if err != nil {
		return nil, err
	}
	v, err := b.Get(key)
	return v, translateErr(err)
}

// Put implements store.Store.
func (a *adapter) Put(bucket, key, value []byte) error {
	b, err := a.bucket(bucket)
	if err != nil {
		return err
	}
	return b.Put(key, value)
}

// Delete implements store.Store.
func (a *adapter) Delete(bucket, key []byte) error {
	b, err := a.bucket(bucket)
	if err != nil {
		return err
	}
	return b.Delete(key)
}

// ForEach implements store.Store.
func (a *adapter) ForEach(bucket []byte, fn func(key, value []byte) error) error {
	b, err := a.bucket(bucket)
	if err != nil {
		return err
	}
	return b.ForEach(fn)
}

// Close releases the underlying chain-commons store.
func (a *adapter) Close() error { return a.inner.Close() }

// Compile-time: adapter satisfies store.Store.
var _ store.Store = (*adapter)(nil)
