// Package compat hosts the wire-compat round-trip test against the
// canonical fixture produced by fixturegen.
//
// The test reads `testdata/payment-canonical.bin` (a Payment marshalled
// by go-livepeer's `net.Payment` proto type), unmarshals it into our
// generated `livepeer/payments/v1.Payment`, re-marshals it, and asserts
// byte-identity. Identity proves our generated bindings agree with
// go-livepeer's on the wire format — which is the contract plan 0014
// pinned and plan 0016 enforces.
//
// To regenerate the fixture (e.g. after bumping the go-livepeer pin in
// fixturegen/go.mod):
//
//	cd fixturegen && go run .
package compat

import (
	"bytes"
	_ "embed"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

//go:embed testdata/payment-canonical.bin
var canonicalPayment []byte

func TestPaymentWireCompat_RoundTrip(t *testing.T) {
	if len(canonicalPayment) == 0 {
		t.Fatal("canonical fixture is empty; run `cd fixturegen && go run .`")
	}

	var pay pb.Payment
	if err := proto.Unmarshal(canonicalPayment, &pay); err != nil {
		t.Fatalf("unmarshal canonical fixture: %v", err)
	}

	got, err := proto.Marshal(&pay)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}

	if !bytes.Equal(got, canonicalPayment) {
		t.Errorf("round-trip mismatch:\n  len(got) = %d, len(want) = %d\n  got  = %x\n  want = %x",
			len(got), len(canonicalPayment), got, canonicalPayment)
	}
}

func TestPaymentWireCompat_FieldsPresent(t *testing.T) {
	var pay pb.Payment
	if err := proto.Unmarshal(canonicalPayment, &pay); err != nil {
		t.Fatalf("unmarshal canonical fixture: %v", err)
	}

	if got, want := len(pay.GetSender()), 20; got != want {
		t.Errorf("sender length = %d; want %d", got, want)
	}
	// The fixturegen pads sender with 0xa0..0xb3.
	if pay.GetSender()[0] != 0xa0 {
		t.Errorf("sender[0] = %#x; want 0xa0 (fixturegen pattern)", pay.GetSender()[0])
	}
	if got := pay.GetTicketParams(); got == nil {
		t.Fatal("ticket_params is nil")
	}
	if got := len(pay.GetTicketParams().GetRecipient()); got != 20 {
		t.Errorf("recipient length = %d; want 20", got)
	}
	if got := pay.GetExpirationParams(); got == nil {
		t.Fatal("expiration_params is nil")
	}
	if got, want := pay.GetExpirationParams().GetCreationRound(), int64(12345); got != want {
		t.Errorf("creation_round = %d; want %d", got, want)
	}
	if got := len(pay.GetTicketSenderParams()); got != 3 {
		t.Errorf("ticket_sender_params count = %d; want 3", got)
	}
	for i, tsp := range pay.GetTicketSenderParams() {
		if got, want := len(tsp.GetSig()), 65; got != want {
			t.Errorf("sig[%d] length = %d; want %d", i, got, want)
		}
		if got, want := tsp.GetSenderNonce(), uint32(i+1); got != want {
			t.Errorf("sender_nonce[%d] = %d; want %d", i, got, want)
		}
	}
}
