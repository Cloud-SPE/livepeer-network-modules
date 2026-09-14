package sessionstore

import (
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"
	bolt "go.etcd.io/bbolt"
)

// BindSettlementDomain pins durable workload accounting to its payment ledger.
// This is an observation of the receiver-owned ID, never a generated broker ID.
func (s *Store) BindSettlementDomain(id string) error {
	if !identity.ValidDomain(id) {
		return fmt.Errorf("sessionstore: valid settlement domain required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(coverageBucket))
		if err != nil {
			return err
		}
		key := []byte("settlement_domain_id")
		if prior := b.Get(key); prior != nil && string(prior) != id {
			return fmt.Errorf("sessionstore: payment ledger differs from stored settlement domain; explicit broker-state migration required")
		}
		return b.Put(key, []byte(id))
	})
}
