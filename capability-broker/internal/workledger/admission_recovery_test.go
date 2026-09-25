package workledger

import (
	"context"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

type recoveredRevisionReceiver struct{ revisionReceiver }

func (r *recoveredRevisionReceiver) CancelAuthorizationAdmission(context.Context, []byte) (*payment.CanceledAdmission, error) {
	return &payment.CanceledAdmission{Authorization: &payment.SpendAuthorizationStatus{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED), Billed: big.NewInt(7), ActualUnits: 5}}, nil
}

func TestCanceledAdmissionRecoveryPreservesRegionalBaseline(t *testing.T) {
	s, _ := testStore(t)
	bind(t, s, "z-before")
	raw, _ := proto.Marshal(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{AuthorizationId: "a-after", PredecessorAuthorizationId: "z-before", SettlementDomainId: "source", Payer: make([]byte, 20)}})
	remote := &recoveredRevisionReceiver{revisionReceiver{loseResponse: true}}
	client := &Client{Store: s, AccountClient: remote}
	if _, err := client.AdmitAuthorization(context.Background(), payment.AdmitAuthorizationRequest{AuthorizationBytes: raw}); err == nil {
		t.Fatal("expected lost response")
	}
	result, err := client.CancelAuthorizationAdmission(context.Background(), raw)
	if err != nil || result.Canceled {
		t.Fatalf("lost accepted authority: %+v %v", result, err)
	}
	if _, err = s.Prepare(Operation{AuthorizationID: "a-after", Kind: "settle", Sequence: 2, Units: 5}); err != nil {
		t.Fatal("inherited baseline missing after cancellation recovery", err)
	}
	if err = s.SetInheritedBaseline("a-after", "z-before", big.NewInt(8), 5); err == nil {
		t.Fatal("receiver baseline was not preserved")
	}
}

func TestCanceledUnusedAuthorizationDoesNotBlockDeviceDrain(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	receiver := &deviceReceiver{state: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_CANCELED_UNUSED)}
	client := &Client{Store: s, AccountClient: receiver}
	id := "refused-successor"
	raw := deviceAuth(t, id, "previous")
	if err := s.SaveAuthorization(id, raw); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(id, Attribution{Member: "member", Enrollment: "host", Backend: "host|gpu", Capability: "cap", Offering: "offer", DeviceOwnership: map[string]uint64{"gpu": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := client.BeginDeviceDrain("host", "gpu", 1, "revision refused"); err != nil {
		t.Fatal(err)
	}
	proof, err := client.DeviceDrainStatus(ctx, "host", "gpu", 1)
	if err != nil || proof.ActiveAuthorizations+proof.PendingOperations+proof.UnqualifiedWork != 0 {
		t.Fatalf("canceled admission blocked drain: %+v %v", proof, err)
	}
}
