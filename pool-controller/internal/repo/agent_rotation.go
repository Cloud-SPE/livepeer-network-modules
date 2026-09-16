package repo

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

const agentRotationsBucket = "pending_agent_rotations"

var rotationProofPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type AgentCredentialPair struct {
	PoolID           string `json:"pool_id"`
	EnrollmentID     string `json:"enrollment_id"`
	Token            string `json:"enrollment_token"`
	AttachCredential string `json:"attach_credential"`
	Generation       uint64 `json:"credential_generation"`
}
type pendingAgentRotation struct {
	PreviousTokenHash string              `json:"previous_token_hash"`
	RequestHash       string              `json:"request_hash"`
	Pair              AgentCredentialPair `json:"pair"`
	CreatedAt         time.Time           `json:"created_at"`
}

func rotationHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}
func validRotationEnrollment(e types.HostEnrollment) bool {
	return e.Status != types.HostEnrollmentRetired && e.Status != types.HostEnrollmentRevoked
}

// RotateAgentCredentials persists the exact result with the secret request proof
// in the same transaction as the new generation. Arbitrary revoked tokens never
// recover it: both the old token and the pre-persisted 256-bit proof must match,
// and the result must still be the enrollment's current active generation.
func (r *StateRepo) RotateAgentCredentials(id, token, proof string) (AgentCredentialPair, error) {
	var result AgentCredentialPair
	if token == "" || !rotationProofPattern.MatchString(proof) {
		return result, fmt.Errorf("invalid agent rotation authority")
	}
	var secret [64]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return result, err
	}
	err := r.db.Update(func(tx *bolt.Tx) error {
		enrollments := tx.Bucket([]byte(hostEnrollmentsBucket))
		var e types.HostEnrollment
		if err := json.Unmarshal(enrollments.Get([]byte(id)), &e); err != nil {
			return err
		}
		if !validRotationEnrollment(e) {
			return fmt.Errorf("inactive enrollment")
		}
		pending, err := tx.CreateBucketIfNotExists([]byte(agentRotationsBucket))
		if err != nil {
			return err
		}
		if raw := pending.Get([]byte(id)); raw != nil {
			var prior pendingAgentRotation
			if err := json.Unmarshal(raw, &prior); err != nil {
				return err
			}
			if prior.RequestHash == rotationHash(proof) {
				if prior.Pair.PoolID != r.PoolID() || prior.Pair.EnrollmentID != id || prior.PreviousTokenHash != rotationHash(token) || e.EnrollmentTokenHash != rotationHash(prior.Pair.Token) || e.CredentialGeneration != prior.Pair.Generation {
					return fmt.Errorf("agent rotation no longer current")
				}
				result = prior.Pair
				return nil
			}
		}
		if e.EnrollmentTokenHash != rotationHash(token) || e.CredentialGeneration == math.MaxUint64 {
			return fmt.Errorf("agent rotation authority changed")
		}
		result = AgentCredentialPair{PoolID: r.PoolID(), EnrollmentID: id, Token: hex.EncodeToString(secret[:32]), AttachCredential: hex.EncodeToString(secret[32:]), Generation: e.CredentialGeneration + 1}
		e.EnrollmentTokenHash = rotationHash(result.Token)
		e.BrokerSessionCredential = result.AttachCredential
		e.CredentialGeneration = result.Generation
		e.UpdatedAt = time.Now().UTC()
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := enrollments.Put([]byte(id), raw); err != nil {
			return err
		}
		raw, err = json.Marshal(pendingAgentRotation{PreviousTokenHash: rotationHash(token), RequestHash: rotationHash(proof), Pair: result, CreatedAt: e.UpdatedAt})
		if err != nil {
			return err
		}
		return pending.Put([]byte(id), raw)
	})
	return result, err
}
func (r *StateRepo) AckAgentRotation(id, token, proof string) error {
	if token == "" || !rotationProofPattern.MatchString(proof) {
		return fmt.Errorf("invalid agent rotation acknowledgement")
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		var e types.HostEnrollment
		if err := json.Unmarshal(tx.Bucket([]byte(hostEnrollmentsBucket)).Get([]byte(id)), &e); err != nil {
			return err
		}
		if !validRotationEnrollment(e) || e.EnrollmentTokenHash != rotationHash(token) {
			return fmt.Errorf("current agent token required")
		}
		b := tx.Bucket([]byte(agentRotationsBucket))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return nil
		}
		var pending pendingAgentRotation
		if err := json.Unmarshal(raw, &pending); err != nil {
			return err
		}
		if pending.RequestHash != rotationHash(proof) || pending.Pair.Generation != e.CredentialGeneration {
			return fmt.Errorf("rotation acknowledgement mismatch")
		}
		return b.Delete([]byte(id))
	})
}
