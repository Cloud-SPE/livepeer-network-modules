package repo

import (
	bolt "go.etcd.io/bbolt"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
)

func TestExclusiveRegionalOwnershipAndFencedTransfer(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	requests := []ownership.Request{{DeviceID: "GPU-one", PoolID: "eu", EnrollmentID: "host-eu", MemberWallet: "wallet"}, {DeviceID: "GPU-one", PoolID: "us", EnrollmentID: "host-us", MemberWallet: "wallet"}}
	var wg sync.WaitGroup
	wins := make(chan ownership.Record, 2)
	for _, req := range requests {
		wg.Go(func() {
			if record, err := store.Change("claim", "controller", req); err == nil {
				wins <- record
			}
		})
	}
	wg.Wait()
	close(wins)
	var winner ownership.Record
	count := 0
	for w := range wins {
		winner = w
		count++
	}
	if count != 1 {
		t.Fatalf("%d winners", count)
	}
	target := "eu"
	if winner.PoolID == target {
		target = "us"
	}
	transfer := ownership.Request{DeviceID: winner.DeviceID, PoolID: winner.PoolID, EnrollmentID: winner.EnrollmentID, ExpectedGeneration: winner.Generation, DestinationPoolID: target}
	if _, err := store.Change("release", "controller", transfer); err == nil {
		t.Fatal("released without drain")
	}
	if _, err := store.Change("drain", "controller", transfer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Change("release", "controller", transfer); err == nil {
		t.Fatal("released without evidence")
	}
	transfer.DrainEvidence = "all accepted work finalized"
	transfer.RevocationEvidence = "old device admission revoked"
	transfer.StopEvidence = "agent stopped device services"
	if _, err := store.Change("release", "controller", transfer); err != nil {
		t.Fatal(err)
	}
	oldClaim := ownership.Request{DeviceID: winner.DeviceID, PoolID: winner.PoolID, EnrollmentID: winner.EnrollmentID, ExpectedGeneration: winner.Generation, MemberWallet: "wallet"}
	if _, err := store.Change("claim", "restored-source", oldClaim); err == nil {
		t.Fatal("restored source reclaimed released device")
	}
	// Simulate a database from before the release index existed. Reopening
	// must recover replay receipts from the atomic historical audit events.
	if err := store.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("release_receipts")) }); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.Get(winner.DeviceID)
	if err != nil || record.State != "released" {
		t.Fatalf("lost release: %+v %v", record, err)
	}
	if _, err := store.Change("claim", "destination", ownership.Request{DeviceID: winner.DeviceID, PoolID: target, EnrollmentID: "new-host", ExpectedGeneration: winner.Generation, MemberWallet: "another-wallet"}); err == nil {
		t.Fatal("transfer changed member wallet")
	}
	next, err := store.Change("claim", "destination", ownership.Request{DeviceID: winner.DeviceID, PoolID: target, EnrollmentID: "new-host", ExpectedGeneration: winner.Generation, MemberWallet: "wallet"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != winner.Generation+1 {
		t.Fatal("generation not advanced")
	}
	replay, err := store.Change("release", "restored-source", transfer)
	if err != nil || replay.State != "released" || replay.Generation != winner.Generation {
		t.Fatalf("source release replay: %+v %v", replay, err)
	}
	current, err := store.Get(winner.DeviceID)
	if err != nil || current != next {
		t.Fatal("release replay changed target ownership", current, err)
	}
	conflict := transfer
	conflict.StopEvidence = "changed"
	if _, err := store.Change("release", "restored-source", conflict); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	other, err := store.Change("claim", "controller", ownership.Request{DeviceID: "GPU-two", PoolID: winner.PoolID, EnrollmentID: winner.EnrollmentID, MemberWallet: "wallet"})
	if err != nil || other.PoolID != winner.PoolID {
		t.Fatal("device transfer disturbed sibling GPU")
	}
}
