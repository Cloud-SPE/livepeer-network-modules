package workledger

import (
	"encoding/json"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	bolt "go.etcd.io/bbolt"
	"strconv"
	"time"
)

type DrainState struct {
	Draining            bool      `json:"draining"`
	Reason              string    `json:"reason"`
	StartedAt           time.Time `json:"started_at"`
	PendingOperations   uint64    `json:"pending_operations"`
	UndeliveredReceipts uint64    `json:"undelivered_receipts"`
	LastReceiptRound    int64     `json:"last_receipt_round"`
}

func (s *Store) BeginDrain(reason string) error {
	if reason == "" {
		return fmt.Errorf("source drain requires reason")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		if meta.Get([]byte("drain")) != nil {
			return nil
		}
		raw, err := json.Marshal(DrainState{Draining: true, Reason: reason, StartedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		return meta.Put([]byte("drain"), raw)
	})
}
func (s *Store) DrainStatus() (DrainState, error) {
	var result DrainState
	err := s.db.View(func(tx *bolt.Tx) error {
		if raw := tx.Bucket([]byte("meta")).Get([]byte("drain")); raw != nil {
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if !result.Draining || result.Reason == "" || result.StartedAt.IsZero() {
				return fmt.Errorf("invalid source drain evidence")
			}
		}
		result.PendingOperations = uint64(tx.Bucket([]byte("unbound_admissions")).Stats().KeyN)
		if err := tx.Bucket([]byte("operations")).ForEach(func(_, raw []byte) error {
			var op Operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			if !op.Emitted {
				result.PendingOperations++
			}
			return nil
		}); err != nil {
			return err
		}
		return tx.Bucket([]byte("receipts")).ForEach(func(key, raw []byte) error {
			var receipt receipts.WorkReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return err
			}
			round, err := strconv.ParseInt(receipt.RoundID, 10, 64)
			if err != nil {
				return err
			}
			if round > result.LastReceiptRound {
				result.LastReceiptRound = round
			}
			if tx.Bucket([]byte("delivered")).Get(key) == nil {
				result.UndeliveredReceipts++
			}
			return nil
		})
	})
	return result, err
}

func (s *Store) Draining() (bool, error) {
	var stopped bool
	err := s.db.View(func(tx *bolt.Tx) error {
		if raw := tx.Bucket([]byte("meta")).Get([]byte("drain")); raw != nil {
			var state DrainState
			if err := json.Unmarshal(raw, &state); err != nil {
				return err
			}
			if !state.Draining || state.Reason == "" || state.StartedAt.IsZero() {
				return fmt.Errorf("invalid source drain evidence")
			}
			stopped = true
		}
		return nil
	})
	return stopped, err
}
