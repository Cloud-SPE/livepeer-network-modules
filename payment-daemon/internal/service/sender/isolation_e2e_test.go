package sender_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"sync"
	"testing"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestSharedWalletIndependentStreamsAndExactFundingReceipts(t *testing.T) {
	ctx := context.Background()
	recipient := bytes20(0xd3)
	payee, st, closePayee := receiverStand(t, recipient)
	defer closePayee()
	a, urlA, storeA, closeA := devModeSenderStandOn(t, func() pb.PayeeDaemonClient { return payee }, nil)
	defer closeA()
	b, urlB, storeB, closeB := devModeSenderStandOn(t, func() pb.PayeeDaemonClient { return payee }, nil)
	defer closeB()
	streamA, _ := storeA.TicketStreamID()
	streamB, _ := storeB.TicketStreamID()
	if streamA == streamB || streamA == "" {
		t.Fatal("independent databases share ticket stream")
	}
	mint := func(client pb.PayerDaemonClient, url, account, id string) *pb.CreatePaymentResponse {
		t.Helper()
		req := devModeCreateRequest(recipient, id, url)
		req.AccountFunding.WholesaleAccountId = account
		out, err := client.CreatePayment(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = payee.OpenSession(ctx, &pb.OpenSessionRequest{WorkId: out.WorkId, Capability: "openai:chat-completions", Offering: "model-a", PricePerWorkUnitWei: big.NewInt(1000).Bytes(), PerUnits: 1, WorkUnit: "tokens"}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ma := mint(a, urlA, "loc-prod", "loc-one")
	mb := mint(b, urlB, "loc-prod", "loc-two")
	var pa, pbwire pb.Payment
	_ = proto.Unmarshal(ma.PaymentBytes, &pa)
	_ = proto.Unmarshal(mb.PaymentBytes, &pbwire)
	if ma.WorkId == mb.WorkId || pa.TicketSenderParams[0].SenderNonce != 1 || pbwire.TicketSenderParams[0].SenderNonce != 1 {
		t.Fatal("independent nonce allocators did not start isolated generations")
	}
	if string(pa.Sender) != string(pbwire.Sender) {
		t.Fatal("test must share wallet")
	}
	fundReq := func(m *pb.CreatePaymentResponse, account string) *pb.FundWholesaleAccountRequest {
		return &pb.FundWholesaleAccountRequest{SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WholesaleAccountId: account, PaymentBytes: m.PaymentBytes}
	}
	var receipts [2]*pb.FundWholesaleAccountResponse
	var errs [2]error
	var wg sync.WaitGroup
	for i, m := range []*pb.CreatePaymentResponse{ma, mb} {
		wg.Add(1)
		go func(i int, m *pb.CreatePaymentResponse) {
			defer wg.Done()
			receipts[i], errs[i] = payee.FundWholesaleAccount(ctx, fundReq(m, "loc-prod"))
		}(i, m)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if new(big.Int).SetBytes(receipts[i].CreditedValueWei.Value).Int64() != 1000 {
			t.Fatal("funding swept another operation")
		}
	}
	mc := mint(b, urlB, "blueclaw-prod", "blueclaw-one")
	if _, err := payee.FundWholesaleAccount(ctx, fundReq(mc, "loc-prod")); err == nil {
		t.Fatal("generation redirected to another account")
	}
	blue, err := payee.FundWholesaleAccount(ctx, fundReq(mc, "blueclaw-prod"))
	if err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(blue.Account.CreditedValueWei.Value).Int64() != 1000 {
		t.Fatal("blueclaw inherited loc balance")
	}
	if _, err := payee.FundWholesaleAccount(ctx, fundReq(ma, "blueclaw-prod")); err == nil {
		t.Fatal("receipt replay redirected across accounts")
	}
	digest := sha256.Sum256(ma.PaymentBytes)
	if receipts[0].FundingId != hex.EncodeToString(digest[:]) {
		t.Fatal("receipt digest mismatch")
	}
	// Re-encoding the same ticket changes its funding ID, not its nonce ownership.
	altered := append(append([]byte(nil), ma.PaymentBytes...), 0xa0, 0x06, 0x01)
	bad := fundReq(ma, "loc-prod")
	bad.PaymentBytes = altered
	if _, err := payee.FundWholesaleAccount(ctx, bad); err == nil {
		t.Fatal("same nonce accepted under alternate envelope")
	}
	now := time.Now().UTC()
	bodyDigest := sha256.Sum256([]byte("body"))
	auth, err := a.CreateSpendAuthorization(ctx, &pb.CreateSpendAuthorizationRequest{WholesaleAccountId: "loc-prod", SettlementDomainId: fundReq(ma, "loc-prod").SettlementDomainId, Payee: recipient, AuthorizationId: "job-one", RequestId: "req-one", Protocol: "paid-job/v1", AcceptedPrice: devModeCreateRequest(recipient, "unused", urlA).AcceptedPrice, MaxDebitWei: &pb.BigUInt{Value: big.NewInt(1000).Bytes()}, MaxTotalUnits: 1, NotBefore: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), RequestDigest: bodyDigest[:], BrokerUri: "https://broker.example", ChainId: 42161, Denomination: "wei"})
	if err != nil {
		t.Fatal(err)
	}
	// Standalone then inline is one credit, even though the work admission is new.
	admitted, err := payee.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: auth.AuthorizationBytes, PaymentBytes: ma.PaymentBytes})
	if err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(admitted.Account.CreditedValueWei.Value).Int64() != 2000 {
		t.Fatal("inline funding duplicated standalone payment")
	}
	if _, err = payee.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{WholesaleAccountId: "blueclaw-prod", SettlementDomainId: fundReq(ma, "loc-prod").SettlementDomainId, Payer: pa.Sender, AuthorizationId: "job-one", ActualUnits: 1, SettlementSeq: 1}); err == nil {
		t.Fatal("cross-account authorization settlement succeeded")
	}
	if _, err = payee.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{WholesaleAccountId: "loc-prod", SettlementDomainId: fundReq(ma, "loc-prod").SettlementDomainId, Payer: pa.Sender, AuthorizationId: "job-one", ActualUnits: 1, SettlementSeq: 1}); err != nil {
		t.Fatal(err)
	}
	replay, err := payee.FundWholesaleAccount(ctx, fundReq(ma, "loc-prod"))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || !proto.Equal(receipts[0].Account, replay.Account) || !proto.Equal(receipts[0].CreditedValueWei, replay.CreditedValueWei) {
		t.Fatal("replay changed receipt after later funding and debit")
	}
	// Legacy wallet balance remains separate and untouched.
	legacy, err := st.GetWholesaleAccount(pa.Sender, recipient)
	if err != nil || legacy.CreditedWei != "0" {
		t.Fatal("named accounts changed legacy ledger")
	}
	if _, err = payee.GetWholesaleAccount(ctx, &pb.GetWholesaleAccountRequest{Payer: pa.Sender, SettlementDomainId: fundReq(ma, "loc-prod").SettlementDomainId}); err == nil {
		t.Fatal("missing isolation identity selected legacy account")
	}
}
