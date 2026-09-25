package sessionstore

import (
	"bytes"
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Top-ups bucket — paid-session/v1 §3.3 idempotency. One record per
// (session_id, Livepeer-Request-Id), written with the outcome the
// caller was told, so a retry after a lost response replays that answer
// instead of funding the session twice.
//
// Authorization revisions persist a sealed in-flight intent on the session,
// then atomically commit authority and this replay response.

const topupsBucket = "topups"

// TopUpRecord is the recorded outcome of one top-up.
type TopUpRecord struct {
	RequestID   string `json:"request_id"`
	SessionID   string `json:"session_id"`
	Fingerprint []byte `json:"fingerprint"`
	// LeaseExpiresAt and BalanceWei are the response the caller got.
	// Replayed verbatim: converging on the recorded outcome means the
	// original answer, not a fresh reading of state that has moved on.
	LeaseExpiresAt   time.Time         `json:"lease_expires_at"`
	BalanceWei       string            `json:"balance_wei"`
	CreatedAt        time.Time         `json:"created_at"`
	ErrorCode        string            `json:"error_code,omitempty"`
	ErrorDetail      string            `json:"error_detail,omitempty"`
	Decision         *RevisionDecision `json:"decision,omitempty"`
	RevisionEvidence []byte            `json:"revision_evidence,omitempty"`
	RevisionEnvelope string            `json:"revision_envelope,omitempty"`
}

func topupKey(sessionID, requestID string) []byte {
	return []byte(sessionID + "\x00" + requestID)
}

// TopUpRecall returns the recorded outcome for (sessionID, requestID),
// or nil when this request id has not been seen. A recorded id whose
// content differs returns ErrRequestIDReuse.
func (s *Store) TopUpRecall(sessionID, requestID string, fingerprint []byte) (*TopUpRecord, error) {
	var out *TopUpRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(topupsBucket))
		if b == nil {
			return nil
		}
		raw := b.Get(topupKey(sessionID, requestID))
		if raw == nil {
			return nil
		}
		var rec TopUpRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		if !bytes.Equal(rec.Fingerprint, fingerprint) {
			return ErrRequestIDReuse
		}
		out = &rec
		return nil
	})
	return out, err
}

// TopUpRecord records the outcome a caller was given for a request id.
func (s *Store) TopUpRecord(sessionID, requestID string, fingerprint []byte,
	lease time.Time, balanceWei string) error {
	rec := TopUpRecord{
		RequestID:      requestID,
		SessionID:      sessionID,
		Fingerprint:    bytes.Clone(fingerprint),
		LeaseExpiresAt: lease,
		BalanceWei:     balanceWei,
		CreatedAt:      time.Now().UTC(),
	}
	raw, err := json.Marshal(&rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte(topupsBucket))
		if e != nil {
			return e
		}
		return b.Put(topupKey(sessionID, requestID), raw)
	})
}

// EvictTopUps removes top-up records created before cutoff, returning
// how many were removed. Records outlive their session deliberately: a
// retry that arrives after the session ended must still replay its
// answer rather than be treated as a first attempt.
func (s *Store) EvictTopUps(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(topupsBucket))
		if b == nil {
			return nil
		}
		var evict [][]byte
		if err := b.ForEach(func(k, raw []byte) error {
			var rec TopUpRecord
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
