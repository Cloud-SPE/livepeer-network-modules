package repo

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/accounting"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/payoutpolicy"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

// ApproveRegionalBatch commits the approval, all member intents and its audit
// together. A replay returns the existing approval without touching live intents.
type RegionalApprovalPolicy struct {
	Policy    payoutpolicy.Policy
	Hash      string
	PausePath string
}

func (r *StateRepo) ApproveRegionalBatch(id, actor string) (types.PayoutBatch, error) {
	return r.approveRegionalBatch(id, actor, nil)
}

func (r *StateRepo) ApproveRegionalBatchWithPolicy(id string, policy RegionalApprovalPolicy) (types.PayoutBatch, error) {
	return r.approveRegionalBatch(id, "payout-policy:"+policy.Hash, &policy)
}

func (r *StateRepo) approveRegionalBatch(id, actor string, policy *RegionalApprovalPolicy) (types.PayoutBatch, error) {
	var batch types.PayoutBatch
	if strings.TrimSpace(actor) == "" {
		return batch, fmt.Errorf("approval actor required")
	}
	err := r.db.Update(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(payoutBatchesBucket)).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("batch not found")
		}
		if err := json.Unmarshal(raw, &batch); err != nil {
			return err
		}
		if batch.PoolID != r.PoolID() {
			return fmt.Errorf("batch is not regional")
		}
		if batch.Status != types.PayoutBatchPendingApproval {
			if batch.ApprovedBy != "" && !batch.ApprovedAt.IsZero() {
				return nil
			}
			return fmt.Errorf("batch is not pending approval")
		}
		var window types.SettlementWindow
		if err := json.Unmarshal(tx.Bucket([]byte(settlementWindowsBucket)).Get([]byte(batch.SettlementWindowID)), &window); err != nil {
			return err
		}
		if window.PoolID != r.PoolID() || window.RegionalAllocation == nil || window.Anomaly != "" || window.ChainID == 0 || window.RegionalAllocation.MemberPayoutWei != batch.TotalAmountWei || !(len(window.RegionalAllocation.Members) == 0 && len(batch.LineItems) == 0) && !sameJSON(window.RegionalAllocation.Members, batch.LineItems) {
			return fmt.Errorf("regional allocation integrity failure")
		}
		if err := rejectCorrectedWindow(tx, window); err != nil {
			return err
		}
		now := time.Now().UTC()
		if policy != nil {
			facts := payoutpolicy.Batch{ModelB: true, TotalWei: batch.TotalAmountWei, MaxPerMemberWei: "0", Anomaly: window.Anomaly}
			largest := new(big.Int)
			for _, line := range batch.LineItems {
				n, err := accounting.Wei(line.AmountWei)
				if err != nil {
					return err
				}
				if n.Cmp(largest) > 0 {
					largest = n
				}
			}
			facts.MaxPerMemberWei = largest.String()
			if err := tx.Bucket([]byte(payoutBatchesBucket)).ForEach(func(_, raw []byte) error {
				var prior types.PayoutBatch
				if err := json.Unmarshal(raw, &prior); err != nil {
					return err
				}
				if strings.HasPrefix(prior.ApprovedBy, "payout-policy:") && !prior.ApprovedAt.Before(now.Add(-24*time.Hour)) {
					facts.BatchesToday++
				}
				return nil
			}); err != nil {
				return err
			}
			decision := payoutpolicy.Evaluate(policy.Policy, policy.Hash, facts, policy.PausePath, now)
			if !decision.Approved {
				return fmt.Errorf("atomic policy approval refused: %s", decision.Reason)
			}
		}

		total := new(big.Int)
		for i, line := range batch.LineItems {
			amount, err := accounting.Wei(line.AmountWei)
			if err != nil {
				return err
			}
			total.Add(total, amount)
			if amount.Sign() == 0 {
				continue
			}
			intent := types.PayoutIntent{Revision: 1, ID: fmt.Sprintf("payout-%s-%04d", batch.ID, i), PoolID: r.PoolID(), PayoutBatchID: batch.ID, CreatedAt: now, RoundReceiptID: window.ID, RoundID: window.ID, MemberEthAddress: line.MemberEthAddress, DestinationAddress: line.DestinationAddress, ChainID: window.ChainID, Asset: "native_eth", AmountWei: line.AmountWei, Status: "exported", ExportedAt: now}
			b := tx.Bucket([]byte(payoutIntentsBucket))
			if b.Get([]byte(intent.ID)) != nil {
				return fmt.Errorf("regional intent exists without atomic approval")
			}
			raw, err := json.Marshal(intent)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(intent.ID), raw); err != nil {
				return err
			}
		}
		if total.String() != batch.TotalAmountWei {
			return fmt.Errorf("regional batch total mismatch")
		}
		batch.Status = types.PayoutBatchApproved
		batch.ApprovedBy = actor
		batch.ApprovedAt = now
		batch.UpdatedAt = now
		window.Status = types.SettlementWindowApproved
		window.ApprovedAt = now
		window.UpdatedAt = now
		audit := types.AuditEvent{ID: "approval-" + batch.ID, Kind: "payout_batch_approved", OccurredAt: now, Actor: actor, ResourceID: batch.ID, ResourceType: "payout_batch", Details: map[string]any{"pool_id": r.PoolID(), "settlement_window_id": window.ID, "terms_version": window.TermsVersion, "total_amount_wei": batch.TotalAmountWei}}
		for _, entry := range []struct {
			bucket, key string
			value       any
		}{{payoutBatchesBucket, batch.ID, batch}, {settlementWindowsBucket, window.ID, window}, {auditEventsBucket, audit.ID, audit}} {
			raw, err := json.Marshal(entry.value)
			if err != nil {
				return err
			}
			if err := tx.Bucket([]byte(entry.bucket)).Put([]byte(entry.key), raw); err != nil {
				return err
			}
		}
		return nil
	})
	return batch, err
}
