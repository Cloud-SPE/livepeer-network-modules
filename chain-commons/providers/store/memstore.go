package store

import (
	"bytes"
	"sort"
	"sync"
)

// Memory returns an in-memory Store implementation. Suitable for tests and
// for daemon dev mode (--dev) where persistence isn't required. Not
// thread-safe across processes; safe across goroutines via internal mutex.
func Memory() Store { return newMemStore() }

func newMemStore() *memStore {
	return &memStore{buckets: map[string]*memBucket{}}
}

type memStore struct {
	mu      sync.RWMutex
	buckets map[string]*memBucket
}

func (s *memStore) Bucket(name string) (Bucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[name]
	if !ok {
		b = newMemBucket()
		s.buckets[name] = b
	}
	return b, nil
}

func (s *memStore) Update(fn func(tx Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&memTx{s: s})
}

func (s *memStore) View(fn func(tx Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&memTx{s: s})
}

func (s *memStore) Close() error { return nil }

type memTx struct{ s *memStore }

func (t *memTx) Bucket(name string) (Bucket, error) {
	b, ok := t.s.buckets[name]
	if !ok {
		b = newMemBucket()
		t.s.buckets[name] = b
	}
	return b, nil
}

func newMemBucket() *memBucket {
	return &memBucket{data: map[string][]byte{}}
}

type memBucket struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func (b *memBucket) Put(key, value []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	b.data[string(key)] = cp
	return nil
}

func (b *memBucket) Get(key []byte) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.data[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (b *memBucket) Delete(key []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.data, string(key))
	return nil
}

func (b *memBucket) ForEach(fn func(key, value []byte) error) error {
	b.mu.RLock()
	keys := make([]string, 0, len(b.data))
	for k := range b.data {
		keys = append(keys, k)
	}
	b.mu.RUnlock()
	sort.Strings(keys)
	for _, k := range keys {
		b.mu.RLock()
		v, ok := b.data[k]
		var cp []byte
		if ok {
			cp = make([]byte, len(v))
			copy(cp, v)
		}
		b.mu.RUnlock()
		if !ok {
			continue
		}
		if err := fn([]byte(k), cp); err != nil {
			return err
		}
	}
	return nil
}

func (b *memBucket) Scan(prefix []byte, fn func(key, value []byte) error) error {
	return b.ForEach(func(k, v []byte) error {
		if !bytes.HasPrefix(k, prefix) {
			return nil
		}
		return fn(k, v)
	})
}
