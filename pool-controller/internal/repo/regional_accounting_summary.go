package repo

import (
	"encoding/json"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/accounting"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
	"math/big"
)

func (r *StateRepo) RegionalAccountingSummary() (types.RegionalAccountingSummary, error) {
	out := types.RegionalAccountingSummary{PoolID: r.PoolID()}
	totals := make([]*big.Int, 5)
	for i := range totals {
		totals[i] = new(big.Int)
	}
	err := r.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(settlementWindowsBucket)).ForEach(func(_, raw []byte) error {
			var w types.SettlementWindow
			if err := json.Unmarshal(raw, &w); err != nil {
				return err
			}
			if w.PoolID != r.PoolID() || w.RegionalAllocation == nil {
				return nil
			}
			a := w.RegionalAllocation
			out.CompletedWindows++
			for i, s := range []string{a.RevenueWei, a.ZeroWorkOperatorWei, a.CommissionWei, a.RoundingResidualWei, a.MemberPayoutWei} {
				n, err := accounting.Wei(s)
				if err != nil {
					return err
				}
				totals[i].Add(totals[i], n)
				if i == 1 && n.Sign() > 0 {
					out.ZeroWorkRevenueWindows++
				}
			}
			return nil
		})
	})
	out.ConfirmedRevenueWei = totals[0].String()
	out.ZeroWorkOperatorWei = totals[1].String()
	out.CommissionWei = totals[2].String()
	out.RoundingResidualWei = totals[3].String()
	out.MemberPayoutWei = totals[4].String()
	return out, err
}
