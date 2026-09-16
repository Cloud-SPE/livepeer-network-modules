package workledger

import (
	"context"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"math/big"
	"testing"
	"time"
)

func TestDrainRetainsBillingAndDeliveryObligationsAcrossRestart(t *testing.T) {
	s, path := testStore(t)
	bind(t, s, "job")
	c := &Client{Store: s}
	op, err := s.Prepare(Operation{AuthorizationID: "job", Kind: "settle", Sequence: 1, Units: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.BeginDrain("regional source retirement"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AdmitAuthorization(context.Background(), payment.AdmitAuthorizationRequest{}); err == nil {
		t.Fatal("draining source admitted new work")
	}
	state, err := s.DrainStatus()
	if err != nil || !state.Draining || state.PendingOperations != 1 {
		t.Fatalf("pending drain %+v %v", state, err)
	}
	if err = s.Complete(op.ID, big.NewInt(5)); err != nil {
		t.Fatal(err)
	}
	if err = s.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err = s.DrainStatus()
	if err != nil || !state.Draining || state.PendingOperations != 0 || state.UndeliveredReceipts != 1 || state.LastReceiptRound != 100 {
		t.Fatalf("restart drain %+v %v", state, err)
	}
	queue, _ := s.Undelivered(0)
	if err = s.Delivered(queue[0].ID); err != nil {
		t.Fatal(err)
	}
	state, err = s.DrainStatus()
	if err != nil || state.UndeliveredReceipts != 0 {
		t.Fatalf("delivered drain %+v %v", state, err)
	}
}
