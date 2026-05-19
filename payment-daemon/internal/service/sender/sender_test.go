package sender_test

import (
	"context"
	"math/big"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/sender"
	senderTypes "github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
)

// stand spins up an in-process sender Service over a unix socket and
// returns a gRPC client + cleanup.
func stand(t *testing.T) (pb.PayerDaemonClient, func()) {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, fakeFetcher{})

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		gs.GracefulStop()
	}
	return pb.NewPayerDaemonClient(conn), cleanup
}

type fakeFetcher struct{}

func (fakeFetcher) Fetch(_ context.Context, req sender.TicketParamsRequest) (*senderTypes.TicketParams, error) {
	return &senderTypes.TicketParams{
		Recipient:         append([]byte(nil), req.Recipient...),
		FaceValue:         new(big.Int).Set(req.FaceValue),
		WinProb:           big.NewInt(0),
		RecipientRandHash: []byte("0123456789abcdef0123456789abcdef"),
		Seed:              []byte("seed-seed-seed-seed-seed-seed-12"),
		ExpirationBlock:   big.NewInt(123456),
		ExpirationParams: &senderTypes.TicketExpirationParams{
			CreationRound:          1,
			CreationRoundBlockHash: make([]byte, 32),
		},
	}, nil
}

type recordingFetcher struct {
	lastBaseURL string
}

func (f *recordingFetcher) Fetch(_ context.Context, req sender.TicketParamsRequest) (*senderTypes.TicketParams, error) {
	f.lastBaseURL = req.BaseURL
	return (&fakeFetcher{}).Fetch(context.Background(), req)
}

func TestCreatePayment_HappyPath(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.CreatePayment(ctx, makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:/v1/chat/completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker.example.com",
	))
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.GetTicketsCreated() != 1 {
		t.Errorf("tickets_created = %d; want 1", resp.GetTicketsCreated())
	}
	if len(resp.GetPaymentBytes()) == 0 {
		t.Fatal("payment_bytes is empty")
	}

	// Decode the wire bytes into the wire-compat Payment and check its
	// shape.
	var pay pb.Payment
	if err := proto.Unmarshal(resp.GetPaymentBytes(), &pay); err != nil {
		t.Fatalf("decode payment: %v", err)
	}
	if pay.GetTicketParams() == nil {
		t.Fatal("payment.ticket_params is nil")
	}
	if got := len(pay.GetSender()); got != 20 {
		t.Errorf("sender length = %d; want 20", got)
	}
	if len(pay.GetTicketSenderParams()) != 1 {
		t.Errorf("ticket_sender_params count = %d; want 1", len(pay.GetTicketSenderParams()))
	}
	if got := pay.GetExpectedPrice(); got == nil || got.GetPricePerUnit() != 1000 || got.GetPixelsPerUnit() != 1 {
		t.Fatalf("expected_price = %+v; want 1000 wei / 1 unit", got)
	}
	tsp := pay.GetTicketSenderParams()[0]
	if got := len(tsp.GetSig()); got != 65 {
		t.Errorf("sig length = %d; want 65 (R||S||V)", got)
	}
	if tsp.GetSenderNonce() == 0 {
		t.Error("sender_nonce should be > 0 after first ticket")
	}
}

func TestCreatePayment_NonceAdvances(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:/v1/chat/completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker.example.com",
	)

	first, err := client.CreatePayment(ctx, req)
	if err != nil {
		t.Fatalf("CreatePayment 1: %v", err)
	}
	second, err := client.CreatePayment(ctx, req)
	if err != nil {
		t.Fatalf("CreatePayment 2: %v", err)
	}

	var p1, p2 pb.Payment
	_ = proto.Unmarshal(first.GetPaymentBytes(), &p1)
	_ = proto.Unmarshal(second.GetPaymentBytes(), &p2)

	n1 := p1.GetTicketSenderParams()[0].GetSenderNonce()
	n2 := p2.GetTicketSenderParams()[0].GetSenderNonce()
	if n2 != n1+1 {
		t.Errorf("nonces should advance by 1: got %d → %d", n1, n2)
	}

	// Same recipient/capability/offering should reuse the
	// recipient_rand_hash session key.
	if string(p1.GetTicketParams().GetRecipientRandHash()) != string(p2.GetTicketParams().GetRecipientRandHash()) {
		t.Error("recipient_rand_hash should be stable across calls in same session")
	}
}

func TestCreatePayment_ReusedSessionRefreshesAcceptedQuoteMetadata(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstReq := makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:chat-completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker.example.com",
	)
	firstReq.AcceptedPrice.QuoteRef.QuoteId = "quote-a"
	firstReq.Funding.EstimatedUnits = 10
	firstReq.Funding.MaxTotalUnits = 10

	secondReq := makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:chat-completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker.example.com",
	)
	secondReq.AcceptedPrice.QuoteRef.QuoteId = "quote-b"
	secondReq.AcceptedPrice.QuoteRef.QuoteVersion = 7
	secondReq.Funding.EstimatedUnits = 20
	secondReq.Funding.MaxTotalUnits = 20

	first, err := client.CreatePayment(ctx, firstReq)
	if err != nil {
		t.Fatalf("CreatePayment 1: %v", err)
	}
	second, err := client.CreatePayment(ctx, secondReq)
	if err != nil {
		t.Fatalf("CreatePayment 2: %v", err)
	}

	if got := second.GetAcceptedQuoteRef().GetQuoteId(); got != "quote-b" {
		t.Fatalf("accepted_quote_ref.quote_id = %q; want quote-b", got)
	}
	if got := second.GetAcceptedQuoteRef().GetQuoteVersion(); got != 7 {
		t.Fatalf("accepted_quote_ref.quote_version = %d; want 7", got)
	}

	var p1, p2 pb.Payment
	if err := proto.Unmarshal(first.GetPaymentBytes(), &p1); err != nil {
		t.Fatalf("decode first payment: %v", err)
	}
	if err := proto.Unmarshal(second.GetPaymentBytes(), &p2); err != nil {
		t.Fatalf("decode second payment: %v", err)
	}
	if p1.GetTicketSenderParams()[0].GetSenderNonce()+1 != p2.GetTicketSenderParams()[0].GetSenderNonce() {
		t.Fatalf("nonce stream was not reused across quote refresh: %d -> %d", p1.GetTicketSenderParams()[0].GetSenderNonce(), p2.GetTicketSenderParams()[0].GetSenderNonce())
	}
	if got := p2.GetExpectedPrice().GetConstraint(); !strings.Contains(got, "qid=quote-b") || !strings.Contains(got, "est=20") {
		t.Fatalf("expected_price.constraint = %q; want refreshed quote/estimate metadata", got)
	}
}

func TestCreatePayment_UsesAuthoritativeTicketFaceValue(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, authoritativeFetcher{})

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	defer gs.GracefulStop()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewPayerDaemonClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.CreatePayment(ctx, makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:/v1/chat/completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker.example.com",
	))
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	var pay pb.Payment
	if err := proto.Unmarshal(resp.GetPaymentBytes(), &pay); err != nil {
		t.Fatalf("decode payment: %v", err)
	}
	gotFaceValue := new(big.Int).SetBytes(pay.GetTicketParams().GetFaceValue())
	if gotFaceValue.Cmp(big.NewInt(5000)) != 0 {
		t.Fatalf("ticket face_value = %s; want 5000", gotFaceValue)
	}
	if gotEV := new(big.Int).SetBytes(resp.GetExpectedValue().GetValue()); gotEV.Cmp(big.NewInt(5000)) != 0 {
		t.Fatalf("expected_value = %s; want 5000", gotEV)
	}
}

func TestCreatePayment_PrefersPerRequestTicketParamsBaseURL(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	fetcher := &recordingFetcher{}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, fetcher)

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	defer gs.GracefulStop()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewPayerDaemonClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.CreatePayment(ctx, makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:chat-completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker-a.example.com",
	))
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if fetcher.lastBaseURL != "https://broker-a.example.com" {
		t.Fatalf("fetcher base URL = %q; want request-supplied broker URL", fetcher.lastBaseURL)
	}
}

func TestCreatePayment_RejectsEmptyFields(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []struct {
		name string
		req  *pb.CreatePaymentRequest
	}{
		{"empty recipient", &pb.CreatePaymentRequest{
			AcceptedPrice: baseAcceptedPrice("x", "y", "token", 1, 1),
			Funding:       baseFunding(1, 1),
		}},
		{"empty accepted_price", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), Funding: baseFunding(1, 1),
		}},
		{"empty funding", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), AcceptedPrice: baseAcceptedPrice("x", "y", "token", 1, 1),
		}},
		{"empty capability", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), AcceptedPrice: baseAcceptedPrice("", "y", "token", 1, 1), Funding: baseFunding(1, 1),
		}},
		{"empty offering", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), AcceptedPrice: baseAcceptedPrice("x", "", "token", 1, 1), Funding: baseFunding(1, 1),
		}},
		{"empty work unit", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), AcceptedPrice: baseAcceptedPrice("x", "y", "", 1, 1), Funding: baseFunding(1, 1),
		}},
		{"empty funded value", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), AcceptedPrice: baseAcceptedPrice("x", "y", "token", 1, 1), Funding: &pb.FundingIntent{},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.CreatePayment(ctx, tc.req); err == nil {
				t.Errorf("CreatePayment: want error for %s", tc.name)
			}
		})
	}
}

func TestCreatePayment_RejectsEmptySeedFromFetcher(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, emptySeedFetcher{})

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	defer gs.GracefulStop()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewPayerDaemonClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.CreatePayment(ctx, makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:chat-completions",
		"gpt-5",
		"token",
		1000,
		1,
		1000,
		"https://broker.example.com",
	))
	if err == nil || !strings.Contains(err.Error(), "seed is empty") {
		t.Fatalf("CreatePayment error = %v; want empty-seed failure", err)
	}
}

func TestHealth(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if got := resp.GetStatus(); got != "ok" {
		t.Errorf("status = %q; want %q", got, "ok")
	}
}

func TestGetDepositInfo(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.GetDepositInfo(ctx, &pb.GetDepositInfoRequest{})
	if err != nil {
		t.Fatalf("GetDepositInfo: %v", err)
	}
	if len(resp.GetDeposit()) == 0 {
		t.Error("deposit should be > 0 in dev mode")
	}
	if len(resp.GetReserve()) == 0 {
		t.Error("reserve should be > 0 in dev mode")
	}
	if resp.GetWithdrawRound() != 0 {
		t.Errorf("withdraw_round = %d; want 0 (no unlock pending)", resp.GetWithdrawRound())
	}
}

func makeCreatePaymentRequest(recipient []byte, capability, offering, workUnit string, pricePerUnitWei, unitsPerPrice, fundedValueWei uint64, baseURL string) *pb.CreatePaymentRequest {
	return &pb.CreatePaymentRequest{
		Recipient:           recipient,
		TicketParamsBaseUrl: baseURL,
		AcceptedPrice:       baseAcceptedPrice(capability, offering, workUnit, pricePerUnitWei, unitsPerPrice),
		Funding:             baseFunding(fundedValueWei, unitsPerPrice),
	}
}

func baseAcceptedPrice(capability, offering, workUnit string, pricePerUnitWei, unitsPerPrice uint64) *pb.AcceptedPrice {
	return &pb.AcceptedPrice{
		PricePerUnitWei: &pb.BigUInt{Value: new(big.Int).SetUint64(pricePerUnitWei).Bytes()},
		UnitsPerPrice:   unitsPerPrice,
		WorkUnitName:    workUnit,
		Capability:      capability,
		Offering:        offering,
		QuoteRef: &pb.QuoteRef{
			QuoteId:               "quote-1",
			QuoteVersion:          1,
			ConstraintFingerprint: []byte{0x01, 0x02, 0x03},
			RouteFingerprint:      []byte{0x04, 0x05, 0x06},
		},
	}
}

func baseFunding(fundedValueWei, estimatedUnits uint64) *pb.FundingIntent {
	return &pb.FundingIntent{
		EstimatedUnits: estimatedUnits,
		FundedValueWei: &pb.BigUInt{Value: new(big.Int).SetUint64(fundedValueWei).Bytes()},
		MaxTotalUnits:  estimatedUnits,
	}
}

type authoritativeFetcher struct{}

func (authoritativeFetcher) Fetch(_ context.Context, req sender.TicketParamsRequest) (*senderTypes.TicketParams, error) {
	return &senderTypes.TicketParams{
		Recipient:         append([]byte(nil), req.Recipient...),
		FaceValue:         big.NewInt(5000),
		WinProb:           new(big.Int).Set(senderTypes.MaxWinProb),
		RecipientRandHash: []byte("fedcba9876543210fedcba9876543210"),
		Seed:              []byte("seed-seed-seed-seed-seed-seed-12"),
		ExpirationBlock:   big.NewInt(123456),
		ExpirationParams: &senderTypes.TicketExpirationParams{
			CreationRound:          1,
			CreationRoundBlockHash: make([]byte, 32),
		},
	}, nil
}

type emptySeedFetcher struct{}

func (emptySeedFetcher) Fetch(_ context.Context, req sender.TicketParamsRequest) (*senderTypes.TicketParams, error) {
	params, err := (fakeFetcher{}).Fetch(context.Background(), req)
	if err != nil {
		return nil, err
	}
	params.Seed = nil
	return params, nil
}
