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
// nonAdmissionEntry stores the envelope with the time it was observed,
// because non-admission retention runs from observed_at rather than from
// an exchange that never happened.
type nonAdmissionEntry struct {
	Envelope   string    `json:"envelope"`
	ObservedAt time.Time `json:"observed_at"`
}

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
			var e nonAdmissionEntry
			if uerr := json.Unmarshal(prior, &e); uerr == nil {
				existing = e.Envelope
			}
			return nil
		}
		raw, merr := json.Marshal(nonAdmissionEntry{Envelope: envelope, ObservedAt: observedAt.UTC()})
		if merr != nil {
			return merr
		}
		return b.Put([]byte(requestID), raw)
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
			var e nonAdmissionEntry
			if uerr := json.Unmarshal(raw, &e); uerr != nil {
				return nil
			}
			out = e.Envelope
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

const admittedBucket = "admitted"
const evidenceHorizonKey = "evidence_horizon"

// admissionTombstone is the minimal fact that outlives a job record:
// this broker admitted this request.
//
// Written at admission and kept after the full record is evicted,
// because eviction otherwise creates FALSE evidence. A broker asked for
// non-admission after its record aged out would find nothing and sign
// NOT_ADMITTED for an exchange it had served — the coverage marker does
// not catch it, since coverage was continuous the whole time.
type admissionTombstone struct {
	JobID      string    `json:"job_id"`
	AdmittedAt time.Time `json:"admitted_at"`
}

// EvidenceHorizon is the earliest issuance time this broker can answer
// "was this admitted" for.
//
// It is the later of two things: when this store began (coverage), and
// how far back its admission tombstones still reach. Both are ways of
// not knowing, and the answer to a question before either is the same —
// refuse, rather than mistake an absence for a fact.
func (s *Store) EvidenceHorizon() (time.Time, error) {
	coverage, err := s.CoverageStartedAt()
	if err != nil {
		return time.Time{}, err
	}
	var pruned time.Time
	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(coverageBucket))
		if b == nil {
			return nil
		}
		if raw := b.Get([]byte(evidenceHorizonKey)); raw != nil {
			if t, perr := time.Parse(time.RFC3339Nano, string(raw)); perr == nil {
				pruned = t
			}
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if pruned.After(coverage) {
		return pruned, nil
	}
	return coverage, nil
}

// WasAdmitted reports whether this broker ever admitted the request,
// consulting the tombstone rather than the full record so the answer
// survives eviction.
func (s *Store) WasAdmitted(requestID string) (bool, string, error) {
	var found bool
	var jobID string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(admittedBucket))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(requestID))
		if raw == nil {
			return nil
		}
		found = true
		var t admissionTombstone
		if err := json.Unmarshal(raw, &t); err == nil {
			jobID = t.JobID
		}
		return nil
	})
	return found, jobID, err
}

// EvictAdmissionTombstones drops tombstones admitted before cutoff and
// advances the evidence horizon to match, so the broker stops claiming
// it can answer for a period it can no longer see.
func (s *Store) EvictAdmissionTombstones(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(admittedBucket))
		if b == nil {
			return nil
		}
		var drop [][]byte
		if err := b.ForEach(func(k, raw []byte) error {
			var t admissionTombstone
			if err := json.Unmarshal(raw, &t); err != nil {
				return nil
			}
			if t.AdmittedAt.Before(cutoff) {
				drop = append(drop, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range drop {
			if err := b.Delete(k); err != nil {
				return err
			}
			n++
		}
		if n == 0 {
			return nil
		}
		cov, err := tx.CreateBucketIfNotExists([]byte(coverageBucket))
		if err != nil {
			return err
		}
		// Advance monotonically. A horizon that could move backwards
		// would re-qualify the broker to answer for a period it has
		// already forgotten.
		if raw := cov.Get([]byte(evidenceHorizonKey)); raw != nil {
			if prev, perr := time.Parse(time.RFC3339Nano, string(raw)); perr == nil && prev.After(cutoff) {
				return nil
			}
		}
		return cov.Put([]byte(evidenceHorizonKey), []byte(cutoff.UTC().Format(time.RFC3339Nano)))
	})
	return n, err
}

// EvictNonAdmissions drops non-admission records observed before cutoff.
//
// Retention runs from observed_at, not from an exchange — there was no
// exchange. A consumer's reconciliation window starts when it obtained
// the claim, so that is when the clock starts.
func (s *Store) EvictNonAdmissions(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(nonAdmissionBucket))
		if b == nil {
			return nil
		}
		var drop [][]byte
		if err := b.ForEach(func(k, raw []byte) error {
			var e nonAdmissionEntry
			if uerr := json.Unmarshal(raw, &e); uerr != nil {
				return nil
			}
			if e.ObservedAt.Before(cutoff) {
				drop = append(drop, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range drop {
			if err := b.Delete(k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
