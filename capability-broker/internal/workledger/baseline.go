package workledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"math/big"
	"strconv"
)

type inheritedBaseline struct {
	Predecessor string `json:"predecessor"`
	Billed      string `json:"billed"`
	Units       uint64 `json:"units"`
}

// SetInheritedBaseline records receiver-proven inherited cumulative usage once.
// It cannot rewrite an already emitted receipt or change on admission replay.
func (s *Store) SetInheritedBaseline(id, predecessor string, billed *big.Int, units uint64) error {
	if id == "" || predecessor == "" || id == predecessor || billed == nil || billed.Sign() < 0 {
		return fmt.Errorf("invalid inherited billing baseline")
	}
	value, err := json.Marshal(inheritedBaseline{Predecessor: predecessor, Billed: billed.String(), Units: units})
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("inherited_baselines"))
		if previous := bucket.Get([]byte(id)); previous != nil {
			if !bytes.Equal(previous, value) {
				return fmt.Errorf("inherited baseline changed")
			}
			return nil
		}
		if tx.Bucket([]byte("billed")).Get([]byte(id)) != nil {
			return fmt.Errorf("successor already finalized without inherited evidence; audited correction required")
		}
		if err := tx.Bucket([]byte("billed")).Put([]byte(id), []byte(billed.String())); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("units")).Put([]byte(id), []byte(strconv.FormatUint(units, 10))); err != nil {
			return err
		}
		return bucket.Put([]byte(id), value)
	})
}
