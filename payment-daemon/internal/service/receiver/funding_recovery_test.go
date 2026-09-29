package receiver_test

import (
	"math/big"
	"strings"
	"testing"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestFundingReceiptLookupIsReadOnlyAndSurvivesSourceFreeze(t *testing.T) {
	client, st, cleanup := stand(t)
	defer cleanup()
	payer, payee := bytes20(1), bytes20(0xaa)
	domain := "0x" + strings.Repeat("a", 64)
	_, _, err := st.GetOrCreateTicketSession(store.TicketSessionKey{Sender: payer, Recipient: payee, Capability: "c", Offering: "o", WholesaleAccountID: "test-account", TicketStreamID: "stream"}, store.Session{WorkID: "receipt-generation", RecipientRand: "123"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st.ApplyWholesaleFunding(payer, payee, "test-account", "receipt-generation", strings.Repeat("a", 64), []store.FundingTicket{{Nonce: 1, Credit: big.NewInt(1000)}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	base := &pb.GetWholesaleFundingReceiptRequest{Payer: payer, SettlementDomainId: domain, WholesaleAccountId: "test-account", FundingId: strings.Repeat("a", 64)}
	if _, err = client.FreezeRevenueSource(t.Context(), &pb.FreezeRevenueSourceRequest{SettlementDomainId: domain, Reason: "test receipt recovery"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*pb.GetWholesaleFundingReceiptRequest)
		want   codes.Code
	}{
		{"found", func(*pb.GetWholesaleFundingReceiptRequest) {}, codes.OK},
		{"missing", func(r *pb.GetWholesaleFundingReceiptRequest) { r.FundingId = strings.Repeat("b", 64) }, codes.NotFound},
		{"account", func(r *pb.GetWholesaleFundingReceiptRequest) { r.WholesaleAccountId = "other-account" }, codes.PermissionDenied},
		{"payer", func(r *pb.GetWholesaleFundingReceiptRequest) { r.Payer = bytes20(2) }, codes.PermissionDenied},
		{"domain", func(r *pb.GetWholesaleFundingReceiptRequest) { r.SettlementDomainId = "0x" + strings.Repeat("b", 64) }, codes.PermissionDenied},
		{"invalid_payer", func(r *pb.GetWholesaleFundingReceiptRequest) { r.Payer = nil }, codes.InvalidArgument},
		{"invalid_digest", func(r *pb.GetWholesaleFundingReceiptRequest) { r.FundingId = "bad" }, codes.InvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := proto.Clone(base).(*pb.GetWholesaleFundingReceiptRequest)
			test.change(req)
			result, err := client.GetWholesaleFundingReceipt(t.Context(), req)
			if status.Code(err) != test.want {
				t.Fatalf("lookup=%v want=%v", err, test.want)
			}
			if test.want == codes.OK && (!result.GetReplayed() || new(big.Int).SetBytes(result.GetCreditedValueWei().GetValue()).Int64() != 1000) {
				t.Fatalf("receipt=%v", result)
			}
		})
	}
	account, err := st.GetWholesaleAccount(payer, payee, "test-account")
	if err != nil || account.CreditedWei != "1000" || account.ReservedWei != "0" || account.DebitedWei != "0" {
		t.Fatalf("lookup mutated account: %+v %v", account, err)
	}
}
