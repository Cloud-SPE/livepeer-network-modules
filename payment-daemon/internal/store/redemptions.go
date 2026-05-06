package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"

	bolt "go.etcd.io/bbolt"
)

// SignedTicket is the storage form of a winning ticket awaiting
// redemption. JSON-encoded under the redemptions_pending bucket.
type SignedTicket struct {
	Recipient         []byte   `json:"recipient"`
	Sender            []byte   `json:"sender"`
	FaceValue         *big.Int `json:"face_value"`
	WinProb           *big.Int `json:"win_prob"`
	SenderNonce       uint32   `json:"sender_nonce"`
	RecipientRandHash []byte   `json:"recipient_rand_hash"`
	CreationRound     int64    `json:"creation_round"`
	CreationRoundHash []byte   `json:"creation_round_hash"`
	Sig               []byte   `json:"sig"`
	RecipientRand     *big.Int `json:"recipient_rand"`
}

// PendingRedemption pairs a SignedTicket with its on-disk sequence
// number for ordered iteration / dedup.
type PendingRedemption struct {
	Seq    uint64
	Ticket *SignedTicket
	Hash   []byte
}

// EnqueueRedemption inserts a winning ticket into the FIFO redemption
// queue, idempotently. Returns (true, nil) if the ticket was enqueued;
// (false, nil) if it was already pending or already redeemed.
func (s *Store) EnqueueRedemption(ticketHash []byte, t *SignedTicket) (bool, error) {
	if len(ticketHash) != 32 {
		return false, fmt.Errorf("ticketHash must be 32 bytes, got %d", len(ticketHash))
	}
	if t == nil {
		return false, fmt.Errorf("nil ticket")
	}
	encoded, err := json.Marshal(t)
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	enqueued := false
	err = s.db.Update(func(tx *bolt.Tx) error {
		byHash := tx.Bucket([]byte(redemptionsByHash))
		redeemed := tx.Bucket([]byte(redemptionsRedeemed))
		pending := tx.Bucket([]byte(redemptionsPending))
		meta := tx.Bucket([]byte(redemptionsMeta))

		if v := byHash.Get(ticketHash); len(v) > 0 {
			return nil
		}
		if v := redeemed.Get(ticketHash); len(v) > 0 {
			return nil
		}
		seq := readSeq(meta.Get([]byte(metaNextSeq))) + 1
		if err := meta.Put([]byte(metaNextSeq), seqBytes(seq)); err != nil {
			return err
		}
		if err := pending.Put(seqBytes(seq), encoded); err != nil {
			return err
		}
		if err := byHash.Put(ticketHash, seqBytes(seq)); err != nil {
			return err
		}
		enqueued = true
		return nil
	})
	return enqueued, err
}

// PendingRedemptions returns the queued tickets in seq order (oldest
// first).
func (s *Store) PendingRedemptions() ([]PendingRedemption, error) {
	var out []PendingRedemption
	err := s.db.View(func(tx *bolt.Tx) error {
		pending := tx.Bucket([]byte(redemptionsPending))
		byHash := tx.Bucket([]byte(redemptionsByHash))
		// Reverse-lookup map seq -> ticketHash via an O(N) scan of byHash.
		seqToHash := map[uint64][]byte{}
		_ = byHash.ForEach(func(k, v []byte) error {
			seqToHash[readSeq(v)] = append([]byte(nil), k...)
			return nil
		})
		return pending.ForEach(func(k, v []byte) error {
			seq := readSeq(k)
			var t SignedTicket
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("decode pending seq=%d: %w", seq, err)
			}
			out = append(out, PendingRedemption{Seq: seq, Ticket: &t, Hash: seqToHash[seq]})
			return nil
		})
	})
	return out, err
}

// MarkRedeemed removes a queued ticket and records its on-chain tx
// hash (or all-zero for "drained locally without on-chain redemption").
func (s *Store) MarkRedeemed(ticketHash, txHash []byte) error {
	if len(ticketHash) != 32 {
		return fmt.Errorf("ticketHash must be 32 bytes, got %d", len(ticketHash))
	}
	stamped := make([]byte, 32)
	if len(txHash) > 0 {
		copy(stamped, txHash[:min(32, len(txHash))])
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		byHash := tx.Bucket([]byte(redemptionsByHash))
		pending := tx.Bucket([]byte(redemptionsPending))
		redeemed := tx.Bucket([]byte(redemptionsRedeemed))
		if seq := byHash.Get(ticketHash); len(seq) > 0 {
			if err := pending.Delete(seq); err != nil {
				return err
			}
			if err := byHash.Delete(ticketHash); err != nil {
				return err
			}
		}
		return redeemed.Put(ticketHash, stamped)
	})
}

// RedeemedTxHash returns the recorded tx hash for a ticket-hash, if
// present. The "all-zero" sentinel means "drained locally". Returns
// (nil, nil) if the ticket was never marked.
func (s *Store) RedeemedTxHash(ticketHash []byte) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(redemptionsRedeemed)).Get(ticketHash)
		if v == nil {
			return nil
		}
		out = append([]byte(nil), v...)
		return nil
	})
	return out, err
}

func seqBytes(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return append([]byte(nil), b[:]...)
}

func readSeq(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
