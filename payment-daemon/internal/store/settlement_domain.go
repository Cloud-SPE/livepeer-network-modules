package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"

	bolt "go.etcd.io/bbolt"
)

const settlementDomainBucket = "settlement_domain"

type settlementDomain struct {
	ID      string `json:"id"`
	ChainID uint64 `json:"chain_id,omitempty"`
	Payee   string `json:"payee,omitempty"`
}

// ValidSettlementDomainID accepts the canonical public 256-bit identifier.
func ValidSettlementDomainID(id string) bool { return identity.ValidDomain(id) }

// InitSettlementDomain binds a receiver's ledger to one immutable namespace.
// Initialization does not re-key, merge, or reset existing accounts. The bucket
// is a format marker: a missing/corrupt record in an existing bucket fails closed.
func (s *Store) InitSettlementDomain(configured string, chainID uint64, payee []byte) (string, error) {
	if configured != "" && !ValidSettlementDomainID(configured) {
		return "", errors.New("invalid settlement-domain-id: require 0x and 64 lowercase hex digits, nonzero")
	}
	var result string
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(settlementDomainBucket))
		var d settlementDomain
		if b != nil {
			if err := json.Unmarshal(b.Get([]byte("identity")), &d); err != nil || !ValidSettlementDomainID(d.ID) {
				return errors.New("settlement domain metadata is corrupt; restore complete ledger backup")
			}
			if configured != "" && configured != d.ID {
				return errors.New("configured settlement domain differs from stored ledger identity")
			}
			if d.Payee != "" && (d.Payee != hex.EncodeToString(payee) || d.ChainID != chainID) {
				return errors.New("settlement ledger chain/payee identity cannot change")
			}
		} else {
			marker := tx.Bucket([]byte(wholesaleTotalsBucket)).Get([]byte("settlement_domain_format"))
			if marker != nil {
				return errors.New("settlement domain metadata was lost; restore complete ledger backup")
			}
			if err := tx.Bucket([]byte(spendAuthorizationsBucket)).ForEach(func(_, raw []byte) error {
				var a WholesaleAuthorization
				if err := json.Unmarshal(raw, &a); err != nil {
					return err
				}
				if a.State == AuthorizationAdmitted {
					return errors.New("drain admitted legacy authorizations before settlement-domain upgrade")
				}
				return nil
			}); err != nil {
				return err
			}
			d.ID = configured
			if d.ID == "" {
				var raw [32]byte
				if _, err := rand.Read(raw[:]); err != nil {
					return err
				}
				d.ID = "0x" + hex.EncodeToString(raw[:])
			}
			var err error
			b, err = tx.CreateBucket([]byte(settlementDomainBucket))
			if err != nil {
				return err
			}
		}
		if len(payee) != 0 {
			d.Payee = hex.EncodeToString(payee)
			d.ChainID = chainID
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		if err := b.Put([]byte("identity"), raw); err != nil {
			return err
		}
		if err := tx.Bucket([]byte(wholesaleTotalsBucket)).Put([]byte("settlement_domain_format"), []byte("1")); err != nil {
			return err
		}
		result = d.ID
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("settlement domain: %w", err)
	}
	return result, nil
}
