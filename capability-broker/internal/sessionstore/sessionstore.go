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

const (
	sessionsBucket = "sessions"
	// openRequestsBucket maps Livepeer-Request-Id -> session id, making
	// session open idempotent (paid-session/v1 §3.1): a retried open
	// resolves to the original session instead of minting a sibling.
	openRequestsBucket = "open_requests"
)

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

	// OpenFingerprint binds the request id to the open it answered, so a
	// reused id with different content is refused instead of being given
	// somebody else's session.
	OpenFingerprint []byte `json:"open_fingerprint,omitempty"`

	// ReplayMaterial is the credential and grant secrets an idempotent
	// open must be able to re-deliver, sealed under the store key like
	// the descriptor's private part. A gateway whose open response was
	// lost otherwise holds a funded session it can never drive.
	//
	// Cleared at winddown: the replay window is the session's life, and
	// secrets outliving the thing they unlock is how a store becomes a
	// liability.
	ReplayMaterial       []byte `json:"-"`
	ReplayMaterialSealed []byte `json:"replay_material_sealed,omitempty"`

	// Runtime descriptor. Public is stored as-is (it is public by
	// contract); Private is sealed under the store key on write and
	// unsealed on read; Grants hold audit metadata only.
	DescriptorSchema  string          `json:"descriptor_schema"`
	DescriptorPublic  json.RawMessage `json:"descriptor_public,omitempty"`
	DescriptorPrivate json.RawMessage `json:"-"`
	PrivateSealed     []byte          `json:"descriptor_private_sealed,omitempty"`
	Grants            []GrantAudit    `json:"grants,omitempty"`

	// Rotation chain. A recipient rotation rebinds the session to a new
	// payment identity; session_id and the credential do not move, so
	// this is the only record of which work_id paid for which stretch.
	// GenerationStartUnits is the cumulative debited total at the moment
	// this generation began, so a generation's own subtotal is
	// DebitedTotal - GenerationStartUnits without a second counter to
	// keep in step.
	RotationGeneration uint32 `json:"rotation_generation,omitempty"`
	// SettlementSeq orders settlement records for this session. Per
	// session, not per work_id: a rotation mints a new identity, and a
	// per-identity counter would restart mid-session.
	SettlementSeq uint64 `json:"settlement_seq,omitempty"`
	// FundedWei is cumulative credited value over the whole logical
	// session; GenerationFundedWei covers the current identity only.
	// Both are decimal strings, because a wei total outgrows int64.
	// Funding is per identity while billing is cumulative, so a reader
	// reconciling one envelope needs the generation figure and one
	// reconciling the whole session needs the total.
	FundedWei            string `json:"funded_wei,omitempty"`
	GenerationFundedWei  string `json:"generation_funded_wei,omitempty"`
	PredecessorWorkID    string `json:"predecessor_work_id,omitempty"`
	GenerationStartUnits uint64 `json:"generation_start_units,omitempty"`

	// Event/usage/debit progress — the exactly-once commit set.
	LastEventID  string `json:"last_event_id,omitempty"`
	LastSequence uint64 `json:"last_sequence"`
	Unit         string `json:"unit"`
	ClaimedTotal uint64 `json:"claimed_total"`
	DebitedTotal uint64 `json:"debited_total"`
	DebitSeq     uint64 `json:"debit_seq"`
	// PendingDebitSeq is a debit sequence allocated but not yet
	// committed. It is persisted BEFORE the debit is attempted so a
	// retry re-presents the same number and the payee deduplicates it;
	// allocating again on retry would double-debit.
	PendingDebitSeq uint64 `json:"pending_debit_seq,omitempty"`

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
		if _, e := tx.CreateBucketIfNotExists([]byte(sessionsBucket)); e != nil {
			return e
		}
		_, e := tx.CreateBucketIfNotExists([]byte(openRequestsBucket))
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

// CreateIndexed inserts a new record and, in the same transaction,
// indexes it under the open request id. ErrExists if either the session
// id or the request id is already taken (a request-id collision means a
// concurrent open won the race; the caller re-resolves via
// SessionIDForRequest).
func (s *Store) CreateIndexed(rec *Record, requestID string) error {
	if rec.SessionID == "" || requestID == "" {
		return errors.New("sessionstore: empty session id or request id")
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sessionsBucket))
		idx := tx.Bucket([]byte(openRequestsBucket))
		if b.Get([]byte(rec.SessionID)) != nil || idx.Get([]byte(requestID)) != nil {
			return ErrExists
		}
		raw, err := s.seal(rec)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(rec.SessionID), raw); err != nil {
			return err
		}
		return idx.Put([]byte(requestID), []byte(rec.SessionID))
	})
}

// SessionIDForRequest resolves an open request id to its session id.
// ErrNotFound when the request id is unknown.
func (s *Store) SessionIDForRequest(requestID string) (string, error) {
	var id string
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(openRequestsBucket)).Get([]byte(requestID))
		if v == nil {
			return ErrNotFound
		}
		id = string(v)
		return nil
	})
	return id, err
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
	if len(rec.ReplayMaterial) > 0 {
		nonce := make([]byte, s.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("sessionstore: nonce: %w", err)
		}
		clone.ReplayMaterialSealed = append(nonce, s.aead.Seal(nil, nonce, rec.ReplayMaterial, []byte(rec.SessionID))...)
	} else {
		clone.ReplayMaterialSealed = nil
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
	if len(rec.ReplayMaterialSealed) > 0 {
		ns := s.aead.NonceSize()
		if len(rec.ReplayMaterialSealed) < ns {
			return nil, errors.New("sessionstore: sealed replay material truncated")
		}
		plain, err := s.aead.Open(nil, rec.ReplayMaterialSealed[:ns], rec.ReplayMaterialSealed[ns:], []byte(rec.SessionID))
		if err != nil {
			return nil, fmt.Errorf("sessionstore: unseal replay material: %w", err)
		}
		rec.ReplayMaterial = plain
	}
	return &rec, nil
}

// GetByWorkID finds a session by a payment identity it holds or held.
// Rotation means a reader can arrive with a superseded work_id — a
// settlement forwarded through a slow path, say — and matching only the
// current one would answer "unknown session" about a session that is
// right there.
func (s *Store) GetByWorkID(workID string) (*Record, error) {
	if workID == "" {
		return nil, ErrNotFound
	}
	var out *Record
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sessionsBucket))
		if b == nil {
			return ErrNotFound
		}
		return b.ForEach(func(_, raw []byte) error {
			rec, err := s.unseal(raw)
			if err != nil {
				return nil // a record we cannot read is not a match
			}
			if rec.WorkID == workID || rec.PredecessorWorkID == workID {
				out = rec
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, ErrNotFound
	}
	return out, nil
}
