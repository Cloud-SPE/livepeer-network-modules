package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const canceledAdmissionsBucket = "canceled_authorization_admissions"

type canceledAdmission struct {
	Fingerprint []byte    `json:"fingerprint"`
	At          time.Time `json:"at"`
}

func canceledAdmissionIn(tx *bolt.Tx, key []byte) (*canceledAdmission, error) {
	fences := tx.Bucket([]byte(canceledAdmissionsBucket))
	if fences == nil || fences.Get(key) == nil {
		return nil, nil
	}
	var fence canceledAdmission
	if err := json.Unmarshal(fences.Get(key), &fence); err != nil {
		return nil, err
	}
	return &fence, nil
}

var (
	ErrAuthorizationCanceled       = fmt.Errorf("%w: admission canceled", ErrAuthorizationState)
	ErrRevisionPredecessor         = fmt.Errorf("%w: invalid revision predecessor", ErrAuthorizationState)
	ErrRevisionLimits              = fmt.Errorf("%w: cumulative limits below inherited usage", ErrAuthorizationState)
	ErrReservationExceedsRemaining = fmt.Errorf("%w: reservation exceeds remaining debit", ErrAuthorizationState)
)

// CancelAuthorizationAdmission serializes with AdmitWholesale. Existing
// authority is returned unchanged, including inherited usage; otherwise the
// identity is durably fenced before reporting non-admission.
func (s *Store) CancelAuthorizationAdmission(payer, payee []byte, id string, fingerprint []byte) (*WholesaleAdmissionResult, bool, error) {
	if len(payer) != 20 || len(payee) != 20 || id == "" || len(fingerprint) != sha256.Size {
		return nil, false, fmt.Errorf("invalid admission cancellation identity")
	}
	result := &WholesaleAdmissionResult{}
	canceled := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		key := wholesaleAuthorizationKey(payer, id)
		if raw := tx.Bucket([]byte(spendAuthorizationsBucket)).Get(key); raw != nil {
			var auth WholesaleAuthorization
			if err := json.Unmarshal(raw, &auth); err != nil {
				return err
			}
			if !bytes.Equal(auth.Payee, payee) || !bytes.Equal(auth.Fingerprint, fingerprint) {
				return ErrAuthorizationFingerprint
			}
			result.Authorization = &auth
		} else {
			fences, err := tx.CreateBucketIfNotExists([]byte(canceledAdmissionsBucket))
			if err != nil {
				return err
			}
			prior, err := canceledAdmissionIn(tx, key)
			if err != nil {
				return err
			}
			if prior != nil {
				if !bytes.Equal(prior.Fingerprint, fingerprint) {
					return ErrAuthorizationFingerprint
				}
			} else {
				raw, err := json.Marshal(canceledAdmission{Fingerprint: fingerprint, At: time.Now().UTC()})
				if err != nil {
					return err
				}
				if err := fences.Put(key, raw); err != nil {
					return err
				}
			}
			canceled = true
		}
		var err error
		result.Account, err = loadWholesaleAccount(tx, payer, payee)
		return err
	})
	return result, canceled, err
}
