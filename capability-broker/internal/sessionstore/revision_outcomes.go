package sessionstore

import (
	"bytes"
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

// RevisionDecision is safe to expose and log. Reasons and stages are assigned
// by the engine from a bounded vocabulary, never copied from error text.
type RevisionDecision struct {
	RequestedReservationWei string    `json:"requested_reservation_wei,omitempty"`
	ReservationWei          string    `json:"reservation_wei,omitempty"`
	BilledWei               string    `json:"receiver_billed_wei,omitempty"`
	ActualUnits             uint64    `json:"receiver_actual_units,omitempty"`
	MaxUnits                uint64    `json:"max_total_units,omitempty"`
	MaxDebitWei             string    `json:"max_debit_wei,omitempty"`
	Outcome                 string    `json:"outcome"`
	Reason                  string    `json:"reason"`
	Stage                   string    `json:"stage"`
	ReceiverCode            string    `json:"receiver_code,omitempty"`
	SessionID               string    `json:"session_id"`
	GatewaySessionID        string    `json:"gateway_session_id"`
	RequestID               string    `json:"request_id"`
	AuthorizationID         string    `json:"authorization_id"`
	PredecessorID           string    `json:"predecessor_authorization_id"`
	Revision                uint64    `json:"revision"`
	ObservedAt              time.Time `json:"observed_at"`
	NextRetryAt             time.Time `json:"next_retry_at,omitzero"`
}

// Request coverage is deliberately retained after outcome/session eviction. A
// revision is never safe to treat as an unknown initial exchange.
const revisionRequestsBucket = "revision_requests"
const revisionCoverageKey = "revision_coverage_started_at"

func (s *Store) BeginRevision(id string, intent *RevisionIntent) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(nonAdmissionBucket)); b != nil && b.Get([]byte(intent.RequestID)) != nil {
			return ErrNonAdmissionIssued
		}
		b := tx.Bucket([]byte(sessionsBucket))
		raw := b.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		r, err := s.unseal(raw)
		if err != nil {
			return err
		}
		if r.RevisionIntent != nil {
			return ErrExists
		}
		index, err := tx.CreateBucketIfNotExists([]byte(revisionRequestsBucket))
		if err != nil {
			return err
		}
		if index.Get([]byte(intent.RequestID)) != nil {
			return ErrRequestIDReuse
		}
		if err := index.Put([]byte(intent.RequestID), []byte(id)); err != nil {
			return err
		}
		r.RevisionIntent, r.LastRevision = intent, intent.Decision
		r.UpdatedAt = time.Now().UTC()
		encoded, err := s.seal(r)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), encoded)
	})
}

// Backfill old intents/outcomes before serving requests. No signed assertion can
// race this migration; Open has not returned the store to the server yet.
func (s *Store) migrateRevisionCoverage() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		coverage, err := tx.CreateBucketIfNotExists([]byte(coverageBucket))
		if err != nil {
			return err
		}
		if coverage.Get([]byte(revisionCoverageKey)) == nil {
			if err := coverage.Put([]byte(revisionCoverageKey), []byte(time.Now().UTC().Format(time.RFC3339Nano))); err != nil {
				return err
			}
		}
		index, err := tx.CreateBucketIfNotExists([]byte(revisionRequestsBucket))
		if err != nil {
			return err
		}
		if b := tx.Bucket([]byte(topupsBucket)); b != nil {
			if err := b.ForEach(func(_, raw []byte) error {
				var r TopUpRecord
				if err := json.Unmarshal(raw, &r); err != nil {
					return err
				}
				return index.Put([]byte(r.RequestID), []byte(r.SessionID))
			}); err != nil {
				return err
			}
		}
		return tx.Bucket([]byte(sessionsBucket)).ForEach(func(_, raw []byte) error {
			var r Record
			if err := json.Unmarshal(raw, &r); err != nil {
				return err
			}
			if len(r.RevisionIntentSealed) == 0 {
				return nil
			}
			decoded, err := s.unseal(raw)
			if err != nil {
				return err
			}
			r = *decoded
			if r.RevisionIntent != nil {
				return index.Put([]byte(r.RevisionIntent.RequestID), []byte(r.SessionID))
			}
			return nil
		})
	})
}

// Before the upgrade, evicted refill IDs had no tombstones. Absence for older
// session requests cannot support a generic non-admission assertion.
func (s *Store) RevisionCoverageStartedAt() (time.Time, error) {
	var at time.Time
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		at, err = time.Parse(time.RFC3339Nano, string(tx.Bucket([]byte(coverageBucket)).Get([]byte(revisionCoverageKey))))
		return err
	})
	return at, err
}

func (s *Store) RevisionRequestSession(requestID string) (string, error) {
	var id string
	err := s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(revisionRequestsBucket)); b != nil {
			id = string(b.Get([]byte(requestID)))
		}
		return nil
	})
	return id, err
}

func (s *Store) GetRevision(id, requestID string) (*TopUpRecord, error) {
	var result TopUpRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(topupsBucket))
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get(topupKey(id, requestID))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, &result)
	})
	return &result, err
}

// Cache only a signature over the exact committed immutable evidence. Concurrent
// queries return the winner verbatim, including across signer rotation/restart.
func (s *Store) RecordRevisionEnvelope(id, requestID string, evidence []byte, envelope string) (string, error) {
	var result string
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(topupsBucket))
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get(topupKey(id, requestID))
		if raw == nil {
			return ErrNotFound
		}
		var r TopUpRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if len(evidence) == 0 || !bytes.Equal(r.RevisionEvidence, evidence) {
			return ErrRequestIDReuse
		}
		if r.RevisionEnvelope == "" {
			r.RevisionEnvelope = envelope
		}
		result = r.RevisionEnvelope
		encoded, err := json.Marshal(&r)
		if err != nil {
			return err
		}
		return b.Put(topupKey(id, requestID), encoded)
	})
	return result, err
}
