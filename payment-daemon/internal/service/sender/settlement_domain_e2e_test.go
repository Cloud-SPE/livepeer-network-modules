package sender_test

import (
	"context"
	"crypto/sha256"
	"math/big"
	"testing"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// One payer, one on-chain payee, two real independent receiver Bolt stores.
// Tickets cross the HTTP params proxy and gRPC sender/receiver boundaries.
func TestIndependentSettlementDomainsConformance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	payeeAddress := bytes20(0xd1)
	a, _, stopA := defaultConfigReceiverStandWithStore(t, payeeAddress, "")
	defer stopA()
	b, _, stopB := defaultConfigReceiverStandWithStore(t, payeeAddress, "")
	defer stopB()
	da, db := receiverDomain(t, a), receiverDomain(t, b)
	if da == db || da == "" || db == "" {
		t.Fatal("independent ledgers need distinct IDs")
	}
	payer, urlA, stopP := devModeSenderStand(t, a)
	defer stopP()
	_, urlB, stopProxyB := devModeSenderStand(t, b)
	defer stopProxyB()
	// An alternate URL reaches A's SAME ledger: relocation preserves the domain.
	_, urlA2, stopAlias := devModeSenderStand(t, a)
	defer stopAlias()
	mint := func(url, domain, id string, amount int64) *pb.CreatePaymentResponse {
		t.Helper()
		req := devModeCreateRequest(payeeAddress, id, url)
		req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(amount).Bytes()}
		req.AccountFunding = &pb.AccountFundingIntent{WholesaleAccountId: "test-account", SettlementDomainId: domain, TargetAvailableWei: &pb.BigUInt{Value: big.NewInt(amount).Bytes()}, ObservedAvailableWei: &pb.BigUInt{}}
		out, err := payer.CreatePayment(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ma := mint(urlA, da, "fund-a", 100000)
	var payment pb.Payment
	if err := proto.Unmarshal(ma.GetPaymentBytes(), &payment); err != nil {
		t.Fatal(err)
	}
	payerAddress := payment.GetSender()
	account := func(c pb.PayeeDaemonClient, d string) *pb.WholesaleAccountView {
		t.Helper()
		v, e := c.GetWholesaleAccount(ctx, &pb.GetWholesaleAccountRequest{WholesaleAccountId: "test-account", Payer: payerAddress, SettlementDomainId: d})
		if e != nil {
			t.Fatal(e)
		}
		return v.GetAccount()
	}
	if account(a, da).GetVersion() != 0 || account(b, db).GetVersion() != 0 {
		t.Fatal("fresh account versions must start independently at zero")
	}
	fund := func(c pb.PayeeDaemonClient, d string, m *pb.CreatePaymentResponse) {
		t.Helper()
		_, e := c.OpenSession(ctx, &pb.OpenSessionRequest{WorkId: m.GetWorkId(), Capability: "openai:chat-completions", Offering: "model-a", PricePerWorkUnitWei: big.NewInt(1000).Bytes(), PerUnits: 1, WorkUnit: "tokens"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: d, PaymentBytes: m.GetPaymentBytes()})
		if e != nil {
			t.Fatal(e)
		}
	}
	fund(a, da, ma)
	av := account(a, da)
	if av.GetVersion() == 0 || account(b, db).GetVersion() != 0 || len(account(b, db).GetCreditedValueWei().GetValue()) != 0 {
		t.Fatal("funding A affected B")
	}
	// Even with B's correct namespace, A's ticket cannot fund B: B lacks A's
	// recipient-random session. Changing the RPC namespace does not make it valid.
	if _, err := b.OpenSession(ctx, &pb.OpenSessionRequest{WorkId: ma.GetWorkId(), Capability: "openai:chat-completions", Offering: "model-a", PricePerWorkUnitWei: big.NewInt(1000).Bytes(), PerUnits: 1, WorkUnit: "tokens"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: db, PaymentBytes: ma.GetPaymentBytes()}); err == nil {
		t.Fatal("A ticket credited B")
	}
	mb := mint(urlB, db, "fund-b", 200000)
	fund(b, db, mb)
	bv := account(b, db)
	if bv.GetVersion() != av.GetVersion() || account(a, da).GetVersion() != av.GetVersion() {
		t.Fatal("versions must advance independently")
	}
	auth := func(domain, url, id string) []byte {
		t.Helper()
		now := time.Now().UTC()
		digest := sha256.Sum256([]byte("body"))
		price := devModeCreateRequest(payeeAddress, "unused", url).GetAcceptedPrice()
		v, e := payer.CreateSpendAuthorization(ctx, &pb.CreateSpendAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: domain, Payee: payeeAddress, AuthorizationId: id, RequestId: id, Protocol: "paid-job/v1", AcceptedPrice: price, MaxDebitWei: &pb.BigUInt{Value: big.NewInt(10000).Bytes()}, MaxTotalUnits: 10, NotBefore: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), RequestDigest: digest[:], BrokerUri: url, ChainId: 42161, Denomination: "wei"})
		if e != nil {
			t.Fatal(e)
		}
		return v.GetAuthorizationBytes()
	}
	aa := auth(da, urlA, "same-id")
	if _, e := b.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: aa}); e == nil {
		t.Fatal("B admitted A authorization")
	}
	if _, e := a.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: aa}); e != nil {
		t.Fatal(e)
	}
	ab := auth(db, urlB, "same-id")
	if _, e := b.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: ab}); e != nil {
		t.Fatal(e)
	}
	if _, e := b.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: da, Payer: payerAddress, AuthorizationId: "same-id", ActualUnits: 1, SettlementSeq: 1}); e == nil {
		t.Fatal("foreign domain settled colliding authorization ID")
	}
	for _, tc := range []struct {
		c pb.PayeeDaemonClient
		d string
	}{{a, da}, {b, db}} {
		if _, e := tc.c.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: tc.d, Payer: payerAddress, AuthorizationId: "same-id", ActualUnits: 1, SettlementSeq: 1}); e != nil {
			t.Fatal(e)
		}
	}
	before := account(a, da)
	relocated := auth(da, urlA2, "relocated")
	if _, e := a.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: relocated}); e != nil {
		t.Fatal(e)
	}
	if new(big.Int).SetBytes(account(a, da).GetCreditedValueWei().GetValue()).Cmp(new(big.Int).SetBytes(before.GetCreditedValueWei().GetValue())) != 0 {
		t.Fatal("URL migration moved or reset credit")
	}
}
