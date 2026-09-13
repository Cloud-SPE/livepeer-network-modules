package types

import (
	"bytes"
	"math/big"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

func TestExpectedValueMath(t *testing.T) {
	if EV(nil, big.NewInt(1)) != nil || EV(big.NewInt(1), nil) != nil {
		t.Fatal("EV with a nil operand must be nil")
	}
	if got := EV(big.NewInt(25), MaxWinProb); got.Cmp(big.NewRat(25, 1)) != 0 {
		t.Fatalf("full-probability EV = %s, want 25", got)
	}
	if got := CreditedEV(nil, MaxWinProb); got.Sign() != 0 {
		t.Fatalf("nil credited EV = %s, want 0", got)
	}
	if got := CreditedEV(big.NewInt(25), MaxWinProb); got.Int64() != 25 {
		t.Fatalf("credited EV = %s, want 25", got)
	}
}

func TestTicketBatchWireConversion(t *testing.T) {
	if (*TicketBatch)(nil).ToWirePayment() != nil {
		t.Fatal("nil batch must convert to nil")
	}
	b := &TicketBatch{
		Sender:           []byte{1, 2},
		ExpirationParams: &TicketExpirationParams{CreationRound: 9, CreationRoundBlockHash: []byte{3}},
		TicketParams: &TicketParams{
			Recipient: []byte{4}, FaceValue: big.NewInt(5), WinProb: big.NewInt(6),
			RecipientRandHash: []byte{7}, Seed: []byte{8}, ExpirationBlock: big.NewInt(10),
			ExpirationParams: &TicketExpirationParams{CreationRound: 11, CreationRoundBlockHash: []byte{12}},
		},
		ExpectedPrice:      &PriceInfo{PricePerUnit: 13, PixelsPerUnit: 14, Capability: 15, Constraint: "gpu"},
		TicketSenderParams: []*TicketSenderParams{{SenderNonce: 16, Sig: []byte{17}}},
	}
	w := b.ToWirePayment()
	if !bytes.Equal(w.GetSender(), b.Sender) || w.GetExpirationParams().GetCreationRound() != 9 {
		t.Fatalf("wire envelope mismatch: %+v", w)
	}
	if got := new(big.Int).SetBytes(w.GetTicketParams().GetFaceValue()); got.Int64() != 5 {
		t.Fatalf("face value = %s, want 5", got)
	}
	if w.GetExpectedPrice().GetConstraint() != "gpu" || w.GetTicketSenderParams()[0].GetSenderNonce() != 16 {
		t.Fatalf("wire nested fields mismatch: %+v", w)
	}
	roundTrip := TicketParamsFromWire(w.GetTicketParams())
	if roundTrip.FaceValue.Int64() != 5 || roundTrip.ExpirationParams.CreationRound != 11 {
		t.Fatalf("round trip mismatch: %+v", roundTrip)
	}
	if TicketParamsFromWire(nil) != nil || expirationParamsToWire(nil) != nil || ticketParamsToWire(nil) != nil || priceInfoToWire(nil) != nil || bigIntBytes(nil) != nil {
		t.Fatal("nil conversion helpers must return nil")
	}
	withoutExpiration := TicketParamsFromWire(&pb.TicketParams{FaceValue: []byte{1}})
	if withoutExpiration.ExpirationParams != nil {
		t.Fatal("absent expiration params became present")
	}
}

func TestParseFaceValue(t *testing.T) {
	got, err := ParseFaceValue([]byte{0x01, 0x02})
	if err != nil || got.Int64() != 258 {
		t.Fatalf("ParseFaceValue = %v, %v", got, err)
	}
	if _, err := ParseFaceValue(nil); err == nil {
		t.Fatal("empty face value accepted")
	}
	if _, err := ParseFaceValue(make([]byte, 33)); err == nil {
		t.Fatal("oversized face value accepted")
	}
}
