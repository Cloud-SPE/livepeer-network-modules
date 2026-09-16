package repo

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberreport"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/accounting"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

// RegionalMemberReport reads one transaction: approval or payment transitions
// cannot make a figure appear in both pending and paid. No limit truncates history.
func (r *StateRepo) RegionalMemberReport(wallet string) (memberreport.Report, error) {
	report := memberreport.Report{Telemetry: memberreport.Telemetry{ConfirmedRevenueWei: "0", ZeroWorkOperatorWei: "0", CommissionWei: "0", RoundingResidualWei: "0"}, PoolID: r.PoolID(), Wallet: strings.ToLower(wallet), ObservedAt: time.Now().UTC(), CompleteRoundSpans: []memberreport.RoundSpan{}, Windows: []memberreport.Window{}}
	err := r.db.View(func(tx *bolt.Tx) error {
		intents := map[string][]types.PayoutIntent{}
		if err := tx.Bucket([]byte(payoutIntentsBucket)).ForEach(func(_, raw []byte) error {
			var intent types.PayoutIntent
			if err := json.Unmarshal(raw, &intent); err != nil {
				return err
			}
			if intent.PoolID == r.PoolID() && strings.EqualFold(intent.MemberEthAddress, wallet) {
				intents[intent.PayoutBatchID] = append(intents[intent.PayoutBatchID], intent)
			}
			return nil
		}); err != nil {
			return err
		}
		var numbers []uint64
		if index := tx.Bucket([]byte("regional_round_numbers")); index != nil {
			if err := index.ForEach(func(key, id []byte) error {
				n, err := strconv.ParseUint(string(key), 10, 64)
				if err != nil {
					return err
				}
				var round types.RoundReceipt
				if err := json.Unmarshal(tx.Bucket([]byte(roundReceiptsBucket)).Get(id), &round); err != nil {
					return err
				}
				if round.PoolID != r.PoolID() {
					return fmt.Errorf("round pool mismatch")
				}
				numbers = append(numbers, n)
				if round.CreatedAt.After(report.LedgerObservedAt) {
					report.LedgerObservedAt = round.CreatedAt
				}
				return nil
			}); err != nil {
				return err
			}
		}
		sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
		for _, n := range numbers {
			last := len(report.CompleteRoundSpans) - 1
			if last >= 0 && report.CompleteRoundSpans[last].End != ^uint64(0) && report.CompleteRoundSpans[last].End+1 == n {
				report.CompleteRoundSpans[last].End = n
			} else {
				report.CompleteRoundSpans = append(report.CompleteRoundSpans, memberreport.RoundSpan{Start: n, End: n})
			}
		}
		if b := tx.Bucket([]byte("regional_window_holds")); b != nil {
			report.HeldWindowCount = uint64(b.Stats().KeyN)
		}
		return tx.Bucket([]byte(settlementWindowsBucket)).ForEach(func(_, raw []byte) error {
			var window types.SettlementWindow
			if err := json.Unmarshal(raw, &window); err != nil {
				return err
			}
			if window.PoolID != r.PoolID() {
				return nil
			}
			if window.RegionalAllocation == nil {
				return fmt.Errorf("regional allocation missing")
			}
			report.Telemetry.CompletedWindows++
			allocation := window.RegionalAllocation
			zero, err := accounting.Wei(allocation.ZeroWorkOperatorWei)
			if err != nil {
				return err
			}
			if zero.Sign() > 0 {
				report.Telemetry.ZeroWorkRevenueWindows++
			}
			for _, pair := range []struct {
				dst *string
				src string
			}{{&report.Telemetry.ConfirmedRevenueWei, allocation.RevenueWei}, {&report.Telemetry.ZeroWorkOperatorWei, allocation.ZeroWorkOperatorWei}, {&report.Telemetry.CommissionWei, allocation.CommissionWei}, {&report.Telemetry.RoundingResidualWei, allocation.RoundingResidualWei}} {
				a, err := accounting.Wei(*pair.dst)
				if err != nil {
					return err
				}
				b, err := accounting.Wei(pair.src)
				if err != nil {
					return err
				}
				*pair.dst = a.Add(a, b).String()
			}
			start, err := strconv.ParseUint(window.StartRoundID, 10, 64)
			if err != nil {
				return err
			}
			end, err := strconv.ParseUint(window.EndRoundID, 10, 64)
			if err != nil {
				return err
			}
			var batch types.PayoutBatch
			if err := json.Unmarshal(tx.Bucket([]byte(payoutBatchesBucket)).Get([]byte("batch-"+window.ID)), &batch); err != nil {
				return err
			}
			if batch.PoolID != r.PoolID() || batch.SettlementWindowID != window.ID {
				return fmt.Errorf("regional batch mismatch")
			}
			row := memberreport.Window{PoolID: r.PoolID(), WindowID: window.ID, BatchID: batch.ID, ChainID: window.ChainID, Asset: "native_eth", StartRound: start, EndRound: end, TermsVersion: window.TermsVersion, ClosedAt: window.ClosedAt, BatchStatus: string(batch.Status), BilledWeightWei: "0", EarnedWei: "0", AwaitingApprovalWei: "0", PendingWei: "0", PaidWei: "0", Sources: []memberreport.SourceEvidence{}, Payments: []memberreport.Payment{}}
			for _, line := range window.RegionalAllocation.Members {
				if strings.EqualFold(line.MemberEthAddress, wallet) {
					row.BilledWeightWei = line.AttributedRevenueWei
					row.EarnedWei = line.AmountWei
				}
			}
			earned, err := accounting.Wei(row.EarnedWei)
			if err != nil {
				return err
			}
			if batch.ApprovedAt.IsZero() {
				row.AwaitingApprovalWei = row.EarnedWei
				if len(intents[batch.ID]) != 0 {
					return fmt.Errorf("unapproved window has payout intent")
				}
			} else {
				total := new(big.Int)
				paid := new(big.Int)
				for _, intent := range intents[batch.ID] {
					if intent.ChainID != window.ChainID || intent.Asset != row.Asset {
						return fmt.Errorf("payout denomination mismatch")
					}
					n, err := accounting.Wei(intent.AmountWei)
					if err != nil {
						return err
					}
					total.Add(total, n)
					if intent.Status == "paid" {
						if intent.PaidAt.IsZero() || intent.TxHash == "" {
							return fmt.Errorf("paid intent lacks confirmation")
						}
						paid.Add(paid, n)
					}
					row.Payments = append(row.Payments, memberreport.Payment{PoolID: r.PoolID(), IntentID: intent.ID, AmountWei: intent.AmountWei, Status: intent.Status, TxHash: intent.TxHash, PaidAt: intent.PaidAt})
				}
				if total.Cmp(earned) != 0 {
					return fmt.Errorf("member obligation integrity failure")
				}
				row.PaidWei = paid.String()
				row.PendingWei = new(big.Int).Sub(earned, paid).String()
			}
			row.CorrectionHeld = rejectCorrectedWindow(tx, window) != nil
			for _, id := range window.IncludedRoundReceiptIDs {
				var round types.RoundReceipt
				if err := json.Unmarshal(tx.Bucket([]byte(roundReceiptsBucket)).Get([]byte(id)), &round); err != nil {
					return err
				}
				work := map[string]string{}
				for _, proof := range round.WorkReports {
					work[proof.SourceID] = proof.ReceiptDigest
				}
				for _, proof := range round.RevenueReports {
					if proof.PoolID != r.PoolID() || !proof.Complete || work[proof.SourceID] == "" {
						return fmt.Errorf("window source proof incomplete")
					}
					row.Sources = append(row.Sources, memberreport.SourceEvidence{PoolID: r.PoolID(), SourceID: proof.SourceID, BrokerID: proof.BrokerID, Round: uint64(proof.Round), InclusionDigest: proof.InclusionDigest, WorkDigest: work[proof.SourceID], ObservedAt: proof.ObservedAt})
				}
			}
			report.Windows = append(report.Windows, row)
			return nil
		})
	})
	sort.Slice(report.Windows, func(i, j int) bool { return report.Windows[i].StartRound < report.Windows[j].StartRound })
	return report, err
}
