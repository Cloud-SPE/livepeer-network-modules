package repo

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestStateRepoSaveAndListSnapshots(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	first := Snapshot{
		ID:            "2026-05-16T12:00:00Z",
		CreatedAt:     time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		Source:        "startup",
		MemberCount:   1,
		RenderedBytes: 10,
	}
	second := Snapshot{
		ID:            "2026-05-16T13:00:00Z",
		CreatedAt:     time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC),
		Source:        "reload",
		MemberCount:   2,
		RenderedBytes: 20,
	}
	if err := repo.SaveSnapshot(first); err != nil {
		t.Fatalf("SaveSnapshot(first) error = %v", err)
	}
	if err := repo.SaveSnapshot(second); err != nil {
		t.Fatalf("SaveSnapshot(second) error = %v", err)
	}

	latest, err := repo.LatestSnapshot()
	if err != nil {
		t.Fatalf("LatestSnapshot() error = %v", err)
	}
	if latest == nil || latest.ID != second.ID {
		t.Fatalf("LatestSnapshot() = %#v, want %q", latest, second.ID)
	}

	items, err := repo.ListSnapshots(10)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(ListSnapshots()) = %d, want 2", len(items))
	}
	if items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("snapshot order = %#v", items)
	}
}

func TestStateRepoSaveAndListReceipts(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	work := types.WorkReceipt{
		ID:                "work-1",
		CreatedAt:         time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		RequestID:         "req-1",
		CapabilityID:      "openai:chat-completions",
		OfferingID:        "shared",
		MemberEthAddress:  "0xabc",
		BackendID:         "backend-a",
		ActualUnits:       42,
		GatewayRevenueWei: "1000",
		Status:            "final",
	}
	round := types.RoundReceipt{
		ID:               "round-1",
		CreatedAt:        time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC),
		RoundID:          "123",
		PoolRevenueWei:   "10000",
		PoolCutWei:       "1000",
		DistributableWei: "9000",
	}

	if err := repo.SaveWorkReceipt(work); err != nil {
		t.Fatalf("SaveWorkReceipt() error = %v", err)
	}
	if err := repo.SaveRoundReceipt(round); err != nil {
		t.Fatalf("SaveRoundReceipt() error = %v", err)
	}

	workItems, err := repo.ListWorkReceipts(10)
	if err != nil {
		t.Fatalf("ListWorkReceipts() error = %v", err)
	}
	if len(workItems) != 1 || workItems[0].ID != "work-1" {
		t.Fatalf("work receipts = %#v", workItems)
	}

	roundItems, err := repo.ListRoundReceipts(10)
	if err != nil {
		t.Fatalf("ListRoundReceipts() error = %v", err)
	}
	if len(roundItems) != 1 || roundItems[0].ID != "round-1" {
		t.Fatalf("round receipts = %#v", roundItems)
	}

	gotWork, err := repo.GetWorkReceipts([]string{"work-1"})
	if err != nil {
		t.Fatalf("GetWorkReceipts() error = %v", err)
	}
	if len(gotWork) != 1 || gotWork[0].ID != "work-1" {
		t.Fatalf("GetWorkReceipts() = %#v", gotWork)
	}
}
