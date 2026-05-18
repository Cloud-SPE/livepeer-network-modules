package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	offersBucket         = "offers"
	joinRequestsBucket   = "join_requests"
	membersBucket        = "members"
	memberBackendsBucket = "member_backends"
	assignmentsBucket    = "assignments"
	auditEventsBucket    = "audit_events"
)

func (r *StateRepo) initControlPlaneBuckets(tx *bolt.Tx) error {
	for _, bucket := range []string{
		offersBucket,
		joinRequestsBucket,
		membersBucket,
		memberBackendsBucket,
		assignmentsBucket,
		auditEventsBucket,
		desiredBrokerRuntimeBucket,
		appliedBrokerRuntimeBucket,
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
