package sessionstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"time"
)

// RevisionIntent is sealed with the session record before receiver admission.
// The exact financial request and promised lease survive a lost response.
type RevisionIntent struct {
	RequestID          string    `json:"request_id"`
	AuthorizationBytes []byte    `json:"authorization_bytes"`
	PaymentBytes       []byte    `json:"payment_bytes"`
	Fingerprint        []byte    `json:"fingerprint"`
	ReservationWei     string    `json:"reservation_wei"`
	LeaseExpiresAt     time.Time `json:"lease_expires_at"`
}

// CommitRevision changes authority and records its idempotent response in one
// transaction. There is no crash interval between those two facts.
func (s *Store) CommitRevision(id, requestID string, fingerprint []byte, lease time.Time, balance string, mutate func(*Record) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(sessionsBucket))
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		rec, err := s.unseal(raw)
		if err != nil {
			return err
		}
		if rec.RevisionIntent == nil || rec.RevisionIntent.RequestID != requestID || !bytes.Equal(rec.RevisionIntent.Fingerprint, fingerprint) {
			return fmt.Errorf("revision intent changed or missing")
		}
		if err = mutate(rec); err != nil {
			return err
		}
		rec.RevisionIntent = nil
		rec.UpdatedAt = time.Now().UTC()
		encoded, err := s.seal(rec)
		if err != nil {
			return err
		}
		if err = bucket.Put([]byte(id), encoded); err != nil {
			return err
		}
		response := TopUpRecord{RequestID: requestID, SessionID: id, Fingerprint: bytes.Clone(fingerprint), LeaseExpiresAt: lease, BalanceWei: balance, CreatedAt: rec.UpdatedAt}
		encoded, err = json.Marshal(response)
		if err != nil {
			return err
		}
		topups, err := tx.CreateBucketIfNotExists([]byte(topupsBucket))
		if err != nil {
			return err
		}
		key := topupKey(id, requestID)
		if topups.Get(key) != nil {
			return ErrRequestIDReuse
		}
		return topups.Put(key, encoded)
	})
}
