package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Mint idempotency — payer-side (`CreatePayment`).
//
// Minting signs tickets against real deposit, so a retry after an
// uncertain response must replay rather than re-sign. Two buckets, and
// the second is the one that makes eviction safe:
//
//	bucket "mints"           — full recorded response, keyed by
//	                           hash(sender, mint_request_id). Evictable.
//	bucket "mint_tombstones" — the same key, with the request
//	                           fingerprint and when it was first seen.
//	                           PERMANENT.
//
// Retention alone cannot be safe. If an evicted key became mintable
// again, a retry delayed past the window would mint a second batch and
// the payer would pay twice — quietly, because nothing in the system
// would report it. The tombstone converts that case into a deterministic
// refusal: a key the daemon has ever issued a payment for is never
// treated as new, however long the caller waits.
//
// The tombstone stores a hash, not the key, and no response payload, so
// permanence costs about 80 bytes per mint. A caller minting ten
// thousand payments a day accumulates roughly 300 MB a year, and the
// records compact to nothing but hashes.

const (
	mintsBucket      = "mints"
	tombstonesBucket = "mint_tombstones"
)

// ErrMintFingerprintMismatch reports a mint id reused with different
// request content. The id is a promise about content; answering with the
// earlier payment would hand the caller a batch it did not ask for.
var ErrMintFingerprintMismatch = errors.New("store: mint_request_id reused with different content")

// ErrMintExpired reports a retry whose replay record has been evicted.
// The mint is refused, never re-signed.
var ErrMintExpired = errors.New("store: mint_request_id was seen but its replay record has expired")

// ErrMintIncomplete reports a mint id whose reservation exists but whose
// response was never recorded — the daemon died between signing and
// persisting.
//
// The retry is refused rather than re-signed. The reservation cannot
// tell us whether the first attempt got far enough to produce a ticket,
// and re-signing on a maybe is how a payer pays twice. Refusing costs
// the caller a new idempotency key; guessing costs it money.
var ErrMintIncomplete = errors.New("store: mint_request_id was reserved but never completed")

// MintRecord is the recorded response for one mint intent, replayed
// verbatim on retry.
type MintRecord struct {
	Fingerprint    []byte    `json:"fingerprint"`
	PaymentBytes   []byte    `json:"payment_bytes"`
	TicketsCreated uint32    `json:"tickets_created"`
	ExpectedValue  []byte    `json:"expected_value,omitempty"`
	FundedValueWei []byte    `json:"funded_value_wei,omitempty"`
	QuoteRefJSON   []byte    `json:"quote_ref_json,omitempty"`
	WorkID         string    `json:"work_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type mintTombstone struct {
	Fingerprint []byte    `json:"fingerprint"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	// Completed distinguishes "the response aged out of retention" from
	// "the response never landed". The first is an expired replay; the
	// second is a crash between signing and recording, and they must not
	// be answered the same way.
	Completed bool `json:"completed,omitempty"`
}

// MintKey is the storage key: the mint id scoped to the sender identity
// that would sign for it. A daemon serves one sender, but keying on both
// means a keystore change cannot collide with ids issued to the old one.
func MintKey(sender []byte, mintRequestID string) []byte {
	h := sha256.New()
	h.Write(sender)
	h.Write([]byte{0})
	h.Write([]byte(mintRequestID))
	return h.Sum(nil)
}

// MintReserve claims a mint id before anything is signed, and reports
// what was already known about it:
//
//	(nil, nil)                — reserved by this call; go mint
//	(record, nil)             — completed before; replay it verbatim
//	(nil, ErrMintIncomplete)  — reserved but never completed; refuse
//	(nil, ErrMintExpired)     — completed, replay record evicted; refuse
//	(nil, ErrMintFingerprintMismatch) — seen with other content; refuse
//
// Reserving BEFORE signing is what closes the crash window: a daemon
// that dies after signing leaves a reservation, so the retry refuses
// instead of minting a second batch against the same intent.
func (s *Store) MintReserve(sender []byte, mintRequestID string, fingerprint []byte) (*MintRecord, error) {
	key := MintKey(sender, mintRequestID)
	var out *MintRecord
	err := s.db.Update(func(tx *bolt.Tx) error {
		tb, err := tx.CreateBucketIfNotExists([]byte(tombstonesBucket))
		if err != nil {
			return err
		}
		if rawTomb := tb.Get(key); rawTomb != nil {
			var tomb mintTombstone
			if err := json.Unmarshal(rawTomb, &tomb); err != nil {
				return err
			}
			if !bytes.Equal(tomb.Fingerprint, fingerprint) {
				return ErrMintFingerprintMismatch
			}
			mb := tx.Bucket([]byte(mintsBucket))
			if mb == nil {
				return ErrMintExpired
			}
			raw := mb.Get(key)
			if raw == nil {
				// Tombstoned with no payload: either the response aged
				// out, or it never landed. Completed tells them apart.
				if tomb.Completed {
					return ErrMintExpired
				}
				return ErrMintIncomplete
			}
			var rec MintRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			out = &rec
			return nil
		}
		// First sight: stake the claim before a signature exists.
		tomb, err := json.Marshal(&mintTombstone{
			Fingerprint: bytes.Clone(fingerprint),
			FirstSeenAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		return tb.Put(key, tomb)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MintRecall reports what the daemon already knows about a mint id:
//
//	(record, nil)             — replay this response verbatim
//	(nil, nil)                — never seen; mint it
//	(nil, ErrMintExpired)     — seen, replay record evicted; refuse
//	(nil, ErrMintFingerprintMismatch) — seen with other content; refuse
func (s *Store) MintRecall(sender []byte, mintRequestID string, fingerprint []byte) (*MintRecord, error) {
	key := MintKey(sender, mintRequestID)
	var out *MintRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		tb := tx.Bucket([]byte(tombstonesBucket))
		if tb == nil {
			return nil
		}
		rawTomb := tb.Get(key)
		if rawTomb == nil {
			return nil // never seen
		}
		var tomb mintTombstone
		if err := json.Unmarshal(rawTomb, &tomb); err != nil {
			return err
		}
		if !bytes.Equal(tomb.Fingerprint, fingerprint) {
			return ErrMintFingerprintMismatch
		}
		mb := tx.Bucket([]byte(mintsBucket))
		if mb == nil {
			return ErrMintExpired
		}
		raw := mb.Get(key)
		if raw == nil {
			return ErrMintExpired
		}
		var rec MintRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		out = &rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MintRecord stores the response for a mint id and its permanent
// tombstone in one transaction. Both or neither: a payload without a
// tombstone could be re-minted after eviction, and that is the case this
// exists to prevent.
func (s *Store) MintRecord(sender []byte, mintRequestID string, rec MintRecord) error {
	key := MintKey(sender, mintRequestID)
	rec.CreatedAt = time.Now().UTC()
	payload, err := json.Marshal(&rec)
	if err != nil {
		return err
	}
	tomb, err := json.Marshal(&mintTombstone{
		Fingerprint: bytes.Clone(rec.Fingerprint),
		FirstSeenAt: rec.CreatedAt,
		Completed:   true,
	})
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		tb, e := tx.CreateBucketIfNotExists([]byte(tombstonesBucket))
		if e != nil {
			return e
		}
		if e := tb.Put(key, tomb); e != nil {
			return e
		}
		mb, e := tx.CreateBucketIfNotExists([]byte(mintsBucket))
		if e != nil {
			return e
		}
		return mb.Put(key, payload)
	})
}

// EvictMints drops recorded responses older than cutoff. Tombstones are
// never evicted — see the package comment: an evicted key must refuse,
// not re-mint.
func (s *Store) EvictMints(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mintsBucket))
		if b == nil {
			return nil
		}
		var evict [][]byte
		if err := b.ForEach(func(k, raw []byte) error {
			var rec MintRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			if rec.CreatedAt.Before(cutoff) {
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
