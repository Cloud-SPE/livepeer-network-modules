package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

type WorkReceiptPage struct {
	Receipts   []types.WorkReceipt `json:"receipts"`
	Snapshot   string              `json:"snapshot"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Total      int                 `json:"total"`
}

// PageWorkReceipts filters before pagination and binds every page to the same
// complete filtered snapshot. A concurrent receipt change requires a retry.
func (r *StateRepo) PageWorkReceipts(round, status, cursor, snapshot string, limit int) (WorkReceiptPage, error) {
	result := WorkReceiptPage{Receipts: []types.WorkReceipt{}}
	if limit <= 0 || limit > 1000 {
		return result, fmt.Errorf("page limit must be 1..1000")
	}
	err := r.db.View(func(tx *bolt.Tx) error {
		digest := sha256.New()
		if err := tx.Bucket([]byte(workReceiptsBucket)).ForEach(func(key, raw []byte) error {
			var receipt types.WorkReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return err
			}
			if round != "" && receipt.RoundID != round || status != "" && receipt.Status != status {
				return nil
			}
			_, _ = digest.Write(raw)
			_, _ = digest.Write([]byte{0})
			result.Total++
			if string(key) <= cursor {
				return nil
			}
			if len(result.Receipts) < limit {
				result.Receipts = append(result.Receipts, receipt)
			} else if result.NextCursor == "" {
				result.NextCursor = result.Receipts[len(result.Receipts)-1].ID
			}
			return nil
		}); err != nil {
			return err
		}
		result.Snapshot = hex.EncodeToString(digest.Sum(nil))
		if snapshot != "" && result.Snapshot != snapshot {
			return fmt.Errorf("receipt snapshot changed; restart collection")
		}
		return nil
	})
	return result, err
}
