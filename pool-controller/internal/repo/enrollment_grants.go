package repo

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

// Ordinary enrollment updates cannot roll back grants changed by a concurrent
// acceptance or ownership transaction. Those grants have dedicated writers.
func (r *StateRepo) putEnrollmentPreservingGrants(enrollment types.HostEnrollment) error {
	if enrollment.ID == "" {
		return fmt.Errorf("enrollment id required")
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(hostEnrollmentsBucket))
		if raw := b.Get([]byte(enrollment.ID)); raw != nil {
			var prior types.HostEnrollment
			if err := json.Unmarshal(raw, &prior); err != nil {
				return err
			}
			if !strings.EqualFold(prior.MemberEthAddress, enrollment.MemberEthAddress) {
				return fmt.Errorf("enrollment member identity is immutable")
			}
			enrollment.GPUUUIDs = prior.GPUUUIDs
			enrollment.CredentialGeneration = prior.CredentialGeneration
			enrollment.EnrollmentTokenHash = prior.EnrollmentTokenHash
			enrollment.BrokerSessionCredential = prior.BrokerSessionCredential
			enrollment.TermsVersion = prior.TermsVersion
			enrollment.DeviceOwnership = prior.DeviceOwnership
			enrollment.PoolID = prior.PoolID
			if prior.Status == types.HostEnrollmentRevoked || prior.Status == types.HostEnrollmentRetired {
				enrollment.Status = prior.Status
				enrollment.RevokedAt = prior.RevokedAt
			}
		} else if enrollment.TermsVersion != "" {
			var accepted types.TermsAcceptance
			raw := tx.Bucket([]byte(termsAcceptanceBucket)).Get([]byte(acceptanceKey(enrollment.MemberEthAddress, enrollment.TermsVersion)))
			if err := json.Unmarshal(raw, &accepted); err != nil {
				return ErrTermsNotAccepted
			}
			if accepted.PoolID != r.PoolID() || enrollment.PoolID != r.PoolID() {
				return ErrTermsNotAccepted
			}
		}
		if b.Get([]byte(enrollment.ID)) == nil && enrollment.CredentialGeneration == 0 {
			enrollment.CredentialGeneration = 1
		}
		raw, err := json.Marshal(enrollment)
		if err != nil {
			return err
		}
		return b.Put([]byte(enrollment.ID), raw)
	})
}

// RotateEnrollmentCredentials commits only the secret pair, preserving concurrent
// ownership and terms grants. Comparing the old token gives one rotation winner.
func (r *StateRepo) RotateEnrollmentCredentials(id, expectedHash, newHash, newCredential string, at time.Time) (types.HostEnrollment, error) {
	var enrollment types.HostEnrollment
	if expectedHash == "" || newHash == "" || newCredential == "" {
		return enrollment, fmt.Errorf("credential rotation requires complete secret pair")
	}
	err := r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(hostEnrollmentsBucket))
		if err := json.Unmarshal(b.Get([]byte(id)), &enrollment); err != nil {
			return err
		}
		if enrollment.EnrollmentTokenHash != expectedHash {
			return fmt.Errorf("enrollment credentials changed concurrently")
		}
		if enrollment.Status == types.HostEnrollmentRevoked || enrollment.Status == types.HostEnrollmentRetired {
			return fmt.Errorf("inactive enrollment cannot rotate")
		}
		if enrollment.CredentialGeneration == math.MaxUint64 {
			return fmt.Errorf("credential generation exhausted")
		}
		enrollment.CredentialGeneration++
		enrollment.EnrollmentTokenHash = newHash
		enrollment.BrokerSessionCredential = newCredential
		enrollment.UpdatedAt = at
		raw, err := json.Marshal(enrollment)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), raw)
	})
	return enrollment, err
}
