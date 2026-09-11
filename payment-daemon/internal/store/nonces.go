package store

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"

	bolt "go.etcd.io/bbolt"
)

// MaxSenderNonces is the per-recipientRand cap on tracked nonces.
// Beyond this the receiver should re-quote with a fresh
// recipientRandHash; senders cannot replay across the cap because the
// receiver's secret rand changes per session.
const MaxSenderNonces = 600

// ErrNonceAlreadySeen indicates the (recipientRand, senderNonce) tuple
// was already recorded.
var ErrNonceAlreadySeen = errors.New("nonce already seen for this recipientRand")

// ErrTooManyNonces indicates the per-recipientRand nonce cap is
// reached. The receiver should re-quote.
var ErrTooManyNonces = errors.New("too many nonces for this recipientRand")

// NonceSeen reports whether (recipientRand, nonce) has been recorded.
func (s *Store) NonceSeen(recipientRand *big.Int, nonce uint32) (bool, error) {
	var seen bool
	err := s.db.View(func(tx *bolt.Tx) error {
		seen = tx.Bucket([]byte(noncesBucket)).Get(nonceKey(recipientRand, nonce)) != nil
		return nil
	})
	return seen, err
}

// RecordNonce inserts a presence marker for (recipientRand, nonce) iff
// the per-rand count is below MaxSenderNonces and the tuple is not
// already present. Returns ErrNonceAlreadySeen / ErrTooManyNonces in
// those cases.
func (s *Store) RecordNonce(recipientRand *big.Int, nonce uint32) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(noncesBucket))
		key := nonceKey(recipientRand, nonce)
		if v := bucket.Get(key); v != nil {
			return ErrNonceAlreadySeen
		}
		// Count the existing entries under this rand prefix.
		prefix := append(randHex(recipientRand), 0x00)
		count := 0
		c := bucket.Cursor()
		for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
			count++
			if count >= MaxSenderNonces {
				return ErrTooManyNonces
			}
		}
		return bucket.Put(key, []byte{1})
	})
}

// NonceCount reports how many nonces have been recorded under a
// recipient rand. It is the session's consumed budget: once it reaches
// MaxSenderNonces the payee refuses further tickets on this rand, so
// both sides need to be able to ask before one of them signs something
// the other will reject.
func (s *Store) NonceCount(recipientRand *big.Int) (int, error) {
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(noncesBucket))
		if bucket == nil {
			return nil
		}
		prefix := append(randHex(recipientRand), 0x00)
		c := bucket.Cursor()
		for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
			count++
		}
		return nil
	})
	return count, err
}

func nonceKey(recipientRand *big.Int, nonce uint32) []byte {
	prefix := append(randHex(recipientRand), 0x00)
	out := make([]byte, len(prefix)+4)
	copy(out, prefix)
	binary.BigEndian.PutUint32(out[len(prefix):], nonce)
	return out
}

func randHex(recipientRand *big.Int) []byte {
	if recipientRand == nil {
		return nil
	}
	return []byte(hex.EncodeToString(recipientRand.Bytes()))
}

func deleteNonceLedger(bucket *bolt.Bucket, recipientRand *big.Int) error {
	if bucket == nil || recipientRand == nil {
		return nil
	}
	prefix := append(randHex(recipientRand), 0x00)
	var keys [][]byte
	c := bucket.Cursor()
	for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
		keys = append(keys, append([]byte(nil), k...))
	}
	for _, k := range keys {
		if err := bucket.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// HighestSenderNonce returns the largest nonce recorded under a
// recipient rand, and false when none are.
//
// The nonce is the low four bytes of the key, big-endian, so the last
// key under the rand's prefix carries the maximum. Used to tell a sender
// that REWOUND — its nonce stream restarted below what this payee has
// already seen, which is what partial payer state loss looks like from
// here — apart from an ordinary duplicate delivery, which replays a
// nonce at the top of the stream rather than the bottom.
func (s *Store) HighestSenderNonce(recipientRand *big.Int) (uint32, bool, error) {
	var (
		highest uint32
		found   bool
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(noncesBucket))
		if bucket == nil {
			return nil
		}
		prefix := append(randHex(recipientRand), 0x00)
		c := bucket.Cursor()
		for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
			if len(k) < len(prefix)+4 {
				continue
			}
			n := binary.BigEndian.Uint32(k[len(prefix):])
			if !found || n > highest {
				highest, found = n, true
			}
		}
		return nil
	})
	return highest, found, err
}

// FillNonceLedger records a run of nonces under a rand without the
// per-call cap check, so a test can arrange a ledger that is already at
// capacity. Not for production use: the cap in RecordNonce is the point.
func (s *Store) FillNonceLedger(recipientRand *big.Int, from, count uint32) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(noncesBucket))
		for i := uint32(0); i < count; i++ {
			if err := bucket.Put(nonceKey(recipientRand, from+i), []byte{1}); err != nil {
				return err
			}
		}
		return nil
	})
}
