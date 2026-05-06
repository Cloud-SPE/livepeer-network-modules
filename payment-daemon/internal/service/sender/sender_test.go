package sender_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/sender"
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
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil)

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

func TestCreatePayment_HappyPath(t *testing.T) {
	client, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.CreatePayment(ctx, &pb.CreatePaymentRequest{
		FaceValue:  []byte{0x03, 0xe8}, // 1000
		Recipient:  []byte("recipient-20-bytes!!"),
		Capability: "openai:/v1/chat/completions",
		Offering:   "gpt-5",
	})
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

	req := &pb.CreatePaymentRequest{
		FaceValue:  []byte{0x03, 0xe8},
		Recipient:  []byte("recipient-20-bytes!!"),
		Capability: "openai:/v1/chat/completions",
		Offering:   "gpt-5",
	}

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
			FaceValue: []byte{0x01}, Capability: "x", Offering: "y",
		}},
		{"empty capability", &pb.CreatePaymentRequest{
			FaceValue: []byte{0x01}, Recipient: []byte("r"), Offering: "y",
		}},
		{"empty offering", &pb.CreatePaymentRequest{
			FaceValue: []byte{0x01}, Recipient: []byte("r"), Capability: "x",
		}},
		{"empty face_value", &pb.CreatePaymentRequest{
			Recipient: []byte("r"), Capability: "x", Offering: "y",
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
