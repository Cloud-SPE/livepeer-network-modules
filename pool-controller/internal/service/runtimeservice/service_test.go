package runtimeservice

import (
	"fmt"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestRuntimeTransitionsAndView(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	desired := &types.DesiredBrokerRuntime{
		Revision:        "rev-1",
		OfferCount:      1,
		MemberCount:     2,
		BackendCount:    3,
		AssignmentCount: 4,
	}
	now := time.Now().UTC()
	if err := stateRepo.PutAppliedBrokerRuntime(types.AppliedBrokerRuntime{
		DesiredRevision: "rev-0",
		AppliedRevision: "rev-0",
		LastApplyStatus: "applied",
	}); err != nil {
		t.Fatalf("PutAppliedBrokerRuntime() error = %v", err)
	}
	applied, err := MarkStarted(stateRepo, desired, now)
	if err != nil || applied.LastApplyStatus != "started" {
		t.Fatalf("MarkStarted() applied=%#v err=%v", applied, err)
	}
	applied, err = MarkFailed(stateRepo, desired, MarkRequest{Error: "boom"}, now)
	if err != nil || applied.LastApplyStatus != "failed" || applied.LastApplyError != "boom" {
		t.Fatalf("MarkFailed() applied=%#v err=%v", applied, err)
	}
	applied, err = MarkApplied(stateRepo, desired, MarkRequest{}, now)
	if err != nil || applied.AppliedRevision != "rev-1" || applied.LastApplyStatus != "applied" {
		t.Fatalf("MarkApplied() applied=%#v err=%v", applied, err)
	}
	view := BuildView(desired, applied)
	if view.Dirty || view.OfferCount != 1 || view.AssignmentCount != 4 {
		t.Fatalf("BuildView() view=%#v", view)
	}
	diff := BuildDiff(desired, applied)
	if diff["dirty"] != false {
		t.Fatalf("BuildDiff() diff=%#v", diff)
	}
}

func TestBuildViewTracksBrokerLoadedRevisionSeparately(t *testing.T) {
	desired := &types.DesiredBrokerRuntime{Revision: "rev-10"}
	applied := types.AppliedBrokerRuntime{
		AppliedRevision:       "rev-10",
		BrokerReloadAttemptID: "reload-77",
		BrokerLoadedRevision:  "rev-9",
		BrokerReloadStatus:    "applied",
	}
	view := BuildView(desired, applied)
	if view.Dirty {
		t.Fatalf("BuildView() dirty=%v, want false", view.Dirty)
	}
	if !view.BrokerDirty || view.BrokerLoadedRevision != "rev-9" || view.BrokerReloadAttemptID != "reload-77" {
		t.Fatalf("BuildView() view=%#v", view)
	}
}

func TestApplyMarksStartedAndApplied(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	desired := &types.DesiredBrokerRuntime{Revision: "rev-2"}
	now := time.Now().UTC()
	called := false
	applied, status, err := Apply(stateRepo, desired, MarkRequest{Actor: "tester"}, now, func(runtime *types.DesiredBrokerRuntime) error {
		called = true
		if runtime == nil || runtime.Revision != "rev-2" {
			t.Fatalf("applyFn runtime=%#v", runtime)
		}
		return nil
	})
	if err != nil || status != "applied" || applied.AppliedRevision != "rev-2" || applied.LastApplyStatus != "applied" {
		t.Fatalf("Apply() applied=%#v status=%q err=%v", applied, status, err)
	}
	if !called {
		t.Fatalf("Apply() did not call applyFn")
	}
}

func TestApplyMarksFailedOnCallbackError(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	desired := &types.DesiredBrokerRuntime{Revision: "rev-3"}
	now := time.Now().UTC()
	applied, status, err := Apply(stateRepo, desired, MarkRequest{Actor: "tester"}, now, func(*types.DesiredBrokerRuntime) error {
		return fmt.Errorf("reload failed")
	})
	if err == nil || status != "failed" || applied.LastApplyStatus != "failed" || applied.LastApplyError != "reload failed" {
		t.Fatalf("Apply() applied=%#v status=%q err=%v", applied, status, err)
	}
}
