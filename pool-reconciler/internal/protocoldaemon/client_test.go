package protocoldaemon

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	protocolv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/protocol/v1"
	"google.golang.org/grpc"
)

type stubProtocolDaemon struct {
	protocolv1.UnimplementedProtocolDaemonServer
	roundStatus protocolv1.RoundStatus
	roundEvents []*protocolv1.RoundEvent
}

func (s *stubProtocolDaemon) GetRoundStatus(context.Context, *protocolv1.Empty) (*protocolv1.RoundStatus, error) {
	return &s.roundStatus, nil
}

func (s *stubProtocolDaemon) StreamRoundEvents(_ *protocolv1.Empty, stream grpc.ServerStreamingServer[protocolv1.RoundEvent]) error {
	for _, evt := range s.roundEvents {
		if err := stream.Send(evt); err != nil {
			return err
		}
	}
	return nil
}

func TestGetRoundStatus(t *testing.T) {
	client := newTestClient(t, &stubProtocolDaemon{
		roundStatus: protocolv1.RoundStatus{
			LastRound:               124,
			LastIntentId:            []byte{0xde, 0xad, 0xbe, 0xef},
			LastError:               "none",
			CurrentRoundInitialized: true,
		},
	})

	status, err := client.GetRoundStatus(context.Background())
	if err != nil {
		t.Fatalf("GetRoundStatus() error = %v", err)
	}
	if status.LastRound != 124 {
		t.Fatalf("LastRound = %d; want 124", status.LastRound)
	}
	if status.LastIntentIDHex != "deadbeef" {
		t.Fatalf("LastIntentIDHex = %q; want deadbeef", status.LastIntentIDHex)
	}
	if !status.CurrentRoundInitialized {
		t.Fatal("CurrentRoundInitialized = false; want true")
	}
}

func TestStreamRoundEvents(t *testing.T) {
	client := newTestClient(t, &stubProtocolDaemon{
		roundEvents: []*protocolv1.RoundEvent{
			{Number: 123, StartBlock: 1000, Initialized: false},
			{Number: 124, StartBlock: 1100, Initialized: true, BlockHash: []byte{0x01, 0x02}},
		},
	})

	var got []RoundEvent
	err := client.StreamRoundEvents(context.Background(), func(evt RoundEvent) error {
		got = append(got, evt)
		if len(got) == 2 {
			return context.Canceled
		}
		return nil
	})
	if err != context.Canceled {
		t.Fatalf("StreamRoundEvents() error = %v; want context.Canceled", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d; want 2", len(got))
	}
	if got[1].BlockHashHex != "0102" {
		t.Fatalf("BlockHashHex = %q; want 0102", got[1].BlockHashHex)
	}
}

func TestNewClientRequiresSocket(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("NewClient() error = nil, want missing socket error")
	}
}

func newTestClient(t *testing.T, srv protocolv1.ProtocolDaemonServer) *Client {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "protocol.sock")
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	gsrv := grpc.NewServer()
	protocolv1.RegisterProtocolDaemonServer(gsrv, srv)
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
