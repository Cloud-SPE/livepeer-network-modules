package paymentdaemon

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"time"

	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/payments/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	SocketPath string
	Timeout    time.Duration
}

type Client struct {
	socketPath string
	timeout    time.Duration
}

type RoundRevenue struct {
	RoundID              int64
	ConfirmedRevenueWei  string
	ConfirmedTicketCount uint64
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("payment daemon socket path is required")
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("payment daemon timeout must be >= 0")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 1500 * time.Millisecond
	}
	return &Client{socketPath: cfg.SocketPath, timeout: cfg.Timeout}, nil
}

func (c *Client) GetRoundRevenue(ctx context.Context, roundID int64) (RoundRevenue, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return RoundRevenue{}, err
	}
	defer conn.Close()

	reqCtx, cancel := withTimeout(ctx, c.timeout)
	defer cancel()
	res, err := paymentsv1.NewPayeeDaemonClient(conn).GetRoundRevenue(reqCtx, &paymentsv1.GetRoundRevenueRequest{
		RoundId: roundID,
	})
	if err != nil {
		return RoundRevenue{}, fmt.Errorf("payment daemon GetRoundRevenue: %w", err)
	}
	return RoundRevenue{
		RoundID:              res.GetRoundId(),
		ConfirmedRevenueWei:  decimalString(res.GetConfirmedRevenueWei()),
		ConfirmedTicketCount: res.GetConfirmedTicketCount(),
	}, nil
}

func (c *Client) dial(ctx context.Context) (*grpc.ClientConn, error) {
	conn, err := grpc.DialContext(
		ctx,
		"unix://"+c.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", c.socketPath)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("dial payment daemon: %w", err)
	}
	return conn, nil
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func decimalString(raw []byte) string {
	if len(raw) == 0 {
		return "0"
	}
	n := new(big.Int).SetBytes(raw)
	return n.String()
}
