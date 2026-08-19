package sessionstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Jobs bucket — paid-job/v1 §4 idempotency. One record per
// Livepeer-Request-Id: created in_flight before the backend is
// touched, finished with the terminal outcome, retained for the
// operator's idempotency window, then evicted.

const jobsBucket = "jobs"

// Job states.
const (
	JobInFlight = "in_flight"
	JobTerminal = "terminal"
)

// ErrJobFingerprintMismatch reports a request-id replay whose content
// differs — request_id_reuse on the wire.
var ErrJobFingerprintMismatch = errors.New("sessionstore: request id reused with different content")

// JobRecord is the durable idempotency record for one exchange.
type JobRecord struct {
	RequestID   string    `json:"request_id"`
	JobID       string    `json:"job_id"`
	Fingerprint []byte    `json:"fingerprint"`
	State       string    `json:"state"`
	Status      int       `json:"status,omitempty"`
	WorkUnits   uint64    `json:"work_units,omitempty"`
	Unit        string    `json:"unit,omitempty"`
	Deadline    time.Time `json:"deadline"`
	CreatedAt   time.Time `json:"created_at"`
	EndedAt     time.Time `json:"ended_at,omitzero"`
}

// JobBegin records an in-flight exchange, or returns the existing
// record for the request id. created=false means the caller must
// consult the returned record: terminal → replay its outcome;
// in_flight → refuse job_in_flight (or, past Deadline, treat as a
// failed terminal). A fingerprint mismatch returns
// ErrJobFingerprintMismatch.
func (s *Store) JobBegin(requestID string, fingerprint []byte, jobID string, deadline time.Time) (rec *JobRecord, created bool, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte(jobsBucket))
		if e != nil {
			return e
		}
		if raw := b.Get([]byte(requestID)); raw != nil {
			var existing JobRecord
			if e := json.Unmarshal(raw, &existing); e != nil {
				return e
			}
			if !bytes.Equal(existing.Fingerprint, fingerprint) {
				return ErrJobFingerprintMismatch
			}
			rec = &existing
			return nil
		}
		fresh := JobRecord{
			RequestID:   requestID,
			JobID:       jobID,
			Fingerprint: bytes.Clone(fingerprint),
			State:       JobInFlight,
			Deadline:    deadline,
			CreatedAt:   time.Now().UTC(),
		}
		raw, e := json.Marshal(&fresh)
		if e != nil {
			return e
		}
		created = true
		rec = &fresh
		return b.Put([]byte(requestID), raw)
	})
	return rec, created, err
}

// JobFinish records the terminal outcome for the request id.
func (s *Store) JobFinish(requestID string, status int, workUnits uint64, unit string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(jobsBucket))
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get([]byte(requestID))
		if raw == nil {
			return ErrNotFound
		}
		var rec JobRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		rec.State = JobTerminal
		rec.Status = status
		rec.WorkUnits = workUnits
		rec.Unit = unit
		rec.EndedAt = time.Now().UTC()
		out, err := json.Marshal(&rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(requestID), out)
	})
}

// EvictJobs removes terminal job records that ended before cutoff and
// in-flight records whose deadline passed before cutoff (crash
// leftovers), returning how many were removed.
func (s *Store) EvictJobs(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(jobsBucket))
		if b == nil {
			return nil
		}
		var evict [][]byte
		if err := b.ForEach(func(k, raw []byte) error {
			var rec JobRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
			switch rec.State {
			case JobTerminal:
				if !rec.EndedAt.IsZero() && rec.EndedAt.Before(cutoff) {
					evict = append(evict, bytes.Clone(k))
				}
			case JobInFlight:
				if rec.Deadline.Before(cutoff) {
					evict = append(evict, bytes.Clone(k))
				}
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
