package sessionstore

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// JobRecovery is sealed alongside pending accounting. ExecutionStarted is
// written before dispatch; absence of completed usage never proves zero work.
type JobRecovery struct {
	Admission         PendingDebit
	PaymentBytes      []byte
	AdmissionRecorded bool
	ExecutionStarted  bool
	UsageRecorded     bool
	IssuedAt          string
}
type jobSecrets struct {
	Recovery *JobRecovery
	Pending  *PendingDebit
	Rejected []byte
}

func (s *Store) encodeJob(rec *JobRecord) ([]byte, error) {
	cp := *rec
	secret := jobSecrets{rec.Recovery, rec.Pending, rec.RejectedAuthorization}
	cp.Recovery = nil
	cp.Pending = nil
	cp.RejectedAuthorization = nil
	cp.RecoverySealed = nil
	if secret.Recovery != nil || secret.Pending != nil || len(secret.Rejected) > 0 {
		raw, err := json.Marshal(secret)
		if err != nil {
			return nil, err
		}
		nonce := make([]byte, s.aead.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return nil, err
		}
		cp.RecoverySealed = append(nonce, s.aead.Seal(nil, nonce, raw, []byte(rec.RequestID+":job"))...)
	}
	return json.Marshal(&cp)
}
func (s *Store) decodeJob(raw []byte, rec *JobRecord) error {
	if err := json.Unmarshal(raw, rec); err != nil {
		return err
	}
	if len(rec.RecoverySealed) == 0 {
		return nil
	} // Existing records retain conservative semantics.
	n := s.aead.NonceSize()
	if len(rec.RecoverySealed) < n {
		return fmt.Errorf("truncated job recovery")
	}
	plain, err := s.aead.Open(nil, rec.RecoverySealed[:n], rec.RecoverySealed[n:], []byte(rec.RequestID+":job"))
	if err != nil {
		return err
	}
	var secret jobSecrets
	if err = json.Unmarshal(plain, &secret); err != nil {
		return err
	}
	rec.Recovery, rec.Pending, rec.RejectedAuthorization = secret.Recovery, secret.Pending, secret.Rejected
	return nil
}
func (s *Store) UpdateJob(id string, fn func(*JobRecord) error) error { return s.mutateJob(id, fn) }
func (s *Store) RecoverableJobs() ([]*JobRecord, error) {
	var out []*JobRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(jobsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var r JobRecord
			if err := s.decodeJob(raw, &r); err != nil {
				return err
			}
			if r.Recovery != nil && r.State != JobTerminal && r.State != JobPaymentRejected && r.Pending == nil {
				out = append(out, &r)
			}
			return nil
		})
	})
	return out, err
}
func FinishRecoveredJob(rec *JobRecord, encoded string) error {
	if encoded == "" {
		return fmt.Errorf("settlement evidence missing")
	}
	if rec.State == JobTerminal {
		if rec.Settlement == encoded {
			return nil
		}
		return ErrExists
	}
	rec.State = JobTerminal
	rec.Settlement = encoded
	rec.Pending = nil
	rec.EndedAt = time.Now().UTC()
	return nil
}
