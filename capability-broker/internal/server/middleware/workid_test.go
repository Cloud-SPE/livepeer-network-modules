package middleware

import (
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestDerivePayeeWorkID_FromRecipientRandHash(t *testing.T) {
	hash := []byte("0123456789abcdef0123456789abcdef")
	raw, err := proto.Marshal(&pb.Payment{
		TicketParams: &pb.TicketParams{RecipientRandHash: hash},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := DerivePayeeWorkID(raw)
	if !ok {
		t.Fatal("expected derived work id")
	}
	want := "3031323334353637383961626364656630313233343536373839616263646566"
	if got != want {
		t.Fatalf("work id = %s; want %s", got, want)
	}
}

func TestDerivePayeeWorkID_FallbackForStubBytes(t *testing.T) {
	if got, ok := DerivePayeeWorkID([]byte("not-a-payment")); ok || got != "" {
		t.Fatalf("expected no derived work id, got %q ok=%v", got, ok)
	}
}

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
