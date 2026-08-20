// Package store is the BoltDB-backed receiver-side session ledger.
//
// Schema:
//
//	bucket "sessions"        — keyed by composite (sender, work_id);
//	                            value is JSON-encoded Session record.
//	bucket "debit_seqs"       — keyed by composite (sender, work_id,
//	                            debit_seq); value is the recorded
//	                            work_units. Used for idempotent debits.
//	bucket "capability_index" — keyed by work_id; value is the sender
//	                            that opened it. Lets OpenSession be
//	                            idempotent before the sender is sealed
//	                            on first ProcessPayment.
//	bucket "ticket_session_index" — keyed by stable
//	                            (sender, recipient, capability,
//	                            offering); value is the current open
//	                            work_id. Lets GetTicketParams reuse the
//	                            same recipientRand across restarts.
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
	sessionsBucket  = "sessions"
	debitSeqsBucket = "debit_seqs"
	capIndexBucket  = "capability_index"
	ticketIdxBucket = "ticket_session_index"

	// Plan 0016 buckets — owned by store, consumed by receiver +
	// settlement via the helper methods further down this file.
	noncesBucket        = "nonces"
	redemptionsPending  = "redemptions_pending"
	redemptionsByHash   = "redemptions_by_hash"
	redemptionsRedeemed = "redemptions_redeemed"
	redemptionsMeta     = "redemptions_meta"
)

const metaNextSeq = "next_seq"

// Session is the on-disk receiver session record.
type Session struct {
	WorkID              string `json:"work_id"`
	Sender              []byte `json:"sender,omitempty"` // nil until first ProcessPayment seals it
	Recipient           []byte `json:"recipient,omitempty"`
	Capability          string `json:"capability"`
	Offering            string `json:"offering"`
	PricePerWorkUnitWei string `json:"price_per_work_unit_wei"` // big.Int decimal string
	// PerUnits is the price denominator: PricePerWorkUnitWei buys this
	// many work units. Zero is read as 1, which keeps sessions written
	// before the field existed billing exactly as they did.
	PerUnits uint64 `json:"per_units,omitempty"`
	WorkUnit string `json:"work_unit"`
	// DebitedUnits is cumulative work units debited since open. Billing
	// is a function of this running total, not of any single debit —
	// see billFor.
	DebitedUnits uint64    `json:"debited_units,omitempty"`
	BalanceWei   string    `json:"balance_wei"` // big.Int decimal string; may be negative (overdraft)
	Closed       bool      `json:"closed"`
	OpenedAt     time.Time `json:"opened_at"`
	ClosedAt     time.Time `json:"closed_at,omitempty"`

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

// BillFor returns the total wei owed for `units` cumulative work units:
//
//	bill(U) = ceil(U * price / per_units)
//
// Ceiling, so a payee is never left short on work it already delivered;
// cumulative, so a debit costs bill(total+delta) - bill(total) and the
// sum of every debit equals one ceiling over the running total. Rounding
// each debit independently would cost the payer up to a wei per debit,
// which over a long session with a fast tick is real money and — worse —
// makes the payer's and payee's arithmetic disagree.
//
// perUnits of 0 means 1: a session persisted before the denominator
// existed bills exactly as it used to.
func BillFor(price *big.Int, perUnits uint64, units uint64) *big.Int {
	if price == nil || price.Sign() == 0 || units == 0 {
		return new(big.Int)
	}
	if perUnits == 0 {
		perUnits = 1
	}
	total := new(big.Int).Mul(price, new(big.Int).SetUint64(units))
	denom := new(big.Int).SetUint64(perUnits)
	quo, rem := new(big.Int).QuoRem(total, denom, new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	return quo
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

// TicketSessionKey identifies the receiver-issued sender/payee session
// that GetTicketParams should reuse for the lifetime of an open
// session.
type TicketSessionKey struct {
	Sender     []byte
	Recipient  []byte
	Capability string
	Offering   string
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
			sessionsBucket, debitSeqsBucket, capIndexBucket, ticketIdxBucket,
			noncesBucket, mintsBucket, tombstonesBucket,
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

// GetOrCreateTicketSession returns the open receiver-issued session for
// this stable sender/recipient/capability/offering identity. Closed or
// stale indexed sessions are discarded and replaced with a fresh one.
func (s *Store) GetOrCreateTicketSession(key TicketSessionKey, seed Session) (sess *Session, alreadyOpen bool, err error) {
	if seed.WorkID == "" {
		return nil, false, errors.New("work_id is required")
	}
	seed.Sender = append([]byte(nil), key.Sender...)
	seed.Recipient = append([]byte(nil), key.Recipient...)
	seed.Capability = key.Capability
	seed.Offering = key.Offering
	seed.OpenedAt = time.Now().UTC()
	if seed.BalanceWei == "" {
		seed.BalanceWei = "0"
	}

	indexKey := ticketSessionIndexKey(key)
	err = s.db.Update(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(ticketIdxBucket))
		sessions := tx.Bucket([]byte(sessionsBucket))
		capIdx := tx.Bucket([]byte(capIndexBucket))

		if workID := idx.Get(indexKey); workID != nil {
			raw := sessions.Get(compositeKey(key.Sender, string(workID)))
			if raw == nil {
				if err := idx.Delete(indexKey); err != nil {
					return err
				}
			} else {
				var found Session
				if err := json.Unmarshal(raw, &found); err != nil {
					return fmt.Errorf("unmarshal indexed session: %w", err)
				}
				if found.Closed {
					if err := idx.Delete(indexKey); err != nil {
						return err
					}
				} else {
					sess = &found
					alreadyOpen = true
					return nil
				}
			}
		}

		raw, err := json.Marshal(seed)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		if err := sessions.Put(compositeKey(key.Sender, seed.WorkID), raw); err != nil {
			return err
		}
		if err := capIdx.Put([]byte(seed.WorkID), append([]byte(nil), key.Sender...)); err != nil {
			return err
		}
		if err := idx.Put(indexKey, []byte(seed.WorkID)); err != nil {
			return err
		}
		sess = &seed
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return sess, alreadyOpen, nil
}

// ResetTicketSession closes the active stable-identity ticket session,
// drops its nonce ledger, and removes the active index so the next
// GetTicketParams call mints a fresh work_id.
func (s *Store) ResetTicketSession(key TicketSessionKey) (oldWorkID string, reset bool, err error) {
	indexKey := ticketSessionIndexKey(key)
	err = s.db.Update(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(ticketIdxBucket))
		workID := idx.Get(indexKey)
		if workID == nil {
			return nil
		}
		oldWorkID = string(workID)
		sessions := tx.Bucket([]byte(sessionsBucket))
		raw := sessions.Get(compositeKey(key.Sender, oldWorkID))
		if raw == nil {
			return idx.Delete(indexKey)
		}
		var sess Session
		if err := json.Unmarshal(raw, &sess); err != nil {
			return fmt.Errorf("unmarshal indexed session: %w", err)
		}
		sess.Closed = true
		sess.ClosedAt = time.Now().UTC()
		updated, err := json.Marshal(sess)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		if err := sessions.Put(compositeKey(key.Sender, oldWorkID), updated); err != nil {
			return err
		}
		if err := idx.Delete(indexKey); err != nil {
			return err
		}
		if sess.RecipientRand != "" {
			randInt, ok := new(big.Int).SetString(sess.RecipientRand, 10)
			if !ok {
				return errors.New("session rand corrupt")
			}
			if err := deleteNonceLedger(tx.Bucket([]byte(noncesBucket)), randInt); err != nil {
				return err
			}
		}
		reset = true
		return nil
	})
	return oldWorkID, reset, err
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

		// Cumulative billing: charge the difference between the bill
		// for everything debited so far and the bill including this
		// delta. Never price the delta on its own.
		price := parseDecimalBig(sess.PricePerWorkUnitWei)
		before := BillFor(price, sess.PerUnits, sess.DebitedUnits)
		sess.DebitedUnits += uint64(workUnits)
		debitWei := new(big.Int).Sub(BillFor(price, sess.PerUnits, sess.DebitedUnits), before)
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
	err = s.db.Update(func(tx *bolt.Tx) error {
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
		if sess.Closed {
			alreadyClosed = true
			return nil
		}
		sess.Closed = true
		sess.ClosedAt = time.Now().UTC()
		updated, err := json.Marshal(sess)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		if err := bucket.Put(composite, updated); err != nil {
			return err
		}
		if len(sess.Sender) > 0 && len(sess.Recipient) > 0 {
			return tx.Bucket([]byte(ticketIdxBucket)).Delete(ticketSessionIndexKey(TicketSessionKey{
				Sender:     sess.Sender,
				Recipient:  sess.Recipient,
				Capability: sess.Capability,
				Offering:   sess.Offering,
			}))
		}
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

func ticketSessionIndexKey(key TicketSessionKey) []byte {
	out := make([]byte, 0, len(key.Sender)+len(key.Recipient)+len(key.Capability)+len(key.Offering)+4)
	out = append(out, byte(len(key.Sender)))
	out = append(out, key.Sender...)
	out = append(out, ':')
	out = append(out, byte(len(key.Recipient)))
	out = append(out, key.Recipient...)
	out = append(out, ':')
	out = append(out, []byte(key.Capability)...)
	out = append(out, ':')
	out = append(out, []byte(key.Offering)...)
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
