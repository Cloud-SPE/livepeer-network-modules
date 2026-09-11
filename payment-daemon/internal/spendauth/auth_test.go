package spendauth

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
)

func TestVerify(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ks, err := inmemory.New(key)
	if err != nil {
		t.Fatal(err)
	}
	payload := &pb.SpendAuthorizationPayload{
		Domain: Domain, Payer: ks.Address(), Payee: make([]byte, 20),
		AuthorizationId: "auth-1", RequestId: "request-1", Protocol: "paid-job/v1",
		Capability: "custom:any", Offering: "offer", MaxDebitWei: &pb.BigUInt{Value: []byte{10}},
	}
	digest, err := Digest(payload)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ks.Sign(digest)
	if err != nil {
		t.Fatal(err)
	}
	auth := &pb.SpendAuthorization{Payload: payload, Signature: sig}
	if err := Verify(auth); err != nil {
		t.Fatalf("verify: %v", err)
	}
	auth.Payload.RequestId = "tampered"
	if err := Verify(auth); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tamper error = %v", err)
	}
}

func TestDigestAndVerifyRejectMalformedAuthorizations(t *testing.T) {
	if _, err := Digest(nil); err == nil {
		t.Fatal("nil payload accepted")
	}
	if _, err := Digest(&pb.SpendAuthorizationPayload{Domain: "wrong-domain"}); err == nil {
		t.Fatal("wrong domain accepted")
	}
	if err := Verify(nil); err == nil {
		t.Fatal("nil authorization accepted")
	}
	if err := Verify(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{Domain: Domain, Payer: []byte{1}}}); err == nil {
		t.Fatal("short payer accepted")
	}
	payload := &pb.SpendAuthorizationPayload{Domain: Domain, Payer: make([]byte, 20)}
	if err := Verify(&pb.SpendAuthorization{Payload: payload, Signature: make([]byte, 64)}); !errors.Is(err, ErrMalformedSignature) {
		t.Fatalf("short signature error = %v", err)
	}
	badRecovery := make([]byte, 65)
	badRecovery[64] = 1
	if err := Verify(&pb.SpendAuthorization{Payload: payload, Signature: badRecovery}); !errors.Is(err, ErrMalformedSignature) {
		t.Fatalf("non-canonical recovery id error = %v", err)
	}
}
