// Package store holds the Store provider — persistent key-value
// access used by the manifest cache and audit log. The interface is
// minimal on purpose; implementations choose how to map buckets to
// physical files.
package store

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

// ErrNotFound is returned by Get when the key is absent. Callers that
// distinguish missing from corrupt keys should use errors.Is.
var ErrNotFound = errors.New("store: not found")

// Store is the abstract key-value surface.
type Store interface {
	// Get reads the value at (bucket, key). Returns ErrNotFound if
	// absent.
	Get(bucket, key []byte) ([]byte, error)
	// Put writes value at (bucket, key), creating the bucket if needed.
	Put(bucket, key, value []byte) error
	// Delete removes (bucket, key). No-op if absent.
	Delete(bucket, key []byte) error
	// ForEach iterates entries in bucket in key order. fn returning a
	// non-nil error stops iteration and returns that error.
	ForEach(bucket []byte, fn func(key, value []byte) error) error
	// Close releases underlying resources.
	Close() error
}

// Bolt is the production Store backed by go.etcd.io/bbolt.
type Bolt struct {
	db *bbolt.DB
}

// OpenBolt opens (or creates) a BoltDB file at path.
func OpenBolt(path string) (*Bolt, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return &Bolt{db: db}, nil
}

// Get reads from bucket/key.
func (b *Bolt) Get(bucket, key []byte) ([]byte, error) {
	var out []byte
	err := b.db.View(func(tx *bbolt.Tx) error {
		bk := tx.Bucket(bucket)
		if bk == nil {
			return ErrNotFound
		}
		v := bk.Get(key)
		if v == nil {
			return ErrNotFound
		}
		out = make([]byte, len(v))
		copy(out, v)
		return nil
	})
	return out, err
}

// Put writes value at bucket/key, creating the bucket if needed.
func (b *Bolt) Put(bucket, key, value []byte) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		return bk.Put(key, value)
	})
}

// Delete removes bucket/key. No-op if either is absent.
func (b *Bolt) Delete(bucket, key []byte) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bk := tx.Bucket(bucket)
		if bk == nil {
			return nil
		}
		return bk.Delete(key)
	})
}

// ForEach iterates the bucket in key order.
func (b *Bolt) ForEach(bucket []byte, fn func(k, v []byte) error) error {
	return b.db.View(func(tx *bbolt.Tx) error {
		bk := tx.Bucket(bucket)
		if bk == nil {
			return nil
		}
		return bk.ForEach(fn)
	})
}

// Close closes the underlying file.
func (b *Bolt) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

// Memory is an in-process Store implementation used by tests, examples,
// and --dev mode.
type Memory struct {
	mu      sync.Mutex
	buckets map[string]map[string][]byte
}

// NewMemory returns a fresh Memory store.
func NewMemory() *Memory {
	return &Memory{buckets: map[string]map[string][]byte{}}
}

// Get reads bucket/key.
func (m *Memory) Get(bucket, key []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bk, ok := m.buckets[string(bucket)]
	if !ok {
		return nil, ErrNotFound
	}
	v, ok := bk[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

// Put writes bucket/key/value.
func (m *Memory) Put(bucket, key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bk, ok := m.buckets[string(bucket)]
	if !ok {
		bk = map[string][]byte{}
		m.buckets[string(bucket)] = bk
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	bk[string(key)] = cp
	return nil
}

// Delete removes bucket/key.
func (m *Memory) Delete(bucket, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bk, ok := m.buckets[string(bucket)]
	if !ok {
		return nil
	}
	delete(bk, string(key))
	return nil
}

// ForEach iterates bucket entries in arbitrary order. Memory does not
// promise key-sort order; tests that depend on order should use Bolt.
func (m *Memory) ForEach(bucket []byte, fn func(k, v []byte) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bk, ok := m.buckets[string(bucket)]
	if !ok {
		return nil
	}
	for k, v := range bk {
		if err := fn([]byte(k), v); err != nil {
			return err
		}
	}
	return nil
}

// Close is a no-op for Memory.
func (m *Memory) Close() error { return nil }
