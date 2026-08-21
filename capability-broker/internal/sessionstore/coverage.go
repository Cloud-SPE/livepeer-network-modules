package sessionstore

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

const coverageBucket = "coverage"
const coverageStartKey = "started_at"

// CoverageStartedAt returns the earliest moment this store's records are
// continuous through, stamping it on first use.
//
// It is the honesty check behind a non-admission claim. "I have no
// record of this exchange" is only evidence if the absence means
// something, and it means nothing across a store that was reset,
// restored from a backup, or reinitialized — there, absence is
// indistinguishable from forgetting.
//
// Because the value lives IN the store, a fresh or wiped store has no
// key and stamps a new, later time. A broker that loses its state
// therefore disqualifies itself automatically for every job older than
// the gap, rather than having to remember to.
func (s *Store) CoverageStartedAt() (time.Time, error) {
	var out time.Time
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(coverageBucket))
		if err != nil {
			return err
		}
		if raw := b.Get([]byte(coverageStartKey)); raw != nil {
			t, perr := time.Parse(time.RFC3339Nano, string(raw))
			if perr == nil {
				out = t
				return nil
			}
			// An unparseable stamp is a corrupt one, and treating it as
			// absent would silently re-stamp coverage to now — which is
			// the conservative direction, so do exactly that, loudly by
			// virtue of the value moving.
		}
		now := time.Now().UTC()
		out = now
		return b.Put([]byte(coverageStartKey), []byte(now.Format(time.RFC3339Nano)))
	})
	if err != nil {
		return time.Time{}, err
	}
	return out, nil
}

// HasJobRecord reports whether ANY record exists for the request id —
// in flight, terminal, or pending accounting.
//
// Any of them refutes non-admission. A broker that attested while an
// exchange was still running would be signing a statement its own next
// second could contradict.
func (s *Store) HasJobRecord(requestID string) (bool, string, error) {
	var found bool
	var state string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(jobsBucket))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(requestID))
		if raw == nil {
			return nil
		}
		found = true
		var rec JobRecord
		if err := json.Unmarshal(raw, &rec); err == nil {
			state = rec.State
		}
		return nil
	})
	return found, state, err
}

const nonAdmissionBucket = "non_admissions"

// RecordNonAdmission durably stores a signed non-admission record for a
// request id, and refuses if any job record exists.
//
// The check and the write are ONE transaction on purpose. Checking then
// signing leaves a window in which an exchange is admitted between the
// two, and the broker emits a signed statement that it never admitted
// something it is at that moment running. Bolt's writer serialization
// closes it: JobBegin and this cannot interleave.
//
// It also refuses if a non-admission already exists, returning the
// original. The record is evidence; re-issuing it under a later
// observed_at would produce two different signed statements about the
// same fact, and a consumer holding both has no way to know they agree.
func (s *Store) RecordNonAdmission(requestID, envelope string, observedAt time.Time) (existing string, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		jobs := tx.Bucket([]byte(jobsBucket))
		if jobs != nil && jobs.Get([]byte(requestID)) != nil {
			return ErrExists
		}
		b, err := tx.CreateBucketIfNotExists([]byte(nonAdmissionBucket))
		if err != nil {
			return err
		}
		if prior := b.Get([]byte(requestID)); prior != nil {
			existing = string(prior)
			return nil
		}
		return b.Put([]byte(requestID), []byte(envelope))
	})
	return existing, err
}

// NonAdmissionFor returns a previously issued record, if any.
func (s *Store) NonAdmissionFor(requestID string) (string, bool, error) {
	var out string
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(nonAdmissionBucket))
		if b == nil {
			return nil
		}
		if raw := b.Get([]byte(requestID)); raw != nil {
			out = string(raw)
			found = true
		}
		return nil
	})
	return out, found, err
}

// HasNonAdmission reports whether a non-admission record was issued for
// a request id. Consulted before ADMITTING one, so a broker cannot end
// up having signed both.
func (s *Store) HasNonAdmission(requestID string) (bool, error) {
	_, found, err := s.NonAdmissionFor(requestID)
	return found, err
}
