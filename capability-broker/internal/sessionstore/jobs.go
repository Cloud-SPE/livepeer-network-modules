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
	// JobAccountingPending: the work was delivered and the exchange is
	// over, but its debit has not landed and is being retried. It is
	// deliberately NOT terminal — a terminal record asserts the
	// accounting is settled, and reporting that while a debit is still
	// outstanding is the failure this state exists to avoid.
	JobAccountingPending = "accounting_pending"
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
	// Pending is set while State is JobAccountingPending: everything the
	// retrier needs to finish the debit and then build the settlement
	// the exchange should have had.
	Pending *PendingDebit `json:"pending,omitempty"`
}

// PendingDebit is a debit that was attempted and did not land, plus the
// inputs needed to build the settlement once it does.
//
// Retrying is safe because a debit is idempotent by
// (sender, work_id, debit_seq): a retry of an attempt that actually
// succeeded but lost its response returns the original debit rather than
// charging twice. That property is what makes durable retry the right
// answer here instead of a compensating write.
type PendingDebit struct {
	Sender   []byte `json:"sender"`
	WorkID   string `json:"work_id"`
	DebitSeq uint64 `json:"debit_seq"`
	// Units is the amount THIS debit is for — the final flush, which on
	// a long exchange is less than the exchange's total.
	Units uint64 `json:"units"`
	// DebitedUnits is what already landed before this attempt: interim
	// ticks that succeeded. They took real value and the settlement must
	// not disown them if the retry never lands.
	DebitedUnits uint64 `json:"debited_units"`

	Attempts      int       `json:"attempts"`
	FirstFailedAt time.Time `json:"first_failed_at"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     string    `json:"last_error,omitempty"`

	// Settlement rebuild inputs. Held because the record can only be
	// built once the charge is known, and the charge is exactly what is
	// still outstanding.
	PaymentBytes      []byte `json:"payment_bytes,omitempty"`
	FundedValueWei    string `json:"funded_value_wei,omitempty"`
	ActualUnits       uint64 `json:"actual_units"`
	WorkUnitName      string `json:"work_unit_name,omitempty"`
	TerminationReason string `json:"termination_reason,omitempty"`
	JobID             string `json:"job_id,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	IssuedAt          string `json:"issued_at,omitempty"`
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

// JobFinishPendingAccounting records a delivered exchange whose debit
// did not land. The outcome (status, units, body digest) is terminal —
// the work happened and a replay must return it — but the record is not,
// because its accounting is still outstanding.
func (s *Store) JobFinishPendingAccounting(requestID string, status int, workUnits uint64,
	unit string, bodyDigest []byte, pending *PendingDebit) error {
	if pending == nil {
		return errors.New("sessionstore: nil pending debit")
	}
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
		rec.State = JobAccountingPending
		rec.Status = status
		rec.WorkUnits = workUnits
		rec.Unit = unit
		rec.BodyDigest = bytes.Clone(bodyDigest)
		rec.EndedAt = time.Now().UTC()
		rec.Pending = pending
		out, err := json.Marshal(&rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(requestID), out)
	})
}

// DuePendingDebits returns records whose debit retry is due at now.
// Copies are returned; the caller mutates through the store.
func (s *Store) DuePendingDebits(now time.Time, limit int) ([]*JobRecord, error) {
	var out []*JobRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(jobsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			if limit > 0 && len(out) >= limit {
				return nil
			}
			var rec JobRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return nil // a record we cannot read is not due
			}
			if rec.State != JobAccountingPending || rec.Pending == nil {
				return nil
			}
			if rec.Pending.NextAttemptAt.After(now) {
				return nil
			}
			cp := rec
			out = append(out, &cp)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordDebitRetryFailure bumps the attempt counter and schedules the
// next try. The record stays pending.
func (s *Store) RecordDebitRetryFailure(requestID string, nextAttempt time.Time, cause string) error {
	return s.mutateJob(requestID, func(rec *JobRecord) error {
		if rec.Pending == nil {
			return ErrNotFound
		}
		rec.Pending.Attempts++
		rec.Pending.NextAttemptAt = nextAttempt
		rec.Pending.LastError = cause
		if rec.Pending.FirstFailedAt.IsZero() {
			rec.Pending.FirstFailedAt = time.Now().UTC()
		}
		return nil
	})
}

// SettleJob moves a pending record to terminal with its settlement. Used
// both when a retry finally lands and when retries are exhausted — the
// difference is what the settlement says, which is the caller's to
// decide.
func (s *Store) SettleJob(requestID, settlement string) error {
	return s.mutateJob(requestID, func(rec *JobRecord) error {
		rec.State = JobTerminal
		rec.Settlement = settlement
		rec.Pending = nil
		if rec.EndedAt.IsZero() {
			rec.EndedAt = time.Now().UTC()
		}
		return nil
	})
}

func (s *Store) mutateJob(requestID string, fn func(*JobRecord) error) error {
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
		if err := fn(&rec); err != nil {
			return err
		}
		out, err := json.Marshal(&rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(requestID), out)
	})
}
