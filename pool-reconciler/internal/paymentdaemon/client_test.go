package paymentdaemon

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc"
)

type stubPayeeDaemon struct {
	paymentsv1.UnimplementedPayeeDaemonServer
	roundRevenue paymentsv1.GetRoundRevenueResponse
}

func (s *stubPayeeDaemon) GetRoundRevenue(context.Context, *paymentsv1.GetRoundRevenueRequest) (*paymentsv1.GetRoundRevenueResponse, error) {
	return &s.roundRevenue, nil
}

func TestGetRoundRevenue(t *testing.T) {
	client := newTestClient(t, &stubPayeeDaemon{
		roundRevenue: paymentsv1.GetRoundRevenueResponse{
			RoundId:              124,
			ConfirmedRevenueWei:  []byte{0x0b, 0xb8},
			ConfirmedTicketCount: 2,
		},
	})

	got, err := client.GetRoundRevenue(context.Background(), 124)
	if err != nil {
		t.Fatalf("GetRoundRevenue() error = %v", err)
	}
	if got.RoundID != 124 {
		t.Fatalf("RoundID = %d; want 124", got.RoundID)
	}
	if got.ConfirmedRevenueWei != "3000" {
		t.Fatalf("ConfirmedRevenueWei = %q; want 3000", got.ConfirmedRevenueWei)
	}
	if got.ConfirmedTicketCount != 2 {
		t.Fatalf("ConfirmedTicketCount = %d; want 2", got.ConfirmedTicketCount)
	}
}

func TestNewClientRequiresSocket(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("NewClient() error = nil, want missing socket error")
	}
}

func newTestClient(t *testing.T, srv paymentsv1.PayeeDaemonServer) *Client {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "payment.sock")
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	gsrv := grpc.NewServer()
	paymentsv1.RegisterPayeeDaemonServer(gsrv, srv)
	go func() {
		_ = gsrv.Serve(lis)
	}()
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			gsrv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			gsrv.Stop()
		}
		_ = lis.Close()
	})

	client, err := NewClient(Config{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
