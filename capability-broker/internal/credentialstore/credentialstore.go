// Package credentialstore is the broker-side sealed store of runner
// attach credentials (plan 0043 §3.3; protocols/broker-admin.md §5;
// protocols/runner-attach.md §3.1.1).
//
// One credential is one host enrollment. It grants ATTACH — the right to
// open a connection and send an attach document — and nothing else: not
// eligibility, not a price, not a manifest change. A stolen bearer
// attaches as that host and is then gated by certification like any
// other runner; revoking it ends that.
//
// What is stored: only sha256(token), never the token. Plaintext is
// returned exactly once, from Enroll or Rotate, and the caller hands it
// to the agent. Records are sealed at rest with the same AES-GCM
// discipline as the session store, so a copied database reveals neither
// hosts nor members without the key.
//
// Kind is stored from day one so the upgrade to per-host keypairs
// (ed25519, runner-attach §3.1.1) is additive: a record of another kind
// carries a public key instead of a hash and Authenticate dispatches on
// it. Only bearer is implemented here.
package credentialstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	// KeySize is the sealing key length (AES-256).
	KeySize = 32

	credentialsBucket = "credentials"    // credential_id -> sealed record
	byHashBucket      = "by_token_hash"  // sha256 hex -> credential_id (current and rotating-previous)
	byHostBucket      = "by_host"        // host_id -> credential_id
	replayBucket      = "enroll_replays" // request id -> sealed EnrollResult
	metaBucket        = "meta"           // sync revision

	// KindBearer is the only credential kind implemented.
	KindBearer = "bearer"
	// KindEd25519 is reserved (runner-attach §3.1.1). Enroll refuses it
	// with ErrKindUnsupported until the keypair path ships.
	KindEd25519 = "ed25519"

	SourceEnroll = "enroll"
	SourceSync   = "sync"

	StateActive   = "active"
	StateRotating = "rotating"
	StateExpired  = "expired"
	StateRevoked  = "revoked"

	tokenPrefix = "lpc_"
)

var (
	// ErrRejected is returned by Authenticate for unknown, expired, and
	// revoked tokens alike. The three MUST be indistinguishable to the
	// presenter (runner-attach §4.1).
	ErrRejected = errors.New("credential rejected")
	// ErrNotFound is for admin lookups by id.
	ErrNotFound = errors.New("credential not found")
	// ErrHostTaken means Enroll named a host_id that already has a
	// non-revoked credential.
	ErrHostTaken = errors.New("host_id already enrolled")
	// ErrKindUnsupported is for kinds this store does not implement.
	ErrKindUnsupported = errors.New("credential kind unsupported")
	// ErrRevoked is Rotate on a revoked credential.
	ErrRevoked = errors.New("credential is revoked")
)

// Record is one enrollment. Never carries a plaintext secret.
type Record struct {
	CredentialID     string    `json:"credential_id"`
	HostID           string    `json:"host_id"`
	Kind             string    `json:"kind"`
	TokenSHA256      string    `json:"token_sha256"` // hex; bearer only
	Label            string    `json:"label,omitempty"`
	MemberEthAddress string    `json:"member_eth_address,omitempty"`
	Source           string    `json:"source"`
	State            string    `json:"state"`
	IssuedAt         time.Time `json:"issued_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	LastUsedAt       time.Time `json:"last_used_at,omitempty"`
	// Rotation is set while State is rotating: the previous token's hash
	// stays valid until PreviousExpiresAt.
	Rotation      *Rotation `json:"rotation,omitempty"`
	RevokedAt     time.Time `json:"revoked_at,omitempty"`
	RevokeReason  string    `json:"revoke_reason,omitempty"`
	SyncRevision  string    `json:"sync_revision,omitempty"`
	SchemaVersion int       `json:"schema_version"`
}

// Rotation is the overlap window after Rotate.
type Rotation struct {
	PreviousSHA256    string    `json:"previous_sha256"`
	PreviousExpiresAt time.Time `json:"previous_expires_at"`
}

// EnrollRequest is what POST /admin/v1/enroll carries.
type EnrollRequest struct {
	HostID           string
	Label            string
	Kind             string
	ExpiresIn        time.Duration
	MemberEthAddress string
	// RequestID enables replay: the same id returns the recorded result,
	// plaintext included (broker-admin §1).
	RequestID string
}

// EnrollResult is the one-time response carrying the plaintext token.
type EnrollResult struct {
	Record Record `json:"record"`
	Token  string `json:"token"`
}

// SyncEntry is one credential pushed by a pool controller
// (broker-admin §5.4). Only the hash travels.
type SyncEntry struct {
	CredentialID     string    `json:"credential_id"`
	HostID           string    `json:"host_id"`
	Kind             string    `json:"kind"`
	TokenSHA256      string    `json:"token_sha256"`
	ExpiresAt        time.Time `json:"expires_at"`
	Label            string    `json:"label,omitempty"`
	MemberEthAddress string    `json:"member_eth_address,omitempty"`
	State            string    `json:"state,omitempty"`
}

// Options bound enrollment lifetimes.
type Options struct {
	DefaultExpiry time.Duration // 0 → 90 days
	MaxExpiry     time.Duration // 0 → 365 days
	Now           func() time.Time
}

// Store is the bbolt-backed credential store.
type Store struct {
	db   *bolt.DB
	aead cipher.AEAD
	opts Options
}

// Open opens (creating if needed) the store at path.
func Open(path string, key []byte, opts Options) (*Store, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("credentialstore: sealing key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credentialstore: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credentialstore: gcm: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("credentialstore: open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{credentialsBucket, byHashBucket, byHostBucket, replayBucket, metaBucket} {
			if _, e := tx.CreateBucketIfNotExists([]byte(b)); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("credentialstore: init: %w", err)
	}
	if opts.DefaultExpiry <= 0 {
		opts.DefaultExpiry = 90 * 24 * time.Hour
	}
	if opts.MaxExpiry <= 0 {
		opts.MaxExpiry = 365 * 24 * time.Hour
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{db: db, aead: aead, opts: opts}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// HashToken is the stored form of a bearer token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken mints a bearer secret: lpc_ + 256 bits, base64url.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func newID(prefix string) (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// Enroll mints a credential for a host. The plaintext token is in the
// result and nowhere else. With a RequestID, a repeat call returns the
// recorded result byte-for-byte.
func (s *Store) Enroll(req EnrollRequest) (*EnrollResult, error) {
	if req.Kind == "" {
		req.Kind = KindBearer
	}
	if req.Kind != KindBearer {
		return nil, fmt.Errorf("%w: %q", ErrKindUnsupported, req.Kind)
	}
	if req.RequestID != "" {
		if prior, err := s.replay(req.RequestID); err != nil {
			return nil, err
		} else if prior != nil {
			return prior, nil
		}
	}
	now := s.opts.Now()
	expiry := req.ExpiresIn
	if expiry <= 0 {
		expiry = s.opts.DefaultExpiry
	}
	if expiry > s.opts.MaxExpiry {
		return nil, fmt.Errorf("credentialstore: expires_in %s exceeds max %s", expiry, s.opts.MaxExpiry)
	}
	hostID := strings.TrimSpace(req.HostID)
	if hostID == "" {
		id, err := newID("host-")
		if err != nil {
			return nil, err
		}
		hostID = id[:13]
	}
	token, err := NewToken()
	if err != nil {
		return nil, err
	}
	credID, err := newID("cred_")
	if err != nil {
		return nil, err
	}
	rec := Record{
		CredentialID:     credID,
		HostID:           hostID,
		Kind:             KindBearer,
		TokenSHA256:      HashToken(token),
		Label:            req.Label,
		MemberEthAddress: req.MemberEthAddress,
		Source:           SourceEnroll,
		State:            StateActive,
		IssuedAt:         now,
		ExpiresAt:        now.Add(expiry),
		SchemaVersion:    1,
	}
	result := &EnrollResult{Record: rec, Token: token}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if existing := tx.Bucket([]byte(byHostBucket)).Get([]byte(hostID)); existing != nil {
			prior, err := s.get(tx, string(existing))
			if err == nil && prior.State != StateRevoked && prior.State != StateExpired {
				return fmt.Errorf("%w: %s", ErrHostTaken, hostID)
			}
		}
		if err := s.put(tx, &rec); err != nil {
			return err
		}
		if req.RequestID != "" {
			sealed, err := s.seal(result)
			if err != nil {
				return err
			}
			return tx.Bucket([]byte(replayBucket)).Put([]byte(req.RequestID), sealed)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Authenticate resolves a presented bearer token to its record, or
// ErrRejected. Unknown, expired, revoked, and rotation-window-elapsed
// are all ErrRejected. Updates LastUsedAt on success.
func (s *Store) Authenticate(token string) (*Record, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrRejected
	}
	presented := HashToken(token)
	now := s.opts.Now()
	var out *Record
	err := s.db.Update(func(tx *bolt.Tx) error {
		credID := tx.Bucket([]byte(byHashBucket)).Get([]byte(presented))
		if credID == nil {
			return ErrRejected
		}
		rec, err := s.get(tx, string(credID))
		if err != nil {
			return ErrRejected
		}
		if rec.Kind != KindBearer || rec.State == StateRevoked {
			return ErrRejected
		}
		// Constant-time on the hash, even though the index lookup already
		// matched: the index is a map, and we never want the comparison
		// discipline to depend on how the lookup happened.
		current := subtle.ConstantTimeCompare([]byte(rec.TokenSHA256), []byte(presented)) == 1
		previous := rec.Rotation != nil &&
			subtle.ConstantTimeCompare([]byte(rec.Rotation.PreviousSHA256), []byte(presented)) == 1
		switch {
		case current:
			if !now.Before(rec.ExpiresAt) {
				return ErrRejected
			}
		case previous:
			if !now.Before(rec.Rotation.PreviousExpiresAt) {
				return ErrRejected
			}
		default:
			return ErrRejected
		}
		rec.LastUsedAt = now
		if rec.Rotation != nil && !now.Before(rec.Rotation.PreviousExpiresAt) {
			// Grace elapsed: retire the previous hash.
			_ = tx.Bucket([]byte(byHashBucket)).Delete([]byte(rec.Rotation.PreviousSHA256))
			rec.Rotation = nil
			rec.State = StateActive
		}
		if err := s.put(tx, rec); err != nil {
			return err
		}
		out = rec
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrRejected) {
			return nil, ErrRejected
		}
		return nil, err
	}
	return out, nil
}

// Get returns a record by id.
func (s *Store) Get(credentialID string) (*Record, error) {
	var out *Record
	err := s.db.View(func(tx *bolt.Tx) error {
		rec, err := s.get(tx, credentialID)
		if err != nil {
			return err
		}
		out = rec
		return nil
	})
	return out, err
}

// ByHost returns the current (non-revoked) record for a host, if any.
func (s *Store) ByHost(hostID string) (*Record, error) {
	var out *Record
	err := s.db.View(func(tx *bolt.Tx) error {
		credID := tx.Bucket([]byte(byHostBucket)).Get([]byte(hostID))
		if credID == nil {
			return ErrNotFound
		}
		rec, err := s.get(tx, string(credID))
		if err != nil {
			return err
		}
		out = rec
		return nil
	})
	return out, err
}

// List returns every record, sorted by issued_at then id. Expired
// records are reported with State expired without being rewritten.
func (s *Store) List() ([]Record, error) {
	now := s.opts.Now()
	var out []Record
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(credentialsBucket)).ForEach(func(_, v []byte) error {
			var rec Record
			if err := s.unseal(v, &rec); err != nil {
				return err
			}
			if rec.State != StateRevoked && !now.Before(rec.ExpiresAt) {
				rec.State = StateExpired
			}
			out = append(out, rec)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].IssuedAt.Equal(out[j].IssuedAt) {
			return out[i].IssuedAt.Before(out[j].IssuedAt)
		}
		return out[i].CredentialID < out[j].CredentialID
	})
	return out, nil
}

// Rotate mints a new token; the previous stays valid for grace.
func (s *Store) Rotate(credentialID string, grace time.Duration) (*EnrollResult, error) {
	if grace <= 0 {
		grace = time.Hour
	}
	now := s.opts.Now()
	token, err := NewToken()
	if err != nil {
		return nil, err
	}
	var result *EnrollResult
	err = s.db.Update(func(tx *bolt.Tx) error {
		rec, err := s.get(tx, credentialID)
		if err != nil {
			return err
		}
		if rec.State == StateRevoked {
			return ErrRevoked
		}
		if rec.Kind != KindBearer {
			return fmt.Errorf("%w: %q", ErrKindUnsupported, rec.Kind)
		}
		if rec.Rotation != nil {
			// A rotation in progress: the oldest hash is dropped; only
			// one previous is ever honoured.
			_ = tx.Bucket([]byte(byHashBucket)).Delete([]byte(rec.Rotation.PreviousSHA256))
		}
		rec.Rotation = &Rotation{PreviousSHA256: rec.TokenSHA256, PreviousExpiresAt: now.Add(grace)}
		rec.TokenSHA256 = HashToken(token)
		rec.State = StateRotating
		rec.IssuedAt = now
		rec.ExpiresAt = now.Add(s.opts.DefaultExpiry)
		if err := s.put(tx, rec); err != nil {
			return err
		}
		result = &EnrollResult{Record: *rec, Token: token}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Revoke deletes the secret material and marks the record revoked. The
// caller is responsible for killing the host's connections.
func (s *Store) Revoke(credentialID, reason string) (*Record, error) {
	now := s.opts.Now()
	var out *Record
	err := s.db.Update(func(tx *bolt.Tx) error {
		rec, err := s.get(tx, credentialID)
		if err != nil {
			return err
		}
		if err := s.revoke(tx, rec, reason, now); err != nil {
			return err
		}
		out = rec
		return nil
	})
	return out, err
}

func (s *Store) revoke(tx *bolt.Tx, rec *Record, reason string, now time.Time) error {
	hashes := tx.Bucket([]byte(byHashBucket))
	_ = hashes.Delete([]byte(rec.TokenSHA256))
	if rec.Rotation != nil {
		_ = hashes.Delete([]byte(rec.Rotation.PreviousSHA256))
	}
	rec.TokenSHA256 = ""
	rec.Rotation = nil
	rec.State = StateRevoked
	rec.RevokedAt = now
	rec.RevokeReason = reason
	if cur := tx.Bucket([]byte(byHostBucket)).Get([]byte(rec.HostID)); cur != nil && string(cur) == rec.CredentialID {
		_ = tx.Bucket([]byte(byHostBucket)).Delete([]byte(rec.HostID))
	}
	return s.putRaw(tx, rec)
}

// SyncReplace replaces the synced set (Source sync) with entries.
// Locally enrolled credentials are untouched. Returns the host ids
// whose credentials were revoked by this push (dropped or state
// revoked) so the caller can kill their connections. Idempotent.
func (s *Store) SyncReplace(revision string, entries []SyncEntry) (revokedHosts []string, err error) {
	now := s.opts.Now()
	err = s.db.Update(func(tx *bolt.Tx) error {
		incoming := make(map[string]SyncEntry, len(entries))
		for i := range entries {
			e := &entries[i]
			if e.CredentialID == "" || e.HostID == "" {
				return fmt.Errorf("credentialstore: sync entry needs credential_id and host_id")
			}
			if e.Kind == "" {
				e.Kind = KindBearer
			}
			if e.Kind != KindBearer {
				return fmt.Errorf("%w: %q", ErrKindUnsupported, e.Kind)
			}
			if e.State != StateRevoked && (len(e.TokenSHA256) != sha256.Size*2 || !isHex(e.TokenSHA256)) {
				return fmt.Errorf("credentialstore: sync entry %s: token_sha256 must be 64 hex chars", e.CredentialID)
			}
			incoming[e.CredentialID] = *e
		}
		// Existing synced records not in the push → revoke.
		var existing []Record
		if err := tx.Bucket([]byte(credentialsBucket)).ForEach(func(_, v []byte) error {
			var rec Record
			if err := s.unseal(v, &rec); err != nil {
				return err
			}
			if rec.Source == SourceSync {
				existing = append(existing, rec)
			}
			return nil
		}); err != nil {
			return err
		}
		for i := range existing {
			rec := existing[i]
			e, present := incoming[rec.CredentialID]
			if rec.State == StateRevoked {
				continue
			}
			if !present || e.State == StateRevoked {
				if err := s.revoke(tx, &rec, "controller sync", now); err != nil {
					return err
				}
				revokedHosts = append(revokedHosts, rec.HostID)
			}
		}
		for _, e := range entries {
			if e.State == StateRevoked {
				if _, err := s.get(tx, e.CredentialID); errors.Is(err, ErrNotFound) {
					continue // never seen; nothing to revoke
				}
				continue // handled above
			}
			rec, err := s.get(tx, e.CredentialID)
			if errors.Is(err, ErrNotFound) {
				rec = &Record{CredentialID: e.CredentialID, Source: SourceSync, IssuedAt: now, SchemaVersion: 1}
			} else if err != nil {
				return err
			} else if rec.Source != SourceSync {
				return fmt.Errorf("credentialstore: %s is locally enrolled; sync cannot replace it", e.CredentialID)
			}
			if rec.TokenSHA256 != "" && rec.TokenSHA256 != e.TokenSHA256 {
				_ = tx.Bucket([]byte(byHashBucket)).Delete([]byte(rec.TokenSHA256))
			}
			rec.HostID = e.HostID
			rec.Kind = e.Kind
			rec.TokenSHA256 = e.TokenSHA256
			rec.Label = e.Label
			rec.MemberEthAddress = e.MemberEthAddress
			rec.ExpiresAt = e.ExpiresAt
			rec.State = StateActive
			rec.Rotation = nil
			rec.SyncRevision = revision
			if err := s.put(tx, rec); err != nil {
				return err
			}
		}
		return tx.Bucket([]byte(metaBucket)).Put([]byte("sync_revision"), []byte(revision))
	})
	sort.Strings(revokedHosts)
	return revokedHosts, err
}

// SyncRevision returns the last applied sync revision.
func (s *Store) SyncRevision() (string, error) {
	var out string
	err := s.db.View(func(tx *bolt.Tx) error {
		out = string(tx.Bucket([]byte(metaBucket)).Get([]byte("sync_revision")))
		return nil
	})
	return out, err
}

// --- internals ---------------------------------------------------------

func (s *Store) get(tx *bolt.Tx, credentialID string) (*Record, error) {
	raw := tx.Bucket([]byte(credentialsBucket)).Get([]byte(credentialID))
	if raw == nil {
		return nil, ErrNotFound
	}
	var rec Record
	if err := s.unseal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// put writes the record and maintains both indexes.
func (s *Store) put(tx *bolt.Tx, rec *Record) error {
	if rec.TokenSHA256 != "" {
		if err := tx.Bucket([]byte(byHashBucket)).Put([]byte(rec.TokenSHA256), []byte(rec.CredentialID)); err != nil {
			return err
		}
	}
	if rec.Rotation != nil {
		if err := tx.Bucket([]byte(byHashBucket)).Put([]byte(rec.Rotation.PreviousSHA256), []byte(rec.CredentialID)); err != nil {
			return err
		}
	}
	if rec.State != StateRevoked {
		if err := tx.Bucket([]byte(byHostBucket)).Put([]byte(rec.HostID), []byte(rec.CredentialID)); err != nil {
			return err
		}
	}
	return s.putRaw(tx, rec)
}

func (s *Store) putRaw(tx *bolt.Tx, rec *Record) error {
	sealed, err := s.seal(rec)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(credentialsBucket)).Put([]byte(rec.CredentialID), sealed)
}

func (s *Store) replay(requestID string) (*EnrollResult, error) {
	var out *EnrollResult
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(replayBucket)).Get([]byte(requestID))
		if raw == nil {
			return nil
		}
		var res EnrollResult
		if err := s.unseal(raw, &res); err != nil {
			return err
		}
		out = &res
		return nil
	})
	return out, err
}

func (s *Store) seal(v any) ([]byte, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("credentialstore: nonce: %w", err)
	}
	return append(nonce, s.aead.Seal(nil, nonce, plain, nil)...), nil
}

func (s *Store) unseal(raw []byte, v any) error {
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return errors.New("credentialstore: sealed record truncated")
	}
	// No AAD: records carry their own id inside the sealed JSON, and
	// GCM's tag already binds the ciphertext to the key.
	plain, err := s.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return fmt.Errorf("credentialstore: unseal: %w", err)
	}
	return json.Unmarshal(plain, v)
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
