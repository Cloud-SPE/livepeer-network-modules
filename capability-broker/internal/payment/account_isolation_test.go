package payment

import (
	"bytes"
	"math/big"
	"path/filepath"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestReceiverAccountEvidenceMustMatchScope(t *testing.T) {
	payer := bytes.Repeat([]byte{1}, 20)
	g := &GRPC{settlementDomainID: MockSettlementDomainID}
	valid := &pb.WholesaleAccountView{Payer: payer, WholesaleAccountId: "loc-prod", SettlementDomainId: MockSettlementDomainID}
	if err := g.validateAccount(valid, payer, "loc-prod"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*pb.WholesaleAccountView){
		func(a *pb.WholesaleAccountView) { a.WholesaleAccountId = "blueclaw-prod" },
		func(a *pb.WholesaleAccountView) { a.WholesaleAccountId = "" },
		func(a *pb.WholesaleAccountView) { a.SettlementDomainId = "another-ledger" },
		func(a *pb.WholesaleAccountView) { a.Payer = bytes.Repeat([]byte{2}, 20) },
	} {
		a := proto.Clone(valid).(*pb.WholesaleAccountView)
		change(a)
		if err := g.validateAccount(a, payer, "loc-prod"); err == nil {
			t.Fatalf("accepted mismatched evidence: %+v", a)
		}
	}
	if err := g.validateAccount(nil, payer, "loc-prod"); err == nil {
		t.Fatal("accepted missing evidence")
	}
}

func TestMockCancellationScopeSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock.json")
	m := NewMock()
	if err := m.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	payer := bytes.Repeat([]byte{1}, 20)
	payload := &pb.SpendAuthorizationPayload{WholesaleAccountId: "loc-prod", Payer: payer, AuthorizationId: "shared-id", MaxDebitWei: &pb.BigUInt{Value: big.NewInt(100).Bytes()}}
	wire, err := proto.Marshal(&pb.SpendAuthorization{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := m.CancelAuthorizationAdmission(t.Context(), wire); err != nil || !out.Canceled {
		t.Fatalf("cancel: %+v %v", out, err)
	}
	m = NewMock()
	if err := m.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if out, err := m.GetSpendAuthorization(t.Context(), payer, "shared-id", "loc-prod"); err != nil || out.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_CANCELED_UNUSED) {
		t.Fatalf("fence lost: %+v %v", out, err)
	}
	if _, err := m.GetSpendAuthorization(t.Context(), payer, "shared-id", "other-account"); err == nil {
		t.Fatal("fence crossed account")
	}
	payload.WholesaleAccountId = "other-account"
	otherWire, err := proto.Marshal(&pb.SpendAuthorization{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdmitAuthorization(t.Context(), AdmitAuthorizationRequest{AuthorizationBytes: otherWire, Reservation: big.NewInt(10)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdmitAuthorization(t.Context(), AdmitAuthorizationRequest{AuthorizationBytes: wire, Reservation: big.NewInt(10)}); err == nil {
		t.Fatal("canceled authority admitted")
	}
}
