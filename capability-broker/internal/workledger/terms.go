package workledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/regionalterms"
	bolt "go.etcd.io/bbolt"
)

var ErrTermsPolicyMissing = errors.New("regional terms policy not installed")

func (s *Store) TermsPolicy() (regionalterms.Policy, error) {
	var policy regionalterms.Policy
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("meta")).Get([]byte("terms_policy"))
		if raw == nil {
			return ErrTermsPolicyMissing
		}
		if err := json.Unmarshal(raw, &policy); err != nil {
			return err
		}
		if policy.PoolID != s.PoolID || policy.SourceID != s.SourceID || policy.BrokerID != s.BrokerID || policy.Revision == 0 || (!policy.Paused && policy.Version == "") {
			return fmt.Errorf("invalid persisted terms policy")
		}
		return nil
	})
	return policy, err
}
func (s *Store) CurrentObservedRound() (uint64, error) {
	var round uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		observed, err := time.Parse(time.RFC3339Nano, string(meta.Get([]byte("observed_at"))))
		now := time.Now()
		if err != nil || now.Sub(observed) > 2*time.Minute || observed.After(now.Add(time.Minute)) || readRound(meta) <= 0 {
			return fmt.Errorf("fresh receiver round observation required")
		}
		round = uint64(readRound(meta))
		return nil
	})
	return round, err
}
func (s *Store) TermsPermitAdmission() error {
	policy, err := s.TermsPolicy()
	if err != nil {
		return err
	}
	if policy.Paused {
		return fmt.Errorf("regional terms admission paused: %s", policy.Reason)
	}
	round, err := s.CurrentObservedRound()
	if err != nil {
		return err
	}
	if round < policy.EffectiveRound {
		return fmt.Errorf("regional terms are not effective yet")
	}
	return nil
}
func (c *Client) PauseTerms(expectedRevision uint64, reason string) (regionalterms.Policy, error) {
	c.admissionGate.Lock()
	defer c.admissionGate.Unlock()
	var result regionalterms.Policy
	if reason == "" {
		return result, fmt.Errorf("terms pause reason required")
	}
	err := c.Store.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		result = regionalterms.Policy{PoolID: c.Store.PoolID, SourceID: c.Store.SourceID, BrokerID: c.Store.BrokerID}
		if raw := meta.Get([]byte("terms_policy")); raw != nil {
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
		}
		if result.Paused && result.Revision == expectedRevision+1 && result.Reason == reason {
			return nil
		}
		if result.Revision != expectedRevision {
			return fmt.Errorf("terms revision changed")
		}
		result.Revision++
		result.Paused = true
		result.Reason = reason
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return meta.Put([]byte("terms_policy"), raw)
	})
	return result, err
}
func (c *Client) ActivateTerms(ctx context.Context, expectedRevision uint64, version string, effectiveRound uint64) (regionalterms.Policy, error) {
	c.admissionGate.Lock()
	defer c.admissionGate.Unlock()
	policy, err := c.Store.TermsPolicy()
	if err != nil {
		return policy, err
	}
	if !policy.Paused && policy.Revision == expectedRevision+1 && policy.Version == version && policy.EffectiveRound == effectiveRound {
		return policy, nil
	}
	if !policy.Paused || policy.Revision != expectedRevision || version == "" {
		return policy, fmt.Errorf("matching paused terms revision required")
	}
	round, err := c.Store.CurrentObservedRound()
	if err != nil {
		return policy, err
	}
	pending, err := c.Store.DrainStatus()
	if err != nil {
		return policy, err
	}
	// An empty broker can finish initial bootstrap after an outage: no member
	// could accept the unpublished version or admit work without a policy. The
	// controller still accounts for every round from the initial source boundary.
	if effectiveRound <= round && (policy.Version != "" || pending.LastReceiptRound != 0) {
		return policy, fmt.Errorf("successor terms must start after observed round")
	}
	if pending.PendingOperations != 0 || pending.UndeliveredReceipts != 0 || uint64(pending.LastReceiptRound) >= effectiveRound {
		return policy, fmt.Errorf("terms transition waits for finalized delivered work")
	}
	control, ok := c.AccountClient.(payment.SourceControl)
	if !ok {
		return policy, fmt.Errorf("receiver source status unavailable")
	}
	status, err := control.RevenueSourceStatus(ctx)
	if err != nil {
		return policy, err
	}
	if status == nil || status.SettlementDomainId != c.Store.SourceID || status.ActiveAuthorizations != 0 {
		return policy, fmt.Errorf("terms transition waits for receiver authorization quiescence")
	}
	policy.Version = version
	policy.EffectiveRound = effectiveRound
	policy.Revision++
	policy.Paused = false
	policy.Reason = ""
	raw, err := json.Marshal(policy)
	if err != nil {
		return policy, err
	}
	err = c.Store.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		key := []byte("terms_version/" + version)
		if old := meta.Get(key); old != nil {
			var prior regionalterms.Policy
			if err := json.Unmarshal(old, &prior); err != nil {
				return err
			}
			if prior.EffectiveRound != effectiveRound {
				return fmt.Errorf("broker terms version timing is immutable")
			}
		} else if err := meta.Put(key, raw); err != nil {
			return err
		}
		return meta.Put([]byte("terms_policy"), raw)
	})
	return policy, err
}
