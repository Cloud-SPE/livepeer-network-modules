// Package bolt provides a BoltDB-backed implementation of providers/store.Store.
//
// Daemons share one BoltDB file across services; each service uses its own
// named buckets within. Open with Open(path); close with store.Close() at
// daemon shutdown.
package bolt

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	bbolt "go.etcd.io/bbolt"
)

// Options configures the BoltDB store. Most operators use defaults; tests
// override OpenTimeout to avoid blocking on stuck file locks.
type Options struct {
	// FileMode is the permission mode for the BoltDB file when it's created.
	// Default 0o600 (owner read/write only — protects from accidental disclosure).
	FileMode os.FileMode

	// OpenTimeout bounds how long Open waits for the BoltDB file lock.
	// Default 5s. Set to 0 to disable.
	OpenTimeout time.Duration
}

// Default returns the default Options.
func Default() Options {
	return Options{FileMode: 0o600, OpenTimeout: 5 * time.Second}
}

// Open returns a store.Store backed by a BoltDB file at path. The parent
// directory is created if it doesn't exist.
func Open(path string, opts Options) (store.Store, error) {
	if path == "" {
		return nil, errors.New("bolt: path is required")
	}
	if opts.FileMode == 0 {
		opts.FileMode = 0o600
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("bolt: create parent dir: %w", err)
		}
	}
	db, err := bbolt.Open(path, opts.FileMode, &bbolt.Options{
		Timeout: opts.OpenTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("bolt: open %q: %w", path, err)
	}
	return &boltStore{db: db}, nil
}

type boltStore struct {
	db *bbolt.DB
}

func (s *boltStore) Bucket(name string) (store.Bucket, error) {
	if err := s.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		return err
	}); err != nil {
		return nil, err
	}
	return &boltBucket{db: s.db, name: []byte(name)}, nil
}

func (s *boltStore) Update(fn func(tx store.Tx) error) error {
	return s.db.Update(func(btx *bbolt.Tx) error {
		return fn(&boltTx{tx: btx})
	})
}

func (s *boltStore) View(fn func(tx store.Tx) error) error {
	return s.db.View(func(btx *bbolt.Tx) error {
		return fn(&boltTx{tx: btx})
	})
}

func (s *boltStore) Close() error { return s.db.Close() }

type boltTx struct {
	tx *bbolt.Tx
}

func (t *boltTx) Bucket(name string) (store.Bucket, error) {
	b := t.tx.Bucket([]byte(name))
	if b == nil {
		// Inside Update we can create it; inside View we can't.
		if t.tx.Writable() {
			var err error
			b, err = t.tx.CreateBucket([]byte(name))
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("bolt: bucket %q does not exist (read-only tx)", name)
		}
	}
	return &boltBucketInTx{b: b}, nil
}

// boltBucket is the post-Update bucket handle; each operation opens its own
// transaction. Used when the caller invoked Store.Bucket() outside of a
// shared transaction.
type boltBucket struct {
	db   *bbolt.DB
	name []byte
}

func (b *boltBucket) Put(key, value []byte) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket(b.name)
		if bkt == nil {
			return fmt.Errorf("bolt: bucket %q vanished", b.name)
		}
		return bkt.Put(key, value)
	})
}

func (b *boltBucket) Get(key []byte) ([]byte, error) {
	var out []byte
	err := b.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket(b.name)
		if bkt == nil {
			return store.ErrNotFound
		}
		v := bkt.Get(key)
		if v == nil {
			return store.ErrNotFound
		}
		out = make([]byte, len(v))
		copy(out, v)
		return nil
	})
	return out, err
}

func (b *boltBucket) Delete(key []byte) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket(b.name)
		if bkt == nil {
			return nil
		}
		return bkt.Delete(key)
	})
}

func (b *boltBucket) ForEach(fn func(key, value []byte) error) error {
	return b.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket(b.name)
		if bkt == nil {
			return nil
		}
		return bkt.ForEach(func(k, v []byte) error {
			kc := append([]byte(nil), k...)
			vc := append([]byte(nil), v...)
			return fn(kc, vc)
		})
	})
}

func (b *boltBucket) Scan(prefix []byte, fn func(key, value []byte) error) error {
	return b.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket(b.name)
		if bkt == nil {
			return nil
		}
		c := bkt.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			kc := append([]byte(nil), k...)
			vc := append([]byte(nil), v...)
			if err := fn(kc, vc); err != nil {
				return err
			}
		}
		return nil
	})
}

// boltBucketInTx is the in-transaction bucket handle; operations execute
// within the caller's transaction. Used inside Store.Update or Store.View.
type boltBucketInTx struct {
	b *bbolt.Bucket
}

func (b *boltBucketInTx) Put(key, value []byte) error {
	return b.b.Put(key, value)
}

func (b *boltBucketInTx) Get(key []byte) ([]byte, error) {
	v := b.b.Get(key)
	if v == nil {
		return nil, store.ErrNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (b *boltBucketInTx) Delete(key []byte) error {
	return b.b.Delete(key)
}

func (b *boltBucketInTx) ForEach(fn func(key, value []byte) error) error {
	return b.b.ForEach(func(k, v []byte) error {
		kc := append([]byte(nil), k...)
		vc := append([]byte(nil), v...)
		return fn(kc, vc)
	})
}

func (b *boltBucketInTx) Scan(prefix []byte, fn func(key, value []byte) error) error {
	c := b.b.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		kc := append([]byte(nil), k...)
		vc := append([]byte(nil), v...)
		if err := fn(kc, vc); err != nil {
			return err
		}
	}
	return nil
}
