package sender_test

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/sender"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
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
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, fakeFetcher{}, nil, mintStore(t), sender.Limits{})

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
		Recipient: append([]byte(nil), req.Recipient...),
		FaceValue: new(big.Int).Set(req.FaceValue),
		// MaxWinProb: every ticket credits its full face value, so one
		// ticket funds a request exactly. A win_prob of 0 credits
		// nothing at any batch size, which is now refused rather than
		// returned as a payment that funds no work.
		WinProb:           new(big.Int).Set(senderTypes.MaxWinProb),
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

type rotatingFetcher struct {
	mu    sync.Mutex
	count int
}

func (f *rotatingFetcher) Fetch(_ context.Context, req sender.TicketParamsRequest) (*senderTypes.TicketParams, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	hash := []byte("0123456789abcdef0123456789abcde1")
	if f.count > 1 {
		hash = []byte("0123456789abcdef0123456789abcde2")
	}
	return &senderTypes.TicketParams{
		Recipient: append([]byte(nil), req.Recipient...),
		FaceValue: new(big.Int).Set(req.FaceValue),
		// MaxWinProb: every ticket credits its full face value, so one
		// ticket funds a request exactly. A win_prob of 0 credits
		// nothing at any batch size, which is now refused rather than
		// returned as a payment that funds no work.
		WinProb:           new(big.Int).Set(senderTypes.MaxWinProb),
		RecipientRandHash: hash,
		Seed:              []byte("seed-seed-seed-seed-seed-seed-12"),
		ExpirationBlock:   big.NewInt(123456),
		ExpirationParams: &senderTypes.TicketExpirationParams{
			CreationRound:          1,
			CreationRoundBlockHash: make([]byte, 32),
		},
	}, nil
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
	if resp.GetWorkId() == "" {
		t.Fatal("work_id is empty")
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
	// A second mint, not a retry of the first — distinct intent, distinct
	// key. Reusing the key would (correctly) replay and never advance.
	next := proto.Clone(req).(*pb.CreatePaymentRequest)
	next.MintRequestId = req.GetMintRequestId() + "-2"
	second, err := client.CreatePayment(ctx, next)
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

func TestReportPaymentResult_InvalidRecipientRandEvictsSessionAndReturnsAborted(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	fetcher := &rotatingFetcher{}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, fetcher, nil, mintStore(t), sender.Limits{})

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

	req := makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"video:transcode.abr",
		"default",
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
	_, err = client.ReportPaymentResult(ctx, &pb.ReportPaymentResultRequest{
		WorkId:          first.GetWorkId(),
		Capability:      "video:transcode.abr",
		Offering:        "default",
		RejectionReason: pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND,
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("ReportPaymentResult status = %v; want Aborted (err=%v)", status.Code(err), err)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) failed", err)
	}
	var (
		gotInfo  *errdetails.ErrorInfo
		gotRetry *errdetails.RetryInfo
	)
	for _, detail := range st.Details() {
		switch d := detail.(type) {
		case *errdetails.ErrorInfo:
			gotInfo = d
		case *errdetails.RetryInfo:
			gotRetry = d
		}
	}
	if gotInfo == nil || gotInfo.GetReason() != "INVALID_RECIPIENT_RAND" {
		t.Fatalf("ErrorInfo = %+v; want INVALID_RECIPIENT_RAND", gotInfo)
	}
	if gotInfo.GetMetadata()["old_work_id"] != first.GetWorkId() {
		t.Fatalf("old_work_id metadata = %q; want %q", gotInfo.GetMetadata()["old_work_id"], first.GetWorkId())
	}
	if gotRetry == nil {
		t.Fatal("RetryInfo missing")
	}

	// The rotation retry is a NEW mint intent and needs a new key: the
	// original id would replay the very payment the payee rejected.
	retry := proto.Clone(req).(*pb.CreatePaymentRequest)
	retry.MintRequestId = req.GetMintRequestId() + "-after-rotation"
	second, err := client.CreatePayment(ctx, retry)
	if err != nil {
		t.Fatalf("CreatePayment 2: %v", err)
	}
	if second.GetWorkId() == first.GetWorkId() {
		t.Fatalf("work_id reused after invalidation: %q", second.GetWorkId())
	}
	var p1, p2 pb.Payment
	if err := proto.Unmarshal(first.GetPaymentBytes(), &p1); err != nil {
		t.Fatalf("decode first payment: %v", err)
	}
	if err := proto.Unmarshal(second.GetPaymentBytes(), &p2); err != nil {
		t.Fatalf("decode second payment: %v", err)
	}
	if p2.GetTicketSenderParams()[0].GetSenderNonce() != 1 {
		t.Fatalf("sender nonce after invalidation = %d; want 1", p2.GetTicketSenderParams()[0].GetSenderNonce())
	}
}

func TestCreatePayment_UsesAuthoritativeTicketFaceValue(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, authoritativeFetcher{}, nil, mintStore(t), sender.Limits{})

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
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, fetcher, nil, mintStore(t), sender.Limits{})

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
			// Each case must fail on the field it is about, not on a
			// missing mint id.
			tc.req.MintRequestId = "validation:" + tc.name
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
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, emptySeedFetcher{}, nil, mintStore(t), sender.Limits{})

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

// mintSeq hands out a distinct mint_request_id per constructed request.
// Every call to this helper is a separate mint intent; a test that wants
// a replay reuses one request rather than building two.
var mintSeq atomic.Uint64

func makeCreatePaymentRequest(recipient []byte, capability, offering, workUnit string, pricePerUnitWei, unitsPerPrice, fundedValueWei uint64, baseURL string) *pb.CreatePaymentRequest {
	return &pb.CreatePaymentRequest{
		Recipient:           recipient,
		TicketParamsBaseUrl: baseURL,
		AcceptedPrice:       baseAcceptedPrice(capability, offering, workUnit, pricePerUnitWei, unitsPerPrice),
		Funding:             baseFunding(fundedValueWei, unitsPerPrice),
		MintRequestId:       fmt.Sprintf("test-mint-%d", mintSeq.Add(1)),
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

// mintStore opens a throwaway durable store. CreatePayment refuses to
// mint without one — that is the contract, not a test detail.
func mintStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sender.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ---------------------------------------------------------------------------
// mint idempotency

// TestCreatePayment_ReplaysOnSameMintID: the defect this closes. A retry
// after an uncertain response must return the original batch, not sign a
// second one against the payer's deposit.
func TestCreatePayment_ReplaysOnSameMintID(t *testing.T) {
	ctx, client, _ := newSenderClient(t)
	req := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")

	first, err := client.CreatePayment(ctx, req)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	replay, err := client.CreatePayment(ctx, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !bytes.Equal(first.GetPaymentBytes(), replay.GetPaymentBytes()) {
		t.Fatal("replay minted different payment bytes; a second batch was signed")
	}
	if replay.GetWorkId() != first.GetWorkId() ||
		replay.GetTicketsCreated() != first.GetTicketsCreated() ||
		!bytes.Equal(replay.GetFundedValueWei().GetValue(), first.GetFundedValueWei().GetValue()) ||
		!bytes.Equal(replay.GetExpectedValue().GetValue(), first.GetExpectedValue().GetValue()) {
		t.Fatalf("replay response differs from the recorded one:\nfirst=%+v\nreplay=%+v", first, replay)
	}
}

// TestCreatePayment_RefusesMintIDWithDifferentContent: the key is a
// promise about content. Answering with the earlier payment would hand
// the caller a batch it never asked for.
func TestCreatePayment_RefusesMintIDWithDifferentContent(t *testing.T) {
	ctx, client, _ := newSenderClient(t)
	req := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")
	if _, err := client.CreatePayment(ctx, req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	different := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 9999, "https://broker.example.com")
	different.MintRequestId = req.GetMintRequestId()
	_, err := client.CreatePayment(ctx, different)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reused id with new content: %v; want InvalidArgument", err)
	}
}

// TestCreatePayment_EvictedMintIDRefusesRatherThanRemints is the rule
// LOC asked for in writing. A retry delayed past the replay window must
// not be treated as a fresh mint: the tombstone is permanent, so the
// daemon refuses instead of paying a second time.
func TestCreatePayment_EvictedMintIDRefusesRatherThanRemints(t *testing.T) {
	ctx, client, st := newSenderClient(t)
	req := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")
	if _, err := client.CreatePayment(ctx, req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	// Age out the replay payload, exactly as the retention sweep does.
	if n, err := st.EvictMints(time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("EvictMints = (%d, %v); want (1, nil)", n, err)
	}

	_, err := client.CreatePayment(ctx, req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delayed retry after eviction: %v; want FailedPrecondition, never a second mint", err)
	}
}

// TestCreatePayment_ReplaySurvivesRestart: the record has to outlive the
// process, since the uncertain-response case includes "the daemon died
// before answering".
func TestCreatePayment_ReplaySurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sender.db")
	req := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")

	ctx, client, closeFirst := newSenderClientAt(t, dbPath)
	first, err := client.CreatePayment(ctx, req)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	closeFirst()

	ctx2, client2, closeSecond := newSenderClientAt(t, dbPath)
	defer closeSecond()
	replay, err := client2.CreatePayment(ctx2, req)
	if err != nil {
		t.Fatalf("replay after restart: %v", err)
	}
	if !bytes.Equal(first.GetPaymentBytes(), replay.GetPaymentBytes()) {
		t.Fatal("restarted daemon minted a second batch for the same intent")
	}
}

// newSenderClient is `stand` plus a handle on the mint ledger, for tests
// that drive retention directly.
func newSenderClient(t *testing.T) (context.Context, pb.PayerDaemonClient, *store.Store) {
	t.Helper()
	st := mintStore(t)
	client, _ := standWithStore(t, st)
	return context.Background(), client, st
}

// newSenderClientAt boots a daemon over a specific database file so a
// test can stop it and start another on the same state.
func newSenderClientAt(t *testing.T, dbPath string) (context.Context, pb.PayerDaemonClient, func()) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	client, stop := standWithStore(t, st)
	return context.Background(), client, func() {
		stop()
		_ = st.Close()
	}
}

// standWithStore is `stand` with the ledger supplied by the caller.
func standWithStore(t *testing.T, st *store.Store) (pb.PayerDaemonClient, func()) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "tx.sock")
	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, fakeFetcher{}, nil, st, sender.Limits{})

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
	stop := func() {
		_ = conn.Close()
		gs.GracefulStop()
	}
	t.Cleanup(stop)
	return pb.NewPayerDaemonClient(conn), stop
}

// TestRefillSizingDoesNotChangeSessionIdentity pins the invariant LOC
// asked for in writing. A differently-sized refill is the same session:
// the payee holds its recipient rand for the stable
// (sender, recipient, capability, offering) tuple, so work_id must not
// move because the payer decided to fund more.
func TestRefillSizingDoesNotChangeSessionIdentity(t *testing.T) {
	ctx, client, _ := newSenderClient(t)

	small := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")
	first, err := client.CreatePayment(ctx, small)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	// Same route, ten times the funding: a refill, not a new session.
	large := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 10000, "https://broker.example.com")
	second, err := client.CreatePayment(ctx, large)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	if second.GetWorkId() != first.GetWorkId() {
		t.Fatalf("work_id moved on a resize: %q -> %q", first.GetWorkId(), second.GetWorkId())
	}

	var p1, p2 pb.Payment
	if err := proto.Unmarshal(first.GetPaymentBytes(), &p1); err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(second.GetPaymentBytes(), &p2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p1.GetTicketParams().GetRecipientRandHash(), p2.GetTicketParams().GetRecipientRandHash()) {
		t.Fatal("recipient rand changed on a resize; the payee's identity is not the payer's to move")
	}
	// Face value MAY grow on a resize. It used to be pinned — "a bigger
	// refill buys more tickets, not larger ones" — and that turned out
	// to under-fund: a ticket credits its expected value, so a larger
	// intent needs many small tickets, and the payee caps a session at
	// store.MaxSenderNonces. LOC hit the ceiling at 601 tickets and was
	// credited 613,975 of 616,025 wei, then asked for the face to be
	// resized while the rand is preserved. That is what this now pins.
	//
	// The identity assertion above is the one that must not move: the
	// rand is the payee's, the face value is the payer's to size.
	f1 := new(big.Int).SetBytes(p1.GetTicketParams().GetFaceValue())
	f2 := new(big.Int).SetBytes(p2.GetTicketParams().GetFaceValue())
	if f2.Cmp(f1) < 0 {
		t.Fatalf("face value SHRANK on a bigger refill: %s -> %s", f1, f2)
	}
}

// TestConcurrentIdenticalMintsSignOnce: two callers racing on the same
// mint id must produce one ticket, not two. The durable reservation
// makes a CRASH safe; without serialization a RACE still double-signs,
// because both callers check before either records.
func TestConcurrentIdenticalMintsSignOnce(t *testing.T) {
	ctx, client, _ := newSenderClient(t)
	req := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")

	const callers = 8
	var wg sync.WaitGroup
	results := make([]*pb.CreatePaymentResponse, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = client.CreatePayment(ctx, req)
		}(i)
	}
	close(start)
	wg.Wait()

	var payments [][]byte
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d failed: %v", i, errs[i])
		}
		payments = append(payments, results[i].GetPaymentBytes())
	}
	// Every caller must have received the SAME batch. Differing bytes
	// mean more than one was signed against the payer's deposit.
	for i := 1; i < len(payments); i++ {
		if !bytes.Equal(payments[0], payments[i]) {
			t.Fatalf("caller %d got different payment bytes; a second batch was signed", i)
		}
	}
	// And exactly one nonce was consumed.
	var pay pb.Payment
	if err := proto.Unmarshal(payments[0], &pay); err != nil {
		t.Fatal(err)
	}
	if n := pay.GetTicketSenderParams()[0].GetSenderNonce(); n != 1 {
		t.Fatalf("sender nonce = %d; want 1 — the race consumed more than one", n)
	}
}

// TestMintReservedButNeverCompletedRefuses covers the crash window: the
// daemon died between signing and recording. The reservation survives
// with no response behind it, and the retry must refuse rather than
// re-sign — the reservation cannot prove whether a ticket was produced,
// and re-signing on a maybe is how a payer pays twice.
func TestMintReservedButNeverCompletedRefuses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sender.db")
	req := makeCreatePaymentRequest([]byte("0123456789abcdef0123"), "openai:chat", "gpt-5",
		"token", 1000, 1, 1000, "https://broker.example.com")

	// Simulate the crash: reserve the id, then never record a response.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	senderAddr := devSenderAddress(t)
	fp := sender.MintFingerprint(req)
	if prior, err := st.MintReserve(senderAddr, req.GetMintRequestId(), fp); err != nil || prior != nil {
		t.Fatalf("first reserve = (%v, %v); want a clean claim", prior, err)
	}
	_ = st.Close()

	ctx, client, closeFn := newSenderClientAt(t, dbPath)
	defer closeFn()
	_, err = client.CreatePayment(ctx, req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("retry after an incomplete mint = %v; want FailedPrecondition, never a second mint", err)
	}
}

// devSenderAddress is the address the dev keystore deterministically
// produces, which is the sender the test daemon signs as.
func devSenderAddress(t *testing.T) []byte {
	t.Helper()
	ks, err := devkeystore.New("")
	if err != nil {
		t.Fatal(err)
	}
	return ks.Address()
}

// A minted envelope reports when it dies.
//
// A signed payment envelope cannot be revoked — the signature IS the
// authorization and handing it over is irreversible — so a consumer
// holding an encumbrance against one that was issued but never admitted
// has exactly one unconditional release: expiry, which the chain
// enforces rather than either daemon. Reporting it at mint means the
// deadline travels with the envelope instead of having to be parsed back
// out of it.
func TestCreatePayment_ReportsExpiryRound(t *testing.T) {
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
	if resp.GetCreationRound() == 0 {
		t.Fatal("mint reported no creation_round; an encumbrance holder has no release deadline")
	}
	if resp.GetTicketValidityPeriod() < 1 {
		t.Fatal("ticket_validity_period is unset; a consumer cannot tell whether the deadline " +
			"it was handed still holds after governance moves")
	}
	// The contract redeems while creationRound + period > currentRound,
	// so the LAST redeemable round is creationRound + period - 1 and
	// "expired" is exactly current > that. Sitting a round beyond the
	// contract's boundary is conservative but misdescribes the rule.
	want := resp.GetCreationRound() + resp.GetTicketValidityPeriod() - 1
	if got := resp.GetExpiresAfterRound(); got != want {
		t.Fatalf("expires_after_round = %d; want creation_round + period - 1 = %d", got, want)
	}
}

// current_round comes from the SAME clock that stamps creation_round, so
// a consumer evaluating "has this envelope expired" reads one clock
// rather than correlating two that may disagree.
func TestGetDepositInfo_ReportsCurrentRound(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mint, err := client.CreatePayment(ctx, makeCreatePaymentRequest(
		[]byte("recipient-20-bytes!!"),
		"openai:/v1/chat/completions", "gpt-5", "token", 1000, 1, 1000,
		"https://broker.example.com",
	))
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	info, err := client.GetDepositInfo(ctx, &pb.GetDepositInfoRequest{})
	if err != nil {
		t.Fatalf("GetDepositInfo: %v", err)
	}
	if info.GetCurrentRound() == 0 {
		t.Fatal("current_round is 0; a consumer has nothing to evaluate expiry against")
	}
	if info.GetTicketValidityPeriod() < 1 {
		t.Fatal("deposit info does not report the current ticket_validity_period")
	}
	if info.GetCurrentRound() != mint.GetCreationRound() {
		t.Fatalf("current_round %d != the round that stamped the mint %d; the two must come "+
			"from one clock or a consumer is correlating clocks that can disagree",
			info.GetCurrentRound(), mint.GetCreationRound())
	}
	// The rule the fields exist for: release iff current > expires.
	// At mint time the envelope is live, so the rule must say "hold".
	if info.GetCurrentRound() > mint.GetExpiresAfterRound() {
		t.Fatalf("a freshly minted envelope already reads as releasable "+
			"(current=%d expires_after=%d)", info.GetCurrentRound(), mint.GetExpiresAfterRound())
	}
}
