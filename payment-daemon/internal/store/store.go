// Package store is the BoltDB-backed receiver-side session ledger.
//
// Schema:
//
//   bucket "sessions"        — keyed by composite (sender, work_id);
//                               value is JSON-encoded Session record.
//   bucket "debit_seqs"       — keyed by composite (sender, work_id,
//                               debit_seq); value is the recorded
//                               work_units. Used for idempotent debits.
//   bucket "capability_index" — keyed by work_id; value is the sender
//                               that opened it. Lets OpenSession be
//                               idempotent before the sender is sealed
//                               on first ProcessPayment.
//
// Sessions are sealed to a sender on the first successful
// ProcessPayment. OpenSession sets `sender == nil`; ProcessPayment
// patches it in. After sealing, all subsequent calls require the
// matching sender.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	sessionsBucket = "sessions"
	debitSeqsBucket = "debit_seqs"
	capIndexBucket  = "capability_index"

	// Plan 0016 buckets — owned by store, consumed by receiver +
	// settlement via the helper methods further down this file.
	noncesBucket           = "nonces"
	redemptionsPending     = "redemptions_pending"
	redemptionsByHash      = "redemptions_by_hash"
	redemptionsRedeemed    = "redemptions_redeemed"
	redemptionsMeta        = "redemptions_meta"
)

const metaNextSeq = "next_seq"

// Session is the on-disk receiver session record.
type Session struct {
	WorkID              string    `json:"work_id"`
	Sender              []byte    `json:"sender,omitempty"` // nil until first ProcessPayment seals it
	Capability          string    `json:"capability"`
	Offering            string    `json:"offering"`
	PricePerWorkUnitWei string    `json:"price_per_work_unit_wei"` // big.Int decimal string
	WorkUnit            string    `json:"work_unit"`
	BalanceWei          string    `json:"balance_wei"` // big.Int decimal string; may be negative (overdraft)
	Closed              bool      `json:"closed"`
	OpenedAt            time.Time `json:"opened_at"`
	ClosedAt            time.Time `json:"closed_at,omitempty"`

	// Authoritative ticket params issued by the receiver at session
	// open. RecipientRand is the receiver-only secret; the daemon
	// reveals it as the preimage when redeeming a winning ticket.
	// FaceValueWei + WinProb / CreationRound bind the wire ticket the
	// sender signs; the ticket's hash recomputed by the contract must
	// match the (sender, fields) tuple. Empty-string / nil indicates
	// the session was opened by the v0.2 stub flow before plan 0016
	// landed; ProcessPayment treats those as "skip chain validation".
	RecipientRand string `json:"recipient_rand,omitempty"` // big.Int decimal string
	FaceValueWei  string `json:"face_value_wei,omitempty"`
	WinProb       string `json:"win_prob,omitempty"`
}

// ErrNotFound is returned when a (sender, work_id) tuple has no
// corresponding record. Receiver maps to gRPC NotFound.
var ErrNotFound = errors.New("session not found")

// ErrClosed is returned when a session has been CloseSession'd. Receiver
// maps to gRPC FailedPrecondition.
var ErrClosed = errors.New("session is closed")

// ErrSenderMismatch is returned when a debit / close call's sender
// doesn't match the sender sealed on the session.
var ErrSenderMismatch = errors.New("sender does not match the session's sealed sender")

// Store is the BoltDB-backed receiver session ledger.
type Store struct {
	db *bolt.DB
}

// Open creates or opens the BoltDB file at path and ensures buckets
// exist.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("bolt open %s: %w", path, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{
			sessionsBucket, debitSeqsBucket, capIndexBucket,
			noncesBucket,
			redemptionsPending, redemptionsByHash, redemptionsRedeemed, redemptionsMeta,
		} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bolt init buckets: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// OpenSession creates a session if one with this work_id doesn't exist.
// Returns (existing, nil) when one is already present (idempotent open).
// `sender` is nil — it gets sealed on first ProcessPayment.
func (s *Store) OpenSession(seed Session) (sess *Session, alreadyOpen bool, err error) {
	if seed.WorkID == "" {
		return nil, false, errors.New("work_id is required")
	}
	seed.Sender = nil
	seed.OpenedAt = time.Now().UTC()
	if seed.BalanceWei == "" {
		seed.BalanceWei = "0"
	}

	err = s.db.Update(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(capIndexBucket))
		key := []byte(seed.WorkID)

		if existing := idx.Get(key); existing != nil {
			// Already opened. The index value is either a sealed
			// sender (≥ 1 byte) or the unsealed sentinel (the empty
			// byte slice — distinguishable from missing because Get
			// returned non-nil).
			senderForLookup := existing
			if isUnsealedSentinel(existing) {
				senderForLookup = nil
			}
			rawKey := compositeKey(senderForLookup, seed.WorkID)
			raw := tx.Bucket([]byte(sessionsBucket)).Get(rawKey)
			if raw == nil {
				// Index disagrees with sessions bucket; treat as fresh.
				return openFresh(tx, seed)
			}
			var found Session
			if err := json.Unmarshal(raw, &found); err != nil {
				return fmt.Errorf("unmarshal existing session: %w", err)
			}
			sess = &found
			alreadyOpen = true
			return nil
		}
		return openFresh(tx, seed)
	})
	if err != nil {
		return nil, false, err
	}
	if !alreadyOpen {
		// openFresh wrote with sender=nil; refetch the placeholder.
		sess = &seed
	}
	return sess, alreadyOpen, nil
}

// SealSender patches the sender onto a session that was opened with
// sender=nil. Idempotent if the same sender is supplied; rejects with
// ErrSenderMismatch on disagreement.
func (s *Store) SealSender(workID string, sender []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(capIndexBucket))
		key := []byte(workID)
		existing := idx.Get(key)
		var senderKey []byte
		if existing == nil {
			senderKey = sender
			if err := idx.Put(key, sender); err != nil {
				return err
			}
		} else {
			senderKey = existing
		}

		composite := compositeKey(senderKey, workID)
		bucket := tx.Bucket([]byte(sessionsBucket))
		raw := bucket.Get(composite)
		// If we just learned the sender, the previous record might be
		// stored under a nil-prefix composite. Fall back to scanning.
		if raw == nil {
			raw, composite = scanByWorkID(bucket, workID)
			if raw == nil {
				return ErrNotFound
			}
		}
		var sess Session
		if err := json.Unmarshal(raw, &sess); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		if len(sess.Sender) > 0 && !bytesEqual(sess.Sender, sender) {
			return ErrSenderMismatch
		}
		sess.Sender = sender

		// If the record was stored under the nil-prefix composite, move
		// it to the sender-prefixed composite.
		newComposite := compositeKey(sender, workID)
		if !bytesEqual(composite, newComposite) {
			if err := bucket.Delete(composite); err != nil {
				return err
			}
		}
		updated, err := json.Marshal(sess)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		return bucket.Put(newComposite, updated)
	})
}

// CreditBalance adds wei to the session's balance.
func (s *Store) CreditBalance(sender []byte, workID string, weiToCredit *big.Int) (*big.Int, error) {
	if weiToCredit == nil {
		return nil, errors.New("weiToCredit is nil")
	}
	var newBalance *big.Int
	err := s.mutate(sender, workID, func(sess *Session) error {
		bal := parseDecimalBig(sess.BalanceWei)
		bal.Add(bal, weiToCredit)
		sess.BalanceWei = bal.String()
		newBalance = bal
		return nil
	})
	if err != nil {
		return nil, err
	}
	return newBalance, nil
}

// DebitBalance is idempotent by debit_seq within a session: a debit
// recorded with the same (sender, work_id, debit_seq) returns the
// balance from the original debit, not a re-debit.
func (s *Store) DebitBalance(sender []byte, workID string, workUnits int64, debitSeq uint64) (*big.Int, error) {
	if workUnits < 0 {
		return nil, errors.New("work_units must be >= 0")
	}
	var newBalance *big.Int
	err := s.db.Update(func(tx *bolt.Tx) error {
		composite := compositeKey(sender, workID)
		seqKey := append(append([]byte(nil), composite...), debitSeqBytes(debitSeq)...)

		// Idempotency check.
		if recorded := tx.Bucket([]byte(debitSeqsBucket)).Get(seqKey); recorded != nil {
			// Don't apply again; just return the current balance.
			raw := tx.Bucket([]byte(sessionsBucket)).Get(composite)
			if raw == nil {
				return ErrNotFound
			}
			var sess Session
			if err := json.Unmarshal(raw, &sess); err != nil {
				return fmt.Errorf("unmarshal: %w", err)
			}
			newBalance = parseDecimalBig(sess.BalanceWei)
			return nil
		}

		// Apply the debit.
		bucket := tx.Bucket([]byte(sessionsBucket))
		raw := bucket.Get(composite)
		if raw == nil {
			return ErrNotFound
		}
		var sess Session
		if err := json.Unmarshal(raw, &sess); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		if sess.Closed {
			return ErrClosed
		}

		price := parseDecimalBig(sess.PricePerWorkUnitWei)
		debitWei := new(big.Int).Mul(price, big.NewInt(workUnits))
		bal := parseDecimalBig(sess.BalanceWei)
		bal.Sub(bal, debitWei)
		sess.BalanceWei = bal.String()
		newBalance = bal

		updated, err := json.Marshal(sess)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		if err := bucket.Put(composite, updated); err != nil {
			return err
		}
		return tx.Bucket([]byte(debitSeqsBucket)).Put(seqKey, []byte(fmt.Sprintf("%d", workUnits)))
	})
	if err != nil {
		return nil, err
	}
	return newBalance, nil
}

// GetBalance returns the current balance for a session.
func (s *Store) GetBalance(sender []byte, workID string) (*big.Int, error) {
	sess, err := s.Get(sender, workID)
	if err != nil {
		return nil, err
	}
	return parseDecimalBig(sess.BalanceWei), nil
}

// CloseSession marks the session closed.
func (s *Store) CloseSession(sender []byte, workID string) (alreadyClosed bool, err error) {
	err = s.mutate(sender, workID, func(sess *Session) error {
		if sess.Closed {
			alreadyClosed = true
			return nil
		}
		sess.Closed = true
		sess.ClosedAt = time.Now().UTC()
		return nil
	})
	return alreadyClosed, err
}

// GetByWorkID returns the session matching this work_id, regardless
// of whether it has been sealed to a sender. Used by GetTicketParams
// (called before a sender is sealed) and by ProcessPayment to read the
// session's recipient-rand secret.
func (s *Store) GetByWorkID(workID string) (*Session, error) {
	var out *Session
	err := s.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(capIndexBucket))
		key := []byte(workID)
		v := idx.Get(key)
		if v == nil {
			return ErrNotFound
		}
		var sender []byte
		if !isUnsealedSentinel(v) {
			sender = v
		}
		bucket := tx.Bucket([]byte(sessionsBucket))
		raw := bucket.Get(compositeKey(sender, workID))
		if raw == nil {
			// Sealed-but-stored-under-nil-prefix recovery.
			raw, _ = scanByWorkID(bucket, workID)
		}
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
	return out, err
}

// Get returns a copy of the session for (sender, work_id).
func (s *Store) Get(sender []byte, workID string) (*Session, error) {
	var out *Session
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(sessionsBucket)).Get(compositeKey(sender, workID))
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
	return out, err
}

// ─── helpers ──────────────────────────────────────────────────────────

// unsealedSentinel marks the work_id as opened but not yet bound to a
// sender. Stored in capability_index. Get returns this as a non-nil
// empty []byte; isUnsealedSentinel inspects the length to distinguish.
var unsealedSentinel = []byte{}

func isUnsealedSentinel(b []byte) bool {
	return len(b) == 0
}

func openFresh(tx *bolt.Tx, seed Session) error {
	composite := compositeKey(nil, seed.WorkID) // sender unsealed yet
	raw, err := json.Marshal(seed)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := tx.Bucket([]byte(sessionsBucket)).Put(composite, raw); err != nil {
		return err
	}
	// Mark the work_id as open in the index. Put with empty value =
	// presence-without-sender; SealSender replaces it with the bound
	// sender.
	return tx.Bucket([]byte(capIndexBucket)).Put([]byte(seed.WorkID), unsealedSentinel)
}

func (s *Store) mutate(sender []byte, workID string, fn func(*Session) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		composite := compositeKey(sender, workID)
		bucket := tx.Bucket([]byte(sessionsBucket))
		raw := bucket.Get(composite)
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
		return bucket.Put(composite, updated)
	})
}

func compositeKey(sender []byte, workID string) []byte {
	out := make([]byte, 0, 1+len(sender)+1+len(workID))
	out = append(out, byte(len(sender)))
	out = append(out, sender...)
	out = append(out, ':')
	out = append(out, []byte(workID)...)
	return out
}

func debitSeqBytes(seq uint64) []byte {
	return []byte(fmt.Sprintf(":seq:%020d", seq))
}

func parseDecimalBig(s string) *big.Int {
	if s == "" {
		return new(big.Int)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return v
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// scanByWorkID walks the sessions bucket looking for a record whose
// WorkID matches. Used after sealing to find the nil-prefix composite
// key. Linear scan; acceptable because sessions are bounded per worker.
func scanByWorkID(bucket *bolt.Bucket, workID string) ([]byte, []byte) {
	var foundRaw, foundKey []byte
	_ = bucket.ForEach(func(k, v []byte) error {
		var sess Session
		if err := json.Unmarshal(v, &sess); err != nil {
			return nil
		}
		if sess.WorkID == workID && len(sess.Sender) == 0 {
			foundRaw = append([]byte(nil), v...)
			foundKey = append([]byte(nil), k...)
		}
		return nil
	})
	return foundRaw, foundKey
}
