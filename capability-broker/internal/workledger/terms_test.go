package workledger

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

type termsReceiver struct {
	payment.AccountClient
	active uint64
}

func (r *termsReceiver) FreezeRevenueSource(context.Context, string) (*pb.RevenueSourceStatus, error) {
	panic("terms transition must not freeze receiver")
}
func (r *termsReceiver) RevenueSourceStatus(context.Context) (*pb.RevenueSourceStatus, error) {
	return &pb.RevenueSourceStatus{SettlementDomainId: "source", ActiveAuthorizations: r.active}, nil
}
func TestTermsPauseQuiescenceFutureActivationAndRestart(t *testing.T) {
	store, path := testStore(t)
	receiver := &termsReceiver{}
	client := &Client{Store: store, AccountClient: receiver, RequireTerms: true}
	if err := store.TermsPermitAdmission(); err == nil {
		t.Fatal("missing terms admitted work")
	}
	paused, err := client.PauseTerms(0, "initial terms")
	if err != nil || !paused.Paused || paused.Revision != 1 {
		t.Fatalf("pause %+v %v", paused, err)
	}
	replay, err := client.PauseTerms(0, "initial terms")
	if err != nil || replay.Revision != 1 {
		t.Fatal("pause replay changed revision")
	}
	if err := store.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	receiver.active = 1
	if _, err := client.ActivateTerms(context.Background(), 1, "v1", 101); err == nil {
		t.Fatal("active authorization crossed terms change")
	}
	receiver.active = 0
	policy, err := client.ActivateTerms(context.Background(), 1, "v1", 101)
	if err != nil || policy.Revision != 2 {
		t.Fatalf("activate %+v %v", policy, err)
	}
	if err := store.TermsPermitAdmission(); err == nil {
		t.Fatal("future policy admitted work")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	client.Store = store
	if err := store.Finalize(101, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.TermsPermitAdmission(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PauseTerms(1, "stale request"); err == nil {
		t.Fatal("stale revision paused current policy")
	}
	if _, err := client.PauseTerms(2, "next terms"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ActivateTerms(context.Background(), 3, "v2", 101); err == nil {
		t.Fatal("successor retroactively changed active terms")
	}
	if _, err := client.ActivateTerms(context.Background(), 3, "v1", 115); err == nil {
		t.Fatal("broker rewrote immutable terms version timing")
	}

	if err := store.TermsPermitAdmission(); err == nil {
		t.Fatal("paused policy admitted work")
	}
}

func TestFinalizedWorkPreservesAdmissionTermsAndRejectsRebinding(t *testing.T) {
	store, _ := testStore(t)
	original := Attribution{Member: "member", Backend: "host|gpu", Enrollment: "host", Capability: "cap", Offering: "offer", RequestID: "request", TermsVersion: "v1"}
	if err := store.Bind("work", original); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.TermsVersion = "v2"
	if err := store.Bind("work", changed); err == nil {
		t.Fatal("in-flight work repriced to new terms")
	}
	op, err := store.Prepare(Operation{AuthorizationID: "work", Kind: "settle", Sequence: 1, Units: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(op.ID, big.NewInt(7)); err != nil {
		t.Fatal(err)
	}
	if err := store.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	receipts, err := store.Undelivered(10)
	if err != nil || len(receipts) != 1 || receipts[0].TermsVersion != "v1" {
		t.Fatalf("terms lost %+v %v", receipts, err)
	}
}

func TestEmptyBrokerBootstrapCanResumeAfterInitialBoundary(t *testing.T) {
	store, _ := testStore(t)
	client := &Client{Store: store, AccountClient: &termsReceiver{}, RequireTerms: true}
	if _, err := client.PauseTerms(0, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if err := store.Finalize(110, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ActivateTerms(context.Background(), 1, "v1", 100); err != nil {
		t.Fatal(err)
	}
	if err := store.TermsPermitAdmission(); err != nil {
		t.Fatal(err)
	}
}
