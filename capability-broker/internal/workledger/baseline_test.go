package workledger

import (
	"context"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
	"math/big"
	"testing"
	"time"
)

type revisionReceiver struct {
	payment.AccountClient
	loseResponse bool
}

func (r *revisionReceiver) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	if r.loseResponse {
		r.loseResponse = false
		return nil, fmt.Errorf("successor admitted but response lost")
	}
	return &payment.AdmitAuthorizationResult{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED)}, nil
}
func (r *revisionReceiver) GetSpendAuthorization(_ context.Context, _ []byte, id string) (*payment.SpendAuthorizationStatus, error) {
	if id == "a-after" {
		return &payment.SpendAuthorizationStatus{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED)}, nil
	}
	return &payment.SpendAuthorizationStatus{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED), Billed: big.NewInt(7), ActualUnits: 5}, nil
}
func TestSuccessorBaselinePreventsReemittingInheritedWorkAfterRestart(t *testing.T) {
	s, path := testStore(t)
	bind(t, s, "z-before")
	prior, err := s.Prepare(Operation{AuthorizationID: "z-before", Kind: "advance", Sequence: 1, Units: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(prior.ID, big.NewInt(7)); err != nil {
		t.Fatal(err)
	}
	// Leave predecessor unfinalized; successor sorts first. Emit order cannot
	// establish the inherited receiver baseline.
	raw, _ := proto.Marshal(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{AuthorizationId: "a-after", PredecessorAuthorizationId: "z-before", SettlementDomainId: "source", Payer: make([]byte, 20)}})
	remote := &revisionReceiver{loseResponse: true}
	client := &Client{Store: s, AccountClient: remote}
	request := payment.AdmitAuthorizationRequest{AuthorizationBytes: raw}
	if _, err = client.AdmitAuthorization(context.Background(), request); err == nil {
		t.Fatal("expected lost successor response")
	}
	next := Operation{AuthorizationID: "a-after", Kind: "advance", Sequence: 2, Units: 8}
	if _, err = s.Prepare(next); err == nil {
		t.Fatal("successor billed without inherited evidence")
	}
	s.Close()
	s, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client.Store = s
	if err = s.BeginDrain("operator drain during revision recovery"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.AdmitAuthorization(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	op, err := s.Prepare(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(op.ID, big.NewInt(11)); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.Prepare(Operation{AuthorizationID: "a-after", Kind: "settle", Sequence: 3, Units: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(terminal.ID, big.NewInt(11)); err != nil {
		t.Fatal(err)
	}
	if err = s.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = client.AdmitAuthorization(context.Background(), request); err != nil {
		t.Fatal("admission replay rewrote baseline", err)
	}
	receipts, err := s.Undelivered(0)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("receipts %+v %v", receipts, err)
	}
	total := new(big.Int)
	units := uint64(0)
	for _, receipt := range receipts {
		n, _ := new(big.Int).SetString(receipt.AttributedRevenueWei, 10)
		total.Add(total, n)
		units += receipt.ActualUnits
	}
	if total.Int64() != 11 || units != 8 {
		t.Fatalf("inherited work duplicated: billed=%s units=%d", total, units)
	}
	if err = s.SetInheritedBaseline("a-after", "z-before", big.NewInt(8), 5); err == nil {
		t.Fatal("inherited baseline rewritten")
	}
}
