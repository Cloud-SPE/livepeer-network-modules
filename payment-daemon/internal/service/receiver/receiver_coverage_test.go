package receiver

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/spendauth"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
)

func receiverFixture(t *testing.T) (*Service, *store.Store, *inmemory.KeyStore, []byte) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "receiver.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, _ := crypto.GenerateKey()
	ks, _ := inmemory.New(key)
	payee := bytesOf(0xaa, 20)
	return New(st, Config{SettlementDomainID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Recipient: payee, ChainID: 42161, DefaultFaceValue: big.NewInt(1), DefaultWinProb: types.MaxWinProb}, nil), st, ks, payee
}

func bytesOf(v byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func baseAuthorization(ks *inmemory.KeyStore, payee []byte) *pb.SpendAuthorizationPayload {
	now := time.Now().UTC()
	digest := sha256.Sum256([]byte("request"))
	return &pb.SpendAuthorizationPayload{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Domain: spendauth.Domain, Payer: ks.Address(), Payee: payee, ChainId: 42161, Denomination: "wei",
		AuthorizationId: "auth", RequestId: "request", BrokerUri: "https://broker.example",
		Protocol: "paid-job/v1", Capability: "custom:any", Offering: "default",
		AcceptedPrice: &pb.AcceptedPrice{PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(10).Bytes()}, UnitsPerPrice: 1, WorkUnitName: "widgets", Capability: "custom:any", Offering: "default"},
		MaxDebitWei:   &pb.BigUInt{Value: big.NewInt(100).Bytes()}, MaxTotalUnits: 10,
		NotBefore: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), RequestDigest: digest[:],
	}
}

func encodeAuthorization(t *testing.T, ks *inmemory.KeyStore, payload *pb.SpendAuthorizationPayload) []byte {
	t.Helper()
	digest, err := spendauth.Digest(payload)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ks.Sign(digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&pb.SpendAuthorization{Payload: payload, Signature: sig})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAdmitAuthorizationValidationMatrix(t *testing.T) {
	svc, _, ks, payee := receiverFixture(t)
	ctx := context.Background()
	for name, raw := range map[string][]byte{"empty": nil, "malformed": {0xff}} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: raw}); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code=%v err=%v", status.Code(err), err)
			}
		})
	}
	unsigned, _ := proto.Marshal(&pb.SpendAuthorization{Payload: baseAuthorization(ks, payee)})
	if _, err := svc.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: unsigned}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unsigned code=%v err=%v", status.Code(err), err)
	}
	tests := []struct {
		name   string
		mutate func(*pb.SpendAuthorizationPayload)
	}{
		{"payee", func(p *pb.SpendAuthorizationPayload) { p.Payee = bytesOf(1, 20) }},
		{"chain", func(p *pb.SpendAuthorizationPayload) { p.ChainId = 1 }},
		{"denomination", func(p *pb.SpendAuthorizationPayload) { p.Denomination = "usd" }},
		{"identity", func(p *pb.SpendAuthorizationPayload) { p.AuthorizationId = "" }},
		{"broker", func(p *pb.SpendAuthorizationPayload) { p.BrokerUri = "" }},
		{"digest", func(p *pb.SpendAuthorizationPayload) { p.RequestDigest = []byte{1} }},
		{"caller-key", func(p *pb.SpendAuthorizationPayload) { p.CallerPublicKey = []byte{1} }},
		{"job-session", func(p *pb.SpendAuthorizationPayload) { p.SessionId = "not-allowed" }},
		{"session-id", func(p *pb.SpendAuthorizationPayload) { p.Protocol = "paid-session/v1" }},
		{"protocol", func(p *pb.SpendAuthorizationPayload) { p.Protocol = "other/v1" }},
		{"route", func(p *pb.SpendAuthorizationPayload) { p.AcceptedPrice.Capability = "other" }},
		{"denominator", func(p *pb.SpendAuthorizationPayload) { p.AcceptedPrice.UnitsPerPrice = 0 }},
		{"unit", func(p *pb.SpendAuthorizationPayload) { p.AcceptedPrice.WorkUnitName = "" }},
		{"not-before", func(p *pb.SpendAuthorizationPayload) { p.NotBefore = "bad" }},
		{"expiry", func(p *pb.SpendAuthorizationPayload) { p.ExpiresAt = p.NotBefore }},
		{"future", func(p *pb.SpendAuthorizationPayload) {
			p.NotBefore = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			p.ExpiresAt = time.Now().Add(2 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"maximum", func(p *pb.SpendAuthorizationPayload) { p.MaxDebitWei = &pb.BigUInt{} }},
		{"units", func(p *pb.SpendAuthorizationPayload) { p.MaxTotalUnits = 0 }},
		{"underfunded", func(p *pb.SpendAuthorizationPayload) { p.MaxDebitWei = &pb.BigUInt{Value: []byte{1}} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := proto.Clone(baseAuthorization(ks, payee)).(*pb.SpendAuthorizationPayload)
			tc.mutate(payload)
			_, err := svc.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: encodeAuthorization(t, ks, payload)})
			if err == nil {
				t.Fatal("invalid authorization accepted")
			}
		})
	}
	valid := encodeAuthorization(t, ks, baseAuthorization(ks, payee))
	if _, err := svc.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: valid}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unfunded authorization code=%v err=%v", status.Code(err), err)
	}
}

func TestWholesaleLookupAndMutationErrors(t *testing.T) {
	svc, st, ks, payee := receiverFixture(t)
	ctx := context.Background()
	if _, err := svc.GetWholesaleAccount(ctx, &pb.GetWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid account lookup code=%v", status.Code(err))
	}
	account, err := svc.GetWholesaleAccount(ctx, &pb.GetWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payer: ks.Address()})
	if err != nil || account.GetAccount().GetChainId() != 42161 || account.GetAccount().GetDenomination() != "wei" {
		t.Fatalf("account=%+v err=%v", account, err)
	}
	if _, err := svc.GetSpendAuthorization(ctx, &pb.GetSpendAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payer: ks.Address(), AuthorizationId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing authorization code=%v", status.Code(err))
	}
	if _, err := svc.AdvanceAuthorization(ctx, &pb.AdvanceAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid advance code=%v", status.Code(err))
	}
	if _, err := svc.AdvanceAuthorization(ctx, &pb.AdvanceAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payer: ks.Address(), AuthorizationId: "missing", AdvanceSeq: 1, TargetReservedValueWei: &pb.BigUInt{}}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing advance code=%v", status.Code(err))
	}
	if _, err := svc.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid settle code=%v", status.Code(err))
	}
	if _, err := svc.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payer: ks.Address(), AuthorizationId: "missing", SettlementSeq: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing settle code=%v", status.Code(err))
	}
	if _, err := svc.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PaymentBytes: []byte("bad")}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed funding code=%v", status.Code(err))
	}
	now := time.Now().UTC()
	seed := store.WholesaleAuthorizationSeed{WholesaleAccountID: "test-account", ID: "expired", Fingerprint: []byte("fp"), Payer: ks.Address(), Payee: payee, RequestID: "r", Protocol: "paid-job/v1", Capability: "c", Offering: "o", PriceWei: "1", PerUnits: 1, WorkUnit: "u", MaxDebitWei: "1", MaxTotalUnits: 1, ExpiresAt: now.Add(-time.Second)}
	if _, err := st.AdmitWholesale(seed, "", nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetSpendAuthorization(ctx, &pb.GetSpendAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payer: ks.Address(), AuthorizationId: "expired"})
	if err != nil || got.GetState() != pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED {
		t.Fatalf("authorization=%+v err=%v", got, err)
	}
	svc.recordWholesaleTotals()
}

func TestRedemptionStatusStatesAndHelpers(t *testing.T) {
	svc, st, _, _ := receiverFixture(t)
	ctx := context.Background()
	if _, err := svc.GetRedemptionStatus(ctx, &pb.GetRedemptionStatusRequest{TicketHash: []byte{1}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("short hash code=%v", status.Code(err))
	}
	unknown := bytesOf(0x10, 32)
	if got, err := svc.GetRedemptionStatus(ctx, &pb.GetRedemptionStatusRequest{TicketHash: unknown}); err != nil || got.GetStatus() != pb.GetRedemptionStatusResponse_STATUS_UNSPECIFIED {
		t.Fatalf("unknown=%+v err=%v", got, err)
	}
	ticket := &store.SignedTicket{Sender: bytesOf(1, 20), FaceValue: big.NewInt(5)}
	queued := bytesOf(0x20, 32)
	if _, err := st.EnqueueRedemption(queued, ticket); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetRedemptionStatus(ctx, &pb.GetRedemptionStatusRequest{TicketHash: queued}); err != nil || got.GetStatus() != pb.GetRedemptionStatusResponse_STATUS_QUEUED {
		t.Fatalf("queued=%+v err=%v", got, err)
	}
	if err := st.MarkRedeemed(queued, nil, ticket, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetRedemptionStatus(ctx, &pb.GetRedemptionStatusRequest{TicketHash: queued}); got.GetStatus() != pb.GetRedemptionStatusResponse_STATUS_FAILED {
		t.Fatalf("failed=%+v", got)
	}
	confirmed := bytesOf(0x30, 32)
	if _, err := st.EnqueueRedemption(confirmed, ticket); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRedeemed(confirmed, bytesOf(0x40, 32), ticket, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetRedemptionStatus(ctx, &pb.GetRedemptionStatusRequest{TicketHash: confirmed}); got.GetStatus() != pb.GetRedemptionStatusResponse_STATUS_CONFIRMED || len(got.GetTxHash()) != 32 {
		t.Fatalf("confirmed=%+v", got)
	}
	if _, err := svc.GetRoundRevenue(ctx, &pb.GetRoundRevenueRequest{RoundId: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative round code=%v", status.Code(err))
	}
	for state, want := range map[string]pb.SpendAuthorizationState{
		store.AuthorizationAdmitted:      pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED,
		store.AuthorizationSettled:       pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED,
		store.AuthorizationExpiredUnused: pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED,
		store.AuthorizationSuperseded:    pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED,
		"other":                          pb.SpendAuthorizationState_SPEND_AUTHORIZATION_STATE_UNSPECIFIED,
	} {
		if got := authorizationState(state); got != want {
			t.Fatalf("state %q=%v want=%v", state, got, want)
		}
	}
	if decimalBig("bad").Sign() != 0 || decimalBytes("0") != nil || decimalBytes("bad") != nil || new(big.Int).SetBytes(decimalBytes("12")).Int64() != 12 {
		t.Fatal("decimal helper mismatch")
	}
	for err, code := range map[error]codes.Code{store.ErrPricingUnset: codes.FailedPrecondition, store.ErrNotFound: codes.NotFound, store.ErrClosed: codes.FailedPrecondition, store.ErrSenderMismatch: codes.FailedPrecondition, errors.New("other"): codes.Internal} {
		if got := status.Code(mapStoreErr(err)); got != code {
			t.Fatalf("mapStoreErr(%v)=%v want=%v", err, got, code)
		}
	}
	if equalBytes([]byte{1}, []byte{1, 2}) || equalBytes([]byte{1}, []byte{2}) || !equalBytes([]byte{1}, []byte{1}) {
		t.Fatal("equalBytes mismatch")
	}
}

func TestSignedPriceAndRPCInputValidation(t *testing.T) {
	if err := checkSignedPrice(&pb.Payment{}, &store.Session{PricePerWorkUnitWei: "10"}); err != nil {
		t.Fatal(err)
	}
	pay := &pb.Payment{ExpectedPrice: &pb.PriceInfo{PricePerUnit: 10, PixelsPerUnit: 1}}
	if err := checkSignedPrice(pay, nil); err != nil {
		t.Fatal(err)
	}
	if err := checkSignedPrice(pay, &store.Session{PricePerWorkUnitWei: store.PricingUnset}); err != nil {
		t.Fatal(err)
	}
	if code := status.Code(checkSignedPrice(pay, &store.Session{PricePerWorkUnitWei: "corrupt"})); code != codes.Internal {
		t.Fatalf("corrupt price code=%v", code)
	}
	if code := status.Code(checkSignedPrice(pay, &store.Session{PricePerWorkUnitWei: "11", PerUnits: 1})); code != codes.FailedPrecondition {
		t.Fatalf("price mismatch code=%v", code)
	}
	if code := status.Code(checkSignedPrice(pay, &store.Session{PricePerWorkUnitWei: "10", PerUnits: 2})); code != codes.FailedPrecondition {
		t.Fatalf("denominator mismatch code=%v", code)
	}
	if err := checkSignedPrice(&pb.Payment{ExpectedPrice: &pb.PriceInfo{PricePerUnit: 10}}, &store.Session{PricePerWorkUnitWei: "10"}); err != nil {
		t.Fatalf("default denominators: %v", err)
	}

	svc, st, _, _ := receiverFixture(t)
	ctx := context.Background()
	for name, req := range map[string]*pb.OpenSessionRequest{
		"work":       {},
		"capability": {WorkId: "w"},
		"offering":   {WorkId: "w", Capability: "c"},
		"unit":       {WorkId: "w", Capability: "c", Offering: "o"},
	} {
		t.Run("open-"+name, func(t *testing.T) {
			if _, err := svc.OpenSession(ctx, req); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code=%v err=%v", status.Code(err), err)
			}
		})
	}
	for name, call := range map[string]func() error{
		"debit-sender": func() error { _, err := svc.DebitBalance(ctx, &pb.DebitBalanceRequest{}); return err },
		"debit-work":   func() error { _, err := svc.DebitBalance(ctx, &pb.DebitBalanceRequest{Sender: []byte{1}}); return err },
		"debit-units": func() error {
			_, err := svc.DebitBalance(ctx, &pb.DebitBalanceRequest{Sender: []byte{1}, WorkId: "w", WorkUnits: -1})
			return err
		},
		"sufficient-sender": func() error { _, err := svc.SufficientBalance(ctx, &pb.SufficientBalanceRequest{}); return err },
		"sufficient-work": func() error {
			_, err := svc.SufficientBalance(ctx, &pb.SufficientBalanceRequest{Sender: []byte{1}})
			return err
		},
		"balance-sender": func() error { _, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{}); return err },
		"balance-work":   func() error { _, err := svc.GetBalance(ctx, &pb.GetBalanceRequest{Sender: []byte{1}}); return err },
		"close-sender":   func() error { _, err := svc.CloseSession(ctx, &pb.CloseSessionRequest{}); return err },
		"close-work":     func() error { _, err := svc.CloseSession(ctx, &pb.CloseSessionRequest{Sender: []byte{1}}); return err },
		"reset-sender": func() error {
			_, err := svc.ResetSession(ctx, &pb.ResetSessionRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream"})
			return err
		},
		"reset-recipient": func() error {
			_, err := svc.ResetSession(ctx, &pb.ResetSessionRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream", Sender: []byte{1}, Recipient: []byte{2}})
			return err
		},
		"reset-capability": func() error {
			_, err := svc.ResetSession(ctx, &pb.ResetSessionRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream", Sender: []byte{1}})
			return err
		},
		"reset-offering": func() error {
			_, err := svc.ResetSession(ctx, &pb.ResetSessionRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream", Sender: []byte{1}, Capability: "c"})
			return err
		},
		"params-sender": func() error {
			_, err := svc.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream"})
			return err
		},
		"params-capability": func() error {
			_, err := svc.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream", Sender: bytesOf(1, 20)})
			return err
		},
		"params-offering": func() error {
			_, err := svc.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream", Sender: bytesOf(1, 20), Capability: "c"})
			return err
		},
		"params-recipient": func() error {
			_, err := svc.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream", Sender: bytesOf(1, 20), Capability: "c", Offering: "o", Recipient: bytesOf(2, 20)})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code=%v err=%v", status.Code(err), err)
			}
		})
	}
	seed := store.Session{WorkID: "closed", Capability: "c", Offering: "o", WorkUnit: "u", PricePerWorkUnitWei: "1"}
	if _, _, err := st.OpenSession(seed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CloseSession(nil, "closed"); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.CloseSession(ctx, &pb.CloseSessionRequest{Sender: []byte{1}, WorkId: "closed"})
	if err != nil || resp.GetOutcome() != pb.CloseSessionResponse_OUTCOME_ALREADY_CLOSED {
		t.Fatalf("second close=%+v err=%v", resp, err)
	}
}
