package settlement

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestCloseWindowScalesAttributedRevenueByConfirmedRevenue(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	for _, receipt := range []types.WorkReceipt{
		{
			ID:                   "r1",
			CreatedAt:            now,
			RoundID:              "100",
			RequestID:            "req-1",
			CapabilityID:         "openai:chat-completions",
			OfferingID:           "default",
			MemberEthAddress:     "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			AttributedRevenueWei: "600",
			Status:               "accepted",
		},
		{
			ID:                   "r2",
			CreatedAt:            now.Add(time.Second),
			RoundID:              "100",
			RequestID:            "req-2",
			CapabilityID:         "openai:chat-completions",
			OfferingID:           "default",
			MemberEthAddress:     "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			AttributedRevenueWei: "400",
			Status:               "accepted",
		},
		{
			ID:                   "ignored",
			CreatedAt:            now,
			RoundID:              "101",
			RequestID:            "req-3",
			CapabilityID:         "openai:chat-completions",
			OfferingID:           "default",
			MemberEthAddress:     "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			AttributedRevenueWei: "999",
			Status:               "accepted",
		},
	} {
		if err := stateRepo.SaveWorkReceipt(receipt); err != nil {
			t.Fatalf("SaveWorkReceipt() error = %v", err)
		}
	}

	window, batch, err := NewWithClock(stateRepo, func() time.Time { return now }).CloseWindow(CloseRequest{
		WindowID:             "window-98",
		StartRoundID:         "98",
		EndRoundID:           "111",
		RoundIDs:             []string{"100"},
		ConfirmedRevenueWei:  "500",
		DefaultCommissionBPS: 1000,
	})
	if err != nil {
		t.Fatalf("CloseWindow() error = %v", err)
	}
	if window.AttributedRevenueWei != "1000" || window.ConfirmedRevenueWei != "500" || window.SettlementScalePPM != 500000 {
		t.Fatalf("window = %+v", window)
	}
	if window.Anomaly != "confirmed_revenue_below_attributed_revenue" {
		t.Fatalf("anomaly = %q", window.Anomaly)
	}
	if batch.Status != types.PayoutBatchPendingApproval || batch.TotalAmountWei != "450" {
		t.Fatalf("batch = %+v", batch)
	}
	if len(batch.LineItems) != 2 {
		t.Fatalf("line item count = %d", len(batch.LineItems))
	}
	if batch.LineItems[0].MemberEthAddress != "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || batch.LineItems[0].AmountWei != "180" {
		t.Fatalf("line[0] = %+v", batch.LineItems[0])
	}
	if batch.LineItems[1].MemberEthAddress != "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || batch.LineItems[1].AmountWei != "270" {
		t.Fatalf("line[1] = %+v", batch.LineItems[1])
	}
}

func TestCloseWindowUsesFullAttributedRevenueWhenConfirmedIsHigher(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	now := time.Now().UTC()
	if err := stateRepo.SaveWorkReceipt(types.WorkReceipt{
		ID:                   "r1",
		CreatedAt:            now,
		RoundID:              "100",
		RequestID:            "req-1",
		CapabilityID:         "video:transcode",
		OfferingID:           "abr",
		MemberEthAddress:     "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		AttributedRevenueWei: "100",
		Status:               "accepted",
	}); err != nil {
		t.Fatalf("SaveWorkReceipt() error = %v", err)
	}
	window, batch, err := New(stateRepo).CloseWindow(CloseRequest{
		WindowID:            "window-98",
		RoundIDs:            []string{"100"},
		ConfirmedRevenueWei: "200",
	})
	if err != nil {
		t.Fatalf("CloseWindow() error = %v", err)
	}
	if window.SettlementScalePPM != 1000000 || window.Anomaly != "" || batch.TotalAmountWei != "100" {
		t.Fatalf("window=%+v batch=%+v", window, batch)
	}
}
