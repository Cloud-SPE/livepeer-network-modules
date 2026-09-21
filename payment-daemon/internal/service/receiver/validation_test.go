package receiver_test

import (
	"context"
	"encoding/hex"
	"math/big"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
)

func chainStandAtPaths(t *testing.T, dbPath, sockPath string, recipient []byte) (pb.PayeeDaemonClient, *store.Store, func()) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := receiver.New(st, receiver.Config{SettlementDomainID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Recipient:        recipient,
		DefaultFaceValue: big.NewInt(1_000_000),
		DefaultWinProb:   types.MaxWinProb,
	}, nil)

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		gs.GracefulStop()
		_ = st.Close()
	}
	return pb.NewPayeeDaemonClient(conn), st, cleanup
}

// chainStand is like stand but lets the caller pin the recipient address
// (so the in-memory key signing the ticket on the test side maps to a
// session whose recipient matches).
func chainStand(t *testing.T, recipient []byte) (pb.PayeeDaemonClient, *store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rx.db")
	sockPath := filepath.Join(dir, "rx.sock")
	return chainStandAtPaths(t, dbPath, sockPath, recipient)
}

func signPayment(t *testing.T, signer *inmemory.KeyStore, recipient []byte, params *pb.TicketParams, nonce uint32) []byte {
	t.Helper()
	ticket := &types.Ticket{
		Recipient:         recipient,
		Sender:            signer.Address(),
		FaceValue:         new(big.Int).SetBytes(params.GetFaceValue()),
		WinProb:           new(big.Int).SetBytes(params.GetWinProb()),
		SenderNonce:       nonce,
		RecipientRandHash: params.GetRecipientRandHash(),
	}
	sig, err := signer.Sign(ticket.Hash())
	if err != nil {
		t.Fatalf("sign ticket: %v", err)
	}
	payment := &pb.Payment{
		Sender:           signer.Address(),
		ExpirationParams: &pb.TicketExpirationParams{},
		TicketParams: &pb.TicketParams{
			Recipient:         append([]byte(nil), params.GetRecipient()...),
			FaceValue:         append([]byte(nil), params.GetFaceValue()...),
			WinProb:           append([]byte(nil), params.GetWinProb()...),
			RecipientRandHash: append([]byte(nil), params.GetRecipientRandHash()...),
			Seed:              append([]byte(nil), params.GetSeed()...),
		},
		TicketSenderParams: []*pb.TicketSenderParams{{
			SenderNonce: nonce,
			Sig:         sig,
		}},
	}
	raw, err := proto.Marshal(payment)
	if err != nil {
		t.Fatalf("marshal payment: %v", err)
	}
	return raw
}

func TestGetTicketParams_IsIdempotentForOpenSession(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := inmemory.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	recipient := bytes20(0xab)
	client, _, cleanup := chainStand(t, recipient)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream",
		Sender:     signer.Address(),
		Recipient:  recipient,
		FaceValue:  big.NewInt(1234).Bytes(),
		Capability: "video:transcode.abr",
		Offering:   "default",
	}
	first, err := client.GetTicketParams(ctx, req)
	if err != nil {
		t.Fatalf("first GetTicketParams: %v", err)
	}
	second, err := client.GetTicketParams(ctx, req)
	if err != nil {
		t.Fatalf("second GetTicketParams: %v", err)
	}
	if !equalBytes(first.GetTicketParams().GetRecipientRandHash(), second.GetTicketParams().GetRecipientRandHash()) {
		t.Fatalf("recipient rand hash rotated inside open session: first=%x second=%x", first.GetTicketParams().GetRecipientRandHash(), second.GetTicketParams().GetRecipientRandHash())
	}
	if !equalBytes(first.GetTicketParams().GetSeed(), second.GetTicketParams().GetSeed()) {
		t.Fatalf("seed rotated inside open session: first=%x second=%x", first.GetTicketParams().GetSeed(), second.GetTicketParams().GetSeed())
	}
}

func TestGetTicketParams_PersistsAcrossReceiverRestart(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := inmemory.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	recipient := bytes20(0xab)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rx.db")
	sockPath := filepath.Join(dir, "rx.sock")

	client, _, cleanup := chainStandAtPaths(t, dbPath, sockPath, recipient)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream",
		Sender:     signer.Address(),
		Recipient:  recipient,
		FaceValue:  big.NewInt(1234).Bytes(),
		Capability: "video:transcode.abr",
		Offering:   "default",
	}
	first, err := client.GetTicketParams(ctx, req)
	if err != nil {
		t.Fatalf("first GetTicketParams: %v", err)
	}
	firstWorkID := hex.EncodeToString(first.GetTicketParams().GetRecipientRandHash())
	firstPayment := signPayment(t, signer, recipient, first.GetTicketParams(), 1)
	resp, err := client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: firstPayment,
	})
	if err != nil {
		t.Fatalf("first ProcessPayment: %v", err)
	}
	if got := new(big.Int).SetBytes(resp.GetCreditedValueWei().GetValue()); got.Sign() <= 0 {
		t.Fatalf("first credited_ev = %s; want > 0", got)
	}
	cleanup()

	client, _, cleanup = chainStandAtPaths(t, dbPath, sockPath, recipient)
	defer cleanup()

	second, err := client.GetTicketParams(ctx, req)
	if err != nil {
		t.Fatalf("second GetTicketParams after restart: %v", err)
	}
	if !equalBytes(first.GetTicketParams().GetRecipientRandHash(), second.GetTicketParams().GetRecipientRandHash()) {
		t.Fatalf("recipient rand hash changed across restart: first=%x second=%x", first.GetTicketParams().GetRecipientRandHash(), second.GetTicketParams().GetRecipientRandHash())
	}
	secondWorkID := hex.EncodeToString(second.GetTicketParams().GetRecipientRandHash())
	if firstWorkID != secondWorkID {
		t.Fatalf("work_id changed across restart: first=%s second=%s", firstWorkID, secondWorkID)
	}
	secondPayment := signPayment(t, signer, recipient, second.GetTicketParams(), 2)
	resp, err = client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: secondPayment,
	})
	if err != nil {
		t.Fatalf("second ProcessPayment after restart: %v", err)
	}
	if got := new(big.Int).SetBytes(resp.GetCreditedValueWei().GetValue()); got.Sign() <= 0 {
		t.Fatalf("second credited_ev = %s; want > 0", got)
	}
}

// TestProcessPayment_E2E_RealSig exercises the full validation pipeline:
// receiver issues TicketParams → sender signs a ticket with a real
// keystore → receiver validates, sums EV, queues the winner.
func TestProcessPayment_E2E_RealSig(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := inmemory.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	sender := signer.Address()

	recipient := bytes20(0xab)
	client, st, cleanup := chainStand(t, recipient)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Sender asks the receiver for params.
	tp, err := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream",
		Sender:     sender,
		Recipient:  recipient,
		Capability: "openai:/v1/chat/completions",
		Offering:   "gpt-5",
	})
	if err != nil {
		t.Fatalf("GetTicketParams: %v", err)
	}
	gotRecipient := tp.GetTicketParams().GetRecipient()
	if !equalBytes(gotRecipient, recipient) {
		t.Fatalf("returned recipient = %x; want %x", gotRecipient, recipient)
	}
	rrHash := tp.GetTicketParams().GetRecipientRandHash()
	seed := tp.GetTicketParams().GetSeed()
	faceValue := new(big.Int).SetBytes(tp.GetTicketParams().GetFaceValue())
	winProb := new(big.Int).SetBytes(tp.GetTicketParams().GetWinProb())
	if len(seed) != 32 {
		t.Fatalf("seed length = %d; want 32", len(seed))
	}
	if got := types.HashRecipientRand(new(big.Int).SetBytes(seed)); !equalBytes(got, rrHash) {
		t.Fatalf("seed hash = %x; want recipient_rand_hash %x", got, rrHash)
	}

	// 2. Sender constructs and signs the ticket.
	ticket := &types.Ticket{
		Recipient:         recipient,
		Sender:            sender,
		FaceValue:         faceValue,
		WinProb:           winProb,
		SenderNonce:       1,
		RecipientRandHash: rrHash,
	}
	sig, err := signer.Sign(ticket.Hash())
	if err != nil {
		t.Fatal(err)
	}

	// 3. Build the wire payment and submit.
	payment := &pb.Payment{
		Sender:           sender,
		ExpirationParams: &pb.TicketExpirationParams{},
		TicketParams: &pb.TicketParams{
			Recipient:         recipient,
			FaceValue:         faceValue.Bytes(),
			WinProb:           winProb.Bytes(),
			RecipientRandHash: rrHash,
			Seed:              seed,
		},
		TicketSenderParams: []*pb.TicketSenderParams{{
			SenderNonce: 1,
			Sig:         sig,
		}},
	}
	raw, err := proto.Marshal(payment)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: raw,
	})
	if err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}

	credited := new(big.Int).SetBytes(resp.GetCreditedValueWei().GetValue())
	if credited.Cmp(faceValue) != 0 {
		t.Errorf("CreditedEv = %s; want %s (faceValue × MaxWinProb / 2^256 = faceValue)", credited, faceValue)
	}

	// 4. Pending redemptions must include this ticket.
	pend, err := st.PendingRedemptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 {
		t.Errorf("pending count = %d; want 1", len(pend))
	}
}

// TestProcessPayment_RejectsBadSig: tamper with the sig; the validator
// rejects the ticket and zero EV is credited.
func TestProcessPayment_RejectsBadSig(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer, _ := inmemory.New(priv)
	sender := signer.Address()
	recipient := bytes20(0xab)

	client, _, cleanup := chainStand(t, recipient)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tp, err := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream",
		Sender:     sender,
		Recipient:  recipient,
		Capability: "x", Offering: "y",
	})
	if err != nil {
		t.Fatal(err)
	}
	rrHash := tp.GetTicketParams().GetRecipientRandHash()
	faceValue := new(big.Int).SetBytes(tp.GetTicketParams().GetFaceValue())
	winProb := new(big.Int).SetBytes(tp.GetTicketParams().GetWinProb())

	tk := &types.Ticket{
		Recipient: recipient, Sender: sender,
		FaceValue: faceValue, WinProb: winProb,
		SenderNonce: 1, RecipientRandHash: rrHash,
	}
	sig, _ := signer.Sign(tk.Hash())
	// Flip a byte in sig to invalidate.
	sig[10] ^= 0xff

	payment := &pb.Payment{
		Sender:           sender,
		ExpirationParams: &pb.TicketExpirationParams{},
		TicketParams: &pb.TicketParams{
			Recipient:         recipient,
			FaceValue:         faceValue.Bytes(),
			WinProb:           winProb.Bytes(),
			RecipientRandHash: rrHash,
		},
		TicketSenderParams: []*pb.TicketSenderParams{{SenderNonce: 1, Sig: sig}},
	}
	raw, _ := proto.Marshal(payment)
	_, err = client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: raw,
	})
	if err == nil {
		t.Fatal("invalid funding accepted")
	}
}

// TestProcessPayment_NonceReplayDropped: same nonce twice → second is
// dropped silently.
func TestProcessPayment_NonceReplayDropped(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer, _ := inmemory.New(priv)
	sender := signer.Address()
	recipient := bytes20(0xab)

	client, _, cleanup := chainStand(t, recipient)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tp, _ := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream",
		Sender: sender, Recipient: recipient, Capability: "x", Offering: "y",
	})
	rrHash := tp.GetTicketParams().GetRecipientRandHash()
	faceValue := new(big.Int).SetBytes(tp.GetTicketParams().GetFaceValue())
	winProb := new(big.Int).SetBytes(tp.GetTicketParams().GetWinProb())

	tk := &types.Ticket{
		Recipient: recipient, Sender: sender,
		FaceValue: faceValue, WinProb: winProb,
		SenderNonce: 1, RecipientRandHash: rrHash,
	}
	sig, _ := signer.Sign(tk.Hash())
	payment := &pb.Payment{
		Sender:           sender,
		ExpirationParams: &pb.TicketExpirationParams{},
		TicketParams: &pb.TicketParams{
			Recipient:         recipient,
			FaceValue:         faceValue.Bytes(),
			WinProb:           winProb.Bytes(),
			RecipientRandHash: rrHash,
		},
		TicketSenderParams: []*pb.TicketSenderParams{{SenderNonce: 1, Sig: sig}},
	}
	raw, _ := proto.Marshal(payment)

	first, err := client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: raw,
	})

	second, err := client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: raw,
	})

	if err != nil || !second.GetReplayed() || !proto.Equal(first.GetAccount(), second.GetAccount()) {
		t.Fatalf("replay=%v err=%v", second, err)
	}
}

func TestProcessPayment_InvalidRecipientRandReported(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	signer, _ := inmemory.New(priv)
	sender := signer.Address()
	recipient := bytes20(0xab)

	client, _, cleanup := chainStand(t, recipient)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tp, err := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream",
		Sender: sender, Recipient: recipient, Capability: "x", Offering: "y",
	})
	if err != nil {
		t.Fatal(err)
	}
	params := tp.GetTicketParams()
	badHash := append([]byte(nil), params.GetRecipientRandHash()...)
	badHash[len(badHash)-1] ^= 0xff
	ticket := &types.Ticket{
		Recipient:         recipient,
		Sender:            sender,
		FaceValue:         new(big.Int).SetBytes(params.GetFaceValue()),
		WinProb:           new(big.Int).SetBytes(params.GetWinProb()),
		SenderNonce:       1,
		RecipientRandHash: badHash,
	}
	sig, _ := signer.Sign(ticket.Hash())
	payment := &pb.Payment{
		Sender:           sender,
		ExpirationParams: &pb.TicketExpirationParams{},
		TicketParams: &pb.TicketParams{
			Recipient:         recipient,
			FaceValue:         append([]byte(nil), params.GetFaceValue()...),
			WinProb:           append([]byte(nil), params.GetWinProb()...),
			RecipientRandHash: badHash,
			Seed:              append([]byte(nil), params.GetSeed()...),
		},
		TicketSenderParams: []*pb.TicketSenderParams{{SenderNonce: 1, Sig: sig}},
	}
	raw, _ := proto.Marshal(payment)
	_, err = client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account", SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PaymentBytes: raw,
	})
	if err == nil {
		t.Fatal("invalid funding accepted")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
