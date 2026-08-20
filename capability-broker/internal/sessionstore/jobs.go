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

const (
	jobsBucket = "jobs"
	// jobIDIndexBucket maps the broker-minted job id to the request id
	// that owns the record, so a settlement query can be keyed on the
	// id the broker handed back rather than on the caller's own.
	jobIDIndexBucket = "job_id_index"
)

// Job states.
const (
	JobInFlight = "in_flight"
	JobTerminal = "terminal"
)

// ErrRequestIDReuse reports a request-id replay whose content differs —
// request_id_reuse on the wire. Shared by both protocols: paid-job
// exchanges and paid-session top-ups answer a reused id the same way.
var ErrRequestIDReuse = errors.New("sessionstore: request id reused with different content")

// JobRecord is the durable idempotency record for one exchange.
type JobRecord struct {
	RequestID   string `json:"request_id"`
	JobID       string `json:"job_id"`
	Fingerprint []byte `json:"fingerprint"`
	// BodyDigest is sha256 of the request body, recorded when the
	// exchange finishes. The envelope fingerprint above is known before
	// the body has streamed; this is the half that can only be known
	// after, so it is compared on replay rather than at Begin.
	BodyDigest []byte `json:"body_digest,omitempty"`
	// Settlement is the encoded settlement envelope this exchange
	// produced. Persisted because a streamed job delivers its terminal
	// claim in an HTTP trailer, which Go can read and HTTPX, Fetch and
	// reqwest cannot — so the trailer cannot be the only channel. A
	// caller that missed it queries for this instead.
	Settlement string    `json:"settlement,omitempty"`
	State      string    `json:"state"`
	Status     int       `json:"status,omitempty"`
	WorkUnits  uint64    `json:"work_units,omitempty"`
	Unit       string    `json:"unit,omitempty"`
	Deadline   time.Time `json:"deadline"`
	CreatedAt  time.Time `json:"created_at"`
	EndedAt    time.Time `json:"ended_at,omitzero"`
}

// JobBegin records an in-flight exchange, or returns the existing
// record for the request id. created=false means the caller must
// consult the returned record: terminal → replay its outcome;
// in_flight → refuse job_in_flight (or, past Deadline, treat as a
// failed terminal). A fingerprint mismatch returns
// ErrRequestIDReuse.
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
				return ErrRequestIDReuse
			}
			rec = &existing
			return nil
		}
		if e := putJobIDIndex(tx, jobID, requestID); e != nil {
			return e
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

func putJobIDIndex(tx *bolt.Tx, jobID, requestID string) error {
	if jobID == "" {
		return nil
	}
	b, err := tx.CreateBucketIfNotExists([]byte(jobIDIndexBucket))
	if err != nil {
		return err
	}
	return b.Put([]byte(jobID), []byte(requestID))
}

// JobByID resolves a record by the broker-minted job id.
func (s *Store) JobByID(jobID string) (*JobRecord, error) {
	var out *JobRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(jobIDIndexBucket))
		if idx == nil {
			return ErrNotFound
		}
		requestID := idx.Get([]byte(jobID))
		if requestID == nil {
			return ErrNotFound
		}
		b := tx.Bucket([]byte(jobsBucket))
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get(requestID)
		if raw == nil {
			return ErrNotFound
		}
		var rec JobRecord
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

// JobFinish records the terminal outcome for the request id, including
// the digest of the body the exchange actually consumed and the
// settlement envelope a caller may have to query for.
func (s *Store) JobFinish(requestID string, status int, workUnits uint64, unit string, bodyDigest []byte, settlement string) error {
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
		rec.BodyDigest = bytes.Clone(bodyDigest)
		rec.Settlement = settlement
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
		idx := tx.Bucket([]byte(jobIDIndexBucket))
		for _, k := range evict {
			if raw := b.Get(k); raw != nil && idx != nil {
				var rec JobRecord
				if err := json.Unmarshal(raw, &rec); err == nil && rec.JobID != "" {
					// The index outliving its record would answer a
					// query with "unknown" instead of "expired".
					_ = idx.Delete([]byte(rec.JobID))
				}
			}
			if err := b.Delete(k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
