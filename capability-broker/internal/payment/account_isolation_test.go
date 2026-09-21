package payment

import (
	"bytes"
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
