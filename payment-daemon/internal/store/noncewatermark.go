package store

import (
	"encoding/binary"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

const senderNoncesBucket = "sender_nonces"

// NextSenderNonce allocates the next ticket nonce for a work_id,
// durably, and returns it.
//
// Durable because the RECEIVER's nonce ledger is durable. A sender that
// kept its nonce only in memory restarted at 1 and replayed nonces the
// receiver had already consumed, so every ticket it minted until the
// stream caught up was rejected as a replay and credited NOTHING —
// while the broker went on serving work out of balance credited before
// the restart. The gateway sees success until the old balance runs out,
// then starts being refused, with nothing anywhere saying its payments
// had been worthless since the restart.
//
// paid-session §"Nonce-replay window" states the obligation directly: a
// sender that restarts while the receiver still holds its session MUST
// resume from the last-used nonce. Allocating from the store rather than
// restoring into memory means there is no resume step to forget.
//
// The increment is committed BEFORE the caller signs. A crash between
// allocation and signing burns a nonce, which costs nothing — the
// receiver tolerates gaps. A crash the other way round would reissue
// one, which is the failure this exists to prevent.
func (s *Store) NextSenderNonce(workID string) (uint32, error) {
	if workID == "" {
		return 0, fmt.Errorf("store: empty work_id")
	}
	var next uint32
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(senderNoncesBucket))
		if err != nil {
			return err
		}
		var current uint32
		if raw := b.Get([]byte(workID)); len(raw) == 4 {
			current = binary.BigEndian.Uint32(raw)
		}
		if current == ^uint32(0) {
			return fmt.Errorf("store: nonce space exhausted for work_id %s", workID)
		}
		next = current + 1
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], next)
		return b.Put([]byte(workID), buf[:])
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// SenderNoncesUsed reports the highest nonce allocated for a work_id, so
// a caller can tell how much of the receiver's per-rand budget this
// session has already spent WITHOUT spending another.
//
// Read-only on purpose: NextSenderNonce commits an increment, so asking
// it "how many are left" would consume one to find out. The watermark is
// durable, so this survives a restart at 599 or 600 — which is exactly
// where the answer matters.
func (s *Store) SenderNoncesUsed(workID string) (uint32, error) {
	if workID == "" {
		return 0, fmt.Errorf("store: empty work_id")
	}
	var used uint32
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(senderNoncesBucket))
		if b == nil {
			return nil
		}
		if raw := b.Get([]byte(workID)); len(raw) == 4 {
			used = binary.BigEndian.Uint32(raw)
		}
		return nil
	})
	return used, err
}

// ForgetSenderNonces clears the durable watermark for a work_id.
//
// Exists for tests that need to simulate PARTIAL payer state loss — a
// restored backup, a wiped volume — which is the case that puts the
// payer's estimate out of step with the payee's ledger. Production code
// must not call it: forgetting the watermark is the failure the
// watermark exists to prevent.
func (s *Store) ForgetSenderNonces(workID string) error {
	if workID == "" {
		return fmt.Errorf("store: empty work_id")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(senderNoncesBucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(workID))
	})
}
