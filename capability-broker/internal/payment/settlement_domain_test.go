package payment

import (
	"context"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc"
)

type domainPayee struct {
	pb.UnimplementedPayeeDaemonServer
	domain   atomic.Value
	observed atomic.Value
}

func (s *domainPayee) Health(context.Context, *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok", SettlementDomainId: s.domain.Load().(string)}, nil
}
func (s *domainPayee) GetWholesaleAccount(_ context.Context, r *pb.GetWholesaleAccountRequest) (*pb.GetWholesaleAccountResponse, error) {
	s.observed.Store(r.GetSettlementDomainId())
	return &pb.GetWholesaleAccountResponse{Account: &pb.WholesaleAccountView{Payer: r.Payer, WholesaleAccountId: r.WholesaleAccountId, SettlementDomainId: r.GetSettlementDomainId()}}, nil
}

func TestGRPCPinsLedgerAcrossReconnect(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "payee.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	payee := &domainPayee{}
	payee.domain.Store(MockSettlementDomainID)
	server := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(server, payee)
	go server.Serve(lis)
	defer server.Stop()
	client, err := NewGRPC(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()
	got, err := client.GetWholesaleAccount(context.Background(), make([]byte, 20), "test-account")
	if err != nil || got.SettlementDomainID != MockSettlementDomainID || payee.observed.Load() != MockSettlementDomainID {
		t.Fatalf("domain not carried on account RPC: %+v %v", got, err)
	}
	params, err := client.GetTicketParams(context.Background(), GetTicketParamsRequest{})
	if err != nil || params.SettlementDomainID != MockSettlementDomainID {
		t.Fatalf("ticket parameter domain not relayed: %+v %v", params, err)
	}
	payee.domain.Store("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := client.GetWholesaleAccount(context.Background(), make([]byte, 20), "test-account"); err == nil {
		t.Fatal("silently reconnected to a different ledger")
	}
	payee.domain.Store("")
	if old, err := NewGRPC(context.Background(), socket); err == nil {
		old.Shutdown()
		t.Fatal("accepted daemon without ledger identity")
	}
}

func (s *domainPayee) GetTicketParams(context.Context, *pb.GetTicketParamsRequest) (*pb.GetTicketParamsResponse, error) {
	return &pb.GetTicketParamsResponse{TicketParams: &pb.TicketParams{Recipient: make([]byte, 20), FaceValue: []byte{1}, WinProb: []byte{1}}}, nil
}
