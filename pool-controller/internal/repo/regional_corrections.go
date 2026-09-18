package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

const regionalCorrectionsBucket = "regional_correction_holds"

// Correction evidence is append-only. Resolving financial consequences requires
// an explicit operator decision; there is no automatic adjustment or rewrite.
type RegionalCorrectionHold struct {
	ID          string             `json:"id"`
	PoolID      string             `json:"pool_id"`
	RoundID     string             `json:"round_id"`
	Reason      string             `json:"reason"`
	DetectedAt  time.Time          `json:"detected_at"`
	Accepted    types.RoundReceipt `json:"accepted"`
	Conflicting types.RoundReceipt `json:"conflicting"`
}

func recordRegionalCorrection(tx *bolt.Tx, prior, next types.RoundReceipt) error {
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	id := prior.RoundID + "-" + hex.EncodeToString(sum[:])
	bucket, err := tx.CreateBucketIfNotExists([]byte(regionalCorrectionsBucket))
	if err != nil {
		return err
	}
	if bucket.Get([]byte(id)) != nil {
		return nil
	}
	hold := RegionalCorrectionHold{ID: id, PoolID: prior.PoolID, RoundID: prior.RoundID, Reason: "conflicting post-close accounting evidence; manual review required", DetectedAt: time.Now().UTC(), Accepted: prior, Conflicting: next}
	raw, err = json.Marshal(hold)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(id), raw)
}
func (r *StateRepo) RegionalCorrectionHolds() ([]RegionalCorrectionHold, error) {
	var out []RegionalCorrectionHold
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(regionalCorrectionsBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var h RegionalCorrectionHold
			if err := json.Unmarshal(raw, &h); err != nil {
				return err
			}
			out = append(out, h)
			return nil
		})
	})
	return out, err
}
func rejectCorrectedWindow(tx *bolt.Tx, window types.SettlementWindow) error {
	start, err := strconv.ParseUint(window.StartRoundID, 10, 64)
	if err != nil {
		return err
	}
	b := tx.Bucket([]byte(regionalCorrectionsBucket))
	if b == nil {
		return nil
	}
	return b.ForEach(func(_, raw []byte) error {
		var h RegionalCorrectionHold
		if err := json.Unmarshal(raw, &h); err != nil {
			return err
		}
		var round uint64
		if _, err := fmt.Sscan(h.RoundID, &round); err != nil {
			return err
		}
		if round >= start && round-start < uint64(window.LengthRounds) {
			return fmt.Errorf("regional correction hold %s requires manual review", h.ID)
		}
		return nil
	})
}
