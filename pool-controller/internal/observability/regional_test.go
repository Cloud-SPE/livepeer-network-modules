package observability

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
)

func TestRegionalMetricsRebuildWithoutRetryInflation(t *testing.T) {
	s := types.RegionalAccountingSummary{PoolID: "metric-pool", CompletedWindows: 4, ZeroWorkRevenueWindows: 1, ConfirmedRevenueWei: "1000", ZeroWorkOperatorWei: "100", CommissionWei: "90", RoundingResidualWei: "1", MemberPayoutWei: "809"}
	for i := 0; i < 3; i++ {
		UpdateRegionalAccounting(s)
	}
	for key, want := range map[string]float64{"completed_windows": 4, "zero_work_revenue_windows": 1, "confirmed_revenue_wei": 1000, "zero_work_operator_wei": 100, "zero_work_window_fraction": 0.25, "zero_work_revenue_fraction": 0.1} {
		if got := testutil.ToFloat64(regionalAccounting.WithLabelValues(s.PoolID, key)); got != want {
			t.Fatalf("%s = %v want %v", key, got, want)
		}
	}
}
