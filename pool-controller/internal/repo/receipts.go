package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const (
	workReceiptsBucket  = "work_receipts"
	roundReceiptsBucket = "round_receipts"
	payoutIntentsBucket = "payout_intents"
)

func (r *StateRepo) initReceiptBuckets(tx *bolt.Tx) error {
	if _, err := tx.CreateBucketIfNotExists([]byte(workReceiptsBucket)); err != nil {
		return err
	}
	if _, err := tx.CreateBucketIfNotExists([]byte(roundReceiptsBucket)); err != nil {
		return err
	}
	if _, err := tx.CreateBucketIfNotExists([]byte(payoutIntentsBucket)); err != nil {
		return err
	}
	return nil
}

func (r *StateRepo) SaveWorkReceipt(receipt types.WorkReceipt) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if receipt.ID == "" {
		return fmt.Errorf("work receipt id is required")
	}
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("marshal work receipt: %w", err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(workReceiptsBucket)).Put([]byte(receipt.ID), raw)
	})
}

func (r *StateRepo) ListWorkReceipts(limit int) ([]types.WorkReceipt, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]types.WorkReceipt, 0)
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(workReceiptsBucket)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var receipt types.WorkReceipt
			if err := json.Unmarshal(v, &receipt); err != nil {
				return err
			}
			out = append(out, receipt)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list work receipts: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *StateRepo) GetWorkReceipts(ids []string) ([]types.WorkReceipt, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]types.WorkReceipt, 0, len(ids))
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(workReceiptsBucket))
		for _, id := range ids {
			raw := b.Get([]byte(id))
			if raw == nil {
				return fmt.Errorf("work receipt %q not found", id)
			}
			var receipt types.WorkReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return err
			}
			out = append(out, receipt)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get work receipts: %w", err)
	}
	return out, nil
}

func (r *StateRepo) SaveRoundReceipt(receipt types.RoundReceipt) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if receipt.ID == "" {
		return fmt.Errorf("round receipt id is required")
	}
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("marshal round receipt: %w", err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(roundReceiptsBucket)).Put([]byte(receipt.ID), raw)
	})
}

func (r *StateRepo) ListRoundReceipts(limit int) ([]types.RoundReceipt, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]types.RoundReceipt, 0)
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(roundReceiptsBucket)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var receipt types.RoundReceipt
			if err := json.Unmarshal(v, &receipt); err != nil {
				return err
			}
			out = append(out, receipt)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list round receipts: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *StateRepo) GetRoundReceipt(id string) (types.RoundReceipt, error) {
	if r == nil || r.db == nil {
		return types.RoundReceipt{}, fmt.Errorf("repo is not open")
	}
	if id == "" {
		return types.RoundReceipt{}, fmt.Errorf("round receipt id is required")
	}
	var out types.RoundReceipt
	err := r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(roundReceiptsBucket)).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("round receipt %q not found", id)
		}
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return types.RoundReceipt{}, fmt.Errorf("get round receipt: %w", err)
	}
	return out, nil
}

func (r *StateRepo) FindLatestRoundReceiptByRoundID(roundID string) (types.RoundReceipt, error) {
	if r == nil || r.db == nil {
		return types.RoundReceipt{}, fmt.Errorf("repo is not open")
	}
	if roundID == "" {
		return types.RoundReceipt{}, fmt.Errorf("round receipt round_id is required")
	}
	items, err := r.ListRoundReceipts(0)
	if err != nil {
		return types.RoundReceipt{}, err
	}
	for _, item := range items {
		if item.RoundID == roundID {
			return item, nil
		}
	}
	return types.RoundReceipt{}, fmt.Errorf("round receipt for round_id %q not found", roundID)
}

func (r *StateRepo) SavePayoutIntent(intent types.PayoutIntent) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if intent.ID == "" {
		return fmt.Errorf("payout intent id is required")
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("marshal payout intent: %w", err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(payoutIntentsBucket)).Put([]byte(intent.ID), raw)
	})
}

func (r *StateRepo) ListPayoutIntents(limit int) ([]types.PayoutIntent, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]types.PayoutIntent, 0)
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(payoutIntentsBucket)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var intent types.PayoutIntent
			if err := json.Unmarshal(v, &intent); err != nil {
				return err
			}
			out = append(out, intent)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list payout intents: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *StateRepo) GetPayoutIntent(id string) (types.PayoutIntent, error) {
	if r == nil || r.db == nil {
		return types.PayoutIntent{}, fmt.Errorf("repo is not open")
	}
	if id == "" {
		return types.PayoutIntent{}, fmt.Errorf("payout intent id is required")
	}
	var out types.PayoutIntent
	err := r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(payoutIntentsBucket)).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("payout intent %q not found", id)
		}
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return types.PayoutIntent{}, fmt.Errorf("get payout intent: %w", err)
	}
	return out, nil
}
