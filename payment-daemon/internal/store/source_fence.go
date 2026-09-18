package store

import (
	"encoding/json"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"time"
)

type SourceFence struct {
	Frozen   bool      `json:"frozen"`
	Reason   string    `json:"reason"`
	FrozenAt time.Time `json:"frozen_at"`
}

func (s *Store) SourceFence() (SourceFence, error) {
	var out SourceFence
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("source_fence"))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte("state"))
		if raw == nil {
			return fmt.Errorf("source fence state missing")
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		if !out.Frozen || out.Reason == "" || out.FrozenAt.IsZero() {
			return fmt.Errorf("invalid persisted source fence")
		}
		return nil
	})
	return out, err
}
func (s *Store) ActiveAuthorizations() (uint64, error) {
	var count uint64
	err := s.db.View(func(tx *bolt.Tx) error { var err error; count, err = activeAuthorizations(tx); return err })
	return count, err
}
func activeAuthorizations(tx *bolt.Tx) (uint64, error) {
	var count uint64
	err := tx.Bucket([]byte(spendAuthorizationsBucket)).ForEach(func(_, raw []byte) error {
		var auth WholesaleAuthorization
		if err := json.Unmarshal(raw, &auth); err != nil {
			return err
		}
		if auth.State == AuthorizationAdmitted {
			count++
		}
		return nil
	})
	return count, err
}

// FreezeSource is irreversible. The service holds its admission lock while
// calling it, so no accepted funding/admission can straddle this transaction.
func (s *Store) FreezeSource(reason string) (SourceFence, error) {
	var out SourceFence
	if reason == "" {
		return out, fmt.Errorf("source freeze requires audit reason")
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte("source_fence")); b != nil {
			raw := b.Get([]byte("state"))
			if raw == nil {
				return fmt.Errorf("source fence state missing")
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			if !out.Frozen || out.Reason == "" || out.FrozenAt.IsZero() {
				return fmt.Errorf("invalid persisted source fence")
			}
			return nil
		}
		active, err := activeAuthorizations(tx)
		if err != nil {
			return err
		}
		if active != 0 {
			return fmt.Errorf("source has %d unsettled authorizations", active)
		}
		out = SourceFence{Frozen: true, Reason: reason, FrozenAt: time.Now().UTC()}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		b, err := tx.CreateBucket([]byte("source_fence"))
		if err != nil {
			return err
		}
		return b.Put([]byte("state"), raw)
	})
	return out, err
}
