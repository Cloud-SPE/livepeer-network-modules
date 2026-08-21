package sessionstore

import (
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
)

// Debit sequence allocation.
//
// A debit is deduplicated by the payee on (sender, work_id, debit_seq).
// That is the right key — it is what makes retrying a debit safe — but
// it means the SEQ SPACE BELONGS TO THE work_id, not to a request or a
// session. Anything that derives a seq from per-request state hands out
// the same number twice.
//
// It did. A unary job runs no interim ticker, so every exchange debited
// at seq 1, and a gateway reusing one ticket session had every job after
// the first silently deduplicated away — billed nothing, logged success.
// Sessions had the same flaw across two sessions sharing a work_id.
//
// So the counter lives here, keyed by work_id, durable, monotonic.

const debitSeqBucket = "debit_seq"

// NextDebitSeq allocates the next debit sequence for a work_id.
//
// Allocation is durable before the caller debits, so a crash between
// allocating and debiting burns a number rather than reusing one — a
// gap in the sequence costs nothing, while a repeat costs a debit. A
// caller that must be able to RETRY a specific debit has to persist the
// number it was given and re-present that, rather than allocating again.
func (s *Store) NextDebitSeq(workID string) (uint64, error) {
	var out uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(debitSeqBucket))
		if err != nil {
			return err
		}
		key := []byte(workID)
		next := uint64(1)
		if raw := b.Get(key); len(raw) == 8 {
			next = binary.BigEndian.Uint64(raw) + 1
		}
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], next)
		if err := b.Put(key, buf[:]); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}
