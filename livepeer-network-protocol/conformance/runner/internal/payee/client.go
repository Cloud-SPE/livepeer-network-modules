// Package payee dials the receiver-side payment-daemon for fixture-time
// observability. Only `GetBalance` is used: plan 0015's interim-debit
// fixtures infer the daemon's DebitBalance call count by sampling
// balance over time (plan 0015 §9.1).
//
// The package is best-effort: if the daemon socket is not mounted on the
// runner container (e.g. local `make test` against an external broker),
// Init() returns nil and GetBalance is a no-op returning ErrUnavailable.
package payee

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// ErrUnavailable is returned by GetBalance when the runner has no
// connection to the receiver daemon (Init not called or socket
// unreachable).
var ErrUnavailable = errors.New("payee: receiver daemon socket not configured")

var (
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client pb.PayeeDaemonClient
)

// Init dials the receiver daemon at socketPath. Returns nil on success,
// nil if socketPath is empty (best-effort), or a wrapped error when the
// dial / health probe fails.
func Init(ctx context.Context, socketPath string) error {
	mu.Lock()
	defer mu.Unlock()
	if client != nil {
		return nil
	}
	if socketPath == "" {
		return nil
	}
	c, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial payee-daemon at %s: %w", socketPath, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pc := pb.NewPayeeDaemonClient(c)
	if _, err := pc.Health(probeCtx, &pb.HealthRequest{}); err != nil {
		_ = c.Close()
		return fmt.Errorf("payee-daemon health probe at %s: %w", socketPath, err)
	}
	conn = c
	client = pc
	return nil
}

// Shutdown closes the gRPC connection.
func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if conn != nil {
		_ = conn.Close()
		conn = nil
		client = nil
	}
}

// GetBalance returns the current balance for a (sender, work_id) pair,
// or ErrUnavailable when the receiver daemon socket isn't configured.
func GetBalance(ctx context.Context, sender []byte, workID string) (*big.Int, error) {
	mu.Lock()
	c := client
	mu.Unlock()
	if c == nil {
		return nil, ErrUnavailable
	}
	resp, err := c.GetBalance(ctx, &pb.GetBalanceRequest{Sender: sender, WorkId: workID})
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(resp.GetBalance()), nil
}
