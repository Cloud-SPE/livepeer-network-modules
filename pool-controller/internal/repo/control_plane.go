package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Buckets. The legacy member model's four buckets (join_requests,
// members, member_backends, assignments) and the offers bucket are no
// longer declared or opened — an offer is derived from the enabled
// template set, never stored. Existing databases keep those bytes: nothing enumerates
// buckets, so they are inert, and there is no migration machinery in
// this module to convert them with. Deleting a user's rows on upgrade
// is not a decision this code should make silently.
const (
	auditEventsBucket         = "audit_events"
	poolMembersBucket         = "pool_members_v2"
	memberNoncesBucket        = "member_nonces"
	hostEnrollmentsBucket     = "host_enrollments"
	hardwareUnitsBucket       = "hardware_units"
	templateOverridesBucket   = "template_overrides"
	templateAssignmentsBucket = "template_assignments"
	certificationRunsBucket   = "certification_runs"
	settlementWindowsBucket   = "settlement_windows"
	payoutBatchesBucket       = "payout_batches"
)

func (r *StateRepo) initControlPlaneBuckets(tx *bolt.Tx) error {
	for _, bucket := range []string{
		auditEventsBucket,
		poolMembersBucket,
		memberNoncesBucket,
		hostEnrollmentsBucket,
		hardwareUnitsBucket,
		templateOverridesBucket,
		templateAssignmentsBucket,
		certificationRunsBucket,
		settlementWindowsBucket,
		payoutBatchesBucket,
		desiredBrokerRuntimeBucket,
	} {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucket)); err != nil {
			return err
		}
	}
	return nil
}

func putJSON[T any](r *StateRepo, bucket, key string, value T) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if key == "" {
		return fmt.Errorf("key is required")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Put([]byte(key), raw)
	})
}

func getJSON[T any](r *StateRepo, bucket, key string, out *T) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if key == "" {
		return fmt.Errorf("key is required")
	}
	return r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(bucket)).Get([]byte(key))
		if raw == nil {
			return fmt.Errorf("%s %q: not found", bucket, key)
		}
		return json.Unmarshal(raw, out)
	})
}

func listJSON[T any](r *StateRepo, bucket string, less func(left, right T) bool) ([]T, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]T, 0)
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucket)).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var item T
			if err := json.Unmarshal(v, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", bucket, err)
	}
	if less != nil {
		sort.Slice(out, func(i, j int) bool {
			return less(out[i], out[j])
		})
	}
	return out, nil
}

func deleteKey(r *StateRepo, bucket, key string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if key == "" {
		return fmt.Errorf("key is required")
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Delete([]byte(key))
	})
}

func nowIfZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}
