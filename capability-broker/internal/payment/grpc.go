package payment

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// GRPC is the broker's real PayeeDaemon adapter, talking over a unix
// socket. NewGRPC dials eagerly + Health-probes; gRPC handles
// reconnection internally for the daemon's lifetime.
type GRPC struct {
	conn   *grpc.ClientConn
	client pb.PayeeDaemonClient
	socket string
}

// NewGRPC dials the unix socket and Health-probes the daemon. Fails
// fast if the daemon is unreachable.
func NewGRPC(ctx context.Context, socketPath string) (*GRPC, error) {
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial unix socket %s: %w", socketPath, err)
	}
	g := &GRPC{
		conn:   conn,
		client: pb.NewPayeeDaemonClient(conn),
		socket: socketPath,
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := g.client.Health(probeCtx, &pb.HealthRequest{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("payment-daemon health probe at %s: %w", socketPath, err)
	}
	return g, nil
}

// Shutdown closes the underlying gRPC connection. Called once at broker
// shutdown.
func (g *GRPC) Shutdown() error {
	if g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

func (g *GRPC) OpenSession(ctx context.Context, req OpenSessionRequest) (*OpenSessionResult, error) {
	priceBytes := []byte(nil)
	if req.PricePerWorkUnitWei != nil {
		priceBytes = req.PricePerWorkUnitWei.Bytes()
	}
	resp, err := g.client.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              req.WorkID,
		Capability:          req.Capability,
		Offering:            req.Offering,
		PricePerWorkUnitWei: priceBytes,
		WorkUnit:            req.WorkUnit,
	})
	if err != nil {
		return nil, err
	}
	return &OpenSessionResult{
		AlreadyOpen: resp.GetOutcome() == pb.OpenSessionResponse_OUTCOME_ALREADY_OPEN,
	}, nil
}

func (g *GRPC) ProcessPayment(ctx context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error) {
	resp, err := g.client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: req.PaymentBytes,
		WorkId:       req.WorkID,
	})
	if err != nil {
		return nil, err
	}
	return &ProcessPaymentResult{
		Sender:        resp.GetSender(),
		Balance:       new(big.Int).SetBytes(resp.GetBalance()),
		WinnersQueued: resp.GetWinnersQueued(),
	}, nil
}

func (g *GRPC) DebitBalance(ctx context.Context, req DebitBalanceRequest) (*big.Int, error) {
	resp, err := g.client.DebitBalance(ctx, &pb.DebitBalanceRequest{
		Sender:    req.Sender,
		WorkId:    req.WorkID,
		WorkUnits: req.WorkUnits,
		DebitSeq:  req.DebitSeq,
	})
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(resp.GetBalance()), nil
}

func (g *GRPC) SufficientBalance(ctx context.Context, req SufficientBalanceRequest) (*SufficientBalanceResult, error) {
	resp, err := g.client.SufficientBalance(ctx, &pb.SufficientBalanceRequest{
		Sender:       req.Sender,
		WorkId:       req.WorkID,
		MinWorkUnits: req.MinWorkUnits,
	})
	if err != nil {
		return nil, err
	}
	return &SufficientBalanceResult{
		Sufficient: resp.GetSufficient(),
		Balance:    new(big.Int).SetBytes(resp.GetBalance()),
	}, nil
}

func (g *GRPC) GetBalance(ctx context.Context, sender []byte, workID string) (*big.Int, error) {
	resp, err := g.client.GetBalance(ctx, &pb.GetBalanceRequest{
		Sender: sender,
		WorkId: workID,
	})
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(resp.GetBalance()), nil
}

func (g *GRPC) CloseSession(ctx context.Context, sender []byte, workID string) error {
	_, err := g.client.CloseSession(ctx, &pb.CloseSessionRequest{
		Sender: sender,
		WorkId: workID,
	})
	return err
}

// Compile-time interface check.
var _ Client = (*GRPC)(nil)
