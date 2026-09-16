package repo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
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
		bucket := tx.Bucket([]byte(workReceiptsBucket))
		if prior := bucket.Get([]byte(receipt.ID)); prior != nil {
			var previous types.WorkReceipt
			if err := json.Unmarshal(prior, &previous); err != nil {
				return err
			}
			if previous.PoolID != "" || receipt.PoolID != "" {
				if !bytes.Equal(prior, raw) {
					return fmt.Errorf("regional work receipt is immutable")
				}
				return nil
			}
		}
		if receipt.PoolID != "" {
			round, err := strconv.ParseInt(receipt.RoundID, 10, 64)
			amount, ok := new(big.Int).SetString(receipt.AttributedRevenueWei, 10)
			if err != nil || round < 0 || receipt.RoundID != strconv.FormatInt(round, 10) || !ok || amount.Sign() < 0 || amount.String() != receipt.AttributedRevenueWei {
				return fmt.Errorf("invalid regional receipt round or billed value")
			}
			if receipt.PoolID != r.PoolID() || receipt.SourceID == "" || !strings.HasPrefix(receipt.ID, receipt.SourceID+"/") || receipt.Status != "final" {
				return fmt.Errorf("invalid regional receipt identity or finalization")
			}
			if rounds := tx.Bucket([]byte("regional_round_numbers")); rounds != nil && rounds.Get([]byte(receipt.RoundID)) != nil {
				return fmt.Errorf("regional round already closed")
			}
		}
		return bucket.Put([]byte(receipt.ID), raw)
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
		if sources := tx.Bucket([]byte(revenueSourcesBucket)); sources != nil && sources.Stats().KeyN > 0 {
			return fmt.Errorf("regional source registry requires atomic source-qualified round closure")
		}

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
		b := tx.Bucket([]byte(payoutIntentsBucket))
		if existing := b.Get([]byte(intent.ID)); existing != nil {
			var prior types.PayoutIntent
			if err := json.Unmarshal(existing, &prior); err != nil {
				return err
			}
			if prior.PoolID != "" {
				if sameJSON(prior, intent) {
					return nil
				}
				if prior.Revision != intent.Revision {
					return fmt.Errorf("regional payout intent changed; reload before retry")
				}
				if prior.PoolID != intent.PoolID || prior.PayoutBatchID != intent.PayoutBatchID || prior.RoundReceiptID != intent.RoundReceiptID || prior.RoundID != intent.RoundID || prior.MemberEthAddress != intent.MemberEthAddress || prior.DestinationAddress != intent.DestinationAddress || prior.ChainID != intent.ChainID || prior.Asset != intent.Asset || prior.AmountWei != intent.AmountWei || !prior.CreatedAt.Equal(intent.CreatedAt) {
					return fmt.Errorf("regional payout intent economics are immutable")
				}
				if prior.Status == "paid" {
					return fmt.Errorf("paid regional payout intent is immutable")
				}
				intent.Revision++
				raw, err := json.Marshal(intent)
				if err != nil {
					return err
				}
				return b.Put([]byte(intent.ID), raw)
			}
		}
		if intent.PoolID != "" || intent.PayoutBatchID != "" {
			return fmt.Errorf("regional intent requires atomic batch approval")
		}
		if source := tx.Bucket([]byte(settlementWindowsBucket)).Get([]byte(intent.RoundReceiptID)); source != nil {
			var window types.SettlementWindow
			if err := json.Unmarshal(source, &window); err != nil {
				return err
			}
			if window.PoolID != "" {
				return fmt.Errorf("regional window requires atomic approval")
			}
		}
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
