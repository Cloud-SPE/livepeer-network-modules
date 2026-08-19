// Package sessionstore is the broker's durable paid-session authority —
// the persistence layer behind paid-session/v1 §9 (durability and
// recovery). Everything the spec requires to survive a broker restart
// lives in one Record per session, mutated only through single-bucket
// bbolt transactions so that event dedup, usage watermarks, and payment
// debit progress commit together or not at all.
//
// Schema:
//
//	bucket "sessions" — keyed by session id; value is the JSON-encoded
//	                    Record. DescriptorPrivate is never stored in
//	                    plaintext: it is sealed with AES-256-GCM under
//	                    the store key before the record is written.
//
// Dedup design: the old per-session unbounded event-id set is replaced
// by a monotonic sequence watermark plus the last accepted event id. An
// event is processed iff its sequence is <= the committed watermark; a
// runner retry of an event whose commit failed re-presents the same
// sequence and is accepted, which is what makes the exactly-once debit
// dance work (see paid-session/v1 §7.3).
package sessionstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const sessionsBucket = "sessions"

// KeySize is the required length of the store's sealing key (AES-256).
const KeySize = 32

var (
	// ErrNotFound is returned when no record exists for the session id.
	ErrNotFound = errors.New("sessionstore: session not found")
	// ErrExists is returned by Create when the session id is taken.
	ErrExists = errors.New("sessionstore: session already exists")
)

// Session states (paid-session/v1 §2).
const (
	StateActive      = "active"
	StateWindingDown = "winding_down"
	StateEnded       = "ended"
	StateFailed      = "failed"
)

// GrantAudit is the retained metadata for one issued admission grant.
// The grant secret itself is delivered once at open and never stored in
// recoverable form (runtime-descriptor §2.4 rule 2) — only its hash.
type GrantAudit struct {
	ID         string    `json:"id"`
	Operations []string  `json:"operations"`
	SecretHash []byte    `json:"secret_hash"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Record is the on-disk session record — the paid-session/v1 §9.1
// persistence list, field for field.
type Record struct {
	// Identifiers.
	SessionID        string `json:"session_id"`
	GatewaySessionID string `json:"gateway_session_id"`
	RunnerSessionID  string `json:"runner_session_id"`
	WorkID           string `json:"work_id"`

	// Binding.
	Capability string `json:"capability"`
	Offering   string `json:"offering"`
	BackendRef string `json:"backend_ref"`

	// Payment.
	Sender        []byte `json:"sender,omitempty"`
	PaymentClosed bool   `json:"payment_closed"`

	// Authentication material, hashed (never plaintext).
	CredentialHash    []byte `json:"credential_hash"`
	CallbackTokenHash []byte `json:"callback_token_hash"`

	// Runtime descriptor. Public is stored as-is (it is public by
	// contract); Private is sealed under the store key on write and
	// unsealed on read; Grants hold audit metadata only.
	DescriptorSchema  string          `json:"descriptor_schema"`
	DescriptorPublic  json.RawMessage `json:"descriptor_public,omitempty"`
	DescriptorPrivate json.RawMessage `json:"-"`
	PrivateSealed     []byte          `json:"descriptor_private_sealed,omitempty"`
	Grants            []GrantAudit    `json:"grants,omitempty"`

	// Event/usage/debit progress — the exactly-once commit set.
	LastEventID  string `json:"last_event_id,omitempty"`
	LastSequence uint64 `json:"last_sequence"`
	Unit         string `json:"unit"`
	ClaimedTotal uint64 `json:"claimed_total"`
	DebitedTotal uint64 `json:"debited_total"`
	DebitSeq     uint64 `json:"debit_seq"`

	// Lease and liveness.
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	LastEventAt    time.Time `json:"last_event_at"`

	// Lifecycle.
	State       string    `json:"state"`
	CloseReason string    `json:"close_reason,omitempty"`
	CapacityRef string    `json:"capacity_ref,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	EndedAt     time.Time `json:"ended_at,omitzero"`
}

// Terminal reports whether the record is in a terminal state.
func (r *Record) Terminal() bool {
	return r.State == StateEnded || r.State == StateFailed
}

// HashSecret is the canonical hash for credentials, callback tokens,
// and grant secrets held by the store.
func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// VerifySecret reports whether secret matches the stored hash. The
// comparison is constant-time; hashing first also makes timing
// independent of where the inputs differ.
func VerifySecret(hash []byte, secret string) bool {
	if len(hash) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(hash, HashSecret(secret)) == 1
}

// Store is the bbolt-backed session store.
type Store struct {
	db   *bolt.DB
	aead cipher.AEAD
}

// Open opens (creating if needed) the store at path. key seals
// descriptor private parts at rest and MUST be KeySize bytes.
func Open(path string, key []byte) (*Store, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("sessionstore: sealing key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: gcm: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(sessionsBucket))
		return e
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sessionstore: init bucket: %w", err)
	}
	return &Store{db: db, aead: aead}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Create inserts a new record. ErrExists if the session id is taken.
func (s *Store) Create(rec *Record) error {
	if rec.SessionID == "" {
		return errors.New("sessionstore: empty session id")
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sessionsBucket))
		if b.Get([]byte(rec.SessionID)) != nil {
			return ErrExists
		}
		raw, err := s.seal(rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(rec.SessionID), raw)
	})
}

// Get returns the record for id, with the private descriptor part
// unsealed. ErrNotFound if absent.
func (s *Store) Get(id string) (*Record, error) {
	var rec *Record
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(sessionsBucket)).Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		var e error
		rec, e = s.unseal(raw)
		return e
	})
	return rec, err
}

// Update applies fn to the record inside one write transaction. If fn
// returns an error the transaction aborts and the record is unchanged —
// this is the atomic commit point paid-session/v1 §7.3 requires: dedup
// watermark, usage totals, and debit progress mutate together or not at
// all. bbolt serializes writers, so concurrent Updates on one session
// never interleave.
func (s *Store) Update(id string, fn func(*Record) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sessionsBucket))
		raw := b.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		rec, err := s.unseal(raw)
		if err != nil {
			return err
		}
		if err := fn(rec); err != nil {
			return err
		}
		rec.UpdatedAt = time.Now().UTC()
		out, err := s.seal(rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), out)
	})
}

// ForEach iterates every record (private parts unsealed) in one
// read-only transaction. Returning an error from fn stops iteration.
// This is the sweeper's surface: lease expiry, heartbeat breach, and
// terminal-retention scans all ride it.
func (s *Store) ForEach(fn func(*Record) error) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(sessionsBucket)).ForEach(func(_, raw []byte) error {
			rec, err := s.unseal(raw)
			if err != nil {
				return err
			}
			return fn(rec)
		})
	})
}

// EvictTerminal deletes terminal records whose EndedAt is before
// cutoff, returning how many were removed. Bounds the store per
// paid-session/v1 §2 (terminal records are retained for a window, then
// evicted).
func (s *Store) EvictTerminal(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sessionsBucket))
		var evict [][]byte
		if err := b.ForEach(func(k, raw []byte) error {
			rec, err := s.unseal(raw)
			if err != nil {
				return err
			}
			if rec.Terminal() && !rec.EndedAt.IsZero() && rec.EndedAt.Before(cutoff) {
				evict = append(evict, bytes.Clone(k))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range evict {
			if err := b.Delete(k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// seal serializes the record, encrypting DescriptorPrivate into
// PrivateSealed. The plaintext field carries a `json:"-"` tag, so a
// record can never round-trip private material through the JSON layer
// — deny-by-default, per runtime-descriptor §4.
func (s *Store) seal(rec *Record) ([]byte, error) {
	clone := *rec
	if len(rec.DescriptorPrivate) > 0 {
		nonce := make([]byte, s.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("sessionstore: nonce: %w", err)
		}
		clone.PrivateSealed = append(nonce, s.aead.Seal(nil, nonce, rec.DescriptorPrivate, []byte(rec.SessionID))...)
	}
	return json.Marshal(&clone)
}

func (s *Store) unseal(raw []byte) (*Record, error) {
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("sessionstore: decode: %w", err)
	}
	if len(rec.PrivateSealed) > 0 {
		ns := s.aead.NonceSize()
		if len(rec.PrivateSealed) < ns {
			return nil, errors.New("sessionstore: sealed private part truncated")
		}
		plain, err := s.aead.Open(nil, rec.PrivateSealed[:ns], rec.PrivateSealed[ns:], []byte(rec.SessionID))
		if err != nil {
			return nil, fmt.Errorf("sessionstore: unseal private part: %w", err)
		}
		rec.DescriptorPrivate = plain
	}
	return &rec, nil
}
