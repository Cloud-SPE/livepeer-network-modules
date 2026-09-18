package observability

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"math/big"
)

var regionalAccounting = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "livepeer_pool_regional_accounting", Help: "Regional allocation totals and fractions rebuilt from durable windows; monetary metrics are approximate wei, ledger values are exact."}, []string{"pool_id", "measure"})

func UpdateRegionalAccounting(s types.RegionalAccountingSummary) {
	values := map[string]float64{"completed_windows": float64(s.CompletedWindows), "zero_work_revenue_windows": float64(s.ZeroWorkRevenueWindows), "zero_work_window_fraction": 0, "zero_work_revenue_fraction": 0}
	for key, raw := range map[string]string{"confirmed_revenue_wei": s.ConfirmedRevenueWei, "zero_work_operator_wei": s.ZeroWorkOperatorWei, "commission_wei": s.CommissionWei, "rounding_residual_wei": s.RoundingResidualWei, "member_payout_wei": s.MemberPayoutWei} {
		n, ok := new(big.Int).SetString(raw, 10)
		if !ok {
			continue
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		values[key] = f
	}
	if s.CompletedWindows > 0 {
		values["zero_work_window_fraction"] = float64(s.ZeroWorkRevenueWindows) / float64(s.CompletedWindows)
	}
	revenue, ok := new(big.Int).SetString(s.ConfirmedRevenueWei, 10)
	zero, zok := new(big.Int).SetString(s.ZeroWorkOperatorWei, 10)
	if ok && zok && revenue.Sign() > 0 {
		values["zero_work_revenue_fraction"], _ = new(big.Rat).SetFrac(zero, revenue).Float64()
	}
	for key, value := range values {
		regionalAccounting.WithLabelValues(s.PoolID, key).Set(value)
	}
}
