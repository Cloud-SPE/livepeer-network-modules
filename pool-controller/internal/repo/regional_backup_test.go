package repo

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// This models the documented stopped-writer copy boundary. It uses real
// regional databases and approved obligations, never a newly initialized
// replacement pool or a recomputed payout balance.
func TestStoppedRegionalBackupPreservesIdentitiesObligationsAndFences(t *testing.T) {
	original, restored := t.TempDir(), t.TempDir()
	identities := map[string]string{}
	intents := map[string][]types.PayoutIntent{}
	for _, region := range []string{"eu", "us"} {
		store, err := Open(filepath.Join(original, region))
		if err != nil {
			t.Fatal(err)
		}
		identities[region] = store.PoolID()
		source := windowSource(t, store)
		amount := "10"
		if region == "us" {
			amount = "20"
		}
		for round := int64(100); round < 114; round++ {
			freezeWindowRound(t, store, source, round, amount, "3", "1")
		}
		_, batch, err := store.CloseRegionalWindow(100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApproveRegionalBatch(batch.ID, "backup-test-operator"); err != nil {
			t.Fatal(err)
		}
		intents[region], err = store.ListPayoutIntents(0)
		if err != nil || len(intents[region]) != 2 {
			t.Fatalf("missing approved obligations: %v %v", intents[region], err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	authority, err := OpenOwnership(filepath.Join(original, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := authority.Change("claim", "source", ownership.Request{DeviceID: "gpu-backup", PoolID: identities["eu"], EnrollmentID: "eu-host", MemberWallet: "member"})
	if err != nil {
		t.Fatal(err)
	}
	transfer := ownership.Request{DeviceID: record.DeviceID, PoolID: record.PoolID, EnrollmentID: record.EnrollmentID, ExpectedGeneration: record.Generation, DestinationPoolID: identities["us"]}
	if _, err = authority.Change("drain", "source", transfer); err != nil {
		t.Fatal(err)
	}
	transfer.DrainEvidence, transfer.RevocationEvidence, transfer.StopEvidence = "all sources drained", "all source credentials revoked", "actual agent stop acknowledged"
	if _, err = authority.Change("release", "source", transfer); err != nil {
		t.Fatal(err)
	}
	next, err := authority.Change("claim", "destination", ownership.Request{DeviceID: record.DeviceID, PoolID: identities["us"], EnrollmentID: "us-host", ExpectedGeneration: record.Generation, MemberWallet: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Close(); err != nil {
		t.Fatal(err)
	}

	// All writers are closed before copying any store. An off-host archive is
	// byte-preserving; socket paths and process state are not restored.
	err = filepath.WalkDir(original, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(original, path)
		if err != nil {
			return err
		}
		target := filepath.Join(restored, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"eu", "us"} {
		store, err := Open(filepath.Join(restored, region))
		if err != nil {
			t.Fatal(err)
		}
		if store.PoolID() != identities[region] {
			t.Fatal("restore minted/replaced pool identity")
		}
		got, err := store.ListPayoutIntents(0)
		if err != nil || !reflect.DeepEqual(got, intents[region]) {
			t.Fatalf("approved obligations changed: %v", err)
		}
		windows, err := store.ListSettlementWindows()
		if err != nil || len(windows) != 1 {
			t.Fatal("window history lost", err)
		}
		sources, err := store.RevenueSources(nil)
		if err != nil || len(sources) != 1 || sources[0].Source.PoolID != identities[region] {
			t.Fatal("source identity lost", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	authority, err = OpenOwnership(filepath.Join(restored, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	got, err := authority.Get(record.DeviceID)
	if err != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("ownership generation lost: %+v %v", got, err)
	}
	if _, err := authority.Change("claim", "stale-source", ownership.Request{DeviceID: record.DeviceID, PoolID: identities["eu"], EnrollmentID: "eu-host", ExpectedGeneration: record.Generation, MemberWallet: "member"}); err == nil {
		t.Fatal("restored source revived obsolete assignment")
	}
}
