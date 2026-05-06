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
