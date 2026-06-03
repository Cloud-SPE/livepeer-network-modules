package settlement

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const (
	DefaultWindowLengthRounds = 14
	scaleDenominatorPPM       = int64(1_000_000)
	commissionDenominatorBPS  = int64(10_000)
)

type Service struct {
	repo *repo.StateRepo
	now  func() time.Time
}

type CloseRequest struct {
	WindowID                 string            `json:"window_id"`
	StartRoundID             string            `json:"start_round_id,omitempty"`
	EndRoundID               string            `json:"end_round_id,omitempty"`
	RoundIDs                 []string          `json:"round_ids"`
	ConfirmedRevenueWei      string            `json:"confirmed_revenue_wei"`
	DefaultCommissionBPS     uint32            `json:"default_commission_bps,omitempty"`
	OfferingCommissionBPS    map[string]uint32 `json:"offering_commission_bps,omitempty"`
	IncludedRoundReceiptIDs  []string          `json:"included_round_receipt_ids,omitempty"`
	OperatorAdjustmentReason string            `json:"operator_adjustment_reason,omitempty"`
}

func New(stateRepo *repo.StateRepo) *Service {
	return &Service{repo: stateRepo, now: func() time.Time { return time.Now().UTC() }}
}

func NewWithClock(stateRepo *repo.StateRepo, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: stateRepo, now: now}
}

func (s *Service) CloseWindow(req CloseRequest) (types.SettlementWindow, types.PayoutBatch, error) {
	if strings.TrimSpace(req.WindowID) == "" {
		return types.SettlementWindow{}, types.PayoutBatch{}, fmt.Errorf("window_id is required")
	}
	roundSet := make(map[string]bool, len(req.RoundIDs))
	for _, id := range req.RoundIDs {
		roundSet[strings.TrimSpace(id)] = true
	}
	if len(roundSet) == 0 {
		return types.SettlementWindow{}, types.PayoutBatch{}, fmt.Errorf("round_ids are required")
	}
	confirmed, err := parseWei(req.ConfirmedRevenueWei)
	if err != nil {
		return types.SettlementWindow{}, types.PayoutBatch{}, fmt.Errorf("confirmed_revenue_wei: %w", err)
	}
	receipts, err := s.repo.ListWorkReceipts(0)
	if err != nil {
		return types.SettlementWindow{}, types.PayoutBatch{}, err
	}
	acc := newAccumulator()
	for _, receipt := range receipts {
		if !roundSet[receipt.RoundID] || receipt.Status != "accepted" {
			continue
		}
		if strings.TrimSpace(receipt.AttributedRevenueWei) == "" {
			continue
		}
		amount, err := parseWei(receipt.AttributedRevenueWei)
		if err != nil {
			return types.SettlementWindow{}, types.PayoutBatch{}, fmt.Errorf("receipt %s attributed_revenue_wei: %w", receipt.ID, err)
		}
		acc.add(receipt, amount)
	}
	if acc.total.Sign() == 0 {
		return types.SettlementWindow{}, types.PayoutBatch{}, fmt.Errorf("no accepted attributed revenue in window")
	}
	scalePPM := scalePPM(acc.total, confirmed)
	now := s.now()
	window := types.SettlementWindow{
		ID:                         req.WindowID,
		StartRoundID:               req.StartRoundID,
		EndRoundID:                 req.EndRoundID,
		LengthRounds:               DefaultWindowLengthRounds,
		Status:                     types.SettlementWindowPendingApproval,
		AttributedRevenueWei:       acc.total.String(),
		ConfirmedRevenueWei:        confirmed.String(),
		SettlementScalePPM:         scalePPM,
		IncludedRoundReceiptIDs:    append([]string(nil), req.IncludedRoundReceiptIDs...),
		OfferingSettlementLineItem: make([]types.OfferingSettlement, 0, len(acc.byOffering)),
		CreatedAt:                  now,
		UpdatedAt:                  now,
		ClosedAt:                   now,
	}
	if confirmed.Cmp(acc.total) < 0 {
		window.Anomaly = "confirmed_revenue_below_attributed_revenue"
	}
	offeringKeys := sortedKeys(acc.byOffering)
	totalPayout := big.NewInt(0)
	var lines []types.PayoutLineItem
	for _, offeringKey := range offeringKeys {
		offeringTotal := acc.byOffering[offeringKey]
		commissionBPS := req.DefaultCommissionBPS
		if override, ok := req.OfferingCommissionBPS[offeringKey]; ok {
			commissionBPS = override
		}
		settlementRevenue := applyPPM(offeringTotal, scalePPM)
		commission := applyBPS(settlementRevenue, commissionBPS)
		distributable := new(big.Int).Sub(settlementRevenue, commission)
		capabilityID, offeringID := splitOfferingKey(offeringKey)
		window.OfferingSettlementLineItem = append(window.OfferingSettlementLineItem, types.OfferingSettlement{
			CapabilityID:            capabilityID,
			OfferingID:              offeringID,
			AttributedRevenueWei:    offeringTotal.String(),
			SettlementRevenueWei:    settlementRevenue.String(),
			CommissionWei:           commission.String(),
			DistributableRevenueWei: distributable.String(),
		})
		memberKeys := sortedKeys(acc.byMemberOffering[offeringKey])
		for _, member := range memberKeys {
			memberAttr := acc.byMemberOffering[offeringKey][member]
			memberSettlement := applyPPM(memberAttr, scalePPM)
			memberCommission := applyBPS(memberSettlement, commissionBPS)
			amount := new(big.Int).Sub(memberSettlement, memberCommission)
			totalPayout.Add(totalPayout, amount)
			lines = append(lines, types.PayoutLineItem{
				MemberEthAddress:     member,
				DestinationAddress:   member,
				CapabilityID:         capabilityID,
				OfferingID:           offeringID,
				AttributedRevenueWei: memberAttr.String(),
				AmountWei:            amount.String(),
			})
		}
	}
	batch := types.PayoutBatch{
		ID:                 "batch-" + req.WindowID,
		SettlementWindowID: req.WindowID,
		Status:             types.PayoutBatchPendingApproval,
		TotalAmountWei:     totalPayout.String(),
		LineItems:          lines,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.repo.PutSettlementWindow(window); err != nil {
		return types.SettlementWindow{}, types.PayoutBatch{}, err
	}
	if err := s.repo.PutPayoutBatch(batch); err != nil {
		return types.SettlementWindow{}, types.PayoutBatch{}, err
	}
	return window, batch, nil
}

type accumulator struct {
	total            *big.Int
	byOffering       map[string]*big.Int
	byMemberOffering map[string]map[string]*big.Int
}

func newAccumulator() *accumulator {
	return &accumulator{
		total:            big.NewInt(0),
		byOffering:       make(map[string]*big.Int),
		byMemberOffering: make(map[string]map[string]*big.Int),
	}
}

func (a *accumulator) add(receipt types.WorkReceipt, amount *big.Int) {
	offeringKey := receipt.CapabilityID + "|" + receipt.OfferingID
	a.total.Add(a.total, amount)
	if a.byOffering[offeringKey] == nil {
		a.byOffering[offeringKey] = big.NewInt(0)
	}
	a.byOffering[offeringKey].Add(a.byOffering[offeringKey], amount)
	if a.byMemberOffering[offeringKey] == nil {
		a.byMemberOffering[offeringKey] = make(map[string]*big.Int)
	}
	member := strings.ToLower(strings.TrimSpace(receipt.MemberEthAddress))
	if a.byMemberOffering[offeringKey][member] == nil {
		a.byMemberOffering[offeringKey][member] = big.NewInt(0)
	}
	a.byMemberOffering[offeringKey][member].Add(a.byMemberOffering[offeringKey][member], amount)
}

func parseWei(raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "0"
	}
	out, ok := new(big.Int).SetString(raw, 10)
	if !ok || out.Sign() < 0 {
		return nil, fmt.Errorf("must be a non-negative integer")
	}
	return out, nil
}

func scalePPM(attributed, confirmed *big.Int) uint64 {
	if confirmed.Cmp(attributed) >= 0 {
		return uint64(scaleDenominatorPPM)
	}
	scaled := new(big.Int).Mul(confirmed, big.NewInt(scaleDenominatorPPM))
	scaled.Div(scaled, attributed)
	return scaled.Uint64()
}

func applyPPM(amount *big.Int, ppm uint64) *big.Int {
	out := new(big.Int).Mul(new(big.Int).Set(amount), new(big.Int).SetUint64(ppm))
	out.Div(out, big.NewInt(scaleDenominatorPPM))
	return out
}

func applyBPS(amount *big.Int, bps uint32) *big.Int {
	out := new(big.Int).Mul(new(big.Int).Set(amount), new(big.Int).SetUint64(uint64(bps)))
	out.Div(out, big.NewInt(commissionDenominatorBPS))
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func splitOfferingKey(key string) (string, string) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}
