package repo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func guardRegionalAccounting(bucket string, old, replacement []byte) error {
	switch bucket {
	case settlementWindowsBucket:
		var a, b types.SettlementWindow
		if err := json.Unmarshal(replacement, &b); err != nil {
			return err
		}
		if old != nil {
			if err := json.Unmarshal(old, &a); err != nil {
				return err
			}
		}
		if a.PoolID == "" && b.PoolID == "" {
			return nil
		}
		if old == nil || a.PoolID == "" {
			return fmt.Errorf("regional window requires atomic close")
		}
		a.Status, b.Status = "", ""
		a.ApprovedAt, b.ApprovedAt = time.Time{}, time.Time{}
		a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
		if !sameJSON(a, b) {
			return fmt.Errorf("regional window economics are immutable")
		}
	case payoutBatchesBucket:
		var a, b types.PayoutBatch
		if err := json.Unmarshal(replacement, &b); err != nil {
			return err
		}
		if old != nil {
			if err := json.Unmarshal(old, &a); err != nil {
				return err
			}
		}
		if a.PoolID == "" && b.PoolID == "" {
			return nil
		}
		if old == nil || a.PoolID == "" {
			return fmt.Errorf("regional batch requires atomic close")
		}
		if a.Status == types.PayoutBatchPendingApproval && b.Status != a.Status {
			return fmt.Errorf("regional approval requires atomic intent creation")
		}
		if a.Status != types.PayoutBatchPendingApproval && (b.Status == types.PayoutBatchPendingApproval || a.ApprovedBy != b.ApprovedBy || !a.ApprovedAt.Equal(b.ApprovedAt)) {
			return fmt.Errorf("regional approval is immutable")
		}
		a.Status, b.Status = "", ""
		a.ApprovedBy, b.ApprovedBy = "", ""
		a.FailureReason, b.FailureReason = "", ""
		a.ApprovedAt, b.ApprovedAt = time.Time{}, time.Time{}
		a.SubmittedAt, b.SubmittedAt = time.Time{}, time.Time{}
		a.PaidAt, b.PaidAt = time.Time{}, time.Time{}
		a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
		if !sameJSON(a, b) {
			return fmt.Errorf("regional payout economics are immutable")
		}
	}
	return nil
}
