// Package accounting implements exact regional allocation arithmetic. Its caller
// must establish complete source and receipt coverage before supplying inputs.
package accounting

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/ethereum/go-ethereum/common"
)

type Allocation = types.RegionalAllocation

func Wei(raw string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok || n.Sign() < 0 || n.String() != raw {
		return nil, fmt.Errorf("noncanonical nonnegative wei amount %q", raw)
	}
	return n, nil
}

// ModelB floors the commission once to whole wei, leaving an integer member
// pot. It then floors once per member after summing that member's offerings.
// Billed weight never caps realized revenue. No gas or operating fee is deducted.
func ModelB(poolID, revenueWei string, commissionBPS uint64, receipts []types.WorkReceipt) (Allocation, error) {
	var out Allocation
	if poolID == "" || commissionBPS > 10000 {
		return out, fmt.Errorf("invalid pool or commission")
	}
	revenue, err := Wei(revenueWei)
	if err != nil {
		return out, err
	}
	total := new(big.Int)
	weights := map[string]*big.Int{}
	seen := map[string]bool{}
	for _, r := range receipts {
		if r.PoolID != poolID || r.SourceID == "" || r.ID == "" || !strings.HasPrefix(r.ID, r.SourceID+"/") || r.Status != "final" || !common.IsHexAddress(r.MemberEthAddress) || r.OfferingID == "" || r.BackendID == "" {
			return out, fmt.Errorf("receipt %s lacks final regional attribution", r.ID)
		}
		if seen[r.ID] {
			return out, fmt.Errorf("duplicate receipt %s", r.ID)
		}
		seen[r.ID] = true
		amount, err := Wei(r.AttributedRevenueWei)
		if err != nil {
			return out, fmt.Errorf("receipt %s: %w", r.ID, err)
		}
		if amount.Sign() == 0 {
			continue
		}
		member := strings.ToLower(r.MemberEthAddress)
		if weights[member] == nil {
			weights[member] = new(big.Int)
		}
		weights[member].Add(weights[member], amount)
		total.Add(total, amount)
	}
	out = Allocation{RevenueWei: revenue.String(), BilledWeightWei: total.String(), CommissionWei: "0", MemberPotWei: "0", MemberPayoutWei: "0", RoundingResidualWei: "0", ZeroWorkOperatorWei: "0", Members: []types.PayoutLineItem{}}
	if total.Sign() == 0 {
		out.ZeroWorkOperatorWei = revenue.String()
		return out, nil
	}
	commission := new(big.Int).Mul(revenue, new(big.Int).SetUint64(commissionBPS))
	commission.Quo(commission, big.NewInt(10000))
	pot := new(big.Int).Sub(revenue, commission)
	paid := new(big.Int)
	keys := make([]string, 0, len(weights))
	for member := range weights {
		keys = append(keys, member)
	}
	sort.Strings(keys)
	for _, member := range keys {
		amount := new(big.Int).Mul(pot, weights[member])
		amount.Quo(amount, total)
		paid.Add(paid, amount)
		out.Members = append(out.Members, types.PayoutLineItem{MemberEthAddress: member, DestinationAddress: member, AttributedRevenueWei: weights[member].String(), AmountWei: amount.String()})
	}
	out.CommissionWei = commission.String()
	out.MemberPotWei = pot.String()
	out.MemberPayoutWei = paid.String()
	out.RoundingResidualWei = new(big.Int).Sub(pot, paid).String()
	return out, nil
}
