package receiver_test

import (
	"context"
	"crypto/sha256"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/spendauth"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
)

func signedAuthorization(t *testing.T, ks *inmemory.KeyStore, id string, payee []byte, maxUnits uint64) []byte {
	t.Helper()
	now := time.Now().UTC()
	requestDigest := sha256.Sum256([]byte("request body"))
	payload := &pb.SpendAuthorizationPayload{
		Domain: spendauth.Domain, Payer: ks.Address(), Payee: payee,
		ChainId: 42161, Denomination: "wei",
		AuthorizationId: id, RequestId: "request-" + id, BrokerUri: "https://broker.example",
		Protocol: "paid-job/v1", Capability: "custom:any", Offering: "offer",
		AcceptedPrice: &pb.AcceptedPrice{
			PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(10).Bytes()}, UnitsPerPrice: 1,
			WorkUnitName: "widgets", Capability: "custom:any", Offering: "offer",
		},
		MaxDebitWei:   &pb.BigUInt{Value: new(big.Int).Mul(big.NewInt(10), new(big.Int).SetUint64(maxUnits)).Bytes()},
		MaxTotalUnits: maxUnits, NotBefore: now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:     now.Add(time.Hour).Format(time.RFC3339Nano),
		RequestDigest: requestDigest[:],
	}
	digest, err := spendauth.Digest(payload)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ks.Sign(digest)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&pb.SpendAuthorization{Payload: payload, Signature: sig})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestWholesaleAuthorizationRPC(t *testing.T) {
	client, st, cleanup := stand(t)
	defer cleanup()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ks, err := inmemory.New(key)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := ks.Address(), bytes20(0xaa)

	// Seed reusable wholesale credit by migrating one ticket-generation
	// balance, exactly what the account admission path does after validation.
	if _, _, err := st.OpenSession(store.Session{WorkID: "generation", Capability: "custom:any", Offering: "offer", PricePerWorkUnitWei: "10", PerUnits: 1, WorkUnit: "widgets"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SealSender("generation", payer); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreditBalance(payer, "generation", big.NewInt(1000)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bootstrap := store.WholesaleAuthorizationSeed{ID: "bootstrap", Fingerprint: []byte("bootstrap"), Payer: payer, Payee: payee, RequestID: "bootstrap", Protocol: "paid-job/v1", Capability: "custom:any", Offering: "offer", PriceWei: "1", PerUnits: 1, WorkUnit: "widgets", MaxDebitWei: "1", MaxTotalUnits: 1, ExpiresAt: now.Add(time.Hour)}
	if _, err := st.AdmitWholesale(bootstrap, "generation", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SettleWholesale(payer, payee, "bootstrap", 0, 1, now); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	auth := signedAuthorization(t, ks, "job-1", payee, 10)
	admitted, err := client.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: auth})
	if err != nil {
		t.Fatal(err)
	}
	if admitted.GetState() != pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED || new(big.Int).SetBytes(admitted.GetAccount().GetAvailableValueWei().GetValue()).Int64() != 900 {
		t.Fatalf("admitted=%+v", admitted)
	}
	// A lost gRPC response is retried with the exact authorization. The
	// durable authorization must win before payment parsing/nonce validation;
	// otherwise callers either fail a valid retry or mint a duplicate ticket.
	admitReplay, err := client.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: auth, PaymentBytes: []byte("must-not-be-processed")})
	if err != nil || !admitReplay.GetReplayed() || len(admitReplay.GetCreditedValueWei().GetValue()) != 0 {
		t.Fatalf("admission replay=%+v err=%v", admitReplay, err)
	}
	advanced, err := client.AdvanceAuthorization(ctx, &pb.AdvanceAuthorizationRequest{
		Payer: payer, AuthorizationId: "job-1", CumulativeUnits: 2,
		TargetReservedValueWei: &pb.BigUInt{Value: big.NewInt(80).Bytes()}, AdvanceSeq: 1,
	})
	if err != nil || new(big.Int).SetBytes(advanced.GetBilledDeltaWei().GetValue()).Int64() != 20 {
		t.Fatalf("advanced=%+v err=%v", advanced, err)
	}
	advanceReplay, err := client.AdvanceAuthorization(ctx, &pb.AdvanceAuthorizationRequest{
		Payer: payer, AuthorizationId: "job-1", CumulativeUnits: 2,
		TargetReservedValueWei: &pb.BigUInt{Value: big.NewInt(80).Bytes()}, AdvanceSeq: 1,
		PaymentBytes: []byte("must-not-be-processed"),
	})
	if err != nil || !advanceReplay.GetReplayed() || len(advanceReplay.GetCreditedValueWei().GetValue()) != 0 {
		t.Fatalf("advance replay=%+v err=%v", advanceReplay, err)
	}
	settled, err := client.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{Payer: payer, AuthorizationId: "job-1", ActualUnits: 4, SettlementSeq: 2})
	if err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(settled.GetBilledValueWei().GetValue()).Int64() != 40 || new(big.Int).SetBytes(settled.GetReleasedValueWei().GetValue()).Int64() != 60 {
		t.Fatalf("settled=%+v", settled)
	}
	replay, err := client.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{Payer: payer, AuthorizationId: "job-1", ActualUnits: 4, SettlementSeq: 2})
	if err != nil || !replay.GetReplayed() {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}
