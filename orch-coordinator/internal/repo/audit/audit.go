// Package audit is the BoltDB-backed publish-event log. Every signed-
// manifest upload (accepted or rejected) and every successful publish
// records one event here.
//
// Schema:
//
//	bucket "events"  — keyed by uint64 monotonic seq encoded big-endian;
//	                   value is the JSON-encoded Event record.
//	bucket "meta"    — key "next_seq" holds the next sequence number.
package audit

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	eventsBucket = "events"
	metaBucket   = "meta"
	nextSeqKey   = "next_seq"
)

// Outcome enumerates the stable event-result strings. The set is
// pinned for Prometheus labels and runbook lookups.
type Outcome string

const (
	OutcomeAccepted         Outcome = "accepted"
	OutcomeSchemaInvalid    Outcome = "schema_invalid"
	OutcomeSigInvalid       Outcome = "sig_invalid"
	OutcomeIdentityMismatch Outcome = "identity_mismatch"
	OutcomeDriftRejected    Outcome = "drift_rejected"
	OutcomeWindowInvalid    Outcome = "window_invalid"
	OutcomeRollbackRejected Outcome = "rollback_rejected"
	OutcomePublishFailed    Outcome = "publish_failed"
)

// Event is one audit record.
type Event struct {
	Seq             uint64    `json:"seq"`
	At              time.Time `json:"at"`
	Outcome         Outcome   `json:"outcome"`
	Uploader        string    `json:"uploader,omitempty"`
	SignatureSHA256 string    `json:"signature_sha256,omitempty"`
	ManifestSHA256  string    `json:"manifest_sha256,omitempty"`
	PublicationSeq  uint64    `json:"publication_seq,omitempty"`
	Note            string    `json:"note,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
}

// Log wraps the BoltDB instance.
type Log struct {
	db *bolt.DB
}

// Open opens (or creates) the database file.
func Open(path string) (*Log, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{eventsBucket, metaBucket} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Log{db: db}, nil
}

// Close closes the database.
func (l *Log) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

// Append records an event. The Seq field is overwritten with the next
// monotonic sequence number; the At field is filled in with UTC now
// when zero.
func (l *Log) Append(e Event) (Event, error) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	err := l.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(metaBucket))
		seq := uint64(0)
		if v := meta.Get([]byte(nextSeqKey)); v != nil {
			seq = binary.BigEndian.Uint64(v)
		}
		e.Seq = seq
		body, err := json.Marshal(e)
		if err != nil {
			return err
		}
		bucket := tx.Bucket([]byte(eventsBucket))
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, seq)
		if err := bucket.Put(key, body); err != nil {
			return err
		}
		next := make([]byte, 8)
		binary.BigEndian.PutUint64(next, seq+1)
		return meta.Put([]byte(nextSeqKey), next)
	})
	if err != nil {
		return Event{}, fmt.Errorf("audit: append: %w", err)
	}
	return e, nil
}

// Recent returns the last N events in newest-first order.
func (l *Log) Recent(n int) ([]Event, error) {
	if n <= 0 {
		n = 100
	}
	out := make([]Event, 0, n)
	err := l.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(eventsBucket)).Cursor()
		for k, v := c.Last(); k != nil && len(out) < n; k, v = c.Prev() {
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			out = append(out, e)
		}
		return nil
	})
	return out, err
}
