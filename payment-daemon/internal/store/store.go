// Package store is the BoltDB-backed session ledger.
//
// One bucket: "sessions". Keys are session IDs, values are JSON-encoded
// Session records.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const sessionsBucket = "sessions"

// Session is the on-disk session record. Field tags are stable across
// rewrites; remove only with a migration.
type Session struct {
	ID               string    `json:"id"`
	CapabilityID     string    `json:"capability_id"`
	OfferingID       string    `json:"offering_id"`
	Ticket           []byte    `json:"ticket"`
	ExpectedMaxUnits uint64    `json:"expected_max_units"`
	OpenedAt         time.Time `json:"opened_at"`
	Debits           []uint64  `json:"debits,omitempty"`
	ActualUnits      *uint64   `json:"actual_units,omitempty"`
	Closed           bool      `json:"closed"`
	ClosedAt         time.Time `json:"closed_at,omitempty"`
}

// ErrNotFound is returned when a session ID has no corresponding record.
var ErrNotFound = errors.New("session not found")

// ErrClosed is returned by Debit / Reconcile when the session was Closed.
var ErrClosed = errors.New("session is closed")

// Store is the BoltDB-backed session ledger. Open a single Store per
// process; the underlying file is exclusively-locked.
type Store struct {
	db *bolt.DB
}

// Open creates or opens the BoltDB file at path and ensures the sessions
// bucket exists.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("bolt open %s: %w", path, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(sessionsBucket))
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bolt init bucket: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying BoltDB handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// CreateSession allocates a fresh session ID and writes the record.
// The caller fills CapabilityID, OfferingID, Ticket, ExpectedMaxUnits;
// CreateSession sets ID and OpenedAt.
func (s *Store) CreateSession(seed Session) (*Session, error) {
	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("generate id: %w", err)
	}
	seed.ID = id
	seed.OpenedAt = time.Now().UTC()

	if err := s.put(seed); err != nil {
		return nil, err
	}
	return &seed, nil
}

// AppendDebit adds units to the session's debit list.
func (s *Store) AppendDebit(id string, units uint64) error {
	return s.mutate(id, func(sess *Session) error {
		if sess.Closed {
			return ErrClosed
		}
		sess.Debits = append(sess.Debits, units)
		return nil
	})
}

// SetActualUnits records the post-handler reconciled unit count.
func (s *Store) SetActualUnits(id string, actual uint64) error {
	return s.mutate(id, func(sess *Session) error {
		if sess.Closed {
			return ErrClosed
		}
		sess.ActualUnits = &actual
		return nil
	})
}

// CloseSession marks the session closed. Subsequent Debit / Reconcile
// return ErrClosed.
func (s *Store) CloseSession(id string) error {
	return s.mutate(id, func(sess *Session) error {
		if sess.Closed {
			return nil // idempotent
		}
		sess.Closed = true
		sess.ClosedAt = time.Now().UTC()
		return nil
	})
}

// Get returns a copy of the named session. Test-only outside this package.
func (s *Store) Get(id string) (*Session, error) {
	var out *Session
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(sessionsBucket)).Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		var sess Session
		if err := json.Unmarshal(raw, &sess); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		out = &sess
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) put(sess Session) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(sessionsBucket)).Put([]byte(sess.ID), raw)
	})
}

func (s *Store) mutate(id string, fn func(*Session) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(sessionsBucket))
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		var sess Session
		if err := json.Unmarshal(raw, &sess); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		if err := fn(&sess); err != nil {
			return err
		}
		updated, err := json.Marshal(sess)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		return bucket.Put([]byte(id), updated)
	})
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
