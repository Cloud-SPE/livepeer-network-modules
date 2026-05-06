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

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
)

// chainStand is like stand but lets the caller pin the recipient address
// (so the in-memory key signing the ticket on the test side maps to a
// session whose recipient matches).
func chainStand(t *testing.T, recipient []byte) (pb.PayeeDaemonClient, *store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rx.db")
	sockPath := filepath.Join(dir, "rx.sock")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := receiver.New(st, receiver.Config{
		Recipient:        recipient,
		DefaultFaceValue: big.NewInt(1_000_000),
		DefaultWinProb:   types.MaxWinProb, // every ticket wins → exercise the queueing path
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
	tp, err := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
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
	workID := hex.EncodeToString(rrHash)
	faceValue := new(big.Int).SetBytes(tp.GetTicketParams().GetFaceValue())
	winProb := new(big.Int).SetBytes(tp.GetTicketParams().GetWinProb())

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
			Seed:              []byte{},
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

	resp, err := client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: raw,
		WorkId:       workID,
	})
	if err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
	if resp.GetWinnersQueued() != 1 {
		t.Errorf("WinnersQueued = %d; want 1 (MaxWinProb → always wins)", resp.GetWinnersQueued())
	}
	credited := new(big.Int).SetBytes(resp.GetCreditedEv())
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

	tp, err := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
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
	resp, err := client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: raw, WorkId: hex.EncodeToString(rrHash),
	})
	if err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
	if resp.GetWinnersQueued() != 0 {
		t.Errorf("WinnersQueued = %d; want 0 (bad sig)", resp.GetWinnersQueued())
	}
	if got := new(big.Int).SetBytes(resp.GetCreditedEv()); got.Sign() != 0 {
		t.Errorf("CreditedEv = %s; want 0 (bad sig)", got)
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

	tp, _ := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
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

	first, _ := client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: raw, WorkId: hex.EncodeToString(rrHash),
	})
	if first.GetWinnersQueued() != 1 {
		t.Errorf("first WinnersQueued = %d; want 1", first.GetWinnersQueued())
	}
	second, _ := client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: raw, WorkId: hex.EncodeToString(rrHash),
	})
	if second.GetWinnersQueued() != 0 {
		t.Errorf("replay WinnersQueued = %d; want 0 (nonce replay)", second.GetWinnersQueued())
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
