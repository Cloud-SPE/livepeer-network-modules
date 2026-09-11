package middleware

import (
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestDerivePaymentRoundID(t *testing.T) {
	raw, err := proto.Marshal(&pb.Payment{
		ExpirationParams: &pb.TicketExpirationParams{CreationRound: 124},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := DerivePaymentRoundID(raw); got != "124" {
		t.Fatalf("round id = %q; want 124", got)
	}
}

func TestDerivePaymentRoundID_EmptyForStubBytes(t *testing.T) {
	if got := DerivePaymentRoundID([]byte("not-a-payment")); got != "" {
		t.Fatalf("expected empty round id, got %q", got)
	}
}
