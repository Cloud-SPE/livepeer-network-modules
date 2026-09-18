package repo

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/accounting"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

const regionalWindowsBucket = "regional_window_numbers"

// CloseRegionalWindow derives every financial input from immutable local round
// snapshots. A complete last round already proves that the window has ended.
// Allocation, batch and interval index are committed in one transaction.
func (r *StateRepo) CloseRegionalWindow(start uint64) (types.SettlementWindow, types.PayoutBatch, error) {
	var window types.SettlementWindow
	var batch types.PayoutBatch
	err := r.db.Update(func(tx *bolt.Tx) error {
		index, err := tx.CreateBucketIfNotExists([]byte(regionalWindowsBucket))
		if err != nil {
			return err
		}
		key := strconv.FormatUint(start, 10)
		if id := index.Get([]byte(key)); id != nil {
			if err := json.Unmarshal(tx.Bucket([]byte(settlementWindowsBucket)).Get(id), &window); err != nil {
				return err
			}
			return json.Unmarshal(tx.Bucket([]byte(payoutBatchesBucket)).Get([]byte("batch-"+string(id))), &batch)
		}
		var term types.RegionalTerms
		var next uint64 = math.MaxUint64
		if err := tx.Bucket([]byte(regionalTermsBucket)).ForEach(func(_, raw []byte) error {
			var candidate types.RegionalTerms
			if err := json.Unmarshal(raw, &candidate); err != nil {
				return err
			}
			if candidate.EffectiveRound <= start && (term.Version == "" || candidate.EffectiveRound > term.EffectiveRound) {
				term = candidate
			}
			if candidate.EffectiveRound > start && candidate.EffectiveRound < next {
				next = candidate.EffectiveRound
			}
			return nil
		}); err != nil {
			return err
		}
		if term.Version == "" || term.WindowRounds == 0 || start > math.MaxInt64 || term.WindowRounds > math.MaxInt64-start || (start-term.EffectiveRound)%term.WindowRounds != 0 {
			return fmt.Errorf("window start is not aligned to regional terms")
		}
		end := start + term.WindowRounds - 1
		if end >= next {
			return fmt.Errorf("window crosses a terms boundary")
		}
		roundIndex := tx.Bucket([]byte("regional_round_numbers"))
		if roundIndex == nil {
			return fmt.Errorf("regional rounds have not reconciled")
		}
		var chainID uint64
		revenue := new(big.Int)
		var rounds []string
		var work []types.WorkReceipt
		seen := map[string]bool{}
		for number := start; number <= end; number++ {
			roundKey := strconv.FormatUint(number, 10)
			id := roundIndex.Get([]byte(roundKey))
			if id == nil {
				return fmt.Errorf("round %s is incomplete", roundKey)
			}
			var round types.RoundReceipt
			if err := json.Unmarshal(tx.Bucket([]byte(roundReceiptsBucket)).Get(id), &round); err != nil {
				return err
			}
			if round.PoolID != r.PoolID() || round.RoundID != roundKey || len(round.RevenueReports) == 0 || len(round.WorkReports) == 0 {
				return fmt.Errorf("round %s lacks regional evidence", roundKey)
			}
			for _, report := range round.RevenueReports {
				if report.ChainID == 0 || (chainID != 0 && chainID != report.ChainID) {
					return fmt.Errorf("regional sources disagree on payout chain")
				}
				chainID = report.ChainID
			}
			amount, err := accounting.Wei(round.PoolRevenueWei)
			if err != nil {
				return err
			}
			revenue.Add(revenue, amount)
			rounds = append(rounds, round.ID)
			for _, receiptID := range round.IncludedWorkReceiptIDs {
				if seen[receiptID] {
					return fmt.Errorf("duplicate window receipt %s", receiptID)
				}
				seen[receiptID] = true
				var receipt types.WorkReceipt
				if err := json.Unmarshal(tx.Bucket([]byte(workReceiptsBucket)).Get([]byte(receiptID)), &receipt); err != nil {
					return err
				}
				if receipt.RoundID != roundKey {
					return fmt.Errorf("work receipt round changed")
				}
				if receipt.TermsVersion != term.Version {
					return fmt.Errorf("receipt %s terms do not match the window policy", receipt.ID)
				}
				var accepted types.TermsAcceptance
				acceptanceRaw := tx.Bucket([]byte(termsAcceptanceBucket)).Get([]byte(acceptanceKey(receipt.MemberEthAddress, receipt.TermsVersion)))
				if err := json.Unmarshal(acceptanceRaw, &accepted); err != nil {
					return fmt.Errorf("receipt %s lacks regional terms acceptance", receipt.ID)
				}
				// The broker received this version through a controller-issued
				// credential grant after acceptance. Do not compare unsynchronized
				// controller and broker wall clocks to establish that causal ordering.
				if accepted.PoolID != r.PoolID() || accepted.TermsVersion != receipt.TermsVersion || !strings.EqualFold(accepted.MemberEthAddress, receipt.MemberEthAddress) || accepted.AcceptedAt.IsZero() {
					return fmt.Errorf("receipt %s lacks matching accepted regional terms", receipt.ID)
				}
				work = append(work, receipt)
			}
		}
		allocation, err := accounting.ModelB(r.PoolID(), revenue.String(), term.CommissionBPS, work)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		id := r.PoolID() + "-window-" + key + "-" + strconv.FormatUint(end, 10)
		window = types.SettlementWindow{ID: id, ChainID: chainID, PoolID: r.PoolID(), TermsVersion: term.Version, CommissionBPS: term.CommissionBPS, StartRoundID: key, EndRoundID: strconv.FormatUint(end, 10), LengthRounds: int(term.WindowRounds), Status: types.SettlementWindowPendingApproval, AttributedRevenueWei: allocation.BilledWeightWei, ConfirmedRevenueWei: allocation.RevenueWei, RegionalAllocation: &allocation, IncludedRoundReceiptIDs: rounds, CreatedAt: now, UpdatedAt: now, ClosedAt: now}
		batch = types.PayoutBatch{ID: "batch-" + id, PoolID: r.PoolID(), SettlementWindowID: id, Status: types.PayoutBatchPendingApproval, TotalAmountWei: allocation.MemberPayoutWei, LineItems: allocation.Members, CreatedAt: now, UpdatedAt: now}
		for _, entry := range []struct {
			bucket, key string
			value       any
		}{{settlementWindowsBucket, id, window}, {payoutBatchesBucket, batch.ID, batch}} {
			b := tx.Bucket([]byte(entry.bucket))
			if b.Get([]byte(entry.key)) != nil {
				return fmt.Errorf("window identity already exists outside regional index")
			}
			raw, err := json.Marshal(entry.value)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(entry.key), raw); err != nil {
				return err
			}
		}
		return index.Put([]byte(key), []byte(id))
	})
	return window, batch, err
}
