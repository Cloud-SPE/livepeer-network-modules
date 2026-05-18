package repo

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestAppliedBrokerRuntimePersist(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	item := types.AppliedBrokerRuntime{
		DesiredRevision:     "desired-1",
		AppliedRevision:     "applied-1",
		LastApplyStartedAt:  time.Now().UTC(),
		LastApplyFinishedAt: time.Now().UTC(),
		LastApplyStatus:     "applied",
	}
	if err := repo.PutAppliedBrokerRuntime(item); err != nil {
		t.Fatalf("PutAppliedBrokerRuntime() error = %v", err)
	}
	got, err := repo.GetAppliedBrokerRuntime()
	if err != nil {
		t.Fatalf("GetAppliedBrokerRuntime() error = %v", err)
	}
	if got.AppliedRevision != "applied-1" || got.LastApplyStatus != "applied" {
		t.Fatalf("got = %#v", got)
	}
}
