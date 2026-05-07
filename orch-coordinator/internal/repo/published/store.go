// Package published is the on-disk store for the currently-live
// signed manifest. The resolver-facing endpoint serves bytes from
// here.
//
// The publish operation is write-tempfile-fsync-rename(2) so old
// bytes stay live until the new ones land. Single-writer guarantee
// via flock(2) over the publish directory; concurrent writers fail
// fast with ErrLocked.
package published

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Errors.
var (
	ErrEmpty  = errors.New("published: no manifest published yet")
	ErrLocked = errors.New("published: another writer holds the publish lock")
)

// FileName is the on-disk name of the live manifest within the
// publish dir.
const FileName = "manifest.json"

// LockName is the on-disk lock-sentinel filename. flock(2) is taken
// over this file so the lock survives renames of FileName.
const LockName = ".publish.lock"

// Store wraps the publish dir.
type Store struct {
	dir string

	mu         sync.Mutex
	lockedFile *os.File

	cacheMu  sync.RWMutex
	cache    []byte
	cacheMod time.Time
}

// New opens (or creates) the publish dir.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("published: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("published: mkdir %s: %w", dir, err)
	}
	s := &Store{dir: dir}
	_ = s.warmCache()
	return s, nil
}

// Path returns the absolute path to the live manifest file.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

// Read returns the live manifest bytes. ErrEmpty when nothing has
// been published.
func (s *Store) Read() ([]byte, time.Time, error) {
	s.cacheMu.RLock()
	if s.cache != nil {
		out := append([]byte(nil), s.cache...)
		mod := s.cacheMod
		s.cacheMu.RUnlock()
		return out, mod, nil
	}
	s.cacheMu.RUnlock()
	body, mod, err := s.readDisk()
	if err != nil {
		return nil, time.Time{}, err
	}
	s.cacheMu.Lock()
	s.cache = body
	s.cacheMod = mod
	s.cacheMu.Unlock()
	return append([]byte(nil), body...), mod, nil
}

func (s *Store) readDisk() ([]byte, time.Time, error) {
	body, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, time.Time{}, ErrEmpty
		}
		return nil, time.Time{}, fmt.Errorf("published: read: %w", err)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		return nil, time.Time{}, err
	}
	return body, info.ModTime().UTC(), nil
}

func (s *Store) warmCache() error {
	body, mod, err := s.readDisk()
	if err != nil {
		return err
	}
	s.cacheMu.Lock()
	s.cache = body
	s.cacheMod = mod
	s.cacheMu.Unlock()
	return nil
}

// Lock takes the publish lock (flock LOCK_EX | LOCK_NB). Caller must
// Unlock when done. Returns ErrLocked if another writer holds it.
func (s *Store) Lock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockedFile != nil {
		return errors.New("published: lock already held by this Store instance")
	}
	f, err := os.OpenFile(filepath.Join(s.dir, LockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("published: open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("published: flock: %w", err)
	}
	s.lockedFile = f
	return nil
}

// Unlock releases the publish lock.
func (s *Store) Unlock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockedFile == nil {
		return nil
	}
	_ = syscall.Flock(int(s.lockedFile.Fd()), syscall.LOCK_UN)
	err := s.lockedFile.Close()
	s.lockedFile = nil
	return err
}

// Publish writes body to a tempfile in the publish dir, fsyncs,
// renames to FileName, and updates the cache. Caller must hold the
// lock via Lock().
func (s *Store) Publish(body []byte) error {
	if len(body) == 0 {
		return errors.New("published: empty body")
	}
	s.mu.Lock()
	if s.lockedFile == nil {
		s.mu.Unlock()
		return errors.New("published: Publish called without holding the lock")
	}
	s.mu.Unlock()

	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("published: create tempfile: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("published: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("published: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("published: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.Path()); err != nil {
		return fmt.Errorf("published: rename: %w", err)
	}
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	s.cacheMu.Lock()
	s.cache = append([]byte(nil), body...)
	s.cacheMod = time.Now().UTC()
	s.cacheMu.Unlock()
	return nil
}
