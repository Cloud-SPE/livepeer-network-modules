package payment

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// GRPC is the broker's real payment-daemon adapter, talking to a
// PayeeDaemon over a unix-socket gRPC connection.
//
// The connection is opened eagerly in NewGRPC and held for the broker's
// lifetime; gRPC handles reconnection internally. The constructor calls
// Health() once before returning so the broker fails-fast if the daemon
// is missing.
type GRPC struct {
	conn   *grpc.ClientConn
	client pb.PayeeDaemonClient
	socket string
}

// NewGRPC dials the unix socket, calls Health once to confirm the daemon
// is reachable, and returns a ready client. Returns an error if the socket
// is unreachable or Health fails.
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
// shutdown; not part of the Client interface.
func (g *GRPC) Shutdown() error {
	if g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

// OpenSession sends the decoded envelope and the cross-check fields to the
// daemon and returns the assigned session ID.
func (g *GRPC) OpenSession(ctx context.Context, req OpenSessionRequest) (*Session, error) {
	if req.DecodedPayment == nil {
		return nil, fmt.Errorf("OpenSessionRequest.DecodedPayment is nil")
	}
	resp, err := g.client.OpenSession(ctx, &pb.OpenSessionRequest{
		Payment:      req.DecodedPayment,
		CapabilityId: req.CapabilityID,
		OfferingId:   req.OfferingID,
	})
	if err != nil {
		return nil, err
	}
	return &Session{ID: resp.GetSessionId()}, nil
}

// Debit forwards a debit RPC.
func (g *GRPC) Debit(ctx context.Context, sessionID string, units uint64) error {
	_, err := g.client.Debit(ctx, &pb.DebitRequest{SessionId: sessionID, Units: units})
	return err
}

// Reconcile forwards a reconcile RPC.
func (g *GRPC) Reconcile(ctx context.Context, sessionID string, actualUnits uint64) error {
	_, err := g.client.Reconcile(ctx, &pb.ReconcileRequest{SessionId: sessionID, ActualUnits: actualUnits})
	return err
}

// Close ends the session on the daemon side. Implements Client.Close.
func (g *GRPC) Close(ctx context.Context, sessionID string) error {
	_, err := g.client.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: sessionID})
	return err
}

// Compile-time interface check.
var _ Client = (*GRPC)(nil)
